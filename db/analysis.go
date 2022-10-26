package db

import (
	"errors"

	"github.com/flanksource/config-db/db/models"
	"gorm.io/gorm"
)

func GetAnalysis(analysis models.Analysis) (*models.Analysis, error) {
	existing := models.Analysis{}
	err := db.First(&existing, "config_id = ? AND analyzer = ?", analysis.ConfigID, analysis.Analyzer).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}

	return &existing, err
}

func CreateAnalysis(analysis models.Analysis) error {
	// get analysis by config_id, and summary
	existingAnalysis, err := GetAnalysis(analysis)
	if err != nil {
		return err
	}
	if existingAnalysis != nil {
		analysis.ID = existingAnalysis.ID
		return db.Model(&analysis).Updates(map[string]interface{}{
			"last_observed": gorm.Expr("now()"),
			"message":       analysis.Message,
			"status":        analysis.Status}).Error
	}
	return db.Create(analysis).Error
}
