package jobs

import (
	"encoding/json"

	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/scrapers"
	"github.com/flanksource/duty/job"
	"github.com/flanksource/duty/models"
)

func ProcessChangeRetentionRules(ctx job.JobRuntime) error {
	ctx.History.ResourceType = JobResourceType
	var activeScrapers []models.ConfigScraper
	if err := ctx.DB().Where("deleted_at IS NULL").Find(&activeScrapers).Error; err != nil {
		return err
	}

	for _, s := range activeScrapers {
		var spec v1.ScraperSpec
		if err := json.Unmarshal([]byte(s.Spec), &spec); err != nil {
			return err
		}

		for _, changeSpec := range spec.Retention.Changes {
			err := scrapers.ProcessChangeRetention(ctx.Context, s.ID, changeSpec)
			if err != nil {
				logger.Errorf("Error processing change retention for scraper[%s] config analysis: %v", s.ID, err)
				ctx.History.AddError(err.Error())
			} else {
				ctx.History.SuccessCount++
			}
		}
	}

	return nil
}
