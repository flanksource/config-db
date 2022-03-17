package db

import (
	"database/sql"
	"encoding/json"

	jsonpatch "github.com/evanphx/json-patch"
	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/confighub/api/v1"
	"github.com/flanksource/confighub/db/models"
	. "github.com/flanksource/confighub/db/models"
	"github.com/flanksource/confighub/db/repository"
	"github.com/flanksource/confighub/db/ulid"
	cmap "github.com/orcaman/concurrent-map"
	"gorm.io/gorm"
)

var idCache = cmap.New()

func NewConfigItemFromResult(result v1.ScrapeResult) ConfigItem {
	return ConfigItem{
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

func Update(ctx v1.ScrapeContext, results []v1.ScrapeResult) error {
	// boil.DebugMode = true
	for _, result := range results {
		data, err := json.Marshal(result.Config)
		if err != nil {
			return err
		}

		ci := NewConfigItemFromResult(result)
		dataStr := string(data)
		ci.Config = &dataStr

		ciRepo := repository.NewConfigItem(DefaultDB())
		ccRepo := repository.NewConfigChange(DefaultDB())

		existing, err := ciRepo.GetOne(result.Id)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if err == gorm.ErrRecordNotFound {
			ci.ID = ulid.MustNew().AsUUID()
			if err := ciRepo.Create(&ci); err != nil {
				return err
			}
			continue

		}

		ci.ID = existing.ID
		if err := ciRepo.UpdateAllFields(&ci); err != nil {
			return err
		}
		changes, err := compare(ci, *existing)
		if err != nil {
			return err
		}

		if changes != nil {
			logger.Infof("[%s/%s] detected changes", ci.ConfigType, *ci.ExternalID)
			if err := ccRepo.Create(changes); err != nil {
				return err
			}
		}
	}
	return nil
}

func compare(a, b models.ConfigItem) (*models.ConfigChange, error) {
	patch, err := jsonpatch.CreateMergePatch(GetJSON(b), GetJSON(a))
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
