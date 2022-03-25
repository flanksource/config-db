package repository

import (
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
