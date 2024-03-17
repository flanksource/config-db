package db

import (
	gocontext "context"
	"encoding/json"
	"fmt"

	"github.com/flanksource/commons/logger"
	"github.com/flanksource/commons/utils"
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db/models"
	"github.com/flanksource/duty/context"
	dutyModels "github.com/flanksource/duty/models"
	"github.com/flanksource/duty/query"
	"github.com/flanksource/duty/types"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/ohler55/ojg/oj"
	"gorm.io/gorm/clause"
)

// GetConfigItem returns a single config item result
func GetConfigItem(extType, extID string) (*models.ConfigItem, error) {
	ci := models.ConfigItem{}
	tx := db.
		Select("id", "config_class", "type", "config", "created_at", "updated_at", "deleted_at").
		Limit(1).
		Find(&ci, "type = ? and external_id  @> ?", extType, pq.StringArray{extID})
	if tx.RowsAffected == 0 {
		return nil, nil
	}
	if tx.Error != nil {
		return nil, tx.Error
	}

	return &ci, nil
}

// GetConfigItemFromID returns a single config item result
func GetConfigItemFromID(id string) (*models.ConfigItem, error) {
	var ci models.ConfigItem
	err := db.Limit(1).Omit("config").Find(&ci, "id = ?", id).Error
	return &ci, err
}

// CreateConfigItem inserts a new config item row in the db
func CreateConfigItem(ci *models.ConfigItem) error {
	if err := db.Clauses(clause.OnConflict{UpdateAll: true}).Create(ci).Error; err != nil {
		return err
	}
	return nil
}

func FindConfigIDsByRelationshipSelector(ctx context.Context, selector v1.RelationshipSelector) ([]uuid.UUID, error) {
	if selector.IsEmpty() {
		return nil, nil
	}

	return query.FindConfigIDsByResourceSelector(ctx, selector.ToResourceSelector())
}

// FindConfigIDsByNamespaceNameClass returns the uuid of config items which matches the given type, name & namespace
func FindConfigIDsByNamespaceNameClass(ctx context.Context, namespace, name, configClass string) ([]uuid.UUID, error) {
	rs := types.ResourceSelector{
		Name:          name,
		Namespace:     namespace,
		FieldSelector: fmt.Sprintf("config_class=%s", configClass),
	}

	return query.FindConfigIDsByResourceSelector(ctx, rs)
}

// QueryConfigItems ...
func QueryConfigItems(request v1.QueryRequest) (*v1.QueryResult, error) {
	results := db.Raw(request.Query)
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
			Sort:       true,
			OmitNil:    true,
			Indent:     2,
			TimeFormat: "2006-01-02T15:04:05Z07:00",
			UseTags:    true,
		})
	}

	ci := &models.ConfigItem{
		ExternalID:      append(result.Aliases, result.ID),
		ID:              utils.Deref(result.ConfigID),
		ConfigClass:     result.ConfigClass,
		Type:            &result.Type,
		Name:            &result.Name,
		Namespace:       &result.Namespace,
		Source:          &result.Source,
		Tags:            &result.Tags,
		Properties:      &result.Properties,
		Config:          &dataStr,
		LastScrapedTime: result.LastScrapedTime,
	}

	// If the config result hasn't specified an id for the config,
	// we try to use the external id as the primary key of the config item.
	if ci.ID == "" && uuid.Validate(result.ID) == nil {
		ci.ID = result.ID
	}

	if result.Status != "" {
		ci.Status = &result.Status
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

	if result.ParentExternalID != "" && result.ParentType != "" {

		if found, err := ctx.TempCache().Find(result.ParentType, result.ParentExternalID); err != nil {
			return nil, err
		} else {
			ci.ParentID = &found.ID
		}
	}

	return ci, nil
}

func GetJSON(ci models.ConfigItem) []byte {
	data, err := json.Marshal(ci.Config)
	if err != nil {
		logger.Errorf("Failed to marshal config: %+v", err)
	}
	return data
}

func UpdateConfigRelatonships(relationships []models.ConfigRelationship) error {
	return db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "config_id"}, {Name: "related_id"}, {Name: "selector_id"}},
		DoNothing: true,
	}).CreateInBatches(relationships, 200).Error
}

// FindConfigChangesByItemID returns all the changes of the given config item
func FindConfigChangesByItemID(ctx gocontext.Context, configItemID string) ([]dutyModels.ConfigChange, error) {
	var ci []dutyModels.ConfigChange
	tx := db.WithContext(ctx).Where("config_id = ?", configItemID).Find(&ci)
	if tx.Error != nil {
		return nil, tx.Error
	}

	return ci, nil
}
