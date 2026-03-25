package db

import (
	"errors"

	"github.com/flanksource/config-db/api"
	"github.com/flanksource/duty/models"
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
