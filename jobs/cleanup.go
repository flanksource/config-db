package jobs

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/flanksource/commons/properties"
	v1 "github.com/flanksource/config-db/api/v1"
	cdb "github.com/flanksource/config-db/db"
	"github.com/flanksource/config-db/scrapers"
	"github.com/flanksource/duty/db"
	"github.com/flanksource/duty/job"
	"github.com/flanksource/duty/models"
	"github.com/google/uuid"
	"github.com/samber/lo"
)

const (
	DefaultConfigAnalysisRetentionDays = 60
	DefaultConfigChangeRetentionDays   = 60
	DefaultConfigItemRetentionDays     = 7
)

var (
	ConfigAnalysisRetentionDays int
	ConfigChangeRetentionDays   int
	ConfigItemRetentionDays     int
)

var cleanupJobs = []*job.Job{
	CleanupConfigAnalysis,
	CleanupConfigChanges,
	CleanupConfigItems,
	SoftDeleteAgentStaleItems,
	CleanupConfigScrapers,
}

var CleanupConfigAnalysis = &job.Job{
	Name:       "CleanupConfigAnalysis",
	Schedule:   "@every 24h",
	Singleton:  true,
	JobHistory: true,
	Retention:  job.RetentionBalanced,
	Fn: func(ctx job.JobRuntime) error {
		ctx.History.ResourceType = JobResourceType
		if ConfigAnalysisRetentionDays <= 0 {
			ConfigAnalysisRetentionDays = DefaultConfigAnalysisRetentionDays
		}

		if err := ctx.DB().Exec(`
            UPDATE config_analysis
            SET status = 'closed'
            WHERE (NOW() - last_observed) > INTERVAL '1 day' * ?
        `, properties.Int(7, "config_analysis.set_status_closed_days")).Error; err != nil {
			return err
		}

		tx := ctx.DB().Exec(`
        DELETE FROM config_analysis
        WHERE
            (NOW() - last_observed) > INTERVAL '1 day' * ? AND
            id NOT IN (SELECT config_analysis_id FROM evidences)
    `, ConfigAnalysisRetentionDays)

		ctx.History.SuccessCount = int(tx.RowsAffected)
		return tx.Error
	},
}

var CleanupConfigChanges = &job.Job{
	Name:       "CleanupConfigChanges",
	Schedule:   "@every 24h",
	Singleton:  true,
	JobHistory: true,
	Retention:  job.RetentionBalanced,
	Fn: func(ctx job.JobRuntime) error {
		ctx.History.ResourceType = JobResourceType
		if ConfigChangeRetentionDays <= 0 {
			ConfigChangeRetentionDays = DefaultConfigChangeRetentionDays
		}

		tx := ctx.DB().Exec(`
        DELETE FROM config_changes
        WHERE
            (NOW() - created_at) > INTERVAL '1 day' * ? AND
            id NOT IN (SELECT config_change_id FROM evidences)
    `, ConfigChangeRetentionDays)
		ctx.History.SuccessCount = int(tx.RowsAffected)
		return tx.Error
	},
}

var CleanupConfigItems = &job.Job{
	Name:       "CleanupConfigItems",
	Schedule:   "0 2 * * *", // Everynight at 2 AM
	Singleton:  true,
	JobHistory: true,
	Retention:  job.RetentionBalanced,
	Fn: func(ctx job.JobRuntime) error {
		ctx.History.ResourceType = JobResourceType
		retention := ctx.Properties().Duration("config.retention.period", (time.Hour * 24 * time.Duration(ConfigItemRetentionDays)))
		days := int64(retention.Hours() / 24)

		var prevCount int64
		if err := ctx.DB().Raw("SELECT COUNT(*) FROM config_items").Scan(&prevCount).Error; err != nil {
			return fmt.Errorf("failed to count config items: %w", db.ErrorDetails(err))
		}

		if err := ctx.DB().Exec("SELECT delete_old_config_items(?)", days).Error; err != nil {
			return fmt.Errorf("failed to delete config items: %w", db.ErrorDetails(err))
		}

		var newCount int64
		if err := ctx.DB().Raw("SELECT COUNT(*) FROM config_items").Scan(&newCount).Error; err != nil {
			return fmt.Errorf("failed to count config items: %w", db.ErrorDetails(err))
		}
		ctx.History.SuccessCount = int(newCount - prevCount)
		return nil
	},
}

var SoftDeleteAgentStaleItems = &job.Job{
	Name:       "SoftDeleteAgentStaleItems",
	Schedule:   "0 3 * * *", // Everynight at 3 AM
	Singleton:  true,
	JobHistory: true,
	RunNow:     true,
	Retention:  job.RetentionBalanced,
	Fn: func(ctx job.JobRuntime) error {
		ctx.History.ResourceType = JobResourceType

		var scrapersFromAgents []models.ConfigScraper
		if err := ctx.DB().Where("agent_id != ?", uuid.Nil).Find(&scrapersFromAgents).Error; err != nil {
			return fmt.Errorf("failed to find scrapers from agents: %w", db.ErrorDetails(err))
		}
		ctx.Logger.V(3).Infof("soft deleting stale config items for %d scrapers from agents", len(scrapersFromAgents))

		staleItemAge := scrapers.DefaultStaleTimeout
		if p, exists := ctx.Properties()["config.retention.agent.stale_item_age"]; exists {
			staleItemAge = p
		}

		for _, scraper := range scrapersFromAgents {
			var scraperV1 v1.ScrapeConfig
			if err := json.Unmarshal([]byte(scraper.Spec), &scraperV1); err != nil {
				ctx.History.AddErrorf("failed to unmarshal scraper spec (scraper_id=%s): %v", scraper.ID, err)
				continue
			}

			scraperStaleItemAge := lo.CoalesceOrEmpty(scraperV1.Spec.Retention.StaleItemAge, staleItemAge)
			if deleted, err := scrapers.DeleteStaleConfigItems(ctx.Context, scraperStaleItemAge, scraper.ID); err != nil {
				ctx.History.AddErrorf("failed to delete stale config items of agent (scraper_id=%s): %v", scraper.ID, err)
				continue
			} else {
				ctx.History.SuccessCount += int(deleted)
			}
		}

		return nil
	},
}

var CleanupConfigScrapers = &job.Job{
	Name:       "CleanupConfigScrapers",
	Schedule:   "15 2 * * *", // Everynight at 2:15 AM
	Singleton:  true,
	JobHistory: true,
	Retention:  job.RetentionFew,
	Fn: func(ctx job.JobRuntime) error {
		ctx.History.ResourceType = JobResourceType

		var deletedIDs []string
		if err := ctx.DB().Model(&models.ConfigScraper{}).Select("id").Where("deleted_at IS NOT NULL").Find(&deletedIDs).Error; err != nil {
			return fmt.Errorf("error fetching deleted config_scraper ids: %w", db.ErrorDetails(err))
		}

		for _, id := range deletedIDs {
			if err := cdb.DeleteScrapeConfig(ctx.Context, id); err != nil {
				ctx.History.AddErrorf("error deleting scrape config[%s]: %v", id, err)
			} else {
				ctx.History.SuccessCount += 1
			}
		}

		return nil
	},
}
