package db

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/utils"
	"github.com/flanksource/duty/context"
	"github.com/flanksource/duty/models"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

func FindScraper(ctx context.Context, id string) (*models.ConfigScraper, error) {
	var configScraper models.ConfigScraper
	if err := ctx.DB().Where("id = ?", id).First(&configScraper).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}

		return nil, err
	}

	return &configScraper, nil
}

func DeleteScrapeConfig(ctx api.ScrapeContext, id string) error {
	if err := ctx.DB().Table("config_scrapers").
		Where("id = ?", id).
		Update("deleted_at", time.Now()).
		Error; err != nil {
		return err
	}

	// Fetch all IDs which are linked to other tables
	foreignKeyTables := []string{
		"evidences",
	}

	var selectQueryItems []string
	for _, t := range foreignKeyTables {
		selectQueryItems = append(selectQueryItems, fmt.Sprintf(`SELECT config_id FROM %s`, t))
	}
	selectQuery := strings.Join(selectQueryItems, " UNION ")

	// Remove scraper_id from linked config_items
	if err := ctx.DB().Exec(fmt.Sprintf(`
        UPDATE config_items
        SET scraper_id = NULL
        WHERE id IN (%s) AND scraper_id = ?
    `, selectQuery), id).Error; err != nil {
		return err
	}

	// Soft delete remaining config_items
	if err := ctx.DB().Exec(fmt.Sprintf(`
        UPDATE config_items
        SET deleted_at = NOW()
        WHERE id NOT IN (%s) AND scraper_id = ?
    `, selectQuery), id).Error; err != nil {
		return err
	}
	return nil
}

func PersistScrapeConfigFromCRD(ctx context.Context, scrapeConfig *v1.ScrapeConfig) (bool, error) {
	var changed bool

	spec, err := utils.StructToJSON(scrapeConfig.Spec)
	if err != nil {
		return changed, fmt.Errorf("error converting to json: %w", err)
	}

	var existing models.ConfigScraper
	err = ctx.DB().Where("id = ?", string(scrapeConfig.GetUID())).First(&existing).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return changed, err
	}

	if existing.ID == uuid.Nil {
		changed = true
	} else {
		change, err := GenerateDiff(ctx, existing.Spec, spec)
		if err != nil {
			return changed, err
		}

		changed = change != ""
	}

	configScraper := models.ConfigScraper{
		ID:     uuid.MustParse(string(scrapeConfig.GetUID())),
		Name:   fmt.Sprintf("%s/%s", scrapeConfig.Namespace, scrapeConfig.Name),
		Spec:   spec,
		Source: models.SourceCRD,
	}
	tx := ctx.DB().Save(&configScraper)
	return changed, tx.Error
}

func GetScrapeConfigsOfAgent(ctx context.Context, agentID uuid.UUID) ([]models.ConfigScraper, error) {
	var configScrapers []models.ConfigScraper
	err := ctx.DB().Where("deleted_at IS NULL").Find(&configScrapers, "agent_id = ?", agentID).Error
	return configScrapers, err
}

func PersistScrapeConfigFromFile(ctx context.Context, scrapeConfig v1.ScrapeConfig) (models.ConfigScraper, error) {
	configScraper, err := scrapeConfig.ToModel()
	if err != nil {
		return configScraper, err
	}

	tx := ctx.DB().Table("config_scrapers").Where("spec = ?", configScraper.Spec).Find(&configScraper)
	if tx.Error != nil {
		return configScraper, tx.Error
	}
	if tx.RowsAffected > 0 {
		return configScraper, nil
	}

	configScraper.Name, err = scrapeConfig.Spec.GenerateName()
	configScraper.Source = models.SourceConfigFile
	if err != nil {
		return configScraper, err
	}
	return configScraper, ctx.DB().Create(&configScraper).Error
}
