package azure

import (
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/advisor/armadvisor"
	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty/models"
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
			if recommendation.Properties == nil {
				logger.Warnf("failed to get properties for recommendation: %v", recommendation)
				continue
			}

			externalID := getResourceID(recommendation.Properties.ResourceMetadata)
			if externalID == "" {
				logger.Warnf("failed to get resource id for recommendation: %v", recommendation)
				continue
			}

			configType := getARMType(recommendation.Properties.ImpactedField)
			analysis := results.Analysis(deref(recommendation.Type), configType, externalID)
			analysis.Status = models.AnalysisStatusOpen
			analysis.Severity = mapSeverity(recommendation.Properties.Impact)
			analysis.Source = "Azure Advisor"
			analysis.AnalysisType = mapAnalysisType(recommendation.Properties.Category)
			if recommendation.Properties.ShortDescription != nil {
				problemDesc := deref(recommendation.Properties.ShortDescription.Problem)
				if problemDesc != "" {
					analysis.Analyzer = problemDesc
				}

				analysis.Summary = problemDesc
				analysis.Message(deref(recommendation.Properties.ShortDescription.Problem))
				analysis.Message(deref(recommendation.Properties.ShortDescription.Solution))
			}
			analysis.Message(deref(recommendation.Properties.Description))
			analysis.Analysis = recommendation.Properties.Metadata
		}
	}

	return results
}

func getResourceID(input *armadvisor.ResourceMetadata) string {
	if input == nil {
		return ""
	}

	return getARMID(input.ResourceID)
}

// mapAnalysisType maps the advisor recommendation category to an analysis type.
func mapAnalysisType(impactLevel *armadvisor.Category) models.AnalysisType {
	if impactLevel == nil {
		return models.AnalysisTypeOther
	}

	switch *impactLevel {
	case armadvisor.CategoryCost:
		return models.AnalysisTypeCost
	case armadvisor.CategoryHighAvailability:
		return models.AnalysisTypeAvailability
	case armadvisor.CategoryOperationalExcellence:
		return models.AnalysisTypeRecommendation
	case armadvisor.CategoryPerformance:
		return models.AnalysisTypePerformance
	case armadvisor.CategorySecurity:
		return models.AnalysisTypeSecurity
	default:
		return models.AnalysisTypeOther
	}
}

// mapSeverity maps the advisor impact level to a severity.
func mapSeverity(impactLevel *armadvisor.Impact) models.Severity {
	if impactLevel == nil {
		return models.SeverityLow
	}

	switch *impactLevel {
	case armadvisor.ImpactHigh:
		return models.SeverityHigh
	case armadvisor.ImpactMedium:
		return models.SeverityMedium
	default:
		return models.SeverityLow
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}

	return *s
}
