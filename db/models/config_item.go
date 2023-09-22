package models

import (
	"encoding/json"
	"fmt"
	"time"

	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/google/uuid"
	"github.com/lib/pq"
)

// ConfigItem represents the config item database table
type ConfigItem struct {
	ID             string            `gorm:"primaryKey;unique_index;not null;column:id" json:"id"  `
	ScraperID      *uuid.UUID        `gorm:"column:scraper_id;default:null" json:"scraper_id,omitempty"`
	ConfigClass    string            `gorm:"column:config_class;default:''" json:"config_class"  `
	ExternalID     pq.StringArray    `gorm:"column:external_id;type:[]text" json:"external_id,omitempty"  `
	Type           *string           `gorm:"column:type;default:null" json:"type,omitempty"  `
	Status         *string           `gorm:"column:status;default:null" json:"status,omitempty"  `
	Name           *string           `gorm:"column:name;default:null" json:"name,omitempty"  `
	Namespace      *string           `gorm:"column:namespace;default:null" json:"namespace,omitempty"  `
	Description    *string           `gorm:"column:description;default:null" json:"description,omitempty"  `
	Account        *string           `gorm:"column:account;default:null" json:"account,omitempty"  `
	Config         *string           `gorm:"column:config;default:null" json:"config,omitempty"  `
	Source         *string           `gorm:"column:source;default:null" json:"source,omitempty"  `
	ParentID       *string           `gorm:"column:parent_id;default:null" json:"parent_id,omitempty"`
	Path           string            `gorm:"column:path;default:null" json:"path,omitempty"`
	CostPerMinute  float64           `gorm:"column:cost_per_minute;default:null" json:"cost_per_minute,omitempty"`
	CostTotal1d    float64           `gorm:"column:cost_total_1d;default:null" json:"cost_total_1d,omitempty"`
	CostTotal7d    float64           `gorm:"column:cost_total_7d;default:null" json:"cost_total_7d,omitempty"`
	CostTotal30d   float64           `gorm:"column:cost_total_30d;default:null" json:"cost_total_30d,omitempty"`
	Tags           *v1.JSONStringMap `gorm:"column:tags;default:null" json:"tags,omitempty"  `
	CreatedAt      time.Time         `gorm:"column:created_at" json:"created_at"`
	UpdatedAt      time.Time         `gorm:"column:updated_at" json:"updated_at"`
	DeletedAt      *time.Time        `gorm:"column:deleted_at" json:"deleted_at"`
	TouchDeletedAt bool              `gorm:"-" json:"-"`
}

func (ci ConfigItem) String() string {
	return fmt.Sprintf("%s/%s", ci.ConfigClass, ci.ID)
}

func (ci ConfigItem) ConfigJSONStringMap() (map[string]interface{}, error) {
	var m map[string]interface{}
	err := json.Unmarshal([]byte(*ci.Config), &m)
	return m, err
}
