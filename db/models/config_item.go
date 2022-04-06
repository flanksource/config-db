package models

import "time"

// ConfigItem represents the config item database table
type ConfigItem struct {
	ID          string    `gorm:"primaryKey;unique_index;not null;column:id" json:"id" toml:"id" yaml:"id"`
	ScraperID   *string   `gorm:"column:scraper_id;default:null" json:"scraper_id,omitempty" toml:"scraper_id" yaml:"scraper_id,omitempty"`
	ConfigType  string    `gorm:"column:config_type;default:''" json:"config_type" toml:"config_type" yaml:"config_type"`
	ExternalID  *string   `gorm:"column:external_id;default:null" json:"external_id,omitempty" toml:"external_id" yaml:"external_id,omitempty"`
	Name        *string   `gorm:"column:name;default:null" json:"name,omitempty" toml:"name" yaml:"name,omitempty"`
	Namespace   *string   `gorm:"column:namespace;default:null" json:"namespace,omitempty" toml:"namespace" yaml:"namespace,omitempty"`
	Description *string   `gorm:"column:description;default:null" json:"description,omitempty" toml:"description" yaml:"description,omitempty"`
	Account     *string   `gorm:"column:account;default:null" json:"account,omitempty" toml:"account" yaml:"account,omitempty"`
	Region      *string   `gorm:"column:region;default:null" json:"region,omitempty" toml:"region" yaml:"region,omitempty"`
	Zone        *string   `gorm:"column:zone;default:null" json:"zone,omitempty" toml:"zone" yaml:"zone,omitempty"`
	Network     *string   `gorm:"column:network;default:null" json:"network,omitempty" toml:"network" yaml:"network,omitempty"`
	Subnet      *string   `gorm:"column:subnet;default:null" json:"subnet,omitempty" toml:"subnet" yaml:"subnet,omitempty"`
	Config      *string   `gorm:"column:config;default:null" json:"config,omitempty" toml:"config" yaml:"config,omitempty"`
	CreatedAt   time.Time `gorm:"column:created_at" json:"created_at" toml:"created_at" yaml:"created_at"`
	UpdatedAt   time.Time `gorm:"column:updated_at" json:"updated_at" toml:"updated_at" yaml:"updated_at"`
}

// TableName returns the corresponding table name of the model
func (ci *ConfigItem) TableName() string {
	return "config_item"
}
