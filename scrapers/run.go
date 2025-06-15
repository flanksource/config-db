package scrapers

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/flanksource/commons/logger"
	"github.com/flanksource/commons/timer"
	"github.com/flanksource/duty/models"
	"go.opentelemetry.io/otel/attribute"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
	"github.com/flanksource/config-db/scrapers/analysis"
	"github.com/flanksource/config-db/scrapers/processors"
	"github.com/flanksource/config-db/utils"
)

type contextKey string

const (
	contextKeyScrapeStart contextKey = "scrape_start_time"
)

// Cache store to be used by watch jobs
var TempCacheStore = make(map[string]*api.TempCache)

type ScrapeOutput struct {
	Total   int // all configs & changes
	Summary map[string]v1.ConfigTypeScrapeSummary
}

func RunScraper(ctx api.ScrapeContext) (*ScrapeOutput, error) {
	var timer = timer.NewMemoryTimer()
	ctx, err := ctx.InitTempCache()
	TempCacheStore[ctx.ScraperID()] = ctx.TempCache()
	if err != nil {
		return nil, err
	}

	ctx = ctx.WithValue(contextKeyScrapeStart, time.Now())
	ctx.Context = ctx.
		WithName(fmt.Sprintf("%s/%s", ctx.ScrapeConfig().Namespace, ctx.ScrapeConfig().Name)).
		WithNamespace(ctx.ScrapeConfig().Namespace)

	results, scraperErr := Run(ctx)
	if scraperErr != nil {
		return nil, fmt.Errorf("failed to run scraper %v: %w", ctx.ScrapeConfig().Name, scraperErr)
	}

	savedResult, err := db.SaveResults(ctx, results)
	if err != nil {
		return nil, fmt.Errorf("failed to save results: %w", err)
	}

	if err := UpdateStaleConfigItems(ctx, results); err != nil {
		return nil, fmt.Errorf("failed to update stale config items: %w", err)
	}

	ctx.Logger.Debugf("Completed scrape with %s in %s", savedResult, timer.End())

	return &ScrapeOutput{
		Total:   len(results),
		Summary: savedResult,
	}, nil
}

func UpdateStaleConfigItems(ctx api.ScrapeContext, results v1.ScrapeResults) error {
	basectx, span := ctx.StartSpan("UpdateStaleConfigItems")
	defer span.End()

	ctx.Context = basectx

	persistedID := ctx.ScrapeConfig().GetPersistedID()
	if persistedID != nil {
		ctx.GetSpan().SetAttributes(
			attribute.Int("scrape.results", len(results)),
			attribute.Bool("scrape.hasError", v1.ScrapeResults(results).HasErr()),
		)

		// If error in any of the scrape results, don't delete old items
		if len(results) > 0 && !v1.ScrapeResults(results).HasErr() {
			staleTimeout := ctx.ScrapeConfig().Spec.Retention.StaleItemAge
			if _, err := DeleteStaleConfigItems(ctx.DutyContext(), staleTimeout, *persistedID); err != nil {
				return fmt.Errorf("error deleting stale config items: %w", err)
			}
		}
	}

	return nil
}

// Run ...
func Run(ctx api.ScrapeContext) ([]v1.ScrapeResult, error) {
	plugins, err := db.LoadAllPlugins(ctx.DutyContext())
	if err != nil {
		return nil, ctx.Oops().Wrapf(err, "failed to load plugins")
	}

	var results v1.ScrapeResults
	for _, scraper := range All {
		if !scraper.CanScrape(ctx.ScrapeConfig().Spec) {
			continue
		}

		ctx = ctx.WithScrapeConfig(ctx.ScrapeConfig(), plugins...)

		ctx.Debugf("Starting scraper")
		for _, result := range scraper.Scrape(ctx) {
			scraped := processScrapeResult(ctx, result)

			for i := range scraped {
				if scraped[i].Error != nil {
					ctx.Errorf("Error scraping %s: %v", scraped[i].ID, scraped[i].Error)
					ctx.JobHistory().AddError(scraped[i].Error.Error())
				}
			}

			if !scraped.HasErr() {
				ctx.JobHistory().IncrSuccess()
			}

			results = append(results, scraped...)
		}
	}

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
func processScrapeResult(ctx api.ScrapeContext, result v1.ScrapeResult) v1.ScrapeResults {
	spec := ctx.ScrapeConfig().Spec

	if result.AnalysisResult != nil {
		if rule, ok := analysis.Rules[result.AnalysisResult.Analyzer]; ok {
			result.AnalysisResult.AnalysisType = models.AnalysisType(rule.Category)
			result.AnalysisResult.Severity = models.Severity(rule.Severity)
		}
	}

	// TODO: Decide if this can be removed here. It's newly placed on func updateChange.
	// changes.ProcessRules(&result, result.BaseScraper.Transform.Change.Mapping...)

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

	scraped, err := extractor.Extract(ctx, result)
	if err != nil {
		result.Error = err
		return []v1.ScrapeResult{result}
	}

	// In full mode, we extract changes & access logs from the config.
	if spec.Full {
		allAccessLogs := []v1.ExternalConfigAccessLog{}
		for i := range scraped {
			extractedConfig, changeRes, accessLogs, err := extractConfigChangesFromConfig(scraped[i].Config)
			if err != nil {
				scraped[i].Error = err
				continue
			}

			for _, accessLog := range accessLogs {
				allAccessLogs = append(allAccessLogs, v1.ExternalConfigAccessLog{
					ConfigAccessLog: accessLog,
				})
			}

			for _, cr := range changeRes {
				if cr.ExternalID == "" {
					cr.ExternalID = scraped[i].ID
				}

				if cr.ConfigType == "" {
					cr.ConfigType = scraped[i].Type
				}

				if cr.ExternalID == "" && cr.ConfigType == "" {
					continue
				}

				scraped[i].Changes = append(scraped[i].Changes, cr)
			}

			// The original config should be replaced by the extracted config (could also be nil)
			scraped[i].Config = extractedConfig
		}

		if len(allAccessLogs) > 0 {
			result := v1.NewScrapeResult(scraped[0].BaseScraper)
			result.ConfigAccessLogs = allAccessLogs
			scraped = append(scraped, *result)
		}

		return scraped
	}

	return scraped
}

// extractChangesFromConfig will attempt to extract config & changes from
// the scraped config.
//
// The scraped config is expected to have fields "config" & "changes".
func extractConfigChangesFromConfig(config any) (any, []v1.ChangeResult, []models.ConfigAccessLog, error) {
	configMap, ok := config.(map[string]any)
	if !ok {
		return nil, nil, nil, errors.New("config is not a map")
	}

	var (
		extractedConfig     any
		extractedChanges    []v1.ChangeResult
		extractedAccessLogs []models.ConfigAccessLog
	)

	if eConf, ok := configMap["config"]; ok {
		extractedConfig = eConf
	}

	if changes, ok := configMap["changes"]; ok {
		raw, err := json.Marshal(changes)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to marshal changes: %w", err)
		}

		if err := json.Unmarshal(raw, &extractedChanges); err != nil {
			return nil, nil, nil, fmt.Errorf("failed to unmarshal changes map into []v1.ChangeResult: %w", err)
		}
	}

	if accessLogs, ok := configMap["access_logs"]; ok {
		raw, err := json.Marshal(accessLogs)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to marshal access logs: %w", err)
		}

		if err := json.Unmarshal(raw, &extractedAccessLogs); err != nil {
			return nil, nil, nil, fmt.Errorf("failed to unmarshal access logs into []models.ConfigAccessLog: %w", err)
		}
	}

	return extractedConfig, extractedChanges, extractedAccessLogs, nil
}
