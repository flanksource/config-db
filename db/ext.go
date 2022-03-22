package db

import (
	"encoding/json"

	"github.com/flanksource/commons/logger"
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
