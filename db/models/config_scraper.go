package models

import (
	"encoding/json"

	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty/models"
)

// V1ConfigScraper generates a v1.ConfigScraper from a models.ConfigScraper
func V1ConfigScraper(cs models.ConfigScraper) (v1.ConfigScraper, error) {
	var spec v1.ConfigScraper
	if err := json.Unmarshal([]byte(cs.Spec), &spec); err != nil {
		return spec, err
	}

	spec.ID = cs.ID.String()
	return spec, nil
}
