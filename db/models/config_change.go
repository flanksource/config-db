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
	ExternalID        string     `gorm:"-"`
	ConfigType        string     `gorm:"-"`
	ExternalChangeId  string     `gorm:"column:external_change_id" json:"external_change_id"`
	ID                string     `gorm:"primaryKey;unique_index;not null;column:id" json:"id"`
	ConfigID          string     `gorm:"column:config_id;default:''" json:"config_id"`
	ChangeType        string     `gorm:"column:change_type" json:"change_type"`
	Diff              *string    `gorm:"column:diff" json:"diff,omitempty"`
	Severity          string     `gorm:"column:severity" json:"severity"`
	Source            string     `gorm:"column:source" json:"source"`
	Summary           string     `gorm:"column:summary;default:null" json:"summary,omitempty"`
	Patches           string     `gorm:"column:patches;default:null" json:"patches,omitempty"`
	Details           v1.JSON    `gorm:"column:details" json:"details,omitempty"`
	CreatedAt         *time.Time `gorm:"column:created_at" json:"created_at"`
	CreatedBy         *string    `json:"created_by"`
	ExternalCreatedBy *string    `json:"external_created_by"`
}

func (c ConfigChange) GetExternalID() v1.ExternalID {
	return v1.ExternalID{
		ExternalID: []string{c.ExternalID},
		ConfigType: c.ConfigType,
	}
}

func (c ConfigChange) String() string {
	return fmt.Sprintf("[%s/%s] %s", c.ConfigType, c.ExternalID, c.ChangeType)
}

func NewConfigChangeFromV1(result v1.ScrapeResult, change v1.ChangeResult) *ConfigChange {
	_change := ConfigChange{
		ExternalID:       change.ExternalID,
		ConfigType:       change.ConfigType,
		ExternalChangeId: change.ExternalChangeID,
		ChangeType:       change.ChangeType,
		Source:           change.Source,
		Diff:             change.Diff,
		Severity:         change.Severity,
		Details:          v1.JSON(change.Details),
		Summary:          change.Summary,
		Patches:          change.Patches,
		CreatedAt:        change.CreatedAt,
		CreatedBy:        change.CreatedBy,
	}
	if _change.ExternalID == "" {
		_change.ExternalID = result.ID
	}
	if _change.ConfigType == "" {
		_change.ConfigType = result.Type
	}

	return &_change
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
