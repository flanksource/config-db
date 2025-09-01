package system

import (
	"time"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
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

	jobHistories, err := gorm.G[models.JobHistory](ctx.DB()).Table("job_history_latest_status").Find(ctx)
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

		results = append(results, v1.ScrapeResult{
			ID:          id,
			Name:        id,
			ConfigClass: "Job",
			Type:        "MissionControl::Job",
			Status:      lo.Capitalize(jh.Status),
			Health:      health,
			Config: map[string]any{
				"success_count": jh.SuccessCount,
				"error_count":   jh.ErrorCount,
				"details":       jh.Details,
				"errors":        jh.Errors,
				"duration_ms":   jh.DurationMillis,
				"resource_type": jh.ResourceType,
			},
		})
	}
	return results
}
