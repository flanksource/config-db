package system

import (
	"time"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty/models"
	"github.com/samber/lo"
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

	return results
}
