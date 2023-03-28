package azure

import (
	"encoding/json"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/advisor/armadvisor"
	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/config-db/api/v1"
)

func (azure Scraper) fetchAdvisorAnalysis() v1.ScrapeResults {
	advisorClient, err := armadvisor.NewRecommendationsClient(azure.config.SubscriptionID, azure.cred, nil)
	if err != nil {
		return v1.ScrapeResults{v1.ScrapeResult{Error: fmt.Errorf("failed to initiate advisor client: %w", err)}}
	}

	pager := advisorClient.NewListPager(nil)
	var results v1.ScrapeResults
	for pager.More() {
		nextPage, err := pager.NextPage(azure.ctx)
		if err != nil {
			return v1.ScrapeResults{v1.ScrapeResult{Error: fmt.Errorf("failed to read advisor page: %w", err)}}
		}

		for _, check := range nextPage.Value {
			externalType := check.Properties.ImpactedField
			analysis := results.Analysis(deref(check.Name), deref(externalType), deref(check.ID))
			analysis.Severity = mapSeverity(check.Properties.Impact)
			analysis.AnalysisType = mapAnalysisType(check.Properties.Category)
			analysis.Message(deref(check.Properties.Description))
			analysis.Analysis = getMetadata(check.Properties.Metadata)
		}
	}

	return results
}

func getMetadata(input map[string]any) map[string]string {
	var metadata = make(map[string]string, len(input))
	if input == nil {
		return metadata
	}

	for k, v := range input {
		b, err := json.Marshal(v)
		if err != nil {
			logger.Errorf("failed to marshal metadata: [%v] %v", v, err)
			continue
		}

		metadata[k] = string(b)
	}

	return metadata
}

func mapAnalysisType(impactLevel *armadvisor.Category) string {
	if impactLevel == nil {
		return "unknown"
	}

	return string(*impactLevel)
}

func mapSeverity(impactLevel *armadvisor.Impact) string {
	if impactLevel == nil {
		return "unknown"
	}

	switch *impactLevel {
	case armadvisor.ImpactHigh:
		return "critical"
	case armadvisor.ImpactMedium:
		return "warning"
	default:
		return "info"
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}

	return *s
}
