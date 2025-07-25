package gcp

import (
	"fmt"
	"strings"

	securitycenter "cloud.google.com/go/securitycenter/apiv1"
	"cloud.google.com/go/securitycenter/apiv1/securitycenterpb"
	v1 "github.com/flanksource/config-db/api/v1"
	k8sScraper "github.com/flanksource/config-db/scrapers/kubernetes"
	"github.com/flanksource/config-db/utils"
	"github.com/flanksource/duty/models"
	"github.com/samber/lo"
	"google.golang.org/api/iterator"
)

func mapSeverity(severity securitycenterpb.Finding_Severity) models.Severity {
	switch severity {
	case securitycenterpb.Finding_CRITICAL:
		return models.SeverityCritical
	case securitycenterpb.Finding_HIGH:
		return models.SeverityHigh
	case securitycenterpb.Finding_MEDIUM:
		return models.SeverityMedium
	case securitycenterpb.Finding_LOW:
		return models.SeverityLow
	case securitycenterpb.Finding_SEVERITY_UNSPECIFIED:
		return models.SeverityInfo
	}
	return models.SeverityInfo
}

func mapState(finding *securitycenterpb.Finding) string {
	if finding.Mute == securitycenterpb.Finding_MUTED {
		return models.AnalysisStatusSilenced
	}
	state := finding.State
	switch state {
	case securitycenterpb.Finding_ACTIVE:
		return models.AnalysisStatusOpen
	case securitycenterpb.Finding_INACTIVE:
		return models.AnalysisStatusResolved
	case securitycenterpb.Finding_STATE_UNSPECIFIED:
		return models.AnalysisStatusOpen
	}
	return models.AnalysisStatusOpen
}

// affectedResource can be in format of
// - //k8s.io/core/v1/namespaces/kube-system/secrets/container-watcher-token (namespace-scoped)
// - //k8s.io/rbac.authorization.k8s.io/v1/clusterroles/crossplane:aggregate-to-admin (cluster-scoped)
// We need to translate this into our kubernetes alias function which we use for matching external id to
// resources scraped via the kubernetes scraper
func mapK8sResourceFromGCP(clusterName, affectedResource string) string {
	prefix := "//k8s.io/"
	if !strings.HasPrefix(affectedResource, prefix) {
		return ""
	}
	path := strings.TrimPrefix(affectedResource, prefix)

	parts := strings.Split(path, "/")
	switch len(parts) {
	case 6:
		// Namespaced resource: {apiGroup}/{apiVersion}/namespaces/{namespace}/{resourceType}/{name}
		kind := pluralToKind(parts[4])
		ns := parts[3]
		name := parts[5]
		return k8sScraper.KubernetesAlias(clusterName, kind, ns, name)
	case 4:
		// Cluster-scoped resource: {apiGroup}/{apiVersion}/{resourceType}/{name}
		kind := pluralToKind(parts[2])
		ns := ""
		name := parts[3]
		return k8sScraper.KubernetesAlias(clusterName, kind, ns, name)
	default:
		return ""
	}
}

func parseFinding(finding *securitycenterpb.ListFindingsResponse_ListFindingsResult) *v1.AnalysisResult {
	analysis := v1.AnalysisResult{
		Analyzer:      finding.Finding.SourceProperties["ScannerName"].GetStringValue(),
		Status:        mapState(finding.Finding),
		Severity:      mapSeverity(finding.Finding.Severity),
		Messages:      []string{finding.Finding.Description, finding.Finding.Category},
		AnalysisType:  models.AnalysisTypeSecurity,
		Source:        "GCP Security Center",
		Summary:       lo.CoalesceOrEmpty(finding.Finding.Description, finding.Finding.Category),
		FirstObserved: lo.ToPtr(finding.Finding.CreateTime.AsTime()),
		LastObserved:  lo.ToPtr(finding.Finding.EventTime.AsTime()),
	}

	if _analysis, err := utils.ToJSONMap(finding); err == nil {
		analysis.Analysis = _analysis
	}

	if ar := finding.Finding.SourceProperties["affectedResources"].GetListValue(); ar != nil {
		for _, v := range ar.Values {
			if resourceName := v.GetStructValue().Fields["gcpResourceName"].GetStringValue(); resourceName != "" {
				labels := make(map[string]string)
				if finding.Resource.Type == "google.container.Cluster" {
					labels["cluster"] = finding.Resource.DisplayName
				}
				if k8sAlias := mapK8sResourceFromGCP(finding.Resource.DisplayName, resourceName); k8sAlias != "" {
					analysis.ExternalConfigs = append(analysis.ExternalConfigs, v1.ExternalID{
						ExternalID: k8sAlias,
						ScraperID:  "all",
						Labels:     labels,
					})
				}
			}
		}
	}

	analysis.ExternalConfigs = append(analysis.ExternalConfigs, v1.ExternalID{ExternalID: finding.Resource.Name})

	return &analysis
}

func (gcp Scraper) ListFindings(ctx *GCPContext, config v1.GCP) (v1.ScrapeResults, error) {
	var results v1.ScrapeResults
	client, err := securitycenter.NewClient(ctx, ctx.ClientOpts...)
	if err != nil {
		return nil, fmt.Errorf("error creating security center client: %w", err)
	}
	defer func() {
		if err := client.Close(); err != nil {
			ctx.Warnf("gcp assets: failed to close security center client: %v", err)
		}
	}()

	req := &securitycenterpb.ListFindingsRequest{
		Parent:   fmt.Sprintf("projects/%s/sources/-", config.Project),
		PageSize: 1000,
	}

	it := client.ListFindings(ctx, req)

	for {
		finding, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("error listing findings: %w", err)
		}

		results = append(results, v1.ScrapeResult{
			AnalysisResult: parseFinding(finding),
		})
	}

	return results, nil
}
