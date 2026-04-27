package db

import (
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/flanksource/commons/hash"
	"github.com/flanksource/commons/logger"
	"github.com/flanksource/commons/utils"
	"github.com/flanksource/duty"
	"github.com/flanksource/duty/context"
	dutydb "github.com/flanksource/duty/db"
	dutyModels "github.com/flanksource/duty/models"
	"github.com/flanksource/duty/query"
	"github.com/flanksource/duty/types"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/ohler55/ojg/oj"
	"github.com/samber/lo"
	"gorm.io/gorm/clause"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db/models"
)

// GetConfigItem returns a single config item result
func GetConfigItem(ctx api.ScrapeContext, extType, extID string) (*models.ConfigItem, error) {
	ci := models.ConfigItem{}
	tx := ctx.DB().
		Select("id", "config_class", "type", "config", "created_at", "updated_at", "deleted_at").
		Limit(1).
		Find(&ci, "type = ? and external_id  @> ?", extType, pq.StringArray{v1.NormalizeExternalID(extID)})
	if tx.RowsAffected == 0 {
		return nil, nil
	}
	if tx.Error != nil {
		return nil, dutydb.ErrorDetails(tx.Error)
	}

	return &ci, nil
}

// CreateConfigItem inserts a new config item row in the db
func CreateConfigItem(ctx api.ScrapeContext, ci *models.ConfigItem) error {
	if err := ctx.DB().Clauses(clause.OnConflict{UpdateAll: true}).Create(ci).Error; err != nil {
		return dutydb.ErrorDetails(err)
	}
	return nil
}

func FindConfigIDsByRelationshipSelector(ctx context.Context, selector duty.RelationshipSelector) ([]uuid.UUID, error) {
	if selector.IsEmpty() {
		return nil, nil
	}

	return query.FindConfigIDsByResourceSelector(ctx, 0, selector.ToResourceSelector())
}

func FindConfigsByRelationshipSelector(ctx context.Context, selector duty.RelationshipSelector) ([]dutyModels.ConfigItem, error) {
	if selector.IsEmpty() {
		return nil, nil
	}
	return query.FindConfigsByResourceSelector(ctx, 0, selector.ToResourceSelector())
}

// FindConfigIDsByNamespaceNameClass returns the uuid of config items which matches the given type, name & namespace
func FindConfigIDsByNamespaceNameClass(ctx context.Context, cluster, namespace, name, configClass string) ([]uuid.UUID, error) {
	rs := types.ResourceSelector{
		Name:        name,
		Namespace:   namespace,
		TagSelector: fmt.Sprintf("cluster=%s", cluster),
		Search:      fmt.Sprintf("config_class=%s", configClass),
	}

	return query.FindConfigIDsByResourceSelector(ctx, 0, rs)
}

// QueryConfigItems ...
func QueryConfigItems(ctx api.ScrapeContext, request v1.QueryRequest) (*v1.QueryResult, error) {
	results := ctx.DB().Raw(request.Query)
	logger.Tracef(request.Query)
	if results.Error != nil {
		return nil, fmt.Errorf("failed to parse query: %s -> %s", request.Query, results.Error)
	}

	response := v1.QueryResult{
		Results: make([]map[string]any, 0),
	}

	rows, err := results.Rows()
	if err != nil {
		return nil, fmt.Errorf("failed to run query: %s -> %s", request.Query, err)
	}

	columns, err := rows.Columns()
	if err != nil {
		logger.Errorf("failed to get column details: %v", err)
	}
	if rows.Next() {
		if err := results.ScanRows(rows, &response.Results); err != nil {
			return nil, fmt.Errorf("failed to scan rows: %s -> %s", request.Query, err)
		}
		for _, col := range columns {
			response.Columns = append(response.Columns, v1.QueryColumn{
				Name: col,
			})
		}
	}

	response.Count = len(response.Results)
	return &response, nil
}

// NewConfigItemFromResult creates a new config item instance from result
func NewConfigItemFromResult(ctx api.ScrapeContext, result v1.ScrapeResult) (*models.ConfigItem, error) {
	var dataStr string
	switch data := result.Config.(type) {
	case string:
		dataStr = data
	case []byte:
		dataStr = string(data)
	default:
		dataStr = oj.JSON(data, &oj.Options{
			Sort:        true,
			OmitNil:     result.OmitNil(),
			Indent:      2,
			TimeFormat:  "2006-01-02T15:04:05Z07:00",
			UseTags:     true,
			FloatFormat: "%g",
		})
	}

	// Lowercase + unique  all external ids for easy matching
	externalIDs := append([]string{result.ID}, result.Aliases...)
	externalIDs = lo.Uniq(externalIDs)
	externalIDs = lo.Map(externalIDs, func(s string, _ int) string { return v1.NormalizeExternalID(s) })
	externalIDs = lo.Filter(externalIDs, func(s string, _ int) bool { return s != "" })

	ci := &models.ConfigItem{
		ExternalID:  externalIDs,
		ID:          utils.Deref(result.ConfigID),
		ConfigClass: result.ConfigClass,
		Type:        result.Type,
		Name:        &result.Name,
		Source:      &result.Source,
		Labels:      (*types.JSONStringMap)(&result.Labels),
		Properties:  &result.Properties,
		Config:      &dataStr,
		Ready:       result.Ready,
		Parents:     result.Parents,
		Health:      lo.ToPtr(dutyModels.HealthUnknown),
		Children:    result.Children,
		ScraperID:   ctx.ScrapeConfig().GetPersistedID(),
	}

	if result.ScraperLess || slices.Contains(v1.ScraperLessTypes, ci.Type) {
		ci.ScraperID = nil
	}

	ci.Tags = types.JSONStringMap(result.Tags)
	// If the config result hasn't specified an id for the config,
	// we try to use the external id as the primary key of the config item.
	if ci.ID == "" {
		id, err := hash.DeterministicUUID(result.ID)
		if err != nil {
			return nil, fmt.Errorf("error generating uuid for config (id:%s): %w", result.ID, err)
		}
		ci.ID = id.String()
	}

	if result.Status != "" {
		ci.Status = &result.Status
	}

	if result.Health != "" {
		ci.Health = &result.Health
	}

	if result.Description != "" {
		ci.Description = &result.Description
	}

	if result.CreatedAt != nil {
		ci.CreatedAt = *result.CreatedAt
	}

	if result.DeletedAt != nil {
		ci.DeletedAt = result.DeletedAt
		ci.DeleteReason = result.DeleteReason
	}

	return ci, nil
}

var configRelationshipUpdateMutex sync.Mutex
var mutexWaitBucketsMs = []float64{500, 1_000, 3_000, 5_000, 10_000, 15_000, 30_000, 60_000, 100_000, 150_000, 300_000, 600_000}

func UpdateConfigRelatonships(ctx api.ScrapeContext, relationships []models.ConfigRelationship) error {
	// The mutex prevents ShareLock deadlock errors since this function can be called by
	// both Scraper & consumeWatchKubernetes job and might end up trying to update the same rows
	// at the same time
	lockWaitStart := time.Now()
	configRelationshipUpdateMutex.Lock()
	ctx.Histogram("config_relationship_update_mutex_wait_ms", mutexWaitBucketsMs).Record(time.Duration(time.Since(lockWaitStart).Milliseconds()))
	defer configRelationshipUpdateMutex.Unlock()

	scraperID := uuid.Nil
	if sc := ctx.ScrapeConfig(); sc != nil && sc.GetPersistedID() != nil {
		scraperID = *sc.GetPersistedID()
	}

	for i := range relationships {
		if relationships[i].ScraperID == uuid.Nil {
			relationships[i].ScraperID = scraperID
		}
	}

	tx := ctx.DB().Begin()
	if tx.Error != nil {
		return dutydb.ErrorDetails(tx.Error)
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
			panic(r)
		}
	}()

	tempTable := fmt.Sprintf("_config_relationships_%s", sanitizeForTempTable(scraperID.String()))
	if err := tx.Exec(fmt.Sprintf(`CREATE TEMP TABLE %s (LIKE config_relationships INCLUDING ALL) ON COMMIT DROP`, tempTable)).Error; err != nil {
		tx.Rollback()
		return dutydb.ErrorDetails(err)
	}
	if len(relationships) > 0 {
		if err := tx.Table(tempTable).Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(relationships, 500).Error; err != nil {
			tx.Rollback()
			return dutydb.ErrorDetails(err)
		}
	}

	if err := tx.Exec(fmt.Sprintf(`
		INSERT INTO config_relationships (config_id, related_id, relation, scraper_id)
		SELECT config_id, related_id, relation, scraper_id FROM %s
		ON CONFLICT (related_id, config_id, relation, scraper_id) DO UPDATE SET
			deleted_at = NULL,
			updated_at = NOW()
		WHERE config_relationships.deleted_at IS NOT NULL
	`, tempTable)).Error; err != nil {
		tx.Rollback()
		return dutydb.ErrorDetails(err)
	}

	if scraperID != uuid.Nil && !ctx.IsIncrementalScrape() {
		if err := tx.Exec(fmt.Sprintf(`
			UPDATE config_relationships
			SET deleted_at = NOW(), updated_at = NOW()
			WHERE scraper_id = ?
				AND deleted_at IS NULL
				AND NOT EXISTS (
					SELECT 1 FROM %s t
					WHERE t.config_id = config_relationships.config_id
						AND t.related_id = config_relationships.related_id
						AND t.relation = config_relationships.relation
						AND t.scraper_id = config_relationships.scraper_id
				)
		`, tempTable), scraperID).Error; err != nil {
			tx.Rollback()
			return dutydb.ErrorDetails(err)
		}
	}

	return dutydb.ErrorDetails(tx.Commit().Error)
}

// FindConfigChangesByItemID returns all the changes of the given config item
func FindConfigChangesByItemID(ctx api.ScrapeContext, configItemID string) ([]dutyModels.ConfigChange, error) {
	var ci []dutyModels.ConfigChange
	tx := ctx.DB().Where("config_id = ?", configItemID).Find(&ci)
	if tx.Error != nil {
		return nil, dutydb.ErrorDetails(tx.Error)
	}

	return ci, nil
}

func SoftDeleteConfigItems(ctx context.Context, reason v1.ConfigDeleteReason, ids ...string) (int, error) {
	tx := ctx.DB().
		Model(&models.ConfigItem{}).
		Where("id IN ?", ids).
		UpdateColumns(
			map[string]any{
				"deleted_at":    duty.Now(),
				"delete_reason": reason,
			},
		)
	return int(tx.RowsAffected), tx.Error
}
