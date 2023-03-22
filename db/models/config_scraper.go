package models

import (
	"encoding/json"

	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/google/uuid"
)

type ConfigScraper struct {
	ID          uuid.UUID `json:"id,omitempty"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Spec        string    `json:"spec,omitempty"`
}

func (cs ConfigScraper) V1ConfigScraper() (v1.ConfigScraper, error) {
	var spec v1.ConfigScraper
	if err := json.Unmarshal([]byte(cs.Spec), &spec); err != nil {
		return spec, err
	}
	spec.ID = cs.ID.String()
	return spec, nil
}
