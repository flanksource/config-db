package gcp

import (
	"context"
	"fmt"
	"log"

	securitycenter "cloud.google.com/go/securitycenter/apiv1"
	"cloud.google.com/go/securitycenter/apiv1/securitycenterpb"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty/models"
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

/*
func parseFinding(finding securitycenterpb.ListFindingsResponse_ListFindingsResult) v1.AnalysisResult {
	analysis := v1.AnalysisResult{
		Analyzer:   analyzer,
		ConfigType: configType,
		ExternalID: id,
		Status:     mapState(finding.Finding),
		Severity:   mapSeverity(finding.Finding.Severity),
		Messages:   []string{finding.Finding.Description},
		Source:     "GCP Security Center",
	}
	analysis.Status = models.AnalysisStatusOpen
	analysis.AnalysisType = mapCategoryToAnalysisType(*check.Category)
	analysis.Message(deref(check.Description))
	analysis.Source = "AWS Trusted Advisor"

	if _analysis, err := utils.ToJSONMap(metadata); err != nil {
		analysis.Analysis = _analysis
	}
}
*/

func (gcp Scraper) ListFindings(ctx *GCPContext, config v1.GCP) (v1.ScrapeResults, error) {

	client, err := securitycenter.NewClient(ctx)
	if err != nil {
		log.Fatalf("Failed to create Security Command Center client: %v", err)
	}
	defer client.Close()

	req := &securitycenterpb.ListFindingsRequest{
		Parent: fmt.Sprintf("projects/%s", config.Project),
	}

	it := client.ListFindings(ctx, req)
	count := 0

	for {
		finding, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("error iterating findings: %v", err)
		}

		count++
		printFinding(finding, count)
	}

	fmt.Printf("\nTotal findings/insights found: %d\n", count)
	return nil, nil
}

func printFinding(finding *securitycenterpb.ListFindingsResponse_ListFindingsResult, index int) {
	f := finding.GetFinding()

	fmt.Printf("=== Finding #%d ===\n", index)
	fmt.Printf("Name: %s\n", f.GetName())
	fmt.Printf("Category: %s\n", f.GetCategory())
	fmt.Printf("State: %s\n", f.GetState())
	fmt.Printf("Severity: %s\n", f.GetSeverity())
	fmt.Printf("Resource Name: %s\n", f.GetResourceName())
	fmt.Printf("Create Time: %s\n", f.GetCreateTime().AsTime())
	fmt.Printf("Event Time: %s\n", f.GetEventTime().AsTime())

	if f.GetDescription() != "" {
		fmt.Printf("Description: %s\n", f.GetDescription())
	}

	// Print source properties if available
	if len(f.GetSourceProperties()) > 0 {
		fmt.Println("Source Properties:")
		for key, value := range f.GetSourceProperties() {
			fmt.Printf("  %s: %v\n", key, value)
		}
	}

	// Print security marks if available
	if f.GetSecurityMarks() != nil && len(f.GetSecurityMarks().GetMarks()) > 0 {
		fmt.Println("Security Marks:")
		for key, value := range f.GetSecurityMarks().GetMarks() {
			fmt.Printf("  %s: %s\n", key, value)
		}
	}

	fmt.Println()
}

// Alternative function to list insights with more filtering options
func listInsightsWithFilters(ctx context.Context, client *securitycenter.Client, parent string) error {
	fmt.Printf("Listing filtered insights for: %s\n\n", parent)

	// Example filters you can use:
	filters := []string{
		"state=\"ACTIVE\"",
		"severity=\"HIGH\" OR severity=\"CRITICAL\"",
		"category=\"SUSPICIOUS_ACTIVITY\"",
		"resource.type=\"gce_instance\"",
		// Combine multiple conditions
		"state=\"ACTIVE\" AND (severity=\"HIGH\" OR severity=\"CRITICAL\")",
	}

	for i, filter := range filters {
		fmt.Printf("--- Filter %d: %s ---\n", i+1, filter)

		req := &securitycenterpb.ListFindingsRequest{
			Parent: parent,
			Filter: filter,
		}

		it := client.ListFindings(ctx, req)
		count := 0

		for {
			finding, err := it.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				return fmt.Errorf("error iterating findings with filter '%s': %v", filter, err)
			}

			count++
			f := finding.GetFinding()
			fmt.Printf("%d. %s - %s (%s)\n", count, f.GetCategory(), f.GetResourceName(), f.GetSeverity())
		}

		fmt.Printf("Total findings for this filter: %d\n\n", count)
	}

	return nil
}
