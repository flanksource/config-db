package repository

import (
	"fmt"

	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/confighub/api/v1"
	"github.com/flanksource/confighub/db/models"
	"gorm.io/gorm"
)

// DBRepo should satisfy the database repository interface
type DBRepo struct {
	db *gorm.DB
}

// NewRepo is the factory function for the database repo instance
func NewRepo(db *gorm.DB) Database {
	return &DBRepo{
		db: db,
	}
}

// GetConfigItem returns a single config item result
func (d *DBRepo) GetConfigItem(extID string) (*models.ConfigItem, error) {
	ci := models.ConfigItem{}
	if err := d.db.First(&ci, "external_id = ?", extID).Error; err != nil {
		return nil, err
	}

	return &ci, nil
}

// CreateConfigItem inserts a new config item row in the db
func (d *DBRepo) CreateConfigItem(ci *models.ConfigItem) error {
	if err := d.db.Create(ci).Error; err != nil {
		return err
	}

	return nil
}

// UpdateConfigItem updates all the fields of a given config item row
func (d *DBRepo) UpdateConfigItem(ci *models.ConfigItem) error {
	if err := d.db.Save(ci).Error; err != nil {
		return err
	}

	return nil
}

// CreateConfigChange inserts a new config change row in the db
func (d *DBRepo) CreateConfigChange(cc *models.ConfigChange) error {
	if err := d.db.Create(cc).Error; err != nil {
		return err
	}

	return nil
}

func (d *DBRepo) QueryConfigItems(request v1.QueryRequest) (*v1.QueryResult, error) {
	results := d.db.Raw(request.Query)
	logger.Tracef(request.Query)
	if results.Error != nil {
		return nil, fmt.Errorf("failed to parse query: %s -> %s", request.Query, results.Error)
	}

	response := v1.QueryResult{
		Results: make([]map[string]interface{}, 0),
	}

	if rows, err := results.Rows(); err != nil {
		return nil, fmt.Errorf("failed to run query: %s -> %s", request.Query, err)
	} else {

		columns, err := rows.Columns()
		if err != nil {
			logger.Errorf("failed to get column details: %v", err)
		}
		rows.Next()
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

func isRowEmpty(row map[string]interface{}) bool {
	for _, v := range row {
		if v == nil || fmt.Sprintf("%v", v) == "" {
			return true
		}
	}
	return false
}
