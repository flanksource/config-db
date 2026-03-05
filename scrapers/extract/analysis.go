package extract

import (
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/scrapers/analysis"
	"github.com/flanksource/duty/models"
)

// ApplyAnalysisRules enriches the analysis result with category and severity from built-in rules.
func ApplyAnalysisRules(result *v1.ScrapeResult) {
	if result.AnalysisResult == nil {
		return
	}
	if rule, ok := analysis.Rules[result.AnalysisResult.Analyzer]; ok {
		result.AnalysisResult.AnalysisType = models.AnalysisType(rule.Category)
		result.AnalysisResult.Severity = models.Severity(rule.Severity)
	}
}
