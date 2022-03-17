package repository

import "github.com/flanksource/confighub/db/models"

// ConfigItem defines the contract for a config item repository intsance
type ConfigItem interface {
	GetOne(string) (*models.ConfigItem, error)
	Create(*models.ConfigItem) error
	UpdateAllFields(*models.ConfigItem) error
}

// ConfigChange defines the contract for a config change repository intsance
type ConfigChange interface {
	Create(*models.ConfigChange) error
}
