package github

import (
	"fmt"
	"math"
	"net/url"
	"strings"
	"sync"

	"github.com/flanksource/commons/collections"
	"github.com/flanksource/duty/types"
	"github.com/flanksource/is-healthy/pkg/health"
	"github.com/google/go-github/v73/github"
	"github.com/samber/lo"
	"golang.org/x/sync/errgroup"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
)

const (
	ConfigTypeWorkflow = "GitHubAction::Workflow"

	// Number of concurrent requests to the GitHub API per repository
	DefaultConcurrency = 10
)

type GithubActionsScraper struct {
}

func (gh GithubActionsScraper) CanScrape(spec v1.ScraperSpec) bool {
	return len(spec.GithubActions) > 0
}

// Scrape fetches github workflows and workflow runs from github API and converts the action executions (workflow runs) to change events.
func (gh GithubActionsScraper) Scrape(ctx api.ScrapeContext) v1.ScrapeResults {
	results := v1.ScrapeResults{}
	for _, config := range ctx.ScrapeConfig().Spec.GithubActions {
		client, err := NewGitHubActionsClient(ctx, config)
		if err != nil {
			results.Errorf(err, "failed to create github actions client for owner %s with repository %v", config.Owner, config.Repository)
			continue
		}

		workflows, err := client.GetWorkflows(ctx)
		if err != nil {
			results.Errorf(err, "failed to get projects for %s", config.Repository)
			continue
		}

		for _, workflow := range workflows {
			if !collections.MatchItems(workflow.GetName(), config.Workflows...) {
				continue
			}

			runs, err := getNewWorkflowRuns(ctx, client, workflow, config)
			if err != nil {
				results.Errorf(err, "failed to get workflow runs for %s", workflow.GetName())
				continue
			}

			changeResults, latestCompletedRun := processRuns(workflow, runs)

			result := v1.ScrapeResult{
				ConfigClass: "Deployment",
				Config:      workflow,
				Type:        ConfigTypeWorkflow,
				ID:          fmt.Sprintf("%d/%s", workflow.GetID(), workflow.GetName()),
				Name:        fmt.Sprintf("%s/%s", config.Repository, workflow.GetName()),
				Changes:     changeResults,
				Tags:        v1.Tags{{Name: "org", Value: config.Owner}},
				Aliases:     []string{fmt.Sprintf("%s/%d", workflow.GetName(), workflow.GetID())},
				CreatedAt:   lo.ToPtr(workflow.GetCreatedAt().Time),
				Properties:  workflowProperties(workflow),
			}

			// The latest completed run determines the health of the workflow
			if latestCompletedRun.GetID() != 0 {
				result = result.WithHealthStatus(getHealthFromConclusion(latestCompletedRun))
			}

			results = append(results, result)
		}
	}

	return results
}

func getNewWorkflowRuns(ctx api.ScrapeContext, client *GitHubActionsClient, workflow *github.Workflow, config v1.GitHubActions) ([]*github.WorkflowRun, error) {
	workflowID := fmt.Sprintf("%d/%s", workflow.GetID(), workflow.GetName())

	// Get first page to determine total count
	firstPage, err := client.GetWorkflowRuns(ctx, config, int(workflow.GetID()), 1)
	if err != nil {
		return nil, err
	}

	pagesToFetch := int(math.Ceil(float64(firstPage.GetTotalCount()) / 100))
	if pagesToFetch == 0 {
		return firstPage.WorkflowRuns, nil
	}

	var g errgroup.Group
	g.SetLimit(client.ScrapeContext.Properties().Int("scrapers.githubactions.concurrency", DefaultConcurrency))

	var mu sync.Mutex
	var allRuns = firstPage.WorkflowRuns
	for page := range pagesToFetch {
		pageNumber := page + 1
		if pageNumber == 1 {
			continue // Skip first page, it's already fetched
		}

		g.Go(func() error {
			client.ScrapeContext.Debugf("fetching workflow runs for page (workflow: %s, page: %d)", workflowID, pageNumber)
			pageRuns, err := client.GetWorkflowRuns(ctx, config, int(workflow.GetID()), pageNumber)
			if err != nil {
				return fmt.Errorf("failed to get workflow runs for page %d: %w", pageNumber, err)
			}

			mu.Lock()
			allRuns = append(allRuns, pageRuns.WorkflowRuns...)
			mu.Unlock()
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}

	return allRuns, nil
}

func runToChangeResult(run *github.WorkflowRun, workflow *github.Workflow) v1.ChangeResult {
	summary := fmt.Sprintf("%s (branch: %s)", run.GetDisplayTitle(), run.GetHeadBranch())

	if run.GetStatus() == "completed" {
		duration := run.GetUpdatedAt().Sub(run.GetCreatedAt().Time)
		summary = fmt.Sprintf("%s; duration: %s", summary, duration.String())
	}

	changeType := fmt.Sprintf("GitHubActionRun%s", lo.PascalCase(run.GetConclusion()))
	createdAt := run.GetCreatedAt().Time

	evaluatedRun := createEvaluatedRun(run)

	return v1.ChangeResult{
		ChangeType:       changeType,
		CreatedAt:        &createdAt,
		Severity:         run.GetConclusion(),
		ExternalID:       fmt.Sprintf("%d/%s", workflow.GetID(), workflow.GetName()),
		ConfigType:       ConfigTypeWorkflow,
		Summary:          summary,
		Source:           run.GetTriggeringActor().GetLogin(),
		Details:          v1.NewJSON(evaluatedRun),
		ExternalChangeID: fmt.Sprintf("%s/%d/%d", workflow.GetName(), workflow.GetID(), run.GetID()),
	}
}

func sanitizeRepository(repo *github.Repository) *github.Repository {
	if repo == nil {
		return nil
	}

	return &github.Repository{
		ID:              repo.ID,
		NodeID:          repo.NodeID,
		Name:            repo.Name,
		FullName:        repo.FullName,
		Description:     repo.Description,
		Homepage:        repo.Homepage,
		DefaultBranch:   repo.DefaultBranch,
		CreatedAt:       repo.CreatedAt,
		PushedAt:        repo.PushedAt,
		UpdatedAt:       repo.UpdatedAt,
		Language:        repo.Language,
		Fork:            repo.Fork,
		ForksCount:      repo.ForksCount,
		OpenIssuesCount: repo.OpenIssuesCount,
		StargazersCount: repo.StargazersCount,
		WatchersCount:   repo.WatchersCount,
		Size:            repo.Size,
		Private:         repo.Private,
		Archived:        repo.Archived,
		Disabled:        repo.Disabled,
		Topics:          repo.Topics,
		Owner:           sanitizeActor(repo.Owner),
	}
}

func sanitizeActor(user *github.User) *github.User {
	if user == nil {
		return nil
	}

	return &github.User{
		ID:        user.ID,
		NodeID:    user.NodeID,
		Login:     user.Login,
		Type:      user.Type,
		SiteAdmin: user.SiteAdmin,
		Name:      user.Name,
		Company:   user.Company,
		Blog:      user.Blog,
		Location:  user.Location,
		Email:     user.Email,
		Bio:       user.Bio,
		CreatedAt: user.CreatedAt,
		UpdatedAt: user.UpdatedAt,
	}
}

func createEvaluatedRun(run *github.WorkflowRun) EvaluatedRun {
	// Sanitize the nested objects to remove dummy URLs
	run.Repository = sanitizeRepository(run.Repository)
	run.HeadRepository = sanitizeRepository(run.HeadRepository)
	run.Actor = sanitizeActor(run.Actor)
	run.TriggeringActor = sanitizeActor(run.TriggeringActor)

	return EvaluatedRun{
		WorkflowRun: run,
		Duration:    run.GetUpdatedAt().Sub(run.GetCreatedAt().Time),
	}
}

func getHealthFromConclusion(latestCompletedRun *github.WorkflowRun) health.HealthStatus {
	healthStatus := health.HealthStatus{
		Health: health.HealthUnknown,
		Ready:  true,
		Status: health.HealthStatusCode(latestCompletedRun.GetStatus()),
	}

	switch latestCompletedRun.GetConclusion() {
	case "success":
		healthStatus.Health = health.HealthHealthy
	case "failure":
		healthStatus.Health = health.HealthUnhealthy
		healthStatus.Status = health.HealthStatusCode(fmt.Sprintf("%s: %s", latestCompletedRun.GetConclusion(), latestCompletedRun.GetDisplayTitle()))
	case "timed_out":
		healthStatus.Health = health.HealthWarning
	default:
		healthStatus.Health = health.HealthUnknown
	}

	return healthStatus
}

// processRuns transforms runs to change results and returns the latest run that ran to completion
func processRuns(workflow *github.Workflow, runs []*github.WorkflowRun) ([]v1.ChangeResult, *github.WorkflowRun) {
	changeResults := make([]v1.ChangeResult, 0, len(runs))
	var latestCompletedRun *github.WorkflowRun
	for _, run := range runs {
		changeResults = append(changeResults, runToChangeResult(run, workflow))
		if run.GetStatus() == "completed" && (latestCompletedRun == nil || run.GetRunNumber() > latestCompletedRun.GetRunNumber()) {
			latestCompletedRun = run
		}
	}

	return changeResults, latestCompletedRun
}

func workflowProperties(workflow *github.Workflow) []*types.Property {
	properties := []*types.Property{}
	if workflow.GetBadgeURL() != "" {
		badgeProperty := &types.Property{
			Name: "Badge",
			Type: "badge",
			Text: workflow.GetBadgeURL(),
			Links: []types.Link{
				{
					URL:  workflow.GetBadgeURL(),
					Type: "badge",
				},
			},
		}

		properties = append(properties, badgeProperty)
	}

	if workflow.GetHTMLURL() != "" {
		workflowURLProperty := &types.Property{
			Name: "Source",
			Type: "url",
			Text: workflow.GetHTMLURL(),
			Links: []types.Link{
				{
					URL:  workflow.GetHTMLURL(),
					Type: "url",
				},
			},
		}
		properties = append(properties, workflowURLProperty)

		workflowURL, _ := getWorkflowURL(workflow.GetHTMLURL())
		properties = append(properties, &types.Property{
			Name: "URL",
			Type: "url",
			Text: workflowURL,
			Links: []types.Link{
				{
					URL:  workflowURL,
					Type: "url",
				},
			},
		})
	}

	return properties
}

// Transforms the source URL to the workflow URL
func getWorkflowURL(htmlURL string) (string, error) {
	parsed, err := url.Parse(htmlURL)
	if err != nil {
		return htmlURL, err
	}

	segments := strings.Split(parsed.EscapedPath(), "/")
	owner := segments[1]
	repo := segments[2]
	workflowPath := segments[len(segments)-1]
	path, err := url.JoinPath(owner, repo, "actions", "workflows", workflowPath)
	if err != nil {
		return htmlURL, err
	}

	return fmt.Sprintf("https://github.com/%s", path), nil
}
