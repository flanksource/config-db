package scrapers

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/flanksource/commons/collections/syncmap"
	"github.com/flanksource/commons/logger"
	"github.com/flanksource/commons/timer"
	"github.com/flanksource/duty/models"
	"go.opentelemetry.io/otel/attribute"

	v1 "github.com/flanksource/config-db/api"
	"github.com/flanksource/config-db/db"
	"github.com/flanksource/config-db/pkg/api"
	"github.com/flanksource/config-db/scrapers/analysis"
	"github.com/flanksource/config-db/scrapers/processors"
	"github.com/flanksource/config-db/utils"
)

type contextKey string

const (
	contextKeyScrapeStart contextKey = "scrape_start_time"
)

// Cache store to be used by watch jobs
var TempCacheStore syncmap.SyncMap[string, *api.TempCache]

type ScrapeOutput struct {
	Total   int // all configs & changes
	Summary map[string]v1.ConfigTypeScrapeSummary
}

func RunScraper(ctx api.ScrapeContext) (*ScrapeOutput, error) {
	var timer = timer.NewMemoryTimer()
	ctx, err := ctx.InitTempCache()
	if err != nil {
		return nil, err
	}
	TempCacheStore.Store(ctx.ScraperID(), ctx.TempCache())

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

	// In full mode, we extract changes, access logs, config access, external users, external groups, external user groups, and external roles from the config.
	if spec.Full {
		allAccessLogs := []v1.ExternalConfigAccessLog{}
		allConfigAccess := []v1.ExternalConfigAccess{}
		allExternalUsers := []models.ExternalUser{}
		allExternalGroups := []models.ExternalGroup{}
		allExternalUserGroups := []models.ExternalUserGroup{}
		allExternalRoles := []models.ExternalRole{}
		for i := range scraped {
			extracted, err := extractConfigChangesFromConfig(scraped[i].Config)
			if err != nil {
				scraped[i].Error = err
				continue
			}

			for _, accessLog := range extracted.AccessLogs {
				allAccessLogs = append(allAccessLogs, v1.ExternalConfigAccessLog{
					ConfigAccessLog: accessLog,
				})
			}

			allConfigAccess = append(allConfigAccess, extracted.ConfigAccess...)
			allExternalUsers = append(allExternalUsers, extracted.ExternalUsers...)
			allExternalGroups = append(allExternalGroups, extracted.ExternalGroups...)
			allExternalUserGroups = append(allExternalUserGroups, extracted.ExternalUserGroups...)
			allExternalRoles = append(allExternalRoles, extracted.ExternalRoles...)

			for _, cr := range extracted.Changes {
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
			scraped[i].Config = extracted.Config
		}

		if len(allAccessLogs) > 0 {
			result := v1.NewScrapeResult(scraped[0].BaseScraper)
			result.ConfigAccessLogs = allAccessLogs
			scraped = append(scraped, *result)
		}

		if len(allConfigAccess) > 0 {
			result := v1.NewScrapeResult(scraped[0].BaseScraper)
			result.ConfigAccess = allConfigAccess
			scraped = append(scraped, *result)
		}

		if len(allExternalUsers) > 0 {
			result := v1.NewScrapeResult(scraped[0].BaseScraper)
			result.ExternalUsers = allExternalUsers
			scraped = append(scraped, *result)
		}

		if len(allExternalGroups) > 0 {
			result := v1.NewScrapeResult(scraped[0].BaseScraper)
			result.ExternalGroups = allExternalGroups
			scraped = append(scraped, *result)
		}

		if len(allExternalUserGroups) > 0 {
			result := v1.NewScrapeResult(scraped[0].BaseScraper)
			result.ExternalUserGroups = allExternalUserGroups
			scraped = append(scraped, *result)
		}

		if len(allExternalRoles) > 0 {
			result := v1.NewScrapeResult(scraped[0].BaseScraper)
			result.ExternalRoles = allExternalRoles
			scraped = append(scraped, *result)
		}
	}

	return scraped
}

// ExtractedConfig holds the extracted config, changes, access logs, config access,
// external users, external groups, external user groups, and external roles from a scraped config in full mode.
type ExtractedConfig struct {
	Config             any
	Changes            []v1.ChangeResult
	AccessLogs         []models.ConfigAccessLog
	ConfigAccess       []v1.ExternalConfigAccess
	ExternalUsers      []models.ExternalUser
	ExternalGroups     []models.ExternalGroup
	ExternalUserGroups []models.ExternalUserGroup
	ExternalRoles      []models.ExternalRole
}

// extractConfigChangesFromConfig will attempt to extract config, changes,
// access logs, config access, external users, external groups, external user groups, and external roles
// from the scraped config.
//
// The scraped config is expected to have fields "config", "changes",
// "access_logs", "config_access", "external_users", "external_groups", "external_user_groups", and "external_roles".
func extractConfigChangesFromConfig(config any) (ExtractedConfig, error) {
	configMap, ok := config.(map[string]any)
	if !ok {
		return ExtractedConfig{}, errors.New("config is not a map")
	}

	var result ExtractedConfig

	if eConf, ok := configMap["config"]; ok {
		result.Config = eConf
	}

	if changes, ok := configMap["changes"]; ok {
		raw, err := json.Marshal(changes)
		if err != nil {
			return ExtractedConfig{}, fmt.Errorf("failed to marshal changes: %w", err)
		}

		if err := json.Unmarshal(raw, &result.Changes); err != nil {
			return ExtractedConfig{}, fmt.Errorf("failed to unmarshal changes map into []v1.ChangeResult: %w", err)
		}
	}

	if accessLogs, ok := configMap["access_logs"]; ok {
		raw, err := json.Marshal(accessLogs)
		if err != nil {
			return ExtractedConfig{}, fmt.Errorf("failed to marshal access logs: %w", err)
		}

		if err := json.Unmarshal(raw, &result.AccessLogs); err != nil {
			return ExtractedConfig{}, fmt.Errorf("failed to unmarshal access logs into []models.ConfigAccessLog: %w", err)
		}
	}

	if configAccess, ok := configMap["config_access"]; ok {
		raw, err := json.Marshal(configAccess)
		if err != nil {
			return ExtractedConfig{}, fmt.Errorf("failed to marshal config access: %w", err)
		}

		if err := json.Unmarshal(raw, &result.ConfigAccess); err != nil {
			return ExtractedConfig{}, fmt.Errorf("failed to unmarshal config access into []v1.ExternalConfigAccess: %w", err)
		}
	}

	if externalUsers, ok := configMap["external_users"]; ok {
		raw, err := json.Marshal(externalUsers)
		if err != nil {
			return ExtractedConfig{}, fmt.Errorf("failed to marshal external users: %w", err)
		}

		if err := json.Unmarshal(raw, &result.ExternalUsers); err != nil {
			return ExtractedConfig{}, fmt.Errorf("failed to unmarshal external users into []models.ExternalUser: %w", err)
		}
	}

	if externalGroups, ok := configMap["external_groups"]; ok {
		raw, err := json.Marshal(externalGroups)
		if err != nil {
			return ExtractedConfig{}, fmt.Errorf("failed to marshal external groups: %w", err)
		}

		if err := json.Unmarshal(raw, &result.ExternalGroups); err != nil {
			return ExtractedConfig{}, fmt.Errorf("failed to unmarshal external groups into []models.ExternalGroup: %w", err)
		}
	}

	if externalUserGroups, ok := configMap["external_user_groups"]; ok {
		raw, err := json.Marshal(externalUserGroups)
		if err != nil {
			return ExtractedConfig{}, fmt.Errorf("failed to marshal external user groups: %w", err)
		}

		if err := json.Unmarshal(raw, &result.ExternalUserGroups); err != nil {
			return ExtractedConfig{}, fmt.Errorf("failed to unmarshal external user groups into []models.ExternalUserGroup: %w", err)
		}
	}

	if externalRoles, ok := configMap["external_roles"]; ok {
		raw, err := json.Marshal(externalRoles)
		if err != nil {
			return ExtractedConfig{}, fmt.Errorf("failed to marshal external roles: %w", err)
		}

		if err := json.Unmarshal(raw, &result.ExternalRoles); err != nil {
			return ExtractedConfig{}, fmt.Errorf("failed to unmarshal external roles into []models.ExternalRole: %w", err)
		}
	}

	return result, nil
}
