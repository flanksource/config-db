package system

import (
	"fmt"
	"time"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty/models"
	"github.com/flanksource/duty/query"
	"github.com/flanksource/duty/rbac/policy"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/samber/lo"
	"gorm.io/gorm"
)

type Scraper struct{}

func (s Scraper) CanScrape(configs v1.ScraperSpec) bool {
	return configs.System
}

func (s Scraper) Scrape(ctx api.ScrapeContext) v1.ScrapeResults {
	var results v1.ScrapeResults

	results = append(results, scrapeAgents(ctx)...)
	results = append(results, scrapePlaybooks(ctx)...)
	results = append(results, scrapeJobHistories(ctx)...)

	if persistedID := ctx.ScrapeConfig().GetPersistedID(); persistedID != nil {
		scraperID := lo.FromPtr(persistedID)
		results = append(results, scrapeAccessEntities(ctx, scraperID))
	}

	return results
}

func scrapeAgents(ctx api.ScrapeContext) v1.ScrapeResults {
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

func scrapePlaybooks(ctx api.ScrapeContext) v1.ScrapeResults {
	var results v1.ScrapeResults

	var playbooks []models.Playbook
	if err := ctx.DB().Where("deleted_at IS NULL AND source != ?", models.SourceCRD).Find(&playbooks).Error; err != nil {
		return results.Errorf(err, "error querying playbooks")
	}

	for _, playbook := range playbooks {
		name := playbook.Name
		if playbook.Title != "" {
			name = playbook.Title
		}

		results = append(results, v1.ScrapeResult{
			ID:          playbook.ID.String(),
			Name:        name,
			Type:        "MissionControl::Playbook",
			ConfigClass: "Playbook",
			Config:      playbook.Spec,
		})
	}

	return results
}

func scrapeJobHistories(ctx api.ScrapeContext) v1.ScrapeResults {
	var results v1.ScrapeResults

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

// scrapeAccessEntities returns a single metadata-only ScrapeResult that carries
// external users (from people), external groups (from teams), and external roles
// for playbook actions.
func scrapeAccessEntities(ctx api.ScrapeContext, scraperID uuid.UUID) v1.ScrapeResult {
	result := v1.ScrapeResult{}

	users, errPeople := scrapePeople(ctx, scraperID)
	if errPeople != nil {
		result = result.SetError(errPeople)
		return result
	}
	result.ExternalUsers = users

	groups, errTeams := scrapeTeams(ctx, scraperID)
	if errTeams != nil {
		result = result.SetError(errTeams)
		return result
	}
	result.ExternalGroups = groups

	result.ExternalRoles = scrapePlaybookRoles(scraperID)

	return result
}

func scrapePeople(ctx api.ScrapeContext, scraperID uuid.UUID) ([]models.ExternalUser, error) {
	people, err := query.FindHumanPeople(ctx.DutyContext())
	if err != nil {
		return nil, fmt.Errorf("error querying people: %w", err)
	}

	externalUsers := make([]models.ExternalUser, 0, len(people))
	for _, person := range people {
		eu := models.ExternalUser{
			Name:      person.Name,
			AccountID: "mission-control",
			UserType:  lo.CoalesceOrEmpty(person.Type, "local"),
			Aliases:   pq.StringArray{"people:" + person.ID.String()},
			ScraperID: scraperID,
			CreatedAt: time.Now(),
		}
		if person.Email != "" {
			eu.Email = &person.Email
		}
		externalUsers = append(externalUsers, eu)
	}

	return externalUsers, nil
}

func scrapeTeams(ctx api.ScrapeContext, scraperID uuid.UUID) ([]models.ExternalGroup, error) {
	var teams []models.Team
	if err := ctx.DB().Where("deleted_at IS NULL").Find(&teams).Error; err != nil {
		return nil, fmt.Errorf("error querying teams: %w", err)
	}

	externalGroups := make([]models.ExternalGroup, 0, len(teams))
	for _, team := range teams {
		externalGroups = append(externalGroups, models.ExternalGroup{
			Name:      team.Name,
			AccountID: "mission-control",
			GroupType: "team",
			Aliases:   pq.StringArray{"team:" + team.ID.String()},
			ScraperID: scraperID,
			CreatedAt: time.Now(),
		})
	}

	return externalGroups, nil
}

func scrapePlaybookRoles(scraperID uuid.UUID) []models.ExternalRole {
	return []models.ExternalRole{
		{
			Name:      policy.ActionPlaybookRun,
			AccountID: "mission-control",
			RoleType:  "playbook-action",
			Aliases:   pq.StringArray{"role:" + policy.ActionPlaybookRun},
			ScraperID: &scraperID,
			CreatedAt: time.Now(),
		},
		{
			Name:      policy.ActionPlaybookApprove,
			AccountID: "mission-control",
			RoleType:  "playbook-action",
			Aliases:   pq.StringArray{"role:" + policy.ActionPlaybookApprove},
			ScraperID: &scraperID,
			CreatedAt: time.Now(),
		},
	}
}
