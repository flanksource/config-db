package db

import (
	"encoding/json"

	jsonpatch "github.com/evanphx/json-patch"
	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/confighub/api/v1"
	"github.com/flanksource/confighub/db/models"
	"github.com/flanksource/confighub/db/ulid"
	"github.com/pkg/errors"
	"gorm.io/gorm"
)

// NewConfigItemFromResult creates a new config item instance from result
func NewConfigItemFromResult(result v1.ScrapeResult) models.ConfigItem {
	return models.ConfigItem{
		ConfigType: result.Type,
		ExternalID: &result.Id,
		Account:    &result.Account,
		Region:     &result.Region,
		Zone:       &result.Zone,
		Network:    &result.Network,
		Subnet:     &result.Subnet,
		Name:       &result.Name,
	}
}

// Update creates or update a configuartion with config changes
func Update(ctx v1.ScrapeContext, results []v1.ScrapeResult) error {
	// boil.DebugMode = true
	for _, result := range results {
		data, err := json.Marshal(result.Config)
		if err != nil {
			return errors.Wrapf(err, "Unable to marshal: %v", result.Config)
		}

		ci := NewConfigItemFromResult(result)
		dataStr := string(data)
		ci.Config = &dataStr

		existing, err := GetConfigItem(result.Id)
		if err != nil && err != gorm.ErrRecordNotFound {
			return errors.Wrapf(err, "unable to lookup existing config: %s", result)
		}
		if err == gorm.ErrRecordNotFound {
			ci.ID = ulid.MustNew().AsUUID()
			if err := CreateConfigItem(&ci); err != nil {
				logger.Errorf("[%s] failed to create item %v", ci, err)
			}
			continue
		}

		ci.ID = existing.ID
		if err := UpdateConfigItem(&ci); err != nil {
			if err := CreateConfigItem(&ci); err != nil {
				logger.Errorf("[%s] failed to update item %v", ci, err)
				continue
			}
		}
		changes, err := compare(ci, *existing)
		if err != nil {
			logger.Errorf("[%s] failed to check for changes: %v", ci, err)
		}

		if changes != nil {
			logger.Infof("[%s/%s] detected changes", ci.ConfigType, *ci.ExternalID)
			if err := CreateConfigChange(changes); err != nil {
				logger.Errorf("[%s] failed to update with changes %v", ci, err)
			}
		}
	}
	return nil
}

func compare(a, b models.ConfigItem) (*models.ConfigChange, error) {
	patch, err := jsonpatch.CreateMergePatch([]byte(*a.Config), []byte(*b.Config))
	if err != nil {
		return nil, err
	}

	if len(patch) <= 2 { // no patch or empty array
		return nil, nil
	}

	patchStr := string(patch)

	return &models.ConfigChange{
		ConfigID:   a.ID,
		ChangeType: "diff",
		ID:         ulid.MustNew().AsUUID(),
		Patches:    &patchStr,
	}, nil

}
