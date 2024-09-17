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
	var allScrapers []models.ConfigScraper
	if err := ctx.DB().Find(&allScrapers).Error; err != nil {
		return err
	}

	for _, s := range allScrapers {
		var spec v1.ScraperSpec
		if err := json.Unmarshal([]byte(s.Spec), &spec); err != nil {
			ctx.History.AddErrorf("failed to unmarshal scraper spec (%s): %v", s.ID, err)
			continue
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
