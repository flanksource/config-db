package db

import (
	"database/sql"
	"encoding/json"

	jsonpatch "github.com/evanphx/json-patch"
	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/confighub/api/v1"
	"github.com/flanksource/confighub/db/models"
	. "github.com/flanksource/confighub/db/models"
	"github.com/flanksource/confighub/db/ulid"
	cmap "github.com/orcaman/concurrent-map"
	"github.com/volatiletech/null/v8"
	"github.com/volatiletech/sqlboiler/v4/boil"
)

var idCache = cmap.New()

func NewConfigItemFromResult(result v1.ScrapeResult) ConfigItem {
	return ConfigItem{
		ConfigType: result.Type,
		ExternalID: null.StringFrom(result.Id),
		Account:    null.StringFrom(result.Account),
		Region:     null.StringFrom(result.Region),
		Zone:       null.StringFrom(result.Zone),
		Network:    null.StringFrom(result.Network),
		Subnet:     null.StringFrom(result.Subnet),
		Name:       null.StringFrom(result.Name),
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
		ci.Config = null.JSONFrom(data)

		existing, err := models.ConfigItems(ConfigItemWhere.ExternalID.EQ(null.StringFrom(result.Id))).OneG()
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if err == sql.ErrNoRows {
			ci.ID = ulid.MustNew().AsUUID()
			if err := ci.InsertG(boil.Infer()); err != nil {
				return err
			}
			continue

		}

		ci.ID = existing.ID
		if _, err := ci.UpdateG(boil.Infer()); err != nil {
			return err
		}
		changes, err := compare(ci, *existing)
		if err != nil {
			return err
		}

		if changes != nil {
			logger.Infof("[%s/%s] detected changes", ci.ConfigType, ci.ExternalID.String)
			if err := changes.InsertG(boil.Infer()); err != nil {
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
	return &models.ConfigChange{
		ConfigID:   a.ID,
		ChangeType: "diff",
		ID:         ulid.MustNew().AsUUID(),
		Patches:    null.JSONFrom(patch),
	}, nil

}
