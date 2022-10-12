package repository

import (
	"errors"
	"fmt"

	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db/models"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"gorm.io/gorm"
)

// DBRepo should satisfy the database repository interface
type DBRepo struct {
	*gorm.DB
}

// NewRepo is the factory function for the database repo instance
func NewRepo(db *gorm.DB) Database {
	return &DBRepo{
		DB: db,
	}
}

// GetConfigItem returns a single config item result
func (d *DBRepo) GetConfigItem(extType, extID string) (*models.ConfigItem, error) {
	ci := models.ConfigItem{}
	tx := d.Limit(1).Find(&ci, "external_type = ? and external_id  @> ?", extType, pq.StringArray{extID})
	if tx.RowsAffected == 0 {
		return nil, nil
	}
	if tx.Error != nil {
		return nil, tx.Error
	}

	return &ci, nil
}

// CreateConfigItem inserts a new config item row in the db
func (d *DBRepo) CreateConfigItem(ci *models.ConfigItem) error {
	if err := d.Create(ci).Error; err != nil {
		return err
	}

	return nil
}

// UpdateConfigItem updates all the fields of a given config item row
func (d *DBRepo) UpdateConfigItem(ci *models.ConfigItem) error {
	if err := d.Updates(ci).Error; err != nil {
		return err
	}

	return nil
}

// CreateConfigChange inserts a new config change row in the db
func (d *DBRepo) CreateConfigChange(cc *models.ConfigChange) error {
	if cc.ID == "" {
		cc.ID = uuid.New().String()
	}
	if err := d.Create(cc).Error; err != nil {
		return err
	}

	return nil
}

func (d *DBRepo) GetAnalysis(analysis models.Analysis) (*models.Analysis, error) {
	existing := models.Analysis{}
	err := d.First(&existing, "config_id = ? AND analyzer = ?", analysis.ConfigID, analysis.Analyzer).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}

	return &existing, err
}

func (d *DBRepo) CreateAnalysis(analysis models.Analysis) error {
	// get analysis by config_id, and summary
	existingAnalysis, err := d.GetAnalysis(analysis)
	if err != nil {
		return err
	}
	if existingAnalysis != nil {
		analysis.ID = existingAnalysis.ID
		return d.Model(&analysis).Updates(map[string]interface{}{
			"last_observed": gorm.Expr("now()"),
			"message":       analysis.Message,
			"status":        analysis.Status}).Error
	}
	return d.Create(analysis).Error
}

// QueryConfigItems ...
func (d *DBRepo) QueryConfigItems(request v1.QueryRequest) (*v1.QueryResult, error) {
	results := d.Raw(request.Query)
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

// select id, external_type, external_id, analysis from config_items as ci full join (select config_id,array_agg(analyzer) as analysis from config_analysis group by config_id) as ca on ca.config_id = ci.id where analysis is not null limit 150
