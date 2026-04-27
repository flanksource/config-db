package models

import "github.com/google/uuid"

type ConfigRelationship struct {
	ConfigID   string `gorm:"column:config_id" json:"config_id"`
	RelatedID  string `gorm:"column:related_id" json:"related_id"`
	Relation   string `gorm:"column:relation" json:"relation"`
	SelectorID string `gorm:"selector_id" json:"selector_id"`
	ScraperID  string `gorm:"column:scraper_id" json:"scraper_id,omitempty"`
}
