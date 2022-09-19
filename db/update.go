package db

import (
	"encoding/json"
	"fmt"

	jsonpatch "github.com/evanphx/json-patch"
	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db/models"
	"github.com/flanksource/config-db/db/ulid"
	"github.com/pkg/errors"
	"gorm.io/gorm"
)

// NewConfigItemFromResult creates a new config item instance from result
func NewConfigItemFromResult(result v1.ScrapeResult) (*models.ConfigItem, error) {
	var dataStr string
	switch data := result.Config.(type) {
	case string:
		dataStr = data
	case []byte:
		dataStr = string(data)
	default:
		bytes, err := json.Marshal(data)
		if err != nil {
			return nil, errors.Wrapf(err, "Unable to marshal: %v", result.Config)
		}
		dataStr = string(bytes)
	}

	ci := &models.ConfigItem{
		ExternalID:   append(result.Aliases, result.ID),
		ID:           result.ID,
		ConfigType:   result.Type,
		ExternalType: &result.ExternalType,
		Account:      &result.Account,
		Region:       &result.Region,
		Zone:         &result.Zone,
		Network:      &result.Network,
		Subnet:       &result.Subnet,
		Name:         &result.Name,
		Source:       &result.Source,
		Tags:         &result.Tags,
		Config:       &dataStr,
	}

	if result.CreatedAt != nil {
		ci.CreatedAt = *result.CreatedAt
	}

	return ci, nil

}

func updateCI(ctx v1.ScrapeContext, ci models.ConfigItem) error {
	existing, err := GetConfigItem(*ci.ExternalType, ci.ID)
	if err != nil && err != gorm.ErrRecordNotFound {
		return errors.Wrapf(err, "unable to lookup existing config: %s", ci)
	}
	if existing == nil {
		ci.ID = ulid.MustNew().AsUUID()
		if err := CreateConfigItem(&ci); err != nil {
			logger.Errorf("[%s] failed to create item %v", ci, err)
		}
		return nil
	}

	ci.ID = existing.ID
	if err := UpdateConfigItem(&ci); err != nil {
		if err := CreateConfigItem(&ci); err != nil {
			return fmt.Errorf("[%s] failed to update item %v", ci, err)
		}
	}
	changes, err := compare(ci, *existing)
	if err != nil {
		logger.Errorf("[%s] failed to check for changes: %v", ci, err)
	}

	if changes != nil {
		logger.Infof("[%s/%s] detected changes", ci.ConfigType, ci.ExternalID[0])
		if err := CreateConfigChange(changes); err != nil {
			logger.Errorf("[%s] failed to update with changes %v", ci, err)
		}
	}
	return nil

}

func UpdateChange(ctx v1.ScrapeContext, result *v1.ScrapeResult) error {
	change := models.NewConfigChangeFromV1(*result.ChangeResult)

	ci, err := repository.GetConfigItem(change.ExternalType, change.ExternalID)
	if ci == nil {
		logger.Warnf("[%s/%s] unable to find config item for change: %v", change.ExternalType, change.ExternalID, change)
		return nil
	} else if err != nil {
		return err
	}
	change.ConfigID = ci.ID

	return repository.CreateConfigChange(change)
}

func updateAnalysis(ctx v1.ScrapeContext, result *v1.ScrapeResult) error {
	analysis := models.NewAnalysisFromV1(*result.AnalysisResult)
	ci, err := repository.GetConfigItem(analysis.ExternalType, analysis.ExternalID)
	if ci == nil {
		logger.Warnf("[%s/%s] unable to find config item for analysis: %+v", analysis.ExternalType, analysis.ExternalID, analysis)
		return nil
	} else if err != nil {
		return err
	}

	logger.Tracef("[%s/%s] ==> %s", analysis.ExternalType, analysis.ExternalID, analysis)
	analysis.ConfigID = ci.ID
	analysis.ID = ulid.MustNew().AsUUID()

	return repository.CreateAnalysis(analysis)

}

// Update creates or update a configuartion with config changes
func Update(ctx v1.ScrapeContext, results []v1.ScrapeResult) error {
	for _, result := range results {

		if result.Config != nil {
			ci, err := NewConfigItemFromResult(result)
			if err != nil {
				return errors.Wrapf(err, "unable to create config item: %s", result)
			}

			if err := updateCI(ctx, *ci); err != nil {
				return err
			}
		}

		if result.AnalysisResult != nil {
			if err := updateAnalysis(ctx, &result); err != nil {
				return err
			}
		}

		if result.ChangeResult != nil {
			if err := UpdateChange(ctx, &result); err != nil {
				return err
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
		Patches:    patchStr,
	}, nil

}
