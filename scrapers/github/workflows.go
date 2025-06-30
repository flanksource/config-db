package github

import (
	"fmt"
	"math"
	"sync"

	"github.com/flanksource/commons/collections"
	"github.com/flanksource/is-healthy/pkg/health"
	"github.com/samber/lo"
	"golang.org/x/sync/errgroup"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
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

		workflows, err := client.GetWorkflows()
		if err != nil {
			results.Errorf(err, "failed to get projects for %s", config.Repository)
			continue
		}

		for _, workflow := range workflows {
			if !collections.MatchItems(workflow.Name, config.Workflows...) {
				continue
			}

			runs, err := getNewWorkflowRuns(client, workflow)
			if err != nil {
				results.Errorf(err, "failed to get workflow runs for %s", workflow.GetID())
				continue
			}

			changeResults, latestCompletedRun := processRuns(workflow, runs)

			result := v1.ScrapeResult{
				ConfigClass: "Deployment",
				Config:      workflow,
				Type:        ConfigTypeWorkflow,
				ID:          workflow.GetID(),
				Name:        workflow.Name,
				Changes:     changeResults,
				Tags:        v1.Tags{{Name: "repository", Value: config.Repository}},
				Aliases:     []string{fmt.Sprintf("%s/%d", workflow.Name, workflow.ID)},
			}

			// The latest completed run determines the health of the workflow
			if latestCompletedRun.ID != 0 {
				result = result.WithHealthStatus(getHealthFromConclusion(latestCompletedRun))
			}

			results = append(results, result)
		}
	}

	return results
}

func getNewWorkflowRuns(client *GitHubActionsClient, workflow Workflow) ([]Run, error) {
	totalRunsInDB, err := db.GetWorkflowRunCount(client.ScrapeContext, workflow.GetID())
	if err != nil {
		return nil, err
	}

	// Get first page to determine total count
	firstPage, err := client.GetWorkflowRuns(workflow.ID, 1)
	if err != nil {
		return nil, err
	}

	delta := firstPage.Count - totalRunsInDB
	pagesToFetch := int(math.Ceil(float64(delta) / 100))
	if pagesToFetch == 0 {
		return firstPage.Value, nil
	}

	var g errgroup.Group
	g.SetLimit(client.ScrapeContext.Properties().Int("github.workflows.concurrency", DefaultConcurrency))

	var mu sync.Mutex
	var allRuns []Run
	for page := range pagesToFetch {
		pageNumber := page + 1
		if pageNumber == 1 {
			continue // Skip first page, it's already fetched
		}

		g.Go(func() error {
			client.ScrapeContext.Debugf("fetching workflow runs for page (workflow: %s, page: %d)", workflow.GetID(), pageNumber)
			pageRuns, err := client.GetWorkflowRuns(workflow.ID, pageNumber)
			if err != nil {
				return fmt.Errorf("failed to get workflow runs for page %d: %w", pageNumber, err)
			}

			mu.Lock()
			allRuns = append(allRuns, pageRuns.Value...)
			mu.Unlock()
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}

	return allRuns, nil
}

func runToChangeResult(run Run, workflow Workflow) v1.ChangeResult {
	summary := run.Status
	if run.Status == "completed" {
		duration := run.UpdatedAt.Sub(run.CreatedAt)
		run.DurationSeconds = int(duration.Seconds())
		summary = fmt.Sprintf("completed in %s", duration.String())
	}

	changeType := fmt.Sprintf("GitHubActionRun%s", lo.PascalCase(run.Conclusion))
	return v1.ChangeResult{
		ChangeType:       changeType,
		CreatedAt:        &run.CreatedAt,
		Severity:         run.Conclusion,
		ExternalID:       workflow.GetID(),
		ConfigType:       ConfigTypeWorkflow,
		Summary:          summary,
		Source:           run.TriggeringActor.Login,
		Details:          v1.NewJSON(run),
		ExternalChangeID: fmt.Sprintf("%s/%d/%d", workflow.Name, workflow.ID, run.ID),
	}
}

func getHealthFromConclusion(latestCompletedRun Run) health.HealthStatus {
	healthStatus := health.HealthStatus{
		Health: health.HealthUnknown,
		Ready:  true,
		Status: health.HealthStatusCode(latestCompletedRun.Status),
	}

	switch latestCompletedRun.Conclusion {
	case "success":
		healthStatus.Health = health.HealthHealthy
	case "failure":
		healthStatus.Health = health.HealthUnhealthy
		healthStatus.Status = health.HealthStatusCode(fmt.Sprintf("%s: %s", latestCompletedRun.Conclusion, latestCompletedRun.DisplayTitle))
	case "timed_out":
		healthStatus.Health = health.HealthWarning
	default:
		healthStatus.Health = health.HealthUnknown
	}

	return healthStatus
}

// processRuns transforms runs to change results and returns the latest run that ran to completion
func processRuns(workflow Workflow, runs []Run) ([]v1.ChangeResult, Run) {
	changeResults := make([]v1.ChangeResult, 0, len(runs))
	var latestCompletedRun Run
	for _, run := range runs {
		changeResults = append(changeResults, runToChangeResult(run, workflow))
		if run.Status == "completed" && run.RunNumber > latestCompletedRun.RunNumber {
			latestCompletedRun = run
		}
	}

	return changeResults, latestCompletedRun
}
