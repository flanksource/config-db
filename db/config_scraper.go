package db

import (
	"fmt"

	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/utils"
	"github.com/flanksource/duty/models"
	"github.com/google/uuid"
)

func DeleteScrapeConfig(scrapeConfig *v1.ScrapeConfig) error {
	configScraper := models.ConfigScraper{
		ID: uuid.MustParse(string(scrapeConfig.GetUID())),
	}
	return db.Delete(&configScraper).Error
}

func PersistScrapeConfig(scrapeConfig *v1.ScrapeConfig) (bool, error) {
	configScraper := models.ConfigScraper{
		ID:   uuid.MustParse(string(scrapeConfig.GetUID())),
		Name: fmt.Sprintf("%s/%s", scrapeConfig.Namespace, scrapeConfig.Name),
	}
	configScraper.Spec, _ = utils.StructToJSON(scrapeConfig.Spec.ConfigScraper)

	tx := db.Table("config_scrapers").Save(&configScraper)
	return tx.RowsAffected > 0, tx.Error
}

func GetScrapeConfigs() ([]models.ConfigScraper, error) {
	var configScrapers []models.ConfigScraper
	err := db.Find(&configScrapers).Error
	return configScrapers, err
}

func PersistScrapeConfigFromFile(configScraperSpec v1.ConfigScraper) (models.ConfigScraper, error) {
	var configScraper models.ConfigScraper
	spec, _ := utils.StructToJSON(configScraperSpec)

	// Check if exists
	tx := db.Table("config_scrapers").Where("spec = ?", spec).Find(&configScraper)
	if tx.Error != nil {
		return configScraper, tx.Error
	}
	if tx.RowsAffected > 0 {
		return configScraper, nil
	}

	// Create if not exists
	var err error
	configScraper.Spec = spec
	configScraper.Name, err = configScraperSpec.GenerateName()
	if err != nil {
		return configScraper, err
	}
	tx = db.Table("config_scrapers").Create(&configScraper)
	return configScraper, tx.Error
}
