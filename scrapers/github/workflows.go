package github

import (
	"fmt"
	"math"

	"github.com/flanksource/commons/collections"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
)

const ConfigTypeWorkflow = "GitHubAction::Workflow"

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
			results = append(results, v1.ScrapeResult{
				ConfigClass: "Deployment",
				Config:      workflow,
				Type:        ConfigTypeWorkflow,
				ID:          workflow.GetID(),
				Name:        workflow.Name,
				Changes:     runs,
				Aliases:     []string{fmt.Sprintf("%s/%d", workflow.Name, workflow.ID)},
			})
		}
	}
	return results
}

func getNewWorkflowRuns(client *GitHubActionsClient, workflow Workflow) ([]v1.ChangeResult, error) {
	runs, err := client.GetWorkflowRuns(workflow.ID, 1)
	if err != nil {
		return nil, err
	}

	var allRuns []v1.ChangeResult
	for _, run := range runs.Value {
		changeResult := runToChangeResult(run, workflow)
		allRuns = append(allRuns, changeResult)
	}

	// Get total runs from DB for that workflow
	totalRunsInDB, err := db.GetWorkflowRunCount(client.ScrapeContext, workflow.GetID())
	if err != nil {
		return nil, err
	}
	delta := runs.Count - totalRunsInDB
	pagesToFetch := int(math.Ceil(float64(delta) / 100))
	for page := 2; page <= pagesToFetch; page += 1 {
		runs, err := client.GetWorkflowRuns(workflow.ID, page)
		if err != nil {
			return nil, err
		}

		for _, run := range runs.Value {
			changeResult := runToChangeResult(run, workflow)
			allRuns = append(allRuns, changeResult)
		}
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

	return v1.ChangeResult{
		ChangeType:       "GitHubActionRun",
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
