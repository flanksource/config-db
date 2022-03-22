package repository

import "github.com/flanksource/confighub/db/models"

// Database holds all the repository function contracts
type Database interface {
	GetOneConfigItem(string) (*models.ConfigItem, error)
	CreateConfigItem(*models.ConfigItem) error
	UpdateAllFieldsConfigItem(*models.ConfigItem) error
	CreateConfigChange(*models.ConfigChange) error
}
