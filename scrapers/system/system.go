package system

import (
	"fmt"
	"time"

	v1 "github.com/flanksource/config-db/api"
	"github.com/flanksource/config-db/pkg/api"
	"github.com/flanksource/duty/models"
	"github.com/samber/lo"
	"gorm.io/gorm"
)

type Scraper struct{}

func (s Scraper) CanScrape(configs v1.ScraperSpec) bool {
	return configs.System
}

func (s Scraper) Scrape(ctx api.ScrapeContext) v1.ScrapeResults {
	var results v1.ScrapeResults

	var agents []models.Agent
	if err := ctx.DB().Where("deleted_at IS NULL").Find(&agents).Error; err != nil {
		return results.Errorf(err, "error querying agents")
	}

	for _, agent := range agents {
		lastSeen := time.Since(lo.FromPtr(agent.LastSeen))
		health := models.HealthHealthy
		status := "online"
		if lastSeen > (61 * time.Second) {
			health = models.HealthUnhealthy
			status = "offline"
		}
		results = append(results, v1.ScrapeResult{
			ID:          agent.ID.String(),
			Name:        agent.Name,
			Type:        "MissionControl::Agent",
			ConfigClass: "Agent",
			Health:      health,
			Status:      status,
		})
	}

	jobHistories, err := gorm.G[models.JobHistory](ctx.DB()).
		Table("job_history_latest_status").
		Where("created_at > NOW() - INTERVAL '2 days'").
		Find(ctx)
	if err != nil {
		return results.Errorf(err, "error querying job history")
	}

	for _, jh := range jobHistories {
		health := models.HealthHealthy
		switch models.JobStatus(jh.Status) {
		case models.StatusStale, models.StatusWarning:
			health = models.HealthWarning
		case models.StatusFailed:
			health = models.HealthUnhealthy
		case models.StatusSkipped:
			health = models.HealthUnknown
		}

		id := jh.Name
		if jh.ResourceID != "" {
			id += "/" + jh.ResourceID
		}

		errors := jh.Errors
		if v, ok := jh.Details["errors"]; ok {
			if vStringSlice, ok := v.([]string); ok {
				errors = append(errors, vStringSlice...)
			} else {
				errors = append(errors, fmt.Sprint(v))
			}
		}
		config := map[string]any{
			"error_count":   jh.ErrorCount,
			"errors":        errors,
			"resource_type": jh.ResourceType,
		}
		results = append(results, v1.ScrapeResult{
			ID:          id,
			Name:        id,
			ConfigClass: "Job",
			Type:        "MissionControl::Job",
			Status:      lo.Capitalize(jh.Status),
			Health:      health,
			Config:      config,
		})
	}
	return results
}
