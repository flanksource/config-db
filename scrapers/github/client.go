package github

import (
	"context"
	"fmt"
	"time"

	"github.com/google/go-github/v73/github"
	"golang.org/x/oauth2"

	v1 "github.com/flanksource/config-db/api"
	"github.com/flanksource/config-db/pkg/api"
)

const defaultWorkflowRunMaxAge = 7 * 24 * time.Hour

type GitHubActionsClient struct {
	*github.Client
	api.ScrapeContext
	owner      string
	repository string
}

func NewGitHubActionsClient(ctx api.ScrapeContext, gha v1.GitHubActions) (*GitHubActionsClient, error) {
	var token string
	if connection, err := ctx.HydrateConnectionByURL(gha.ConnectionName); err != nil {
		return nil, err
	} else if connection != nil {
		token = connection.Password
	} else {
		token, err = ctx.GetEnvValueFromCache(gha.PersonalAccessToken, ctx.Namespace())
		if err != nil {
			return nil, err
		}
	}

	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)
	tc := oauth2.NewClient(ctx, ts)
	client := github.NewClient(tc)

	return &GitHubActionsClient{
		ScrapeContext: ctx,
		Client:        client,
		owner:         gha.Owner,
		repository:    gha.Repository,
	}, nil
}

func (gh *GitHubActionsClient) GetWorkflows(ctx context.Context) ([]*github.Workflow, error) {
	workflows, _, err := gh.Client.Actions.ListWorkflows(ctx, gh.owner, gh.repository, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get workflows: %w", err)
	}

	return workflows.Workflows, nil
}

func (gh *GitHubActionsClient) GetWorkflowRuns(ctx context.Context, config v1.GitHubActions, id, page int) (*github.WorkflowRuns, error) {
	duration := gh.ScrapeContext.Properties().Duration("scrapers.githubactions.maxAge", defaultWorkflowRunMaxAge)
	createdAfter := time.Now().Add(-duration)
	createdFilter := fmt.Sprintf(">=%s", createdAfter.Format("2006-01-02"))

	opts := &github.ListWorkflowRunsOptions{
		Status:  config.Status,
		Actor:   config.Actor,
		Branch:  config.Branch,
		Created: createdFilter,
		ListOptions: github.ListOptions{
			Page:    page,
			PerPage: 100,
		},
	}

	runs, _, err := gh.Client.Actions.ListWorkflowRunsByID(ctx, gh.owner, gh.repository, int64(id), opts)
	if err != nil {
		return nil, fmt.Errorf("failed to get workflow runs: %w", err)
	}

	return runs, nil
}

func (gh *GitHubActionsClient) GetAllWorkflowRuns(ctx context.Context) ([]*github.WorkflowRun, error) {
	runs, _, err := gh.Client.Actions.ListRepositoryWorkflowRuns(ctx, gh.owner, gh.repository, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get all workflow runs: %w", err)
	}

	return runs.WorkflowRuns, nil
}

// GetWorkflowRunAnnotations fetches annotations for a specific workflow run
func (gh *GitHubActionsClient) GetWorkflowRunAnnotations(ctx context.Context, runID int64) ([]*github.CheckRunAnnotation, error) {
	jobs, _, err := gh.Client.Actions.ListWorkflowJobs(ctx, gh.owner, gh.repository, runID, &github.ListWorkflowJobsOptions{
		ListOptions: github.ListOptions{
			PerPage: 100,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get workflow jobs: %w", err)
	}

	var allAnnotations []*github.CheckRunAnnotation
	for _, job := range jobs.Jobs {
		if job.GetConclusion() == "failure" {
			annotations, _, err := gh.Client.Checks.ListCheckRunAnnotations(ctx, gh.owner, gh.repository, job.GetID(), nil)
			if err != nil {
				return nil, fmt.Errorf("failed to get annotations for job %d: %w", job.GetID(), err)
			}

			allAnnotations = append(allAnnotations, annotations...)
		}
	}

	return allAnnotations, nil
}
