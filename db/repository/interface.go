package repository

import "github.com/flanksource/confighub/db/models"

// Database holds all the repository function contracts
type Database interface {
	GetConfigItem(string) (*models.ConfigItem, error)
	CreateConfigItem(*models.ConfigItem) error
	UpdateConfigItem(*models.ConfigItem) error
	CreateConfigChange(*models.ConfigChange) error
}
