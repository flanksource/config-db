package models

import (
	"github.com/google/uuid"
	"gorm.io/gorm/clause"
)

type ConfigRelationship struct {
	ConfigID   string     `gorm:"column:config_id" json:"config_id"`
	RelatedID  string     `gorm:"column:related_id" json:"related_id"`
	Relation   string     `gorm:"column:relation" json:"relation"`
	ScraperID  *uuid.UUID `gorm:"column:scraper_id" json:"scraper_id"`
	SelectorID string     `gorm:"selector_id" json:"selector_id"`
}

func (c ConfigRelationship) PKCols() []clause.Column {
	return []clause.Column{{Name: "related_id"}, {Name: "config_id"}, {Name: "relation"}, {Name: "scraper_id"}}
}
