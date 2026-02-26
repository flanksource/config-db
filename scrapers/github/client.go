package github

import (
	"context"
	"fmt"
	gohttp "net/http"
	"os"
	"time"

	"github.com/google/go-github/v73/github"
	"golang.org/x/oauth2"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
)

const defaultWorkflowRunMaxAge = 7 * 24 * time.Hour

// GitHubClient wraps the GitHub client for the unified GitHub scraper
type GitHubClient struct {
	*github.Client
	api.ScrapeContext
	owner string
	repo  string
}

func NewGitHubClient(ctx api.ScrapeContext, config v1.GitHub, owner, repo string) (*GitHubClient, error) {
	var token string
	if connection, err := ctx.HydrateConnectionByURL(config.ConnectionName); err != nil {
		return nil, fmt.Errorf("failed to hydrate connection: %w", err)
	} else if connection != nil {
		token = connection.Password
	} else if !config.PersonalAccessToken.IsEmpty() {
		var err error
		token, err = ctx.GetEnvValueFromCache(config.PersonalAccessToken, ctx.Namespace())
		if err != nil {
			return nil, fmt.Errorf("failed to get token: %w", err)
		}
	}

	if token == "" {
		token = os.Getenv("GITHUB_TOKEN")
		if token == "" {
			token = os.Getenv("GH_TOKEN")
		}
	}

	var tc *gohttp.Client
	if token != "" {
		ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
		tc = oauth2.NewClient(ctx, ts)
	}
	client := github.NewClient(tc)

	return &GitHubClient{
		ScrapeContext: ctx,
		Client:        client,
		owner:         owner,
		repo:          repo,
	}, nil
}

// AlertListOptions contains options for listing security alerts
type AlertListOptions struct {
	State     string
	Severity  string
	Page      int
	PerPage   int
	CreatedAt string
}

func (c *GitHubClient) GetDependabotAlerts(ctx context.Context, opts AlertListOptions) ([]*github.DependabotAlert, *github.Response, error) {
	reqOpts := &github.ListAlertsOptions{
		ListCursorOptions: github.ListCursorOptions{First: opts.PerPage},
	}
	if opts.State != "" {
		reqOpts.State = &opts.State
	}
	if opts.Severity != "" {
		reqOpts.Severity = &opts.Severity
	}
	alerts, resp, err := c.Client.Dependabot.ListRepoAlerts(ctx, c.owner, c.repo, reqOpts)
	if err != nil {
		return nil, resp, fmt.Errorf("failed to fetch Dependabot alerts: %w", err)
	}
	return alerts, resp, nil
}

func (c *GitHubClient) GetCodeScanningAlerts(ctx context.Context, opts AlertListOptions) ([]*github.Alert, *github.Response, error) {
	reqOpts := &github.AlertListOptions{
		State:       opts.State,
		ListOptions: github.ListOptions{Page: opts.Page, PerPage: opts.PerPage},
	}
	if opts.Severity != "" {
		reqOpts.Severity = opts.Severity
	}
	alerts, resp, err := c.Client.CodeScanning.ListAlertsForRepo(ctx, c.owner, c.repo, reqOpts)
	if err != nil {
		return nil, resp, fmt.Errorf("failed to fetch code scanning alerts: %w", err)
	}
	return alerts, resp, nil
}

func (c *GitHubClient) GetSecretScanningAlerts(ctx context.Context, opts AlertListOptions) ([]*github.SecretScanningAlert, *github.Response, error) {
	reqOpts := &github.SecretScanningAlertListOptions{
		State:       opts.State,
		ListOptions: github.ListOptions{Page: opts.Page, PerPage: opts.PerPage},
	}
	alerts, resp, err := c.Client.SecretScanning.ListAlertsForRepo(ctx, c.owner, c.repo, reqOpts)
	if err != nil {
		return nil, resp, fmt.Errorf("failed to fetch secret scanning alerts: %w", err)
	}
	return alerts, resp, nil
}

func (c *GitHubClient) ShouldPauseForRateLimit(ctx context.Context) (bool, time.Duration, error) {
	rateLimits, _, err := c.Client.RateLimit.Get(ctx)
	if err != nil {
		return false, 0, err
	}

	c.ScrapeContext.Logger.V(3).Infof("GitHub rate limit: remaining=%d, limit=%d",
		rateLimits.Core.Remaining, rateLimits.Core.Limit)

	const threshold = 100
	if rateLimits.Core.Remaining < threshold {
		waitDuration := time.Until(rateLimits.Core.Reset.Time)
		return true, waitDuration, nil
	}

	return false, 0, nil
}

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
