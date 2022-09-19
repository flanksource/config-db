package db

import (
	"encoding/json"

	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db/models"
)

func GetJSON(ci models.ConfigItem) []byte {
	data, err := json.Marshal(ci.Config)
	if err != nil {
		logger.Errorf("Failed to marshal config: %+v", err)
	}
	return data
}

func GetConfigItem(externalType, id string) (*models.ConfigItem, error) {
	return repository.GetConfigItem(externalType, id)
}

func CreateConfigItem(item *models.ConfigItem) error {
	return repository.CreateConfigItem(item)
}

func UpdateConfigItem(item *models.ConfigItem) error {
	return repository.UpdateConfigItem(item)
}

func CreateConfigChange(change *models.ConfigChange) error {
	return repository.CreateConfigChange(change)
}

func QueryConfigItems(request v1.QueryRequest) (*v1.QueryResult, error) {
	return repository.QueryConfigItems(request)
}
