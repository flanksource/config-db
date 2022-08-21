package models

import (
	"fmt"
	"time"

	v1 "github.com/flanksource/confighub/api/v1"
	"github.com/lib/pq"
)

// ConfigItem represents the config item database table
type ConfigItem struct {
	ID           string            `gorm:"primaryKey;unique_index;not null;column:id" json:"id"  `
	ScraperID    *string           `gorm:"column:scraper_id;default:null" json:"scraper_id,omitempty"  `
	ConfigType   string            `gorm:"column:config_type;default:''" json:"config_type"  `
	ExternalID   pq.StringArray    `gorm:"column:external_id;type:[]text" json:"external_id,omitempty"  `
	ExternalType *string           `gorm:"column:external_type;default:null" json:"external_type,omitempty"  `
	Name         *string           `gorm:"column:name;default:null" json:"name,omitempty"  `
	Namespace    *string           `gorm:"column:namespace;default:null" json:"namespace,omitempty"  `
	Description  *string           `gorm:"column:description;default:null" json:"description,omitempty"  `
	Account      *string           `gorm:"column:account;default:null" json:"account,omitempty"  `
	Region       *string           `gorm:"column:region;default:null" json:"region,omitempty"  `
	Zone         *string           `gorm:"column:zone;default:null" json:"zone,omitempty"  `
	Network      *string           `gorm:"column:network;default:null" json:"network,omitempty"  `
	Subnet       *string           `gorm:"column:subnet;default:null" json:"subnet,omitempty"  `
	Config       *string           `gorm:"column:config;default:null" json:"config,omitempty"  `
	Source       *string           `gorm:"column:source;default:null" json:"source,omitempty"  `
	Tags         *v1.JSONStringMap `gorm:"column:tags;default:null" json:"tags,omitempty"  `
	CreatedAt    time.Time         `gorm:"column:created_at" json:"created_at"  `
	UpdatedAt    time.Time         `gorm:"column:updated_at" json:"updated_at"  `
}

func (ci ConfigItem) String() string {
	return fmt.Sprintf("%s/%s", ci.ConfigType, ci.ID)
}
