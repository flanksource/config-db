package models

import (
	"time"
)

// ConfigChange represents the config change database table
type ConfigChange struct {
	ID         string    `gorm:"primaryKey;unique_index;not null;column:id" json:"id" toml:"id" yaml:"id"`
	ConfigID   string    `gorm:"column:config_id;default:''" json:"config_id" toml:"config_id" yaml:"config_id"`
	ChangeType string    `gorm:"column:change_type" json:"change_type" toml:"change_type" yaml:"change_type"`
	Summary    *string   `gorm:"column:summary;default:null" json:"summary,omitempty" toml:"summary" yaml:"summary,omitempty"`
	Patches    *string   `gorm:"column:patches;default:null" json:"patches,omitempty" toml:"patches" yaml:"patches,omitempty"`
	CreatedAt  time.Time `gorm:"column:created_at" json:"created_at" toml:"created_at" yaml:"created_at"`
}

// TableName returns the corresponding table name of the model
func (ci *ConfigChange) TableName() string {
	return "config_change"
}
