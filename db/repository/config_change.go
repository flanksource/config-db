package repository

import (
	"time"

	"github.com/flanksource/confighub/db/models"
	"gorm.io/gorm"
)

// ConfigChangeRepo should satisfy the config change repository interface
type ConfigChangeRepo struct {
	db *gorm.DB
}

// NewConfigChange is the factory function for the config change repo instance
func NewConfigChange(db *gorm.DB) ConfigChange {
	return &ConfigChangeRepo{
		db: db,
	}
}

// Create inserts a new config change row in the db
func (c *ConfigChangeRepo) Create(cc *models.ConfigChange) error {

	cc.CreatedAt = time.Now().UTC()

	if err := c.db.Create(cc).Error; err != nil {
		return err
	}

	return nil
}
