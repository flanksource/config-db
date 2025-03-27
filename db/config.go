package db

import (
	"fmt"
	"slices"

	"github.com/flanksource/commons/hash"
	"github.com/flanksource/commons/logger"
	"github.com/flanksource/commons/utils"
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db/models"
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
)

// GetConfigItem returns a single config item result
func GetConfigItem(ctx api.ScrapeContext, extType, extID string) (*models.ConfigItem, error) {
	ci := models.ConfigItem{}
	tx := ctx.DB().
		Select("id", "config_class", "type", "config", "created_at", "updated_at", "deleted_at").
		Limit(1).
		Find(&ci, "type = ? and external_id  @> ?", extType, pq.StringArray{extID})
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
		Results: make([]map[string]interface{}, 0),
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
			OmitNil:     true,
			Indent:      2,
			TimeFormat:  "2006-01-02T15:04:05Z07:00",
			UseTags:     true,
			FloatFormat: "%g",
		})
	}

	ci := &models.ConfigItem{
		ExternalID:      append([]string{result.ID}, result.Aliases...),
		ID:              utils.Deref(result.ConfigID),
		ConfigClass:     result.ConfigClass,
		Type:            result.Type,
		Name:            &result.Name,
		Source:          &result.Source,
		Labels:          &result.Labels,
		Properties:      &result.Properties,
		Config:          &dataStr,
		Ready:           result.Ready,
		LastScrapedTime: result.LastScrapedTime,
		Parents:         result.Parents,
		Health:          lo.ToPtr(dutyModels.HealthUnknown),
		Children:        result.Children,
		ScraperID:       ctx.ScrapeConfig().GetPersistedID(),
	}

	if result.ScraperLess || slices.Contains(v1.ScraperLessTypes, ci.Type) {
		ci.ScraperID = nil
	}

	ci.Tags = result.Tags.AsMap()
	// If the config result hasn't specified an id for the config,
	// we try to use the external id as the primary key of the config item.
	if ci.ID == "" {
		if uuid.Validate(result.ID) == nil {
			ci.ID = result.ID
		} else {
			id, err := hash.DeterministicUUID(result.ID)
			if err != nil {
				return nil, fmt.Errorf("error generating uuid for config (id:%s): %w", result.ID, err)
			}
			ci.ID = id.String()
		}
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

func UpdateConfigRelatonships(ctx api.ScrapeContext, relationships []models.ConfigRelationship) error {
	return dutydb.ErrorDetails(ctx.DB().Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "config_id"}, {Name: "related_id"}, {Name: "relation"}},
		DoNothing: true,
	}).CreateInBatches(relationships, 200).Error)
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
