package db

import (
	"errors"
	"fmt"
	"strings"
	"time"

	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/utils"
	"github.com/flanksource/duty/models"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

func FindScraper(id string) (*models.ConfigScraper, error) {
	var configScraper models.ConfigScraper
	if err := db.Where("id = ?", id).First(&configScraper).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}

		return nil, err
	}

	return &configScraper, nil
}

func DeleteScrapeConfig(id string) error {
	if err := db.Table("config_scrapers").
		Where("id = ?", id).
		Update("deleted_at", time.Now()).
		Error; err != nil {
		return err
	}

	// Fetch all IDs which are linked to other tables
	foreignKeyTables := []string{
		"config_changes",
		"config_analysis",
		"config_component_relationships",
		"config_relationships",
		"evidences",
	}

	var selectQueryItems []string
	for _, t := range foreignKeyTables {
		selectQueryItems = append(selectQueryItems, fmt.Sprintf(`SELECT config_id FROM %s`, t))
	}
	selectQuery := strings.Join(selectQueryItems, " UNION ")

	// Remove scraper_id from linked config_items
	if err := db.Exec(fmt.Sprintf(`
        UPDATE config_items
        SET scraper_id = NULL
        WHERE id IN (%s) AND scraper_id = ?
    `, selectQuery), id).Error; err != nil {
		return err
	}

	// Soft delete remaining config_items
	if err := db.Exec(fmt.Sprintf(`
        UPDATE config_items
        SET deleted_at = NOW()
        WHERE id NOT IN (%s) AND scraper_id = ?
    `, selectQuery), id).Error; err != nil {
		return err
	}
	return nil
}

func PersistScrapeConfigFromCRD(scrapeConfig *v1.ScrapeConfig) (bool, error) {
	configScraper := models.ConfigScraper{
		ID:     uuid.MustParse(string(scrapeConfig.GetUID())),
		Name:   fmt.Sprintf("%s/%s", scrapeConfig.Namespace, scrapeConfig.Name),
		Source: models.SourceCRD,
	}
	configScraper.Spec, _ = utils.StructToJSON(scrapeConfig.Spec.ConfigScraper)

	tx := db.Table("config_scrapers").Save(&configScraper)
	return tx.RowsAffected > 0, tx.Error
}

func GetScrapeConfigs() ([]models.ConfigScraper, error) {
	var configScrapers []models.ConfigScraper
	err := db.Find(&configScrapers, "deleted_at IS NULL").Error
	return configScrapers, err
}

func PersistScrapeConfigFromFile(configScraperSpec v1.ConfigScraper) (models.ConfigScraper, error) {
	var configScraper models.ConfigScraper
	spec, err := utils.StructToJSON(configScraperSpec)
	if err != nil {
		return configScraper, fmt.Errorf("error converting scraper spec to JSON: %w", err)
	}

	// Check if exists
	tx := db.Table("config_scrapers").Where("spec = ?", spec).Find(&configScraper)
	if tx.Error != nil {
		return configScraper, tx.Error
	}
	if tx.RowsAffected > 0 {
		return configScraper, nil
	}

	// Create if not exists
	configScraper.Spec = spec
	configScraper.Name, err = configScraperSpec.GenerateName()
	configScraper.Source = models.SourceConfigFile
	if err != nil {
		return configScraper, err
	}
	tx = db.Table("config_scrapers").Create(&configScraper)
	return configScraper, tx.Error
}
