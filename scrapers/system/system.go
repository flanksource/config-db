package system

import (
	"fmt"
	"strings"
	"time"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty/models"
	"github.com/flanksource/duty/query"
	"github.com/flanksource/duty/rbac"
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
		// local agent config id should be models.LocalAgentConfigID
		id := lo.Ternary(agent.ID == uuid.Nil, models.LocalAgentConfigID, agent.ID)

		health := models.HealthHealthy
		status := "online"
		lastSeen := time.Since(lo.FromPtr(agent.LastSeen))
		if lastSeen > (61*time.Second) && id != models.LocalAgentConfigID {
			health = models.HealthUnhealthy
			status = "offline"
		}
		results = append(results, v1.ScrapeResult{
			ID:          id.String(),
			Name:        agent.Name,
			Type:        "MissionControl::Agent",
			ConfigClass: "Agent",
			Config:      agent,
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

	access, errAccess := scrapePlaybookAccess(ctx, scraperID, users)
	if errAccess != nil {
		result = result.SetError(errAccess)
		return result
	}
	result.ConfigAccess = access

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
			Tenant:    "mission-control",
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
			Tenant:    "mission-control",
			GroupType: "team",
			Aliases:   pq.StringArray{"team:" + team.ID.String()},
			ScraperID: scraperID,
			CreatedAt: time.Now(),
		})
	}

	return externalGroups, nil
}

func scrapePlaybookRoles(scraperID uuid.UUID) []models.ExternalRole {
	actions := []string{
		policy.ActionMCPRun,
		policy.ActionPlaybookRun,
		policy.ActionPlaybookApprove,
		policy.ActionPlaybookCancel,
	}

	roles := make([]models.ExternalRole, 0, len(actions))
	for _, action := range actions {
		roles = append(roles, models.ExternalRole{
			Name:      action,
			Tenant:    "mission-control",
			RoleType:  "playbook-action",
			Aliases:   pq.StringArray{"role:" + action},
			ScraperID: &scraperID,
			CreatedAt: time.Now(),
		})
	}

	return roles
}

func scrapePlaybookAccess(ctx api.ScrapeContext, scraperID uuid.UUID, users []models.ExternalUser) ([]v1.ExternalConfigAccess, error) {
	actions := []string{
		policy.ActionMCPRun,
		policy.ActionPlaybookRun,
		policy.ActionPlaybookApprove,
		policy.ActionPlaybookCancel,
	}
	source := "mission-control-rbac"
	access := make([]v1.ExternalConfigAccess, 0)

	for _, user := range users {
		personID := personIDFromAliases(user.Aliases)
		if personID == "" {
			continue
		}

		for _, action := range actions {
			response, err := rbac.RunSubjectAccessSearch(ctx.DutyContext(), rbac.SubjectAccessSearchRequest{
				Subject:       personID,
				Action:        action,
				ResourceTypes: []string{"playbook"},
			})
			if err != nil {
				return nil, fmt.Errorf("error running playbook access search for user %s action %s: %w", personID, action, err)
			}

			for _, result := range response.Results {
				if result.ResourceType != "playbook" {
					continue
				}

				playbookID, err := uuid.Parse(result.ID)
				if err != nil {
					return nil, fmt.Errorf("invalid playbook id from access search %q: %w", result.ID, err)
				}

				access = append(access, v1.ExternalConfigAccess{
					ConfigID:            playbookID,
					ExternalUserAliases: []string{"people:" + personID},
					ExternalRoleAliases: []string{"role:" + action},
					ScraperID:           &scraperID,
					Source:              &source,
					CreatedAt:           time.Now(),
					ConfigExternalID:    v1.ExternalID{ConfigID: playbookID.String(), ConfigType: "MissionControl::Playbook"},
				})
			}
		}
	}

	return access, nil
}

func personIDFromAliases(aliases pq.StringArray) string {
	for _, alias := range aliases {
		if strings.HasPrefix(alias, "people:") {
			return strings.TrimPrefix(alias, "people:")
		}
	}
	return ""
}
