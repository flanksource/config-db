package db

import (
	"encoding/json"

	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/confighub/api/v1"
	"github.com/flanksource/confighub/db/models"
)

// GetJSON ...
func GetJSON(ci models.ConfigItem) []byte {
	data, err := json.Marshal(ci.Config)
	if err != nil {
		logger.Errorf("Failed to marshal config: %+v", err)
	}
	return data
}

// GetConfigItem ...
func GetConfigItem(id string) (*models.ConfigItem, error) {
	return repository.GetConfigItem(id)
}

// CreateConfigItem ...
func CreateConfigItem(item *models.ConfigItem) error {
	return repository.CreateConfigItem(item)
}

// UpdateConfigItem ...
func UpdateConfigItem(item *models.ConfigItem) error {
	return repository.UpdateConfigItem(item)
}

// CreateConfigChange ...
func CreateConfigChange(change *models.ConfigChange) error {
	return repository.CreateConfigChange(change)
}

// QueryConfigItems ...
func QueryConfigItems(request v1.QueryRequest) (*v1.QueryResult, error) {
	return repository.QueryConfigItems(request)
}
