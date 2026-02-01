package models

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/flanksource/duty/models"
	"github.com/flanksource/duty/types"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/samber/lo"

	v1 "github.com/flanksource/config-db/api"
)

// ConfigItem represents the config item database table
// Deprecated: Use models.ConfigItem from duty.
type ConfigItem struct {
	ID            string                `gorm:"primaryKey;unique_index;not null;column:id;default:generate_ulid()" json:"id"  `
	ScraperID     *uuid.UUID            `gorm:"column:scraper_id;default:null" json:"scraper_id,omitempty"`
	ConfigClass   string                `gorm:"column:config_class;default:''" json:"config_class"  `
	ExternalID    pq.StringArray        `gorm:"column:external_id;type:[]text" json:"external_id,omitempty"  `
	Type          string                `gorm:"column:type" json:"type,omitempty"  `
	Status        *string               `gorm:"column:status;default:null" json:"status,omitempty"  `
	Ready         bool                  `json:"ready,omitempty"  `
	Health        *models.Health        `json:"health,omitempty"`
	Name          *string               `gorm:"column:name;default:null" json:"name,omitempty"  `
	Description   *string               `gorm:"column:description;default:null" json:"description,omitempty"  `
	Config        *string               `gorm:"column:config;default:null" json:"config,omitempty"  `
	Source        *string               `gorm:"column:source;default:null" json:"source,omitempty"  `
	ParentID      *string               `gorm:"column:parent_id;default:null" json:"parent_id,omitempty"`
	Path          string                `gorm:"column:path;default:null" json:"path,omitempty"`
	CostPerMinute float64               `gorm:"column:cost_per_minute;default:null" json:"cost_per_minute,omitempty"`
	CostTotal1d   float64               `gorm:"column:cost_total_1d;default:null" json:"cost_total_1d,omitempty"`
	CostTotal7d   float64               `gorm:"column:cost_total_7d;default:null" json:"cost_total_7d,omitempty"`
	CostTotal30d  float64               `gorm:"column:cost_total_30d;default:null" json:"cost_total_30d,omitempty"`
	Labels        *types.JSONStringMap  `gorm:"column:labels;default:null" json:"labels,omitempty"`
	Tags          types.JSONStringMap   `gorm:"column:tags;default:null" json:"tags,omitempty"`
	Properties    *types.Properties     `gorm:"column:properties;default:null" json:"properties,omitempty"`
	CreatedAt     time.Time             `gorm:"column:created_at" json:"created_at"`
	UpdatedAt     *time.Time            `gorm:"column:updated_at;autoUpdateTime:false;<-:update" json:"updated_at"`
	DeletedAt     *time.Time            `gorm:"column:deleted_at" json:"deleted_at"`
	DeleteReason  v1.ConfigDeleteReason `gorm:"column:delete_reason" json:"delete_reason"`

	Parents         []v1.ConfigExternalKey `gorm:"-" json:"parents,omitempty"`
	Children        []v1.ConfigExternalKey `gorm:"-" json:"children,omitempty"`
	ProbableParents []string               `gorm:"-" json:"-"`

	// For storing struct as map[string]any
	_map map[string]any `json:"-"`
}

func (ci ConfigItem) Label() string {
	if len(ci.ExternalID) == 0 {
		return fmt.Sprintf("%s/%s id=%s ", ci.Type, lo.FromPtr(ci.Name), ci.ID)
	}
	if ci.ID == ci.ExternalID[0] {
		return fmt.Sprintf("%s/%s id=%s", ci.Type, lo.FromPtr(ci.Name), ci.ID)
	}

	return fmt.Sprintf("%s/%s id=%s external=%s", ci.Type, lo.FromPtr(ci.Name), ci.ID, ci.ExternalID[0])
}

func (ci ConfigItem) String() string {
	if len(ci.ExternalID) == 0 {
		return fmt.Sprintf("id=%s type=%s name=%s ", ci.ID, ci.Type, lo.FromPtr(ci.Name))
	}

	return fmt.Sprintf("id=%s type=%s name=%s external_id=%s", ci.ID, ci.Type, lo.FromPtr(ci.Name), ci.ExternalID[0])
}

func (ci ConfigItem) ConfigJSONStringMap() (map[string]interface{}, error) {
	var m map[string]interface{}
	err := json.Unmarshal([]byte(*ci.Config), &m)
	return m, err
}

type ConfigItems []*ConfigItem

func (cis ConfigItems) GetByID(id string) *ConfigItem {
	for _, ci := range cis {
		if ci.ID == id {
			return ci
		}
	}
	return nil
}

func (ci *ConfigItem) AsMap() map[string]any {
	if ci._map != nil {
		return ci._map
	}
	ci._map = map[string]any{
		"id":              ci.ID,
		"scraper_id":      lo.FromPtr(ci.ScraperID),
		"config_class":    ci.ConfigClass,
		"external_id":     ci.ExternalID,
		"type":            ci.Type,
		"status":          lo.FromPtr(ci.Status),
		"ready":           ci.Ready,
		"health":          lo.FromPtr(ci.Health),
		"name":            lo.FromPtr(ci.Name),
		"description":     lo.FromPtr(ci.Description),
		"config":          lo.FromPtr(ci.Config),
		"source":          lo.FromPtr(ci.Source),
		"parent_id":       lo.FromPtr(ci.ParentID),
		"path":            ci.Path,
		"cost_per_minute": ci.CostPerMinute,
		"cost_total_1d":   ci.CostTotal1d,
		"cost_total_7d":   ci.CostTotal7d,
		"cost_total_30d":  ci.CostTotal30d,
		"labels":          lo.FromPtr(ci.Labels),
		"tags":            ci.Tags,
		"properties":      lo.FromPtr(ci.Properties),
		"created_at":      ci.CreatedAt,
		"updated_at":      lo.FromPtr(ci.UpdatedAt),
		"deleted_at":      lo.FromPtr(ci.DeletedAt),
		"delete_reason":   ci.DeleteReason,
	}
	return ci._map
}

func (ci *ConfigItem) FlushMap() {
	ci._map = nil
}
