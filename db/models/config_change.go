package models

import (
	"fmt"
	"time"

	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ConfigChange represents the config change database table
type ConfigChange struct {
	ExternalID       string           `gorm:"-"`
	ExternalType     string           `gorm:"-"`
	ExternalChangeId string           `gorm:"column:external_change_id" json:"external_change_id"`
	ID               string           `gorm:"primaryKey;unique_index;not null;column:id" json:"id"`
	ConfigID         string           `gorm:"column:config_id;default:''" json:"config_id"`
	ChangeType       string           `gorm:"column:change_type" json:"change_type"`
	Severity         string           `gorm:"column:severity" json:"severity"`
	Source           string           `gorm:"column:source" json:"source"`
	Summary          string           `gorm:"column:summary;default:null" json:"summary,omitempty"`
	Patches          string           `gorm:"column:patches;default:null" json:"patches,omitempty"`
	Details          v1.JSONStringMap `gorm:"column:details" json:"details,omitempty"`
	CreatedAt        *time.Time       `gorm:"column:created_at" json:"created_at"`
}

func (c ConfigChange) String() string {
	return fmt.Sprintf("[%s/%s] %s", c.ExternalType, c.ExternalID, c.ChangeType)
}

func NewConfigChangeFromV1(change v1.ChangeResult) *ConfigChange {
	return &ConfigChange{
		ExternalID:       change.ExternalID,
		ExternalType:     change.ExternalType,
		ExternalChangeId: change.ExternalChangeID,
		ChangeType:       change.ChangeType,
		Source:           change.Source,
		Severity:         change.Severity,
		Details:          v1.JSONStringMap(change.Details),
		Summary:          change.Summary,
		Patches:          change.Patches,
		CreatedAt:        change.CreatedAt,
	}
}

func (c *ConfigChange) BeforeCreate(tx *gorm.DB) (err error) {
	if c.ID == "" {
		c.ID = uuid.New().String()
	}

	if c.CreatedAt == nil {
		now := time.Now()
		c.CreatedAt = &now
	}
	tx.Statement.AddClause(clause.OnConflict{DoNothing: true})
	return
}
