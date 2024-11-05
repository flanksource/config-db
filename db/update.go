package db

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aws/smithy-go/ptr"
	jsonpatch "github.com/evanphx/json-patch"
	"github.com/flanksource/commons/collections"
	"github.com/flanksource/commons/logger"
	"github.com/flanksource/commons/text"
	"github.com/flanksource/commons/timer"
	cUtils "github.com/flanksource/commons/utils"
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	pkgChanges "github.com/flanksource/config-db/changes"
	"github.com/flanksource/config-db/db/models"
	"github.com/flanksource/config-db/db/ulid"
	"github.com/flanksource/config-db/scrapers/changes"
	"github.com/flanksource/config-db/utils"
	dutyContext "github.com/flanksource/duty/context"
	dutydb "github.com/flanksource/duty/db"
	dutyModels "github.com/flanksource/duty/models"
	"github.com/flanksource/gomplate/v3"
	"github.com/flanksource/is-healthy/events"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/patrickmn/go-cache"
	"github.com/pkg/errors"
	"github.com/robfig/cron/v3"
	"github.com/samber/lo"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const configItemsBulkInsertSize = 200

// parentCache stores child -> parent relationship
// derived from children lookup hooks.
//
// This is to cater to cases where only the parent has the knowledge of its direct child.
// During incremental scrape, the child can look into this cache to find its parent
// (that would have been set in a full scape).

var parentCache = cache.New(time.Hour*24, time.Hour*24)

func deleteChangeHandler(ctx api.ScrapeContext, change v1.ChangeResult) error {
	var deletedAt interface{}
	if change.CreatedAt != nil && !change.CreatedAt.IsZero() {
		deletedAt = change.CreatedAt
	} else {
		deletedAt = gorm.Expr("NOW()")
	}

	configs := []models.ConfigItem{}
	tx := ctx.DB().Model(&configs).
		Clauses(clause.Returning{Columns: []clause.Column{{Name: "id"}}}).
		Where("type = ? and external_id  @> ?", change.ConfigType, pq.StringArray{change.ExternalID}).
		Update("deleted_at", deletedAt)

	if tx.Error != nil {
		return errors.Wrapf(tx.Error, "unable to delete config item %s/%s", change.ConfigType, change.ExternalID)
	}
	if tx.RowsAffected == 0 || len(configs) == 0 {
		if ctx.PropertyOn(false, "log.missing") {
			ctx.Logger.V(2).Infof("attempt to delete non-existent config item %s/%s: %v", change.ConfigType, change.ExternalID, change.Details)
		}
		return nil
	}

	ctx.Logger.V(3).Infof("deleted %s from change %s", configs[0].ID, change)
	return nil
}

func stringEqual(a, b *string) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func mapStringEqual(a, b map[string]string) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if (b)[k] != v {
			return false
		}
	}
	return true
}

func mapEqual(a, b map[string]any) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func updateCI(ctx api.ScrapeContext, summary *v1.ScrapeSummary, result v1.ScrapeResult, ci, existing *models.ConfigItem) (bool, []*models.ConfigChange, error) {
	ci.ID = existing.ID
	updates := make(map[string]interface{})
	changes := make([]*models.ConfigChange, 0)

	if lo.FromPtr(ci.DeletedAt) != lo.FromPtr(existing.DeletedAt) {
		updates["deleted_at"] = ci.DeletedAt
		updates["delete_reason"] = ci.DeleteReason
	} else if existing.DeletedAt != nil && ci.DeletedAt == nil {
		// item was previously deleted but is now being restored
		updates["deleted_at"] = gorm.Expr("NULL")
		updates["delete_reason"] = gorm.Expr("NULL")
	}

	changeResult, err := generateConfigChange(ctx, *ci, *existing)
	if err != nil {
		ctx.Errorf("[%s] failed to check for changes: %v", ci, err)
	} else if changeResult != nil {
		if ctx.Logger.V(5).Enabled() {
			ctx.Logger.V(5).Infof("[%s/%s] detected changes %v", ci.Type, ci.ExternalID[0], lo.FromPtr(changeResult.Diff))
		} else {
			ctx.Logger.V(3).Infof("[%s/%s] detected changes", ci.Type, ci.ExternalID[0])

		}
		result.Changes = []v1.ChangeResult{*changeResult}
		if newChanges, _, _, err := extractChanges(ctx, &result, ci); err != nil {
			return false, nil, err
		} else {
			changes = append(changes, newChanges...)
		}

		if lo.IsEmpty(ci.Config) || lo.FromPtr(ci.Config) == "null" {
			ctx.Warnf("config is empty, skipping update: %s", *ci)
			return false, nil, nil
		}
		updates["config"] = *ci.Config
		updates["updated_at"] = gorm.Expr("NOW()")
	}

	previousHealth := lo.CoalesceOrEmpty(lo.FromPtr(existing.Health), dutyModels.HealthUnknown)
	newHealth := lo.CoalesceOrEmpty(lo.FromPtr(ci.Health), dutyModels.HealthUnknown)
	if previousHealth != newHealth {
		// For every health change, we add a config change.
		healthChange := &models.ConfigChange{
			ConfigID:   ci.ID,
			ChangeType: lo.PascalCase(string(newHealth)),
			Source:     "config-db",
			Count:      1,
			Details: map[string]any{
				"previous": map[string]any{
					"status":      existing.Status,
					"ready":       existing.Ready,
					"description": existing.Description,
				},
				"current": map[string]any{
					"status":      ci.Status,
					"ready":       ci.Ready,
					"description": ci.Description,
				},
			},
		}

		if lo.FromPtr(ci.Status) != "" {
			healthChange.Summary = *ci.Status
		}

		if lo.FromPtr(ci.Description) != "" {
			healthChange.Summary += fmt.Sprintf(": %s", *ci.Description)
		}

		if newHealth == dutyModels.HealthUnknown {
			healthChange.ChangeType = "HealthUnknown"
		}

		switch newHealth {
		case dutyModels.HealthHealthy:
			healthChange.Severity = string(dutyModels.SeverityInfo)
		case dutyModels.HealthUnhealthy:
			healthChange.Severity = string(dutyModels.SeverityMedium)
		case dutyModels.HealthWarning:
			healthChange.Severity = string(dutyModels.SeverityLow)
		case dutyModels.HealthUnknown:
			healthChange.Severity = string(dutyModels.SeverityInfo)
		}

		changes = append(changes, healthChange)
	}

	if ci.ConfigClass != existing.ConfigClass {
		updates["config_class"] = ci.ConfigClass
	}
	if !stringEqual(ci.Source, existing.Source) {
		updates["source"] = ci.Source
	}
	if ci.Type != existing.Type {
		updates["type"] = ci.Type
	}
	if !stringEqual(ci.Status, existing.Status) {
		updates["status"] = ci.Status
	}
	if !stringEqual(ci.Description, existing.Description) {
		updates["description"] = ci.Description
	}
	if ci.Ready != existing.Ready {
		updates["ready"] = ci.Ready
	}
	if lo.FromPtr(ci.Health) != lo.FromPtr(existing.Health) {
		if lo.FromPtr(ci.Health) == "" {
			updates["health"] = dutyModels.HealthUnknown
		} else {
			updates["health"] = ci.Health
		}
	}
	if !stringEqual(ci.Name, existing.Name) {
		updates["name"] = ci.Name
	}

	if !stringEqual(ci.ParentID, existing.ParentID) {
		updates["parent_id"] = ci.ParentID
	}

	if ci.Path != existing.Path {
		updates["path"] = ci.Path
	}

	if ci.CreatedAt.IsZero() && existing.CreatedAt.IsZero() {
		updates["created_at"] = gorm.Expr("NOW()")
	} else if ci.CreatedAt != existing.CreatedAt && !ci.CreatedAt.IsZero() {
		updates["created_at"] = ci.CreatedAt
	}

	// Order of externalID matters
	if !slices.Equal(ci.ExternalID, existing.ExternalID) {
		updates["external_id"] = ci.ExternalID
	}
	if !mapStringEqual(lo.FromPtr(ci.Labels), lo.FromPtr(existing.Labels)) {
		updates["labels"] = ci.Labels
	}
	if !mapStringEqual(existing.Tags, ci.Tags) {
		updates["tags"] = ci.Tags
	}

	// This could happen when kubernetes scrapers are replaced and scrape
	// same config items
	if lo.FromPtr(existing.ScraperID) != lo.FromPtr(ci.ScraperID) {
		updates["scraper_id"] = ci.ScraperID
		summary.AddWarning(ci.Type, fmt.Sprintf("updated scraper_id of config[%s] from %s to %s", ci, existing.ScraperID, ci.ScraperID))
	}

	if ci.Properties != nil && len(*ci.Properties) > 0 && (existing.Properties == nil || !mapEqual(ci.Properties.AsMap(), existing.Properties.AsMap())) {
		updates["properties"] = *ci.Properties
	}

	if len(updates) == 0 {
		return false, changes, nil
	}

	updates["last_scraped_time"] = gorm.Expr("NOW()")
	if err := ctx.DutyContext().DB().Model(ci).Updates(updates).Error; err != nil {
		return false, nil, errors.Wrapf(dutydb.ErrorDetails(err), "unable to update config item: %s", ci)
	}

	return true, changes, nil
}

func shouldExcludeChange(ctx api.ScrapeContext, result *v1.ScrapeResult, changeResult v1.ChangeResult) (bool, error) {
	exclusions := result.BaseScraper.Transform.Change.Exclude

	env := changeResult.AsMap()
	env["config"] = result.Config
	// In some cases, we might just get the change result but not the config
	// so we fetch config here
	if env["config"] == nil {
		ciID := lo.CoalesceOrEmpty(lo.FromPtr(result.ConfigID), changeResult.ConfigID, changeResult.ExternalID)
		confObj, err := ctx.TempCache().Get(ctx, ciID)
		if err != nil && ctx.PropertyOn(true, "log.changes.unmatched") {
			ctx.Errorf("error finding config object with id[%s] for change exclusion: %v", ciID, err)
		} else if confObj != nil && confObj.Config != nil {
			env["config"] = lo.FromPtr(confObj.Config)
		}
	}

	for _, expr := range exclusions {
		if res, err := gomplate.RunTemplate(env, gomplate.Template{Expression: expr}); err != nil {
			return false, fmt.Errorf("[%s] change exclusion expression failed (%s): %w", changeResult, expr, err)
		} else if skipChange, err := strconv.ParseBool(res); err != nil {
			return false, fmt.Errorf("change exclusion expression(%s) didn't evaluate to a boolean: %w", expr, err)
		} else if skipChange {
			return true, nil
		}
	}

	return false, nil
}

func extractChanges(ctx api.ScrapeContext, result *v1.ScrapeResult, ci *models.ConfigItem) ([]*models.ConfigChange, []*models.ConfigChange, v1.ChangeSummary, error) {
	var (
		changeSummary v1.ChangeSummary

		newOnes = []*models.ConfigChange{}
		updates = []*models.ConfigChange{}
	)
	logUnmatched := ctx.PropertyOn(true, "log.changes.unmatched")

	logExclusions := ctx.PropertyOn(false, "log.exclusions")

	if err := changes.ProcessRules(ctx, result, result.BaseScraper.Transform.Change.Mapping...); err != nil {
		ctx.JobHistory().AddError(fmt.Sprintf("error running change mapping transformation: %v", err))
	}

	for _, changeResult := range result.Changes {
		if changeResult.Action == v1.Ignore {
			changeSummary.AddIgnored(changeResult.ChangeType)
			continue
		}

		if changeResult.ConfigID != "" {
			if _, ok := orphanCache.Get(changeResult.ConfigID); ok {
				changeSummary.AddOrphaned(changeResult.ChangeType)
				continue
			}
		}

		if changeResult.ExternalID != "" {
			if _, ok := orphanCache.Get(changeResult.ExternalID); ok {
				changeSummary.AddOrphaned(changeResult.ChangeType)
				continue
			}
		}

		if exclude, err := shouldExcludeChange(ctx, result, changeResult); err != nil {
			ctx.JobHistory().AddError(fmt.Sprintf("error running change exclusion: %v", err))
		} else if exclude {
			changeSummary.AddIgnored(changeResult.ChangeType)
			if logExclusions {
				ctx.Logger.V(3).Infof("excluded change: %v", changeResult)
			}
			continue
		}

		if changeResult.Action == v1.Delete {
			if err := deleteChangeHandler(ctx, changeResult); err != nil {
				return nil, nil, changeSummary, fmt.Errorf("failed to delete config from change: %w", err)
			}
		}

		change := models.NewConfigChangeFromV1(*result, changeResult)
		if fingerprint, err := pkgChanges.Fingerprint(change); err != nil {
			logger.Errorf("failed to fingerprint change: %v", err)
		} else if fingerprint != "" {
			change.Fingerprint = &fingerprint
		}

		if change.CreatedBy != nil {
			person, err := FindPersonByEmail(ctx, ptr.ToString(change.CreatedBy))
			if err != nil {
				return nil, nil, changeSummary, fmt.Errorf("error finding person by email: %w", err)
			} else if person != nil {
				change.CreatedBy = ptr.String(person.ID.String())
			} else {
				change.ExternalCreatedBy = change.CreatedBy
				change.CreatedBy = nil
			}
		}

		if change.ConfigID == "" && change.GetExternalID().IsEmpty() && ci != nil {
			change.ConfigID = ci.ID
		} else if !change.GetExternalID().IsEmpty() {
			if ci, err := ctx.TempCache().FindExternalID(ctx, change.GetExternalID()); err != nil {
				return nil, nil, changeSummary, fmt.Errorf("failed to get config from change (externalID=%s): %w", change.GetExternalID(), err)
			} else if ci != "" {
				change.ConfigID = ci
			}
		}

		if change.Severity == "" && change.ChangeType != v1.ChangeTypeDiff {
			change.Severity = events.GetSeverity(change.ChangeType)
		}

		if change.ConfigID == "" {
			// Some scrapers can generate changes for config items that don't exist on our db.
			// Example: Cloudtrail scraper reporting changes for a resource that has been excluded.
			changeSummary.AddOrphaned(changeResult.ChangeType)

			if change.ExternalID != "" {
				orphanCache.Set(change.ExternalID, true, 0)
			}

			if logUnmatched {
				ctx.Logger.V(2).Infof("change doesn't have an associated config (type=%s source=%s external_id=%s)", change.ChangeType, change.Source, change.GetExternalID())
			}

			continue
		}

		if changeResult.UpdateExisting {
			updates = append(updates, change)
		} else {
			newOnes = append(newOnes, change)
		}
	}

	return newOnes, updates, changeSummary, nil
}

var orphanCache = cache.New(60*time.Minute, 10*time.Minute)

func upsertAnalysis(ctx api.ScrapeContext, result *v1.ScrapeResult) error {
	analysis := result.AnalysisResult.ToConfigAnalysis()
	ciID, err := ctx.TempCache().Find(ctx, v1.ExternalID{ConfigType: analysis.ConfigType, ExternalID: analysis.ExternalID})
	if err != nil {
		return err
	} else if ciID == nil {
		if ctx.PropertyOn(false, "log.missing") {
			ctx.Debugf("unable to find config item for analysis: (source=%s, configType=%s, externalID=%s, analysis: %+v)", analysis.Source, analysis.ConfigType, analysis.ExternalID, analysis)
		}

		return nil
	}

	analysis.ConfigID = uuid.MustParse(ciID.ID)
	analysis.ID = uuid.MustParse(ulid.MustNew().AsUUID())
	analysis.ScraperID = ctx.ScrapeConfig().GetPersistedID()
	if analysis.Status == "" {
		analysis.Status = dutyModels.AnalysisStatusOpen
	}

	return CreateAnalysis(ctx, analysis)
}

func GetCurrentDBTime(ctx api.ScrapeContext) (time.Time, error) {
	var now time.Time
	err := ctx.DB().Raw(`SELECT CURRENT_TIMESTAMP`).Scan(&now).Error
	return now, err
}

// UpdateAnalysisStatusBefore updates the status of config analyses that were last observed before the specified time.
func UpdateAnalysisStatusBefore(ctx api.ScrapeContext, before time.Time, scraperID, status string) error {
	return ctx.DB().
		Model(&dutyModels.ConfigAnalysis{}).
		Where("last_observed <= ? AND first_observed <= ?", before, before).
		Where("scraper_id = ?", scraperID).
		Update("status", status).
		Error
}

// SaveResults creates or update a configuration with config changes
func SaveResults(ctx api.ScrapeContext, results []v1.ScrapeResult) (v1.ScrapeSummary, error) {
	return saveResults(ctx, results)
}

func syncCRDChanges(ctx api.ScrapeContext, configs []*models.ConfigItem) error {
	for _, config := range configs {
		if !strings.HasPrefix(config.Type, api.MissionControlConfigTypePrefix) {
			continue
		}

		if config.Name == nil || *config.Name == "" || config.Config == nil {
			continue
		}

		ctx.Logger.V(3).Infof("syncing mission control crd: %s", config)

		namespace := ctx.ScrapeConfig().Namespace
		if ns := config.Tags["namespace"]; ns != "" {
			namespace = ns
		}

		var obj unstructured.Unstructured
		if err := obj.UnmarshalJSON([]byte(*config.Config)); err != nil {
			return fmt.Errorf("config (%s) is not a kubernetes resource: %w", *config.Name, err)
		}

		spec, err := json.Marshal(obj.Object["spec"])
		if err != nil {
			return err
		}

		switch strings.TrimPrefix(config.Type, api.MissionControlConfigTypePrefix) {
		case "ScrapeConfig":
			scrapeConfig := dutyModels.ConfigScraper{
				Name:   fmt.Sprintf("%s/%s", namespace, *config.Name),
				Spec:   string(spec),
				Source: dutyModels.SourceCRDSync,
			}

			if parsed, err := uuid.Parse(config.ID); err != nil {
				scrapeConfig.ID = parsed
			}

			if description, _, err := unstructured.NestedString(obj.Object, "spec", "description"); err != nil {
				return err
			} else if description != "" {
				scrapeConfig.Description = description
			}

			if err := ctx.DB().Save(&scrapeConfig).Error; err != nil {
				return err
			}

		case "Playbook":
			playbook := dutyModels.Playbook{
				Namespace: namespace,
				Name:      *config.Name,
				Spec:      spec,
				Source:    dutyModels.SourceCRDSync,
			}

			if title, _, err := unstructured.NestedString(obj.Object, "spec", "title"); err != nil {
				return err
			} else if title != "" {
				playbook.Title = title
			}

			if category, _, err := unstructured.NestedString(obj.Object, "spec", "category"); err != nil {
				return err
			} else if category != "" {
				playbook.Category = category
			}

			if description, _, err := unstructured.NestedString(obj.Object, "spec", "description"); err != nil {
				return err
			} else if description != "" {
				playbook.Description = description
			}

			if parsed, err := uuid.Parse(config.ID); err == nil {
				playbook.ID = parsed
			}

			if err := ctx.DB().Save(&playbook).Error; err != nil {
				return err
			}

		case "Canary":
			canary := dutyModels.Canary{
				Name:      *config.Name,
				Namespace: namespace,
				Spec:      spec,
				Source:    dutyModels.SourceCRDSync,
			}

			if parsed, err := uuid.Parse(config.ID); err != nil {
				canary.ID = parsed
			}

			if err := ctx.DB().Save(&canary).Error; err != nil {
				return err
			}

		case "Notification", "Connection":
			ctx.Logger.Warnf("CRDSync doesn't support notifications and connections")
		}
	}

	return nil
}

func saveResults(ctx api.ScrapeContext, results []v1.ScrapeResult) (v1.ScrapeSummary, error) {
	var summary = make(v1.ScrapeSummary)

	if len(results) == 0 {
		return summary, nil
	}

	startTime, err := GetCurrentDBTime(ctx)
	if err != nil {
		return summary, fmt.Errorf("unable to get current db time: %w", err)
	}

	extractResult, err := extractConfigsAndChangesFromResults(ctx, startTime, results)
	if err != nil {
		return summary, fmt.Errorf("failed to extract configs & changes from results: %w", err)
	}
	for configType, cs := range extractResult.changeSummary {
		summary.AddChangeSummary(configType, cs)
	}

	// NOTE: On duplicate primary key do nothing
	// because an incremental scraper might have already inserted the config item.
	if err := ctx.DB().
		Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoNothing: true}).
		CreateInBatches(extractResult.newConfigs, configItemsBulkInsertSize).Error; err != nil {
		return summary, fmt.Errorf("failed to create config items: %w", dutydb.ErrorDetails(err))
	}
	for _, config := range extractResult.newConfigs {
		summary.AddInserted(config.Type)
	}

	// nonUpdatedConfigs are existing configs that were not updated in this scrape.
	// We keep track of them so that we can update their last scraped time.
	var nonUpdatedConfigs []string

	// TODO: Try this in batches as well
	for _, updateArg := range extractResult.configsToUpdate {
		updated, diffChanges, err := updateCI(ctx, &summary, updateArg.Result, updateArg.New, updateArg.Existing)
		if err != nil {
			return summary, fmt.Errorf("failed to update config item (%s): %w", updateArg.Existing, err)
		}

		if updated {
			summary.AddUpdated(updateArg.Existing.Type)
		} else {
			summary.AddUnchanged(updateArg.Existing.Type)
			nonUpdatedConfigs = append(nonUpdatedConfigs, updateArg.Existing.ID)
		}

		if len(diffChanges) != 0 {
			extractResult.newChanges = append(extractResult.newChanges, diffChanges...)
		}
	}

	if err := updateLastScrapedTime(ctx, nonUpdatedConfigs); err != nil {
		return summary, fmt.Errorf("failed to update last scraped time: %w", err)
	}

	if ctx.ScrapeConfig().Spec.CRDSync {
		allScrapedConfigs := append(extractResult.newConfigs, lo.Map(extractResult.configsToUpdate, func(item *updateConfigArgs, _ int) *models.ConfigItem { return item.New })...)
		if err := syncCRDChanges(ctx, allScrapedConfigs); err != nil {
			return summary, fmt.Errorf("failed to sync CRD changes: %w", err)
		}
	}

	dedupWindow := ctx.Properties().Duration("changes.dedup.window", time.Hour)
	newChanges, deduped := dedupChanges(dedupWindow, extractResult.newChanges)

	if err := ctx.DB().CreateInBatches(&newChanges, configItemsBulkInsertSize).Error; err != nil {
		return summary, fmt.Errorf("failed to create config changes: %w", dutydb.ErrorDetails(err))
	}

	for _, dedup := range deduped {
		update := map[string]any{
			"change_type":         dedup.Change.ChangeType,
			"count":               gorm.Expr("count + ?", dedup.CountIncrement),
			"created_at":          gorm.Expr("NOW()"),
			"created_by":          dedup.Change.CreatedBy,
			"details":             dedup.Change.Details,
			"diff":                dedup.Change.Diff,
			"external_change_id":  dedup.Change.ExternalChangeID,
			"external_created_by": dedup.Change.ExternalCreatedBy,
			"severity":            dedup.Change.Severity,
			"source":              dedup.Change.Source,
			"summary":             dedup.Change.Summary,
		}

		if dedup.Change.Patches != "" {
			update["patches"] = dedup.Change.Patches
		}

		if err := ctx.DB().Model(&models.ConfigChange{}).Where("id = ?", dedup.Change.ID).UpdateColumns(update).Error; err != nil {
			return summary, fmt.Errorf("failed to create deduped config changes: %w", dutydb.ErrorDetails(err))
		}
	}

	// TODO: Find a way to bulk insert these changes.
	// Couldn't find a way to do it with gorm.
	// Cannot use .Save() because it will try to insert first and then update.
	// That'll trigger the .BeforeCreate() hook which doesn't have a ON Conflict clause on the primary key.
	for _, changeToUpdate := range extractResult.changesToUpdate {
		if err := ctx.DB().Updates(&changeToUpdate).Error; err != nil {
			return summary, fmt.Errorf("failed to update config changes: %w", err)
		}
	}

	var (
		// Keep note of the all the relationships in each of the results
		// so we can create them once the all the configs are saved.
		relationshipToForm []v1.RelationshipResult

		// resultsWithRelationshipSelectors is a list of scraped results that have
		// relationship selectors. These selectors are stored here to be processed
		// once the all the scraped results are saved.
		resultsWithRelationshipSelectors []v1.ScrapeResult
	)

	for _, result := range results {
		if result.AnalysisResult != nil {
			if err := upsertAnalysis(ctx, &result); err != nil {
				return summary, fmt.Errorf("failed to analysis (%s): %w", result, err)
			}
		}

		relationshipToForm = append(relationshipToForm, result.RelationshipResults...)
		if len(result.RelationshipSelectors) != 0 {
			resultsWithRelationshipSelectors = append(resultsWithRelationshipSelectors, result)
		}
	}

	if res, err := relationshipSelectorToResults(ctx.DutyContext(), resultsWithRelationshipSelectors); err != nil {
		return summary, fmt.Errorf("failed to get relationship results from relationship selectors: %w", err)
	} else {
		relationshipToForm = append(relationshipToForm, res...)
	}

	if err := relationshipResultHandler(ctx, relationshipToForm); err != nil {
		return summary, fmt.Errorf("failed to form relationships: %w", err)
	}

	if !startTime.IsZero() && ctx.ScrapeConfig().GetPersistedID() != nil {
		// Any analysis that weren't observed again will be marked as resolved
		if err := UpdateAnalysisStatusBefore(ctx, startTime, string(ctx.ScrapeConfig().GetUID()), dutyModels.AnalysisStatusResolved); err != nil {
			ctx.Errorf("failed to mark analysis before %v as healthy: %v", startTime, err)
		}
	}

	if summary.HasUpdates() {
		ctx.Logger.Debugf("Updates %s", summary)
	} else {
		ctx.Logger.V(4).Infof("No Update: %s", summary)
	}
	return summary, nil
}

func updateLastScrapedTime(ctx api.ScrapeContext, ids []string) error {
	if len(ids) == 0 {
		return nil
	}

	for i := 0; i < len(ids); i = i + 5000 {
		end := i + 5000
		if end > len(ids) {
			end = len(ids)
		}

		if err := ctx.DB().
			Model(&models.ConfigItem{}).
			Where("id in (?)", ids[i:end]).
			Update("last_scraped_time", gorm.Expr("NOW()")).Error; err != nil {
			return err
		}
	}

	return nil
}

// generateConfigChange calculates the diff (git style) and patches between the
// given 2 config items and returns a ConfigChange object if there are any changes.
func generateConfigChange(ctx api.ScrapeContext, newConf, prev models.ConfigItem) (*v1.ChangeResult, error) {
	if changeTypExclusion, ok := lo.FromPtr(newConf.Labels)[v1.AnnotationIgnoreChangeByType]; ok {
		if collections.MatchItems(v1.ChangeTypeDiff, strings.Split(changeTypExclusion, ",")...) {
			if ctx.PropertyOn(false, "log.exclusions") {
				ctx.Logger.V(4).Infof("excluding diff change for config(%s) with annotation (%s)", newConf, changeTypExclusion)
			}
			return nil, nil
		}
	}

	var _timer *timer.MemoryTimer
	if ctx.Properties().On(false, "scraper.log.items") {
		ctx.Logger.V(5).Infof("generating diff for %s", newConf.Label())
	}
	if ctx.Logger.IsLevelEnabled(4) && len(*newConf.Config) > ctx.Properties().Int("scraper.diff.timer.minSize", 1024*20) {
		_timer = lo.ToPtr(timer.NewMemoryTimer())
	}

	start := time.Now()

	diff, err := GenerateDiff(ctx.Context, *newConf.Config, *prev.Config)
	if err != nil {
		return nil, fmt.Errorf("failed to generate diff: %w", err)
	}

	duration := time.Since(start)

	ctx.Histogram("scraper_diff_duration",
		[]float64{1, 10, 100, 1000, 10000}, "scraper", ctx.ScraperID()).
		Record(time.Duration(duration.Milliseconds()))

	msg := fmt.Sprintf("generated in %dms for %s size=%s diff=%s",
		duration.Milliseconds(),
		newConf.Label(),
		text.HumanizeBytes(len(*newConf.Config)),
		text.HumanizeBytes(len(diff)),
	)
	if _timer != nil {
		msg += " " + _timer.End()
	}
	if duration > 500*time.Millisecond {
		ctx.Logger.Warnf("SLOW DIFF >= %s", msg)
	} else if duration > 50*time.Millisecond {
		ctx.Logger.Infof("SLOW DIFF >= %s", msg)
	} else if ctx.Properties().On(false, "scraper.log.items") {
		ctx.Logger.V(4).Infof(msg)
	}

	if diff == "" {
		return nil, nil
	}

	patch, err := jsonpatch.CreateMergePatch([]byte(*newConf.Config), []byte(*prev.Config))
	if err != nil {
		return nil, fmt.Errorf("failed to create merge patch: %w", err)
	}

	var patchJSON map[string]any
	if err := json.Unmarshal(patch, &patchJSON); err != nil {
		return nil, fmt.Errorf("failed to unmarshal patch: %w", err)
	}

	return &v1.ChangeResult{
		ConfigType: newConf.Type,
		ChangeType: v1.ChangeTypeDiff,
		ExternalID: newConf.ExternalID[0],
		Diff:       &diff,
		Patches:    string(patch),
		Summary:    strings.Join(utils.ExtractLeafNodesAndCommonParents(patchJSON), ", "),
	}, nil
}

func relationshipSelectorToResults(ctx dutyContext.Context, inputs []v1.ScrapeResult) ([]v1.RelationshipResult, error) {
	var relationships []v1.RelationshipResult

	for _, input := range inputs {
		for _, directedRelationship := range input.RelationshipSelectors {
			linkedConfigIDs, err := FindConfigIDsByRelationshipSelector(ctx, directedRelationship.Selector)
			if err != nil {
				return nil, fmt.Errorf("failed to find config items by relationship selector: %w", err)
			}

			for _, id := range linkedConfigIDs {
				rel := v1.RelationshipResult{
					ConfigExternalID: v1.ExternalID{ExternalID: input.ID, ConfigType: input.Type},
					RelatedConfigID:  id.String(),
				}

				if directedRelationship.Parent {
					rel.Swap()
				}

				relationships = append(relationships, rel)
			}
		}
	}

	ctx.Logger.V(5).Infof("formed %d relationships from selectors", len(relationships))
	return relationships, nil
}

func relationshipResultHandler(ctx api.ScrapeContext, relationships v1.RelationshipResults) error {
	ctx.Logger.V(5).Infof("saving %d relationships", len(relationships))
	logMissing := ctx.PropertyOn(false, "log.missing")

	var configItemRelationships []models.ConfigRelationship
	for _, relationship := range relationships {
		var err error

		var configID string
		if relationship.ConfigID != "" {
			configItem, err := ctx.TempCache().Get(ctx, relationship.ConfigID)
			if err != nil {
				ctx.Errorf("error fetching external config item(id=%s): %v", relationship.ConfigID, err)
				continue
			}
			if configItem == nil {
				if logMissing {
					ctx.Logger.Tracef("config item: %s not found in db for relation", relationship.RelatedConfigID)
				}
				continue
			}
			configID = configItem.ID

		} else {
			configID, err = ctx.TempCache().FindExternalID(ctx, relationship.ConfigExternalID)
			if err != nil {
				ctx.Errorf("error fetching config item(id=%s): %v", relationship.ConfigExternalID, err)
				continue
			}
			if configID == "" && logMissing {
				ctx.Logger.Tracef("%s: parent config (%s) not found", relationship.ConfigExternalID, cUtils.Coalesce(relationship.RelatedConfigID, relationship.RelatedExternalID.String()))
				continue
			}
		}

		var relatedID string
		if relationship.RelatedConfigID != "" {
			relatedCI, err := ctx.TempCache().Get(ctx, relationship.RelatedConfigID)
			if err != nil {
				ctx.Errorf("error fetching related config item(id=%s): %v", relationship.RelatedConfigID, err)
				continue
			}
			if relatedCI == nil {
				if logMissing {
					ctx.Logger.Tracef("related config item: %s not found in db for relation", relationship.RelatedConfigID)
				}
				continue
			}
			relatedID = relatedCI.ID
		} else {
			relatedID, err = ctx.TempCache().FindExternalID(ctx, relationship.RelatedExternalID)
			if err != nil {
				ctx.Errorf("error fetching external config item(id=%s): %v", relationship.RelatedExternalID, err)
				continue
			}
			if relatedID == "" && logMissing {
				ctx.Logger.Tracef("%s: related config (%s) not found", configID, relationship.RelatedExternalID)
				continue
			}
		}

		// The configs in the relationships might not be found for various reasons.
		// - the related configs might have been excluded in the scrape config
		// - the config might have been deleted
		if relatedID == "" || configID == "" {
			continue
		}

		configItemRelationships = append(configItemRelationships, models.ConfigRelationship{
			ConfigID:  configID,
			RelatedID: relatedID,
			Relation:  relationship.Relationship,
		})
	}

	return UpdateConfigRelatonships(ctx, configItemRelationships)
}

type updateConfigArgs struct {
	Result   v1.ScrapeResult
	Existing *models.ConfigItem
	New      *models.ConfigItem
}

type configExternalKey struct {
	externalID string
	parentType string
}

// extractResult holds the extracted configs & changes from the scrape result
type extractResult struct {
	newConfigs      []*models.ConfigItem
	configsToUpdate []*updateConfigArgs
	newChanges      []*models.ConfigChange
	changesToUpdate []*models.ConfigChange
	changeSummary   v1.ChangeSummaryByType
}

func NewExtractResult() *extractResult {
	return &extractResult{
		changeSummary: make(v1.ChangeSummaryByType),
	}
}

func extractConfigsAndChangesFromResults(ctx api.ScrapeContext, scrapeStartTime time.Time, results []v1.ScrapeResult) (*extractResult, error) {
	var (
		extractResult         = NewExtractResult()
		allConfigs            = make([]*models.ConfigItem, 0, len(results))
		parentTypeToConfigMap = make(map[configExternalKey]string)
	)

	for _, result := range results {
		result.LastScrapedTime = &scrapeStartTime

		var ci *models.ConfigItem
		var err error

		if result.Name == "" {
			result.Name = lo.CoalesceOrEmpty(lo.FirstOrEmpty(result.Aliases), result.ID)
		}

		if result.ID != "" {
			// A result that only contains changes (example a result created by Cloudtrail scraper)
			// doesn't have any id.
			ci, err = NewConfigItemFromResult(ctx, result)
			if err != nil {
				return nil, fmt.Errorf("unable to create config item(%s): %w", result, err)
			}

			if len(ci.ExternalID) == 0 {
				return nil, fmt.Errorf("config item %s has no external id", ci)
			}

			parentExternalKey := configExternalKey{externalID: ci.ExternalID[0], parentType: ci.Type}
			parentTypeToConfigMap[parentExternalKey] = ci.ID

			existing := &models.ConfigItem{}
			if ci.ID != "" {
				if existing, err = ctx.TempCache().Get(ctx, ci.ID); err != nil {
					return nil, fmt.Errorf("unable to lookup existing config(%s): %w", ci, err)
				}
			} else {
				if existing, err = ctx.TempCache().Find(ctx, v1.ExternalID{ConfigType: ci.Type, ExternalID: ci.ExternalID[0]}); err != nil {
					return nil, fmt.Errorf("unable to lookup external id(%s): %w", ci, err)
				}
			}

			allConfigs = append(allConfigs, ci)
			if result.Config != nil {
				if existing == nil || existing.ID == "" {
					extractResult.newConfigs = append(extractResult.newConfigs, ci)
				} else {
					// In case, we are not able to derive the path & parent_id
					// by forming a tree, we need to use the existing one
					// otherwise they'll be updated to empty values
					ci.ParentID = existing.ParentID
					ci.Path = existing.Path

					extractResult.configsToUpdate = append(extractResult.configsToUpdate, &updateConfigArgs{
						Result:   result,
						Existing: existing,
						New:      ci,
					})
				}
			}
		}

		if toCreate, toUpdate, changeSummary, err := extractChanges(ctx, &result, ci); err != nil {
			return nil, err
		} else {
			if !changeSummary.IsEmpty() {
				var configType string
				if ci != nil {
					configType = ci.Type
				} else if len(result.Changes) > 0 {
					configType = result.Changes[0].ConfigType
				}

				if configType == "" {
					configType = "None"
				}

				extractResult.changeSummary.Merge(configType, changeSummary)
			}

			extractResult.newChanges = append(extractResult.newChanges, toCreate...)
			extractResult.changesToUpdate = append(extractResult.changesToUpdate, toUpdate...)
		}
	}

	// Calculate the parents and children only after we have all the config items.
	// This is because, on the first run, we don't have any configs at all in the DB.
	// So, all the parent lookups will return empty result and no parent will be set.
	// This way, we can first look for the parents within the result set.
	if err := setConfigProbableParents(ctx, parentTypeToConfigMap, allConfigs); err != nil {
		return nil, fmt.Errorf("unable to set parents: %w", err)
	}

	if err := setConfigPaths(ctx, allConfigs); err != nil {
		return nil, fmt.Errorf("unable to set config paths: %w", err)
	}

	// run this after setting the config path. else whatever the parent is set here will be overwritten by it.
	if err := setParentForChildren(ctx, allConfigs); err != nil {
		return nil, fmt.Errorf("unable to set children: %w", err)
	}

	if ctx.IsIncrementalScrape() {
		// This is to preserve the child-parent hard relationship
		// when the child doesn't know about its parent.
		for _, c := range allConfigs {
			if parentID, ok := parentCache.Get(c.ID); ok {
				c.ParentID = lo.ToPtr(parentID.(string))
			}
		}
	}

	// We sort the new config items such that parents are always first.
	// This avoids foreign key constraint errors.
	slices.SortFunc(extractResult.newConfigs, func(a, b *models.ConfigItem) int {
		if len(a.Path) < len(b.Path) {
			return -1
		}

		if len(a.Path) > len(b.Path) {
			return 1
		}

		return 0
	})

	return extractResult, nil
}

func setConfigProbableParents(ctx api.ScrapeContext, parentTypeToConfigMap map[configExternalKey]string, allConfigs []*models.ConfigItem) error {
	for _, ci := range allConfigs {
		if len(ci.Parents) == 0 {
			continue // these are root items.
		}

		// Set probable parents in order of importance
		for _, parent := range ci.Parents {
			if parent.ExternalID == "" || parent.Type == "" {
				continue
			}

			if parentID, found := parentTypeToConfigMap[configExternalKey{
				externalID: parent.ExternalID,
				parentType: parent.Type,
			}]; found {
				// Ignore self parent reference
				if ci.ID == parentID {
					continue
				}
				ci.ProbableParents = append(ci.ProbableParents, parentID)
				continue
			}

			if foundParent, err := ctx.TempCache().Find(ctx, v1.ExternalID{ConfigType: parent.Type, ExternalID: parent.ExternalID}); err != nil {
				return err
			} else if foundParent != nil {
				// Ignore self parent reference
				if ci.ID == foundParent.ID {
					continue
				}
				ci.ProbableParents = append(ci.ProbableParents, foundParent.ID)
				continue
			}
		}
	}

	return nil
}

func setParentForChildren(ctx api.ScrapeContext, allConfigs models.ConfigItems) error {
	var cacheExpiry time.Duration

	// Attempt to get a fixed interval from the schedule so we can set the appropriate cache expiry.
	if parsedSchedule, err := cron.ParseStandard(ctx.ScrapeConfig().Spec.Schedule); err == nil {
		next := parsedSchedule.Next(time.Now())
		cacheExpiry = time.Until(next) * 2
	} else {
		cacheExpiry = 2 * time.Hour
	}

	for _, ci := range allConfigs {
		if len(ci.Children) == 0 {
			// No action required
			continue
		}

		for _, child := range ci.Children {
			if child.ExternalID == "" || child.Type == "" {
				continue
			}

			found, err := ctx.TempCache().Find(ctx, v1.ExternalID{ConfigType: child.Type, ExternalID: child.ExternalID})
			if err != nil {
				return err
			} else if found == nil {
				ctx.Logger.Tracef("child:[%s/%s] not found for config [%s]", child.Type, child.ExternalID, ci)
				continue
			}

			childRef := allConfigs.GetByID(found.ID)
			if childRef != nil {
				parentCache.Set(childRef.ID, ci.ID, cacheExpiry)
				childRef.ParentID = &ci.ID
			}
		}
	}

	return nil
}

func getOrFind(ctx api.ScrapeContext, parentMap map[string]string, id string) string {
	if parent, ok := parentMap[id]; ok {
		return parent
	}
	if parent, _ := ctx.TempCache().Get(ctx, id); parent != nil {
		if parent.ParentID == nil {
			return ""
		}
		return *parent.ParentID
	}
	return ""
}

// getPath returns path till root, it returns false if a cylce is detected
func getPath(ctx api.ScrapeContext, parentMap map[string]string, self string) (string, bool) {
	var paths []string

	for parent := getOrFind(ctx, parentMap, self); parent != ""; parent = getOrFind(ctx, parentMap, parent) {
		// parent == self can happen in recursive hierarchy (eg. flux kustomize refers to the namespace it is created in)
		if slices.Contains(paths, parent) || parent == self {
			return strings.Join(paths, "."), true
		}
		paths = append([]string{parent}, paths...)
	}

	return strings.Join(paths, "."), true
}

func setConfigPaths(ctx api.ScrapeContext, allConfigs []*models.ConfigItem) error {
	if ctx.ScrapeConfig().IsCustom() {
		// TODO: handle full mode
		// When full:true, we have some edge cases.
		return nil
	}

	// Sorting allConfigs on the length of ProbableParents
	// is requred to correctly detect and fix cycles earlier
	// using getPath
	sort.Slice(allConfigs, func(i, j int) bool {
		return len(allConfigs[i].ProbableParents) < len(allConfigs[j].ProbableParents)
	})

	var parentMap = make(map[string]string)
	for _, config := range allConfigs {
		if len(config.ProbableParents) > 0 {
			parentMap[config.ID] = config.ProbableParents[0]
		}
	}

	for _, config := range allConfigs {
		for i := 0; i < len(config.ProbableParents); i++ {
			// If no cycle is detected, we set path and parentID
			path, ok := getPath(ctx, parentMap, config.ID)
			if ok {
				config.Path = path
				// Empty path means root config, where parentID should be nil
				if path != "" {
					config.ParentID = lo.ToPtr(parentMap[config.ID])
				}

				break
			}

			// If a cycle is detected we assume the parent is bad and move to the next
			// probable parent and redo path computation
			parentMap[config.ID] = config.ProbableParents[i]
		}

		if config.ParentID == nil && ctx.PropertyOn(false, "log.missing") && len(config.ProbableParents) > 0 {
			ctx.Logger.Warnf("parent not found for config [%s]", config)
		}
	}
	return nil
}
