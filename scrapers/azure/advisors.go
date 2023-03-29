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

		for _, recommendation := range nextPage.Value {
			externalID := getResourceID(recommendation.Properties.ResourceMetadata)
			if externalID == "" {
				logger.Warnf("failed to get resource id for recommendation: %v", recommendation)
				continue
			}

			externalType := recommendation.Properties.ImpactedField
			analysis := results.Analysis(deref(recommendation.Type), deref(externalType), externalID)
			analysis.Severity = mapSeverity(recommendation.Properties.Impact)
			analysis.AnalysisType = mapAnalysisType(recommendation.Properties.Category)
			analysis.Message(deref(recommendation.Properties.Description))
			analysis.Analysis = getMetadata(recommendation.Properties.Metadata)
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

func getResourceID(input *armadvisor.ResourceMetadata) string {
	if input == nil {
		return ""
	}

	return deref(input.ResourceID)
}

func mapAnalysisType(impactLevel *armadvisor.Category) string {
	if impactLevel == nil {
		return "other"
	}

	switch *impactLevel {
	case armadvisor.CategoryCost:
		return "cost"
	case armadvisor.CategoryHighAvailability:
		return "availability"
	case armadvisor.CategoryOperationalExcellence:
		return "recommendation"
	case armadvisor.CategoryPerformance:
		return "performance"
	case armadvisor.CategorySecurity:
		return "security"
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
