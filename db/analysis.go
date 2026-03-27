package db

import (
	"errors"
	"fmt"
	"time"

	"github.com/flanksource/commons/duration"
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	cModels "github.com/flanksource/config-db/db/models"
	"github.com/flanksource/config-db/db/ulid"
	"github.com/flanksource/duty/models"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

func getAnalysis(ctx api.ScrapeContext, analysis models.ConfigAnalysis) (*models.ConfigAnalysis, error) {
	existing := models.ConfigAnalysis{}
	err := ctx.DB().First(&existing, "config_id = ? AND analyzer = ?", analysis.ConfigID, analysis.Analyzer).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}

	return &existing, err
}

func CreateAnalysis(ctx api.ScrapeContext, analysis models.ConfigAnalysis) error {
	// get analysis by config_id and analyzer
	existingAnalysis, err := getAnalysis(ctx, analysis)
	if err != nil {
		return err
	}

	if existingAnalysis != nil {
		analysis.ID = existingAnalysis.ID

		return ctx.DB().Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(&models.ConfigAnalysis{}).
				Where("id = ?", existingAnalysis.ID).
				Select("*").
				Omit("id", "first_observed", "is_pushed", "last_observed").
				Updates(analysis).Error; err != nil {
				return err
			}

			return tx.Model(&models.ConfigAnalysis{}).
				Where("id = ?", existingAnalysis.ID).
				UpdateColumn("last_observed", gorm.Expr("now()")).Error
		})
	}

	return ctx.DB().Create(&analysis).Error
}

func upsertAnalysis(ctx api.ScrapeContext, result *v1.ScrapeResult) error {
	var ci *cModels.ConfigItem
	var err error

	if result.AnalysisResult.ExternalID != "" {
		ci, err = ctx.TempCache().Find(ctx, v1.ExternalID{
			ConfigType: result.AnalysisResult.ConfigType,
			ExternalID: result.AnalysisResult.ExternalID,
		})
		if err != nil {
			return err
		}
	}

	if ci == nil {
		for _, extID := range result.AnalysisResult.ExternalConfigs {
			ci, err = ctx.TempCache().Find(ctx, extID)
			if err != nil {
				return err
			}
			if ci != nil {
				break
			}
		}
	}

	if ci == nil {
		if ctx.PropertyOn(false, "log.missing") {
			ctx.Debugf("unable to find config item for analysis: (source=%s, externalID=%s, externalConfigs=%v, analysis: %+v)", result.AnalysisResult.Source, result.AnalysisResult.ExternalID, result.AnalysisResult.ExternalConfigs, result.AnalysisResult)
		}
		return nil
	}

	analysis := result.AnalysisResult.ToConfigAnalysis()
	analysis.ConfigID = uuid.MustParse(ci.ID)
	analysis.ID = uuid.MustParse(ulid.MustNew().AsUUID())
	analysis.ScraperID = ctx.ScrapeConfig().GetPersistedID()
	if analysis.Status == "" {
		analysis.Status = models.AnalysisStatusOpen
	}

	return CreateAnalysis(ctx, analysis)
}

// DefaultAnalysisMaxAge is the default retention period for config analysis.
// Analysis not seen within this window will be resolved.
var DefaultAnalysisMaxAge = 48 * time.Hour

// resolveAnalysisMaxAge returns the effective retention duration for analysis resolution.
// It checks (in order): per-scraper AnalysisAge config → global property → hardcoded default.
func resolveAnalysisMaxAge(ctx api.ScrapeContext) (time.Duration, bool, error) {
	configured := ctx.ScrapeConfig().Spec.Retention.StaleAnalysisAge
	switch configured {
	case "keep":
		return 0, false, nil
	case "":
		return ctx.Properties().Duration("config_analysis.retention.max_age", DefaultAnalysisMaxAge), true, nil
	default:
		parsed, err := duration.ParseDuration(configured)
		if err != nil {
			return 0, false, fmt.Errorf("failed to parse analysisAge %q: %w", configured, err)
		}
		return time.Duration(parsed), true, nil
	}
}

// runAnalysisRetentionPass resolves stale config analyses for the current scraper.
// It is safe to call even when the scraper returns zero results.
func runAnalysisRetentionPass(ctx api.ScrapeContext) {
	scraperID := ctx.ScrapeConfig().GetPersistedID()
	if scraperID == nil {
		return
	}

	maxAge, ok, err := resolveAnalysisMaxAge(ctx)
	if err != nil {
		ctx.JobHistory().AddErrorf("invalid analysis retention config: %v", err)
		return
	}
	if !ok {
		return
	}

	if err := UpdateAnalysisStatusByAge(ctx, maxAge, scraperID.String(), models.AnalysisStatusResolved); err != nil {
		ctx.Errorf("failed to mark stale analysis as resolved: %v", err)
	}
}

// UpdateAnalysisStatusByAge resolves config analyses belonging to the given scraper
// that have not been observed within maxAge.
func UpdateAnalysisStatusByAge(ctx api.ScrapeContext, maxAge time.Duration, scraperID, status string) error {
	return ctx.DB().
		Model(&models.ConfigAnalysis{}).
		Where("last_observed <= NOW() - INTERVAL '1 second' * ?", maxAge.Seconds()).
		Where("scraper_id = ?", scraperID).
		Where("status != ?", status).
		Update("status", status).
		Error
}
