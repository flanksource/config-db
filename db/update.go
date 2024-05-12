package db

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/aws/smithy-go/ptr"
	jsonpatch "github.com/evanphx/json-patch"
	"github.com/flanksource/commons/logger"
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db/models"
	"github.com/flanksource/config-db/db/ulid"
	"github.com/flanksource/config-db/scrapers/changes"
	"github.com/flanksource/config-db/utils"
	dutyContext "github.com/flanksource/duty/context"
	dutyModels "github.com/flanksource/duty/models"
	"github.com/flanksource/gomplate/v3"
	"github.com/google/uuid"
	"github.com/hexops/gotextdiff"
	"github.com/hexops/gotextdiff/myers"
	"github.com/lib/pq"
	"github.com/pkg/errors"
	"github.com/samber/lo"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const configItemsBulkInsertSize = 200

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
		logger.Warnf("Attempt to delete non-existent config item %s/%s", change.ConfigType, change.ExternalID)
		return nil
	}

	ctx.Logger.V(3).Infof("Deleted %s from change %s", configs[0].ID, change)
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

func updateCI(ctx api.ScrapeContext, result v1.ScrapeResult, ci, existing *models.ConfigItem) ([]*models.ConfigChange, error) {
	ci.ID = existing.ID
	updates := make(map[string]interface{})
	changes := make([]*models.ConfigChange, 0)

	// In case a resource was marked as deleted but is un-deleted now
	// we set an update flag as gorm ignores nil pointers
	if ci.DeletedAt != existing.DeletedAt {
		ci.TouchDeletedAt = true
		updates["deleted_at"] = ci.DeletedAt
	}

	changeResult, err := generateConfigChange(*ci, *existing)
	if err != nil {
		logger.Errorf("[%s] failed to check for changes: %v", ci, err)
	} else if changeResult != nil {
		ctx.Logger.V(3).Infof("[%s/%s] detected changes", *ci.Type, ci.ExternalID[0])
		result.Changes = []v1.ChangeResult{*changeResult}
		if newChanges, _, err := extractChanges(ctx, &result, ci); err != nil {
			return nil, err
		} else {
			changes = append(changes, newChanges...)
		}

		updates["config"] = *ci.Config
		updates["updated_at"] = gorm.Expr("NOW()")
	}

	if ci.ConfigClass != existing.ConfigClass {
		updates["config_class"] = ci.ConfigClass
	}
	if !stringEqual(ci.Source, existing.Source) {
		updates["source"] = ci.Source
	}
	if !stringEqual(ci.Type, existing.Type) {
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
		updates["health"] = ci.Health
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

	if ci.CreatedAt != existing.CreatedAt {
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
	if ci.Properties != nil && len(*ci.Properties) > 0 && (existing.Properties == nil || !mapEqual(ci.Properties.AsMap(), existing.Properties.AsMap())) {
		updates["properties"] = *ci.Properties
	}

	if ci.TouchDeletedAt && ci.DeleteReason != v1.DeletedReasonFromEvent {
		updates["deleted_at"] = nil
		updates["delete_reason"] = nil
	}

	if len(updates) == 0 {
		return changes, nil
	}

	updates["last_scraped_time"] = gorm.Expr("NOW()")
	if err := ctx.DutyContext().DB().Model(ci).Updates(updates).Error; err != nil {
		return nil, errors.Wrapf(err, "unable to update config item: %s", ci)
	}

	return changes, nil
}

func shouldExcludeChange(result *v1.ScrapeResult, changeResult v1.ChangeResult) (bool, error) {
	exclusions := result.BaseScraper.Transform.Change.Exclude

	env := changeResult.AsMap()
	env["config"] = result.Config

	for _, expr := range exclusions {
		if res, err := gomplate.RunTemplate(env, gomplate.Template{Expression: expr}); err != nil {
			return false, fmt.Errorf("failed to evaluate change exclusion expression(%s): %w", expr, err)
		} else if skipChange, err := strconv.ParseBool(res); err != nil {
			return false, fmt.Errorf("change exclusion expression(%s) didn't evaluate to a boolean: %w", expr, err)
		} else if skipChange {
			return true, nil
		}
	}

	return false, nil
}

func extractChanges(ctx api.ScrapeContext, result *v1.ScrapeResult, ci *models.ConfigItem) ([]*models.ConfigChange, []*models.ConfigChange, error) {
	var (
		newOnes = []*models.ConfigChange{}
		updates = []*models.ConfigChange{}
	)

	changes.ProcessRules(result, result.BaseScraper.Transform.Change.Mapping...)
	for _, changeResult := range result.Changes {
		if changeResult.Action == v1.Ignore {
			continue
		}

		if exclude, err := shouldExcludeChange(result, changeResult); err != nil {
			ctx.JobHistory().AddError(fmt.Sprintf("error running change exclusion: %v", err))
		} else if exclude {
			ctx.DutyContext().Tracef("excluded change: %v", changeResult)
			continue
		}

		if changeResult.Action == v1.Delete {
			if err := deleteChangeHandler(ctx, changeResult); err != nil {
				return nil, nil, fmt.Errorf("failed to delete config from change: %w", err)
			}
		}

		change := models.NewConfigChangeFromV1(*result, changeResult)

		if change.CreatedBy != nil {
			person, err := FindPersonByEmail(ctx, ptr.ToString(change.CreatedBy))
			if err != nil {
				return nil, nil, fmt.Errorf("error finding person by email: %w", err)
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
			if ci, err := ctx.TempCache().FindExternalID(change.GetExternalID()); err != nil {
				return nil, nil, fmt.Errorf("failed to get config from change (externalID=%s): %w", change.GetExternalID(), err)
			} else if ci != "" {
				change.ConfigID = ci
			} else if ci == "" {
				logger.Warnf("[%s/%s] unable to find config item for change: %v", change.ConfigType, change.ExternalID, change.ChangeType)
				continue
			}
		}

		if changeResult.UpdateExisting {
			updates = append(updates, change)
		} else {
			newOnes = append(newOnes, change)
		}
	}

	return newOnes, updates, nil
}

func upsertAnalysis(ctx api.ScrapeContext, result *v1.ScrapeResult) error {
	analysis := result.AnalysisResult.ToConfigAnalysis()
	ciID, err := ctx.TempCache().Find(analysis.ConfigType, analysis.ExternalID)
	if err != nil {
		return err
	}
	if ciID == nil {
		logger.Warnf("[Source=%s] [%s/%s] unable to find config item for analysis: %+v", analysis.Source, analysis.ConfigType, analysis.ExternalID, analysis)
		return nil
	}

	logger.Tracef("[%s/%s] ==> %s", analysis.ConfigType, analysis.ExternalID, analysis)
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
func SaveResults(ctx api.ScrapeContext, results []v1.ScrapeResult) error {
	startTime, err := GetCurrentDBTime(ctx)
	if err != nil {
		return fmt.Errorf("unable to get current db time: %w", err)
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

	// TODO:: Sort the results so that parents are inserted first

	newConfigs, configsToUpdate, newChanges, changesToUpdate, err := extractConfigsAndChangesFromResults(ctx, startTime, results)
	if err != nil {
		return fmt.Errorf("failed to extract configs & changes from results: %w", err)
	}
	ctx.Logger.V(2).Infof("%d new configs, %d configs to update, %d new changes & %d changes to update",
		len(newConfigs), len(configsToUpdate), len(newChanges), len(changesToUpdate))

	if err := ctx.DB().CreateInBatches(newConfigs, configItemsBulkInsertSize).Error; err != nil {
		return fmt.Errorf("failed to create config items: %w", err)
	}

	// TODO: Try this in batches as well
	for _, config := range configsToUpdate {
		if diffChanges, err := updateCI(ctx, config.Result, config.New, config.Existing); err != nil {
			return fmt.Errorf("failed to update config item (%s): %w", config.Existing, err)
		} else if len(diffChanges) != 0 {
			newChanges = append(newChanges, diffChanges...)
		}
	}

	if err := ctx.DB().CreateInBatches(newChanges, 200).Error; err != nil {
		return fmt.Errorf("failed to create config changes: %w", err)
	}

	if len(changesToUpdate) != 0 {
		if err := ctx.DB().Save(changesToUpdate).Error; err != nil {
			return fmt.Errorf("failed to update config changes: %w", err)
		}
	}

	for _, result := range results {
		if result.AnalysisResult != nil {
			if err := upsertAnalysis(ctx, &result); err != nil {
				return fmt.Errorf("failed to analysis (%s): %w", result, err)
			}
		}

		relationshipToForm = append(relationshipToForm, result.RelationshipResults...)
		if len(result.RelationshipSelectors) != 0 {
			resultsWithRelationshipSelectors = append(resultsWithRelationshipSelectors, result)
		}
	}

	if res, err := relationshipSelectorToResults(ctx.DutyContext(), resultsWithRelationshipSelectors); err != nil {
		return fmt.Errorf("failed to get relationship results from relationship selectors: %w", err)
	} else {
		relationshipToForm = append(relationshipToForm, res...)
	}

	if err := relationshipResultHandler(ctx, relationshipToForm); err != nil {
		return fmt.Errorf("failed to form relationships: %w", err)
	}

	if !startTime.IsZero() && ctx.ScrapeConfig().GetPersistedID() != nil {
		// Any analysis that weren't observed again will be marked as resolved
		if err := UpdateAnalysisStatusBefore(ctx, startTime, string(ctx.ScrapeConfig().GetUID()), dutyModels.AnalysisStatusResolved); err != nil {
			logger.Errorf("failed to mark analysis before %v as healthy: %v", startTime, err)
		}
	}

	ctx.Logger.V(3).Infof("saved %d results.", len(results))
	return nil
}

// normalizeJSON returns an indented json string.
// The keys are sorted lexicographically.
func normalizeJSON(jsonStr string) (string, error) {
	var jsonStrMap map[string]any
	if err := json.Unmarshal([]byte(jsonStr), &jsonStrMap); err != nil {
		return "", err
	}

	jsonStrIndented, err := json.MarshalIndent(jsonStrMap, "", "\t")
	if err != nil {
		return "", err
	}

	return string(jsonStrIndented), nil
}

// generateDiff calculates the diff (git style) between the given 2 configs.
func generateDiff(newConf, prevConfig string) (string, error) {
	// We want a nicely indented json config with each key-vals in new line
	// because that gives us a better diff. A one-line json string config produces diff
	// that's not very helpful.
	before, err := normalizeJSON(prevConfig)
	if err != nil {
		return "", fmt.Errorf("failed to normalize json for previous config: %w", err)
	}

	after, err := normalizeJSON(newConf)
	if err != nil {
		return "", fmt.Errorf("failed to normalize json for new config: %w", err)
	}

	edits := myers.ComputeEdits("", before, after)
	if len(edits) == 0 {
		return "", nil
	}

	diff := fmt.Sprint(gotextdiff.ToUnified("before", "after", before, edits))
	return diff, nil
}

// generateConfigChange calculates the diff (git style) and patches between the
// given 2 config items and returns a ConfigChange object if there are any changes.
func generateConfigChange(newConf, prev models.ConfigItem) (*v1.ChangeResult, error) {
	diff, err := generateDiff(*newConf.Config, *prev.Config)
	if err != nil {
		return nil, fmt.Errorf("failed to generate diff: %w", err)
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
		ConfigType:       lo.FromPtr(newConf.Type),
		ChangeType:       "diff",
		ExternalChangeID: utils.Sha256Hex(string(patch)),
		ExternalID:       newConf.ExternalID[0],
		Diff:             &diff,
		Patches:          string(patch),
		Summary:          strings.Join(utils.ExtractLeafNodesAndCommonParents(patchJSON), ", "),
	}, nil
}

func relationshipSelectorToResults(ctx dutyContext.Context, inputs []v1.ScrapeResult) ([]v1.RelationshipResult, error) {
	var relationships []v1.RelationshipResult

	for _, input := range inputs {
		for _, selector := range input.RelationshipSelectors {
			linkedConfigIDs, err := FindConfigIDsByRelationshipSelector(ctx, selector)
			if err != nil {
				return nil, fmt.Errorf("failed to find config items by relationship selector: %w", err)
			}

			for _, id := range linkedConfigIDs {
				rel := v1.RelationshipResult{
					ConfigExternalID: v1.ExternalID{ExternalID: []string{input.ID}, ConfigType: input.Type},
					RelatedConfigID:  id.String(),
				}

				relationships = append(relationships, rel)
			}
		}
	}

	ctx.Logger.V(3).Infof("forming %d relationships from selectors", len(relationships))
	return relationships, nil
}

func relationshipResultHandler(ctx api.ScrapeContext, relationships v1.RelationshipResults) error {
	ctx.Logger.V(3).Infof("saving %d relationships", len(relationships))

	var configItemRelationships []models.ConfigRelationship
	for _, relationship := range relationships {
		var err error

		var configID string
		if relationship.ConfigID != "" {
			configID = relationship.ConfigID
		} else {
			configID, err = ctx.TempCache().FindExternalID(relationship.ConfigExternalID)
			if err != nil {
				logger.Errorf("error fetching config item(id=%s): %v", relationship.ConfigExternalID, err)
				continue
			}
			if configID == "" {
				logger.Warnf("unable to form relationship. failed to find the parent config %s", relationship.ConfigExternalID)
				continue
			}
		}

		var relatedID string
		if relationship.RelatedConfigID != "" {
			relatedID = relationship.RelatedConfigID
		} else {
			relatedID, err = ctx.TempCache().FindExternalID(relationship.RelatedExternalID)
			if err != nil {
				logger.Errorf("error fetching external config item(id=%s): %v", relationship.RelatedExternalID, err)
				continue
			}
			if relatedID == "" {
				logger.V(6).Infof("related external config item(id=%s) not found.", relationship.RelatedExternalID)
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

type parentExternalKey struct {
	externalID string
	parentType string
}

func extractConfigsAndChangesFromResults(ctx api.ScrapeContext, scrapeStartTime time.Time, results []v1.ScrapeResult) ([]*models.ConfigItem, []*updateConfigArgs, []*models.ConfigChange, []*models.ConfigChange, error) {
	var (
		inserts = make([]*models.ConfigItem, 0, len(results))
		updates = make([]*updateConfigArgs, 0, len(results))

		newChanges      = make([]*models.ConfigChange, 0, len(results))
		changesToUpdate = make([]*models.ConfigChange, 0, len(results))

		configToExternalParentMap = make(map[string]parentExternalKey)
		parentToConfigIDMap       = make(map[parentExternalKey]string)
	)

	for _, result := range results {
		if result.Config == nil {
			continue
		}

		result.LastScrapedTime = &scrapeStartTime
		ci, err := NewConfigItemFromResult(ctx, result)
		if err != nil {
			return nil, nil, nil, nil, fmt.Errorf("unable to create config item: %s: %w", result, err)
		}

		if result.ParentExternalID != "" && result.ParentType != "" {
			configToExternalParentMap[ci.ID] = parentExternalKey{
				externalID: result.ParentExternalID,
				parentType: result.ParentType,
			}
		}

		parentToConfigIDMap[parentExternalKey{
			externalID: ci.ExternalID[0],
			parentType: lo.FromPtr(ci.Type),
		}] = ci.ID

		ci.ScraperID = ctx.ScrapeConfig().GetPersistedID()
		if len(ci.ExternalID) == 0 {
			return nil, nil, nil, nil, fmt.Errorf("config item %s has no external id", ci)
		}

		existing := &models.ConfigItem{}
		if ci.ID != "" {
			if existing, err = ctx.TempCache().Get(ci.ID); err != nil {
				return nil, nil, nil, nil, fmt.Errorf("unable to lookup existing config: %s: %w", ci, err)
			}
		} else {
			if existing, err = ctx.TempCache().Find(*ci.Type, ci.ExternalID[0]); err != nil {
				return nil, nil, nil, nil, fmt.Errorf("unable to lookup external id: %s: %w", ci, err)
			}
		}

		if existing == nil || existing.ID == "" {
			inserts = append(inserts, ci)
		} else {
			updates = append(updates, &updateConfigArgs{
				Result:   result,
				Existing: existing,
				New:      ci,
			})
		}

		if toCreate, toUpdate, err := extractChanges(ctx, &result, ci); err != nil {
			return nil, nil, nil, nil, err
		} else {
			newChanges = append(newChanges, toCreate...)
			changesToUpdate = append(changesToUpdate, toUpdate...)
		}
	}

	// Calculate the parents only after we have all the config items.
	// This is because, on the first run, we don't have any configs at all in the DB.
	// So, all the parent lookups will return empty result and no parent will be set.
	// This way, we can first look for the parents within the result set.
	for i := range inserts {
		ci := inserts[i]
		externalParent, ok := configToExternalParentMap[ci.ID]
		if !ok {
			continue
		}

		if parentID, found := parentToConfigIDMap[parentExternalKey{
			externalID: externalParent.externalID,
			parentType: externalParent.parentType,
		}]; found {
			ci.ParentID = &parentID
			continue
		}

		if found, err := ctx.TempCache().Find(externalParent.parentType, externalParent.externalID); err != nil {
			return nil, nil, nil, nil, err
		} else if found != nil {
			ci.ParentID = &found.ID
		} else {
			ctx.DutyContext().Infof("[%s] parent %s/%s not found", ci, externalParent.parentType, externalParent.externalID)
		}
	}

	// TODO: Sort inserts by path
	// i.e. parents should come first.

	return inserts, updates, newChanges, changesToUpdate, nil
}
