package gcp

import (
	"fmt"

	securitycenter "cloud.google.com/go/securitycenter/apiv1"
	"cloud.google.com/go/securitycenter/apiv1/securitycenterpb"
	v1 "github.com/flanksource/config-db/api/v1"
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

func parseFinding(finding *securitycenterpb.ListFindingsResponse_ListFindingsResult) *v1.AnalysisResult {
	analysis := v1.AnalysisResult{
		Analyzer:      finding.Finding.SourceProperties["ScannerName"].GetStringValue(),
		ConfigType:    fmt.Sprintf("GCP::%s", parseGCPConfigClass(finding.Resource.Type)),
		ExternalID:    finding.Resource.Name,
		Status:        mapState(finding.Finding),
		Severity:      mapSeverity(finding.Finding.Severity),
		Messages:      []string{finding.Finding.Description, finding.Finding.Category},
		AnalysisType:  models.AnalysisTypeSecurity,
		Source:        "GCP Security Center",
		Summary:       lo.CoalesceOrEmpty(finding.Finding.Description, finding.Finding.Category),
		FirstObserved: lo.ToPtr(finding.Finding.CreateTime.AsTime()),
		LastObserved:  lo.ToPtr(finding.Finding.EventTime.AsTime()),
	}

	if _analysis, err := utils.ToJSONMap(finding); err != nil {
		analysis.Analysis = _analysis
	}

	return &analysis
}

func (gcp Scraper) ListFindings(ctx *GCPContext, config v1.GCP) (v1.ScrapeResults, error) {
	var results v1.ScrapeResults
	client, err := securitycenter.NewClient(ctx, ctx.ClientOpts...)
	if err != nil {
		return nil, fmt.Errorf("error creating security center client: %w", err)
	}
	defer client.Close()

	req := &securitycenterpb.ListFindingsRequest{
		Parent: fmt.Sprintf("projects/%s/sources/-", config.Project),
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
