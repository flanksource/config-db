package jobs

import (
	gocontext "context"
	"encoding/json"

	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
	"github.com/flanksource/config-db/scrapers"
	"github.com/flanksource/duty/context"
	"github.com/flanksource/duty/models"
)

func ProcessChangeRetentionRules() {
	ctx := context.NewContext(gocontext.Background()).WithDB(db.DefaultDB(), db.Pool)
	jobHistory := models.NewJobHistory(ctx.Logger, "ProcessChangeRetentionRules", "", "").Start()
	_ = db.PersistJobHistory(jobHistory)
	defer func() {
		_ = db.PersistJobHistory(jobHistory.End())
	}()

	var activeScrapers []models.ConfigScraper
	if err := ctx.DB().Where("deleted_at IS NULL").Find(&activeScrapers).Error; err != nil {
		logger.Errorf("Error querying config scrapers from db: %v", err)
		jobHistory.AddError(err.Error())
		return
	}

	for _, s := range activeScrapers {
		var spec v1.ScraperSpec
		if err := json.Unmarshal([]byte(s.Spec), &spec); err != nil {
			logger.Errorf("Error unmarshaling config scraper[%s] into json: %v", s.ID, err)
			jobHistory.AddError(err.Error())
			continue
		}

		for _, changeSpec := range spec.Retention.Changes {
			err := scrapers.ProcessChangeRetention(ctx, s.ID, changeSpec)
			if err != nil {
				logger.Errorf("Error processing change retention for scraper[%s] config analysis: %v", s.ID, err)
				jobHistory.AddError(err.Error())
			} else {
				jobHistory.IncrSuccess()
			}
		}
	}
}
