package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db/models"
	dutyModels "github.com/flanksource/duty/models"
	"github.com/lib/pq"
	"github.com/ohler55/ojg/oj"
	"github.com/patrickmn/go-cache"
	"gorm.io/gorm/clause"
)

// GetConfigItem returns a single config item result
func GetConfigItem(extType, extID string) (*models.ConfigItem, error) {
	ci := models.ConfigItem{}
	tx := db.Limit(1).Find(&ci, "external_type = ? and external_id  @> ?", extType, pq.StringArray{extID})
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

// FindConfigItemID returns the uuid of config_item which matches the externalUID
func FindConfigItemID(externalID v1.ExternalID) (*string, error) {
	if ciID, exists := cacheStore.Get(externalID.CacheKey()); exists {
		return ciID.(*string), nil
	}

	var ci models.ConfigItem
	queryDB := externalID.WhereClause(db)
	tx := queryDB.Select("id").Limit(1).Find(&ci)
	if tx.RowsAffected == 0 {
		return nil, nil
	}
	if tx.Error != nil {
		return nil, tx.Error
	}

	cacheStore.Set(externalID.CacheKey(), &ci.ID, cache.DefaultExpiration)
	return &ci.ID, nil
}

func FindConfigItemFromType(configType string) ([]models.ConfigItem, error) {
	var ci []models.ConfigItem
	err := db.Find(&ci, "external_type = @type OR config_type = @type", sql.Named("type", configType)).Error
	return ci, err
}

// CreateConfigItem inserts a new config item row in the db
func CreateConfigItem(ci *models.ConfigItem) error {
	if err := db.Create(ci).Error; err != nil {
		return err
	}
	return nil
}

// UpdateConfigItem updates all the fields of a given config item row
func UpdateConfigItem(ci *models.ConfigItem) error {

	if err := db.Updates(ci).Error; err != nil {
		return err
	}

	// Since gorm ignores nil fields, we are setting deleted_at explicitly
	if ci.DeletedAt != nil {
		if err := db.Table("config_items").Where("id = ?", ci.ID).UpdateColumn("deleted_at", nil).Error; err != nil {
			return err
		}
	}

	return nil
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
func NewConfigItemFromResult(result v1.ScrapeResult) (*models.ConfigItem, error) {
	var dataStr string
	switch data := result.Config.(type) {
	case string:
		dataStr = data
	case []byte:
		dataStr = string(data)
	default:
		dataStr = oj.JSON(data, &oj.Options{Sort: true, OmitNil: true, Indent: 2, TimeFormat: "2006-01-02T15:04:05Z07:00"})
	}

	ci := &models.ConfigItem{
		ExternalID:   append(result.Aliases, result.ID),
		ID:           result.ID,
		ConfigType:   result.Type,
		ExternalType: &result.ExternalType,
		Account:      &result.Account,
		Region:       &result.Region,
		Zone:         &result.Zone,
		Network:      &result.Network,
		Subnet:       &result.Subnet,
		Name:         &result.Name,
		Source:       &result.Source,
		Tags:         &result.Tags,
		Config:       &dataStr,
	}

	if result.CreatedAt != nil {
		ci.CreatedAt = *result.CreatedAt
	}

	if result.DeletedAt != nil {
		ci.DeletedAt = result.DeletedAt
	}

	if result.ParentExternalID != "" && result.ParentExternalType != "" {
		parentExternalID := v1.ExternalID{
			ExternalType: result.ParentExternalType,
			ExternalID:   []string{result.ParentExternalID},
		}

		var err error
		ci.ParentID, err = FindConfigItemID(parentExternalID)
		if err != nil {
			logger.Errorf("Error fetching parent for %v", parentExternalID)
		}

		// Path will be correct after second iteration of scraping since
		// the first iteration will populate the parent_ids
		// in a non deterministic order
		ci.Path = getParentPath(parentExternalID)
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
	if len(relationships) == 0 {
		return nil
	}

	tx := db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "config_id"}, {Name: "related_id"}, {Name: "selector_id"}},
		UpdateAll: true,
	}).Create(&relationships)
	return tx.Error
}

// FindConfigChangesByItemID returns all the changes of the given config item
func FindConfigChangesByItemID(ctx context.Context, configItemID string) ([]dutyModels.ConfigChange, error) {
	var ci []dutyModels.ConfigChange
	tx := db.WithContext(ctx).Where("config_id = ?", configItemID).Find(&ci)
	if tx.Error != nil {
		return nil, tx.Error
	}

	return ci, nil
}
