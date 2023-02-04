package github

import (
	"fmt"

	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/utils"
)

const WorkflowRun = "GitHubActions::WorkflowRun"

type GithubActionsScraper struct {
}

// Scrape fetches github workflows and workflow runs from github API and converts the action executions (workflow runs) to change events.
func (gh GithubActionsScraper) Scrape(ctx *v1.ScrapeContext, configs v1.ConfigScraper) v1.ScrapeResults {
	results := v1.ScrapeResults{}
	for _, config := range configs.GithubActions {
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
			if !utils.MatchItems(workflow.Name, config.Workflows...) {
				continue
			}
			fmt.Printf("%v\n", workflow.Name)
			var uniqueWorkflows = make(map[string]Workflow)
			fmt.Printf("\t->%v\n", workflow.Name)

			runs, err := client.GetWorkflowRuns(workflow.ID)
			if err != nil {
				results.Errorf(err, "failed to get workflow runs for %d/%s", workflow.ID, workflow.Name)
				continue
			}
			var id string
			var _workflow Workflow
			for _, run := range runs {
				id = workflow.GetID()
				if _, ok := uniqueWorkflows[id]; !ok {
					uniqueWorkflows[id] = workflow
				} else {
					_workflow = uniqueWorkflows[id]
				}
				workflow.Runs = append(workflow.Runs, v1.ChangeResult{
					ChangeType:       "Dispatch",
					CreatedAt:        &run.CreatedAt,
					Severity:         run.Conclusion.(string),
					ExternalID:       id,
					ExternalType:     WorkflowRun,
					Source:           run.Event,
					Details:          v1.NewJSON(run),
					ExternalChangeID: fmt.Sprintf("%s/%d/%d", workflow.Name, workflow.ID, run.ID),
				})
				uniqueWorkflows[id] = _workflow
			}

			for id, workflow := range uniqueWorkflows {
				results = append(results, v1.ScrapeResult{
					Type:         "Deployment",
					Config:       workflow,
					ExternalType: WorkflowRun,
					ID:           id,
					Name:         workflow.Name,
					Changes:      workflow.Runs,
					Aliases:      []string{fmt.Sprintf("%s/%d", workflow.Name, workflow.ID)},
				})
			}
		}
	}
	return results
}
