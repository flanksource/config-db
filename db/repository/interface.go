package repository

import (
	v1 "github.com/flanksource/confighub/api/v1"
	"github.com/flanksource/confighub/db/models"
)

// Database holds all the repository function contracts
type Database interface {
	GetConfigItem(string) (*models.ConfigItem, error)
	CreateConfigItem(*models.ConfigItem) error
	UpdateConfigItem(*models.ConfigItem) error
	CreateConfigChange(*models.ConfigChange) error
	QueryConfigItems(request v1.QueryRequest) (*v1.QueryResult, error)
}
