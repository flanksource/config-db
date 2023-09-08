package scrapers

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
	"github.com/flanksource/config-db/scrapers/analysis"
	"github.com/flanksource/config-db/scrapers/changes"
	"github.com/flanksource/config-db/scrapers/processors"
	"github.com/flanksource/config-db/utils"
	"github.com/flanksource/duty/models"
)

func RunSome(ctx *v1.ScrapeContext, scraper v1.TargettedScraper, configIndex int, ids []string) ([]v1.ScrapeResult, error) {
	jobHistory := models.JobHistory{
		Name:         fmt.Sprintf("scraper:%T", scraper),
		ResourceType: "config_scraper",
		ResourceID:   string(ctx.ScrapeConfig.GetUID()),
	}

	jobHistory.Start()
	if err := db.PersistJobHistory(&jobHistory); err != nil {
		logger.Errorf("Error persisting job history: %v", err)
	}

	var results v1.ScrapeResults
	for _, result := range scraper.ScrapeSome(ctx, configIndex, ids) {
		scraped := processScrapeResult(ctx.ScrapeConfig.Spec, result)

		for i := range scraped {
			if scraped[i].Error != nil {
				logger.Errorf("Error scraping %s: %v", scraped[i].ID, scraped[i].Error)
				jobHistory.AddError(scraped[i].Error.Error())
			}
		}

		if !scraped.HasErr() {
			jobHistory.IncrSuccess()
		}

		results = append(results, scraped...)
	}

	jobHistory.End()
	if err := db.PersistJobHistory(&jobHistory); err != nil {
		logger.Errorf("Error persisting job history: %v", err)
	}

	return results, nil
}

// Run ...
func Run(ctx *v1.ScrapeContext) ([]v1.ScrapeResult, error) {
	cwd, _ := os.Getwd()
	logger.Infof("Scraping configs from (PWD: %s)", cwd)

	var results v1.ScrapeResults
	for _, scraper := range All {
		if !scraper.CanScrape(ctx.ScrapeConfig.Spec) {
			continue
		}

		jobHistory := models.JobHistory{
			Name:         fmt.Sprintf("scraper:%T", scraper),
			ResourceType: "config_scraper",
			ResourceID:   string(ctx.ScrapeConfig.GetUID()),
		}

		jobHistory.Start()
		if err := db.PersistJobHistory(&jobHistory); err != nil {
			logger.Errorf("Error persisting job history: %v", err)
		}

		logger.Debugf("Starting to scrape [%s]", jobHistory.Name)
		for _, result := range scraper.Scrape(ctx) {
			scraped := processScrapeResult(ctx.ScrapeConfig.Spec, result)

			for i := range scraped {
				if scraped[i].Error != nil {
					logger.Errorf("Error scraping %s: %v", scraped[i].ID, scraped[i].Error)
					jobHistory.AddError(scraped[i].Error.Error())
				}
			}

			if !scraped.HasErr() {
				jobHistory.IncrSuccess()
			}

			results = append(results, scraped...)
		}

		jobHistory.End()
		if err := db.PersistJobHistory(&jobHistory); err != nil {
			logger.Errorf("Error persisting job history: %v", err)
		}
	}

	logger.Infof("Completed scraping with %d results.", len(results))
	return results, nil
}

// Add a list of changed json paths. If multiple changed then the highest level.
func summarizeChanges(changes []v1.ChangeResult) []v1.ChangeResult {
	for i, change := range changes {
		if change.Patches == "" {
			continue
		}

		var patch map[string]any
		if err := json.Unmarshal([]byte(change.Patches), &patch); err != nil {
			logger.Errorf("failed to unmarshal patches as map[string]any: %v %v", change.Patches, err)
			continue
		}

		paths := utils.ExtractLeafNodesAndCommonParents(patch)
		if len(paths) == 0 {
			continue
		}

		changes[i].Summary += strings.Join(paths, ", ")
	}

	return changes
}

// processScrapeResult extracts possibly more configs from the result
func processScrapeResult(config v1.ScraperSpec, result v1.ScrapeResult) v1.ScrapeResults {
	if result.AnalysisResult != nil {
		if rule, ok := analysis.Rules[result.AnalysisResult.Analyzer]; ok {
			result.AnalysisResult.AnalysisType = models.AnalysisType(rule.Category)
			result.AnalysisResult.Severity = models.Severity(rule.Severity)
		}
	}

	changes.ProcessRules(&result)

	result.Changes = summarizeChanges(result.Changes)

	// No config means we don't need to extract anything
	if result.Config == nil {
		return []v1.ScrapeResult{result}
	}

	extractor, err := processors.NewExtractor(result.BaseScraper)
	if err != nil {
		result.Error = err
		return []v1.ScrapeResult{result}
	}

	scraped, err := extractor.Extract(result)
	if err != nil {
		result.Error = err
		return []v1.ScrapeResult{result}
	}

	// In full mode, we extract all configs and changes from the result.
	if config.Full {
		for i := range scraped {
			extractedConfig, changeRes, err := extractConfigChangesFromConfig(scraped[i].Config)
			if err != nil {
				scraped[i].Error = err
				continue
			}

			for _, cr := range changeRes {
				cr.ExternalID = scraped[i].ID
				cr.ConfigType = scraped[i].Type

				if cr.ExternalID == "" && cr.ConfigType == "" {
					continue
				}
				scraped[i].Changes = append(scraped[i].Changes, cr)
			}

			// The original config should be replaced by the extracted config (could also be nil)
			scraped[i].Config = extractedConfig
		}

		return scraped
	}

	return scraped
}

// extractChangesFromConfig will attempt to extract config & changes from
// the scraped config.
//
// The scraped config is expected to have fields "config" & "changes".
func extractConfigChangesFromConfig(config any) (any, []v1.ChangeResult, error) {
	configMap, ok := config.(map[string]any)
	if !ok {
		return nil, nil, errors.New("config is not a map")
	}

	var (
		extractedConfig  any
		extractedChanges []v1.ChangeResult
	)

	if eConf, ok := configMap["config"]; ok {
		extractedConfig = eConf
	}

	changes, ok := configMap["changes"].([]any)
	if !ok {
		return nil, nil, errors.New("changes is not a slice of map")
	}

	raw, err := json.Marshal(changes)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal changes: %v", err)
	}

	if err := json.Unmarshal(raw, &extractedChanges); err != nil {
		return nil, nil, fmt.Errorf("failed to unmarshal changes map into []v1.ChangeResult: %v", err)
	}

	return extractedConfig, extractedChanges, nil
}
