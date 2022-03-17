package repository

import (
	"time"

	"github.com/flanksource/confighub/db/models"
	"gorm.io/gorm"
)

// ConfigItemRepo should satisfy the config item repository interface
type ConfigItemRepo struct {
	db *gorm.DB
}

// NewConfigItem is the factory function for the config item repo instance
func NewConfigItem(db *gorm.DB) ConfigItem {
	return &ConfigItemRepo{
		db: db,
	}
}

// GetOne returns a single config item result
func (c *ConfigItemRepo) GetOne(extID string) (*models.ConfigItem, error) {

	ci := models.ConfigItem{}
	if err := c.db.First(&ci, "external_id = ?", extID).Error; err != nil {
		return nil, err
	}

	return &ci, nil
}

// Create inserts a new config item row in the db
func (c *ConfigItemRepo) Create(ci *models.ConfigItem) error {

	ci.CreatedAt = time.Now().UTC()

	if err := c.db.Create(ci).Error; err != nil {
		return err
	}

	return nil
}

// UpdateAllFields updates all the fields of a given config item row
func (c *ConfigItemRepo) UpdateAllFields(ci *models.ConfigItem) error {

	ci.UpdatedAt = time.Now().UTC()

	if err := c.db.Save(ci).Error; err != nil {
		return err
	}

	return nil
}
