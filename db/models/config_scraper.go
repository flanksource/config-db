package models

import (
	"encoding/json"

	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/google/uuid"
)

type ConfigScraper struct {
	ID          uuid.UUID `json:"id,omitempty"`
	Description string    `json:"description,omitempty"`
	ScraperType string    `json:"scraper_type,omitempty"`
	Spec        string    `json:"spec,omitempty"`
}

func (cs ConfigScraper) V1ConfigScraper() (v1.ConfigScraper, error) {
	var spec v1.ConfigScraper
	err := json.Unmarshal([]byte(cs.Spec), &spec)
	return spec, err
}
