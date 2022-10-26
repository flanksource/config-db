package db

import (
	"github.com/flanksource/config-db/db/models"
	"github.com/google/uuid"
	"gorm.io/gorm/clause"
)

// CreateConfigChange inserts a new config change row in the db
func CreateConfigChange(cc *models.ConfigChange) error {
	if cc.ID == "" {
		cc.ID = uuid.New().String()
	}
	return db.Clauses(clause.OnConflict{DoNothing: true}).Create(cc).Error
}
