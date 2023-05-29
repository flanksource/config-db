package db

import (
	"errors"

	"github.com/flanksource/duty/models"
	"gorm.io/gorm"
)

func getAnalysis(analysis models.ConfigAnalysis) (*models.ConfigAnalysis, error) {
	existing := models.ConfigAnalysis{}
	err := db.First(&existing, "config_id = ? AND analyzer = ?", analysis.ConfigID, analysis.Analyzer).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}

	return &existing, err
}

func CreateAnalysis(analysis models.ConfigAnalysis) error {
	// get analysis by config_id, and summary
	existingAnalysis, err := getAnalysis(analysis)
	if err != nil {
		return err
	}

	if existingAnalysis != nil {
		analysis.ID = existingAnalysis.ID
		return db.Model(&analysis).Updates(map[string]interface{}{
			"last_observed": gorm.Expr("now()"),
			"message":       analysis.Message,
			"status":        analysis.Status,
		}).Error
	}

	return db.Create(&analysis).Error
}
