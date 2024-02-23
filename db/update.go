package db

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/aws/smithy-go/ptr"
	jsonpatch "github.com/evanphx/json-patch"
	"github.com/flanksource/commons/hash"
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
	"github.com/patrickmn/go-cache"
	"github.com/pkg/errors"
	"github.com/samber/lo"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func deleteChangeHandler(ctx api.ScrapeContext, change v1.ChangeResult) error {
	var deletedAt interface{}
	if change.CreatedAt != nil && !change.CreatedAt.IsZero() {
		deletedAt = change.CreatedAt
	} else {
		deletedAt = gorm.Expr("NOW()")
	}

	configs := []models.ConfigItem{}
	tx := db.Model(&configs).
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

	logger.Debugf("Deleted %s from change %s", configs[0].ID, change)
	return nil
}

func getConfigItemParentID(id string) string {
	cacheKey := parentIDCacheKey(id)
	if parentID, exists := cacheStore.Get(cacheKey); exists {
		return parentID.(string)
	}

	ci, err := GetConfigItemFromID(id)
	if err != nil {
		logger.Errorf("Error fetching config item with id: %s", id)
		return ""
	}
	if ci.ParentID == nil {
		return ""
	}
	cacheStore.Set(cacheKey, *ci.ParentID, cache.DefaultExpiration)
	return *ci.ParentID
}

func getParentPath(parentExternalUID v1.ExternalID) string {
	var path string
	parentID, _ := FindConfigItemID(parentExternalUID)
	if parentID == nil {
		return ""
	}

	id := *parentID
	path += id
	for {
		id = getConfigItemParentID(id)
		if id == "" {
			break
		}
		path += "." + id
	}
	return path
}

func updateCI(ctx api.ScrapeContext, result v1.ScrapeResult) error {
	ci, err := NewConfigItemFromResult(result)
	if err != nil {
		return errors.Wrapf(err, "unable to create config item: %s", result)
	}

	ci.ScraperID = ctx.ScrapeConfig().GetPersistedID()

	existing, err := GetConfigItem(*ci.Type, ci.ID)
	if err != nil && err != gorm.ErrRecordNotFound {
		return errors.Wrapf(err, "unable to lookup existing config: %s", ci)
	}

	if existing == nil {
		// Use the resource id as the config item's primary key.
		// If it isn't a valid UUID, we generate a new one.
		if parsed, err := uuid.Parse(ci.ID); err != nil || parsed == uuid.Nil {
			id, err := hash.DeterministicUUID(ci.ID)
			if err != nil {
				return fmt.Errorf("error generating uuid for config (id:%s): %w", ci.ID, err)
			}

			ci.ID = id.String()
		}

		if err := CreateConfigItem(ci); err != nil {
			logger.Errorf("[%s] failed to create item %v", ci, err)
		}

		return nil
	}

	ci.ID = existing.ID

	// In case a resource was marked as deleted but is un-deleted now
	// we set an update flag as gorm ignores nil pointers
	if ci.DeletedAt != existing.DeletedAt {
		ci.TouchDeletedAt = true
	}

	if err := UpdateConfigItem(ci); err != nil {
		if err := CreateConfigItem(ci); err != nil {
			return fmt.Errorf("[%s] failed to update item %v", ci, err)
		}
	}

	changeResult, err := generateConfigChange(*ci, *existing)
	if err != nil {
		logger.Errorf("[%s] failed to check for changes: %v", ci, err)
	} else if changeResult != nil {
		logger.Debugf("[%s/%s] detected changes", *ci.Type, ci.ExternalID[0])
		result.Changes = []v1.ChangeResult{*changeResult}
		if err := saveChanges(ctx, &result); err != nil {
			return fmt.Errorf("[%s] failed to save %d changes: %w", ci, len(result.Changes), err)
		}
	}

	return nil
}

func shouldExcludeChange(ctx dutyContext.Context, result *v1.ScrapeResult, changeResult v1.ChangeResult) (bool, error) {
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

func saveChanges(ctx api.ScrapeContext, result *v1.ScrapeResult) error {
	changes.ProcessRules(result, result.BaseScraper.Transform.Change.Mapping...)

	for _, changeResult := range result.Changes {
		if changeResult.Action == v1.Ignore {
			continue
		}

		if exclude, err := shouldExcludeChange(ctx.DutyContext(), result, changeResult); err != nil {
			return err
		} else if exclude {
			ctx.DutyContext().Tracef("excluded change: %v", changeResult)
			continue
		}

		if changeResult.Action == v1.Delete {
			if err := deleteChangeHandler(ctx, changeResult); err != nil {
				return err
			}
		}

		change := models.NewConfigChangeFromV1(*result, changeResult)

		if change.CreatedBy != nil {
			person, err := FindPersonByEmail(ctx, ptr.ToString(change.CreatedBy))
			if err != nil {
				return fmt.Errorf("error finding person by email: %w", err)
			} else if person != nil {
				change.CreatedBy = ptr.String(person.ID.String())
			} else {
				change.ExternalCreatedBy = change.CreatedBy
				change.CreatedBy = nil
			}
		}

		id, err := FindConfigItemID(change.GetExternalID())
		if err != nil {
			return err
		} else if id == nil {
			logger.Warnf("[Source=%s] [%s/%s] unable to find config item for change: %v", change.Source, change.ConfigType, change.ExternalID, change.ChangeType)
			return nil
		}

		change.ConfigID = *id

		if changeResult.UpdateExisting {
			if err := db.Save(change).Error; err != nil {
				return err
			}
		} else {
			if err := db.Create(change).Error; err != nil {
				return err
			}
		}
	}

	return nil
}

func upsertAnalysis(ctx api.ScrapeContext, result *v1.ScrapeResult) error {
	analysis := result.AnalysisResult.ToConfigAnalysis()
	ciID, err := FindConfigItemID(v1.ExternalID{
		ConfigType: analysis.ConfigType,
		ExternalID: []string{analysis.ExternalID},
	})
	if ciID == nil {
		logger.Warnf("[Source=%s] [%s/%s] unable to find config item for analysis: %+v", analysis.Source, analysis.ConfigType, analysis.ExternalID, analysis)
		return nil
	} else if err != nil {
		return err
	}

	logger.Tracef("[%s/%s] ==> %s", analysis.ConfigType, analysis.ExternalID, analysis)
	analysis.ConfigID, err = uuid.Parse(*ciID)
	if err != nil {
		return err
	}
	analysis.ID = uuid.MustParse(ulid.MustNew().AsUUID())
	analysis.ScraperID = ctx.ScrapeConfig().GetPersistedID()
	if analysis.Status == "" {
		analysis.Status = dutyModels.AnalysisStatusOpen
	}

	return CreateAnalysis(analysis)
}

func GetCurrentDBTime(ctx context.Context) (time.Time, error) {
	var now time.Time
	err := db.WithContext(ctx).Raw(`SELECT CURRENT_TIMESTAMP`).Scan(&now).Error
	return now, err
}

// UpdateAnalysisStatusBefore updates the status of config analyses that were last observed before the specified time.
func UpdateAnalysisStatusBefore(ctx context.Context, before time.Time, scraperID, status string) error {
	return db.WithContext(ctx).
		Model(&dutyModels.ConfigAnalysis{}).
		Where("last_observed <= ? AND first_observed <= ?", before, before).
		Where("scraper_id = ?", scraperID).
		Update("status", status).
		Error
}

// SaveResults creates or update a configuartion with config changes
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

	for _, result := range results {
		if result.Config != nil {
			if err := updateCI(ctx, result); err != nil {
				return err
			}
		}

		if result.AnalysisResult != nil {
			if err := upsertAnalysis(ctx, &result); err != nil {
				return err
			}
		}

		if err := saveChanges(ctx, &result); err != nil {
			return err
		}

		relationshipToForm = append(relationshipToForm, result.RelationshipResults...)
		resultsWithRelationshipSelectors = append(resultsWithRelationshipSelectors, result)
	}

	if res, err := relationshipSelectorToResults(ctx.DutyContext(), resultsWithRelationshipSelectors); err != nil {
		return fmt.Errorf("failed to get relationship results from relationship selectors: %w", err)
	} else {
		relationshipToForm = append(relationshipToForm, res...)
	}

	if err := relationshipResultHandler(relationshipToForm); err != nil {
		return fmt.Errorf("failed to form relationships: %w", err)
	}

	if !startTime.IsZero() && ctx.ScrapeConfig().GetPersistedID() != nil {
		// Any analysis that weren't observed again will be marked as resolved
		if err := UpdateAnalysisStatusBefore(ctx, startTime, string(ctx.ScrapeConfig().GetUID()), dutyModels.AnalysisStatusResolved); err != nil {
			logger.Errorf("failed to mark analysis before %v as healthy: %v", startTime, err)
		}
	}

	logger.Debugf("saved %d results.", len(results))
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

	logger.Debugf("forming %d relationships from selectors", len(relationships))
	return relationships, nil
}

func relationshipResultHandler(relationships v1.RelationshipResults) error {
	logger.Debugf("saving %d relationships", len(relationships))

	var configItemRelationships []models.ConfigRelationship
	for _, relationship := range relationships {
		configID, err := FindConfigItemID(relationship.ConfigExternalID)
		if err != nil {
			logger.Errorf("error fetching config item(id=%s): %v", relationship.ConfigExternalID, err)
			continue
		}
		if configID == nil {
			logger.Warnf("unable to form relationship. failed to find config %s", relationship.ConfigExternalID)
			continue
		}

		var relatedID *string
		if relationship.RelatedConfigID != "" {
			relatedID = &relationship.RelatedConfigID
		} else {
			relatedID, err = FindConfigItemID(relationship.RelatedExternalID)
			if err != nil {
				logger.Errorf("error fetching external config item(id=%s): %v", relationship.RelatedExternalID, err)
				continue
			}
			if relatedID == nil {
				logger.Warnf("unable to form relationship. failed to find related config %s.", relationship.RelatedExternalID)
				continue
			}
		}

		// The configs in the relationships might not be found for various reasons.
		// - the related configs might have been excluded in the scrape config
		// - the config might have been deleted
		if relatedID == nil || configID == nil {
			continue
		}

		configItemRelationships = append(configItemRelationships, models.ConfigRelationship{
			ConfigID:  *configID,
			RelatedID: *relatedID,
			Relation:  relationship.Relationship,
		})
	}

	return UpdateConfigRelatonships(configItemRelationships)
}
