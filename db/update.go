package db

import (
	"context"
	"encoding/json"
	"fmt"
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
	"github.com/flanksource/config-db/utils"
	dutyModels "github.com/flanksource/duty/models"
	"github.com/google/uuid"
	"github.com/hexops/gotextdiff"
	"github.com/hexops/gotextdiff/myers"
	"github.com/lib/pq"
	"github.com/patrickmn/go-cache"
	"github.com/pkg/errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func deleteChangeHandler(ctx api.ScrapeContext, change v1.ChangeResult) error {
	var deletedAt interface{}
	if change.CreatedAt != nil {
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

	logger.Infof("Deleted %s from change %s", configs[0].ID, change)
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

func updateCI(ctx api.ScrapeContext, ci models.ConfigItem) error {
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

		if err := CreateConfigItem(&ci); err != nil {
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

	if err := UpdateConfigItem(&ci); err != nil {
		if err := CreateConfigItem(&ci); err != nil {
			return fmt.Errorf("[%s] failed to update item %v", ci, err)
		}
	}

	changes, err := generateConfigChange(ci, *existing)
	if err != nil {
		logger.Errorf("[%s] failed to check for changes: %v", ci, err)
	}

	if changes != nil {
		err := db.Create(changes).Error
		if nil == err {
			logger.Infof("[%s/%s] detected changes", ci.ConfigClass, ci.ExternalID[0])
		} else {
			if IsUniqueConstraintPGErr(err) {
				logger.Debugf("[%s] changes not stored. Another row with the same config_id & external_change_id exists.", ci)
			} else {
				logger.Errorf("[%s] failed to update with changes %v", ci, err)
			}
		}
	}

	return nil
}

func updateChange(ctx api.ScrapeContext, result *v1.ScrapeResult) error {
	for _, changeResult := range result.Changes {
		if changeResult.Action == v1.Ignore {
			continue
		}

		if changeResult.Action == v1.Delete {
			if err := deleteChangeHandler(ctx, changeResult); err != nil {
				return err
			}
			continue
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

	for _, result := range results {
		if result.Config != nil {
			ci, err := NewConfigItemFromResult(result)
			if err != nil {
				return errors.Wrapf(err, "unable to create config item: %s", result)
			}

			ci.ScraperID = ctx.ScrapeConfig().GetPersistedID()

			if err := updateCI(ctx, *ci); err != nil {
				return err
			}
		}

		if result.AnalysisResult != nil {
			if err := upsertAnalysis(ctx, &result); err != nil {
				return err
			}
		}

		if err := updateChange(ctx, &result); err != nil {
			return err
		}

		if result.RelationshipResults != nil {
			if err := relationshipResultHandler(result.RelationshipResults); err != nil {
				return err
			}
		}
	}

	if !startTime.IsZero() && ctx.ScrapeConfig().GetPersistedID() != nil {
		// Any analysis that weren't observed again will be marked as resolved
		if err := UpdateAnalysisStatusBefore(ctx, startTime, string(ctx.ScrapeConfig().GetUID()), dutyModels.AnalysisStatusResolved); err != nil {
			logger.Errorf("failed to mark analysis before %v as healthy: %v", startTime, err)
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
func generateConfigChange(newConf, prev models.ConfigItem) (*dutyModels.ConfigChange, error) {
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

	return &dutyModels.ConfigChange{
		ConfigID:         newConf.ID,
		ChangeType:       "diff",
		ExternalChangeId: utils.Sha256Hex(string(patch)),
		ID:               ulid.MustNew().AsUUID(),
		Diff:             diff,
		Patches:          string(patch),
		Summary:          strings.Join(utils.ExtractLeafNodesAndCommonParents(patchJSON), ", "),
	}, nil
}

func relationshipResultHandler(relationships v1.RelationshipResults) error {
	var configItemRelationships []models.ConfigRelationship
	for _, relationship := range relationships {
		configID, err := FindConfigItemID(relationship.ConfigExternalID)
		if err != nil {
			logger.Errorf("error fetching config item(id=%s): %v", relationship.ConfigExternalID, err)
			continue
		}
		if configID == nil {
			logger.Debugf("failed to find config %s", relationship.ConfigExternalID)
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
				logger.Debugf("related external config item(id=%s) not found.", relationship.RelatedExternalID)
				continue
			}
		}

		// In first iteration, the database is not completely populated
		// so there can be missing references to config items
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
