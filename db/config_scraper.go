package db

import (
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db/models"
	"github.com/flanksource/config-db/utils"
	"github.com/google/uuid"
)

func DeleteScrapeConfig(scrapeConfig *v1.ScrapeConfig) error {
	configScraper := models.ConfigScraper{
		ID: uuid.MustParse(string(scrapeConfig.GetUID())),
	}
	return db.
		Debug().
		Delete(&configScraper).
		Error
}

func PersistScrapeConfig(scrapeConfig *v1.ScrapeConfig) (bool, error) {
	configScraper := models.ConfigScraper{
		ID: uuid.MustParse(string(scrapeConfig.GetUID())),
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
