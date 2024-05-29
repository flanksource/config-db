package db

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/aws/smithy-go/ptr"
	"github.com/dominikbraun/graph"
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
		ctx.Logger.V(2).Infof("attempt to delete non-existent config item %s/%s", change.ConfigType, change.ExternalID)
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

func updateCI(ctx api.ScrapeContext, result v1.ScrapeResult, ci, existing *models.ConfigItem) (bool, []*models.ConfigChange, error) {
	ci.ID = existing.ID
	updates := make(map[string]interface{})
	changes := make([]*models.ConfigChange, 0)

	if ci.DeletedAt != existing.DeletedAt {
		updates["deleted_at"] = ci.DeletedAt
		updates["delete_reason"] = ci.DeleteReason
	}

	changeResult, err := generateConfigChange(*ci, *existing)
	if err != nil {
		logger.Errorf("[%s] failed to check for changes: %v", ci, err)
	} else if changeResult != nil {
		ctx.Logger.V(3).Infof("[%s/%s] detected changes", *ci.Type, ci.ExternalID[0])
		result.Changes = []v1.ChangeResult{*changeResult}
		if newChanges, _, err := extractChanges(ctx, &result, ci); err != nil {
			return false, nil, err
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

	if len(updates) == 0 {
		return false, changes, nil
	}

	updates["last_scraped_time"] = gorm.Expr("NOW()")
	if err := ctx.DutyContext().DB().Model(ci).Updates(updates).Error; err != nil {
		return false, nil, errors.Wrapf(err, "unable to update config item: %s", ci)
	}

	return true, changes, nil
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
			ctx.Logger.V(3).Infof("excluded change: %v", changeResult)
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
			}
		}

		if change.ConfigID == "" {
			// Some scrapers can generate changes for config items that don't exist on our db.
			// Example: Cloudtrail scraper reporting changes for a resource that has been excluded.
			ctx.Logger.V(1).Infof("(type=%s source=%s) change doesn't have an associated config", change.ChangeType, change.Source)
			continue
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
		logger.Warnf("unable to find config item for analysis: (source=%s, configType=%s, externalID=%s, analysis: %+v)", analysis.Source, analysis.ConfigType, analysis.ExternalID, analysis)
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
func SaveResults(ctx api.ScrapeContext, results []v1.ScrapeResult) error {
	return saveResults(ctx, false, results)
}

// SaveResults creates or update a configuration with config changes
func SavePartialResults(ctx api.ScrapeContext, results []v1.ScrapeResult) error {
	return saveResults(ctx, true, results)
}

func saveResults(ctx api.ScrapeContext, isPartialResultSet bool, results []v1.ScrapeResult) error {
	startTime, err := GetCurrentDBTime(ctx)
	if err != nil {
		return fmt.Errorf("unable to get current db time: %w", err)
	}

	newConfigs, configsToUpdate, newChanges, changesToUpdate, err := extractConfigsAndChangesFromResults(ctx, startTime, isPartialResultSet, results)
	if err != nil {
		return fmt.Errorf("failed to extract configs & changes from results: %w", err)
	}
	ctx.Logger.V(2).Infof("%d new configs, %d configs to update, %d new changes & %d changes to update",
		len(newConfigs), len(configsToUpdate), len(newChanges), len(changesToUpdate))

	if err := ctx.DB().CreateInBatches(newConfigs, configItemsBulkInsertSize).Error; err != nil {
		return fmt.Errorf("failed to create config items: %w", err)
	}

	// nonUpdatedConfigs are existing configs that were not updated in this scrape.
	// We keep track of them so that we can update their last scraped time.
	var nonUpdatedConfigs []string

	// TODO: Try this in batches as well
	for _, config := range configsToUpdate {
		updated, diffChanges, err := updateCI(ctx, config.Result, config.New, config.Existing)
		if err != nil {
			return fmt.Errorf("failed to update config item (%s): %w", config.Existing, err)
		}

		if !updated {
			nonUpdatedConfigs = append(nonUpdatedConfigs, config.Existing.ID)
		}

		if len(diffChanges) != 0 {
			newChanges = append(newChanges, diffChanges...)
		}
	}

	if err := updateLastScrapedTime(ctx, nonUpdatedConfigs); err != nil {
		return fmt.Errorf("failed to update last scraped time: %w", err)
	}

	if err := ctx.DB().CreateInBatches(newChanges, configItemsBulkInsertSize).Error; err != nil {
		return fmt.Errorf("failed to create config changes: %w", err)
	}

	if len(changesToUpdate) != 0 {
		if err := ctx.DB().Save(changesToUpdate).Error; err != nil {
			return fmt.Errorf("failed to update config changes: %w", err)
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
				ctx.Logger.V(1).Infof("unable to form relationship. failed to find the parent config %s", relationship.ConfigExternalID)
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

type configExternalKey struct {
	externalID string
	parentType string
}

func extractConfigsAndChangesFromResults(ctx api.ScrapeContext, scrapeStartTime time.Time, isPartialResultSet bool, results []v1.ScrapeResult) ([]*models.ConfigItem, []*updateConfigArgs, []*models.ConfigChange, []*models.ConfigChange, error) {
	var (
		newConfigs      = make([]*models.ConfigItem, 0, len(results))
		configsToUpdate = make([]*updateConfigArgs, 0, len(results))

		newChanges      = make([]*models.ConfigChange, 0, len(results))
		changesToUpdate = make([]*models.ConfigChange, 0, len(results))

		tree       = graph.New(graph.StringHash, graph.Rooted(), graph.PreventCycles())
		allConfigs = make([]*models.ConfigItem, 0, len(results))

		parentTypeToConfigMap = make(map[configExternalKey]string)
	)

	for _, result := range results {
		result.LastScrapedTime = &scrapeStartTime

		var ci *models.ConfigItem
		var err error

		if result.ID != "" {
			// A result that only contains changes (example a result created by Cloudtrail scraper)
			// doesn't have any id.
			ci, err = NewConfigItemFromResult(ctx, result)
			if err != nil {
				return nil, nil, nil, nil, fmt.Errorf("unable to create config item(%s): %w", result, err)
			}

			ci.ScraperID = ctx.ScrapeConfig().GetPersistedID()
			if len(ci.ExternalID) == 0 {
				return nil, nil, nil, nil, fmt.Errorf("config item %s has no external id", ci)
			}

			parentExternalKey := configExternalKey{externalID: ci.ExternalID[0], parentType: lo.FromPtr(ci.Type)}
			parentTypeToConfigMap[parentExternalKey] = ci.ID

			existing := &models.ConfigItem{}
			if ci.ID != "" {
				if existing, err = ctx.TempCache().Get(ci.ID); err != nil {
					return nil, nil, nil, nil, fmt.Errorf("unable to lookup existing config(%s): %w", ci, err)
				}
			} else {
				if existing, err = ctx.TempCache().Find(*ci.Type, ci.ExternalID[0]); err != nil {
					return nil, nil, nil, nil, fmt.Errorf("unable to lookup external id(%s): %w", ci, err)
				}
			}

			allConfigs = append(allConfigs, ci)
			if result.Config != nil {
				if err := tree.AddVertex(ci.ID); err != nil && !errors.Is(err, graph.ErrVertexAlreadyExists) {
					return nil, nil, nil, nil, fmt.Errorf("unable to add vertex(%s): %w", ci, err)
				}

				if existing == nil || existing.ID == "" {
					newConfigs = append(newConfigs, ci)
				} else {
					// In case, we are not able to derive the path & parent_id
					// by forming a tree, we need to use the existing one
					// otherwise they'll be updated to empty values
					ci.ParentID = existing.ParentID
					ci.Path = existing.Path

					configsToUpdate = append(configsToUpdate, &updateConfigArgs{
						Result:   result,
						Existing: existing,
						New:      ci,
					})
				}
			}
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
	if err := setConfigParents(ctx, parentTypeToConfigMap, allConfigs); err != nil {
		return nil, nil, nil, nil, fmt.Errorf("unable to setup parents: %w", err)
	}

	if err := setConfigPaths(ctx, tree, isPartialResultSet, allConfigs); err != nil {
		return nil, nil, nil, nil, fmt.Errorf("unable to set config paths: %w", err)
	}

	// We sort the new config items such that parents are always first.
	// This avoids foreign key constraint errors.
	slices.SortFunc(newConfigs, func(a, b *models.ConfigItem) int {
		if len(a.Path) < len(b.Path) {
			return -1
		}

		if len(a.Path) > len(b.Path) {
			return 1
		}

		return 0
	})

	return newConfigs, configsToUpdate, newChanges, changesToUpdate, nil
}

func setConfigParents(ctx api.ScrapeContext, parentTypeToConfigMap map[configExternalKey]string, allConfigs []*models.ConfigItem) error {
	for _, ci := range allConfigs {
		if ci.ParentID != nil {
			continue // existing item. Parent is already set.
		}

		if ci.ParentExternalID == "" || ci.ParentType == "" {
			continue
		}

		if parentID, found := parentTypeToConfigMap[configExternalKey{
			externalID: ci.ParentExternalID,
			parentType: ci.ParentType,
		}]; found {
			ci.ParentID = &parentID
			continue
		}

		if found, err := ctx.TempCache().Find(ci.ParentType, ci.ParentExternalID); err != nil {
			return err
		} else if found != nil {
			ci.ParentID = &found.ID
		} else {
			ctx.Logger.V(0).Infof("[%s] parent %s/%s not found", ci, ci.ParentType, ci.ParentExternalID)
		}
	}

	return nil
}

func generatePartialTree(ctx api.ScrapeContext, tree graph.Graph[string, string], root string, allConfigs []*models.ConfigItem) error {
	// When we have a partial result set from an incremental scraper
	// we try to form a partial tree that's just sufficient to
	// connect the config item back to the root.
	//
	// The way to do that is by finding the parent with a db lookup.
	//
	// Example: on a partial result set of just a new Deployment, ReplicaSet & Pod
	// we only need to find the parent of the deployment, which is the namespace,
	// from the db as both ReplicaSet's & Pod's parent is withint the result set.

	configIDs := make(map[string]struct{})
	for _, c := range allConfigs {
		configIDs[c.ID] = struct{}{}
	}

	for _, c := range allConfigs {
		if c.ParentID == nil {
			// We aren't supposed to hit this point, except when an incremental scraper runs before a full scrape
			//
			// We fail early here than failing on db insert.
			return fmt.Errorf("a non root config found without a parent %s", c)
		}

		if _, found := configIDs[*c.ParentID]; found {
			continue
		}

		parent, err := ctx.TempCache().Get(*c.ParentID)
		if err != nil {
			return fmt.Errorf("unable to get parent(%s): %w", c, err)
		}

		nodes := strings.Split(parent.Path, ".")
		for i, n := range nodes {
			if err := tree.AddVertex(n); err != nil && !errors.Is(err, graph.ErrVertexAlreadyExists) {
				return fmt.Errorf("unable to add vertex(%s): %w", n, err)
			}

			if i == 0 {
				if err := tree.AddEdge(root, n); err != nil && !errors.Is(err, graph.ErrEdgeAlreadyExists) {
					return fmt.Errorf("unable to add edge(%s,%s): %w", root, n, err)
				}
			} else {
				if err := tree.AddEdge(nodes[i-1], n); err != nil && !errors.Is(err, graph.ErrEdgeAlreadyExists) {
					return fmt.Errorf("unable to add edge(%s,%s): %w", nodes[i-1], n, err)
				}
			}
		}
	}

	return nil
}

func setConfigPaths(ctx api.ScrapeContext, tree graph.Graph[string, string], isPartialResultSet bool, allConfigs []*models.ConfigItem) error {
	if ctx.ScrapeConfig().IsCustom() {
		// TODO: handle full mode
		// When full:true, we have some edge cases.
		return nil
	}

	// Add a virtual vertex which will be the root for the graph.
	// We do this so
	// 1. we don't need to define which config in the result set is the root for each scraper manually
	// 2. the virtual root can act as the root for all the config items
	//    that do not have a parent. example: for aws scraper
	//    all the aws regions & the aws account config does not have a parent.
	//    This solves the problem where a results set has multiple roots (example: aws scraper)
	var virtualRoot = "dummy"
	if err := tree.AddVertex(virtualRoot); err != nil && !errors.Is(err, graph.ErrVertexAlreadyExists) {
		return fmt.Errorf("unable to add vertex(%s): %w", virtualRoot, err)
	}

	if isPartialResultSet {
		if err := generatePartialTree(ctx, tree, virtualRoot, allConfigs); err != nil {
			return err
		}
	}

	for _, c := range allConfigs {
		if c.ParentID == nil || lo.FromPtr(c.ParentID) == "" {
			if err := tree.AddEdge(virtualRoot, c.ID); err != nil && !errors.Is(err, graph.ErrEdgeAlreadyExists) {
				return fmt.Errorf("unable to add edge(%s -> %s): %w", virtualRoot, c.ID, err)
			}
		} else {
			if err := tree.AddEdge(*c.ParentID, c.ID); err != nil && !errors.Is(err, graph.ErrEdgeAlreadyExists) {
				return fmt.Errorf("unable to add edge(%s -> %s): %w", *c.ParentID, c.ID, err)
			}
		}
	}

	for _, c := range allConfigs {
		if paths, err := graph.ShortestPath(tree, virtualRoot, c.ID); err != nil {
			ctx.Logger.V(1).Infof("unable to get the path for config(%s): %v", c, err)
		} else {
			// remove the virtual root
			paths = paths[1:]
			c.Path = strings.Join(paths, ".")
		}
	}

	return nil
}
