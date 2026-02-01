package models

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/samber/lo"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	v1 "github.com/flanksource/config-db/api"
)

type ConfigChangeUpdate struct {
	Change         *ConfigChange
	CountIncrement int
	FirstInBatch   bool // First occurrence in current batch (not found in cache)
}

// ConfigChange represents the config change database table
type ConfigChange struct {
	ExternalID string `gorm:"-"`
	ConfigType string `gorm:"-"`
	ScraperID  string `gorm:"-"`

	Fingerprint       *string    `gorm:"column:fingerprint" json:"fingerprint"`
	ExternalChangeID  *string    `gorm:"column:external_change_id;default:null" json:"external_change_id"`
	ID                string     `gorm:"primaryKey;unique_index;not null;column:id" json:"id"`
	ConfigID          string     `gorm:"column:config_id;default:''" json:"config_id"`
	ChangeType        string     `gorm:"column:change_type" json:"change_type"`
	Diff              *string    `gorm:"column:diff" json:"diff,omitempty"`
	Severity          string     `gorm:"column:severity" json:"severity"`
	Source            string     `gorm:"column:source" json:"source"`
	Summary           string     `gorm:"column:summary" json:"summary,omitempty"`
	Patches           string     `gorm:"column:patches;default:null" json:"patches,omitempty"`
	Details           v1.JSON    `gorm:"column:details" json:"details,omitempty"`
	Count             int        `gorm:"column:count;<-" json:"count"`
	FirstObserved     *time.Time `gorm:"column:first_observed;default:NOW()" json:"first_observed"`
	CreatedAt         time.Time  `gorm:"column:created_at" json:"created_at"`
	CreatedBy         *string    `json:"created_by"`
	ExternalCreatedBy *string    `json:"external_created_by"`
}

func (c ConfigChange) GetExternalID() v1.ExternalID {
	return v1.ExternalID{
		ExternalID: c.ExternalID,
		ConfigType: c.ConfigType,
		ScraperID:  c.ScraperID,
	}
}

func (c ConfigChange) String() string {
	return fmt.Sprintf("[%s/%s] %s", c.ConfigType, c.ExternalID, c.ChangeType)
}

func NewConfigChangeFromV1(result v1.ScrapeResult, change v1.ChangeResult) *ConfigChange {
	_change := ConfigChange{
		ID:         uuid.NewString(),
		ExternalID: change.ExternalID,
		ConfigType: change.ConfigType,
		ScraperID:  change.ScraperID,
		ChangeType: change.ChangeType,
		Source:     change.Source,
		Diff:       change.Diff,
		Severity:   change.Severity,
		Details:    v1.JSON(change.Details),
		Summary:    change.Summary,
		Patches:    change.Patches,
		CreatedBy:  change.CreatedBy,
		Count:      1,
		ConfigID:   change.ConfigID,
	}
	if change.CreatedAt != nil && !change.CreatedAt.IsZero() {
		_change.CreatedAt = lo.FromPtr(change.CreatedAt)
	}

	if change.ExternalChangeID != "" {
		_change.ExternalChangeID = &change.ExternalChangeID
	}

	return &_change
}

func (c *ConfigChange) BeforeCreate(tx *gorm.DB) (err error) {
	if c.ID == "" {
		c.ID = uuid.New().String()
	}

	tx.Statement.AddClause(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "config_id"},
			{Name: "external_change_id"},
		},
		UpdateAll: true,
	})

	return
}
