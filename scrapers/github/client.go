package github

import (
	"context"
	"errors"
	"fmt"
	gohttp "net/http"
	"os"
	"strings"
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

	// authenticated is true when this client was created with a GitHub token.
	// It lets user-owner selectors use /user/repos for private repos owned by
	// the authenticated user instead of the public-only /users/{user}/repos API.
	authenticated bool
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
		authenticated: token != "",
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

// isNotFound returns true if the error is a GitHub 404 response.
// A 404 typically means the feature (e.g. secret scanning, dependabot)
// is not enabled for the repository, which we treat as zero results.
func isNotFound(err error) bool {
	var errResp *github.ErrorResponse
	return errors.As(err, &errResp) && errResp.Response != nil && errResp.Response.StatusCode == gohttp.StatusNotFound
}

func (c *GitHubClient) GetDependabotAlerts(ctx context.Context, opts AlertListOptions) ([]*github.DependabotAlert, error) {
	var allAlerts []*github.DependabotAlert
	reqOpts := &github.ListAlertsOptions{
		ListCursorOptions: github.ListCursorOptions{First: opts.PerPage},
	}
	if opts.State != "" {
		reqOpts.State = &opts.State
	}
	if opts.Severity != "" {
		reqOpts.Severity = &opts.Severity
	}
	for {
		alerts, resp, err := c.Client.Dependabot.ListRepoAlerts(ctx, c.owner, c.repo, reqOpts)
		if err != nil {
			if isNotFound(err) || isDependabotAlertsDisabled(err) {
				return allAlerts, nil
			}
			return nil, fmt.Errorf("failed to fetch Dependabot alerts: %w", err)
		}
		allAlerts = append(allAlerts, alerts...)
		if resp.Cursor == "" {
			break
		}
		reqOpts.ListCursorOptions.Cursor = resp.Cursor
	}
	return allAlerts, nil
}

func (c *GitHubClient) GetDependabotAlertsEnabled(ctx context.Context) (bool, error) {
	enabled, _, err := c.Client.Repositories.GetVulnerabilityAlerts(ctx, c.owner, c.repo)
	if err != nil {
		return false, fmt.Errorf("failed to fetch Dependabot alerts status: %w", err)
	}
	return enabled, nil
}

func (c *GitHubClient) GetCodeScanningAlerts(ctx context.Context, opts AlertListOptions) ([]*github.Alert, error) {
	var allAlerts []*github.Alert
	reqOpts := &github.AlertListOptions{
		State:             opts.State,
		ListCursorOptions: github.ListCursorOptions{First: opts.PerPage},
	}
	if opts.Severity != "" {
		reqOpts.Severity = opts.Severity
	}
	for {
		alerts, resp, err := c.Client.CodeScanning.ListAlertsForRepo(ctx, c.owner, c.repo, reqOpts)
		if err != nil {
			if isNotFound(err) || isCodeScanningDisabled(err) {
				return allAlerts, nil
			}
			return nil, fmt.Errorf("failed to fetch code scanning alerts: %w", err)
		}
		allAlerts = append(allAlerts, alerts...)
		if resp.Cursor == "" {
			break
		}
		reqOpts.ListCursorOptions.Cursor = resp.Cursor
	}
	return allAlerts, nil
}

func (c *GitHubClient) IsCodeScanningConfigured(ctx context.Context) (bool, error) {
	opts := &github.AnalysesListOptions{ListOptions: github.ListOptions{PerPage: 1}}
	_, _, err := c.Client.CodeScanning.ListAnalysesForRepo(ctx, c.owner, c.repo, opts)
	if err != nil {
		if isNotFound(err) || isCodeScanningDisabled(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to fetch code scanning analyses: %w", err)
	}
	return true, nil
}

func isDependabotAlertsDisabled(err error) bool {
	msg, ok := forbiddenMessage(err)
	return ok && strings.Contains(msg, "dependabot alerts are disabled")
}

func isCodeScanningDisabled(err error) bool {
	msg, ok := forbiddenMessage(err)
	return ok && (strings.Contains(msg, "code security must be enabled") ||
		strings.Contains(msg, "code scanning is not enabled") ||
		strings.Contains(msg, "advanced security must be enabled"))
}

func isSecretScanningDisabled(err error) bool {
	msg, ok := forbiddenMessage(err)
	return ok && strings.Contains(msg, "secret scanning") &&
		(strings.Contains(msg, "not enabled") || strings.Contains(msg, "disabled"))
}

func forbiddenMessage(err error) (string, bool) {
	var errResp *github.ErrorResponse
	if !errors.As(err, &errResp) || errResp.Response == nil || errResp.Response.StatusCode != gohttp.StatusForbidden {
		return "", false
	}
	return strings.ToLower(errResp.Message), true
}

func (c *GitHubClient) GetSecretScanningAlerts(ctx context.Context, opts AlertListOptions) ([]*github.SecretScanningAlert, error) {
	var allAlerts []*github.SecretScanningAlert
	reqOpts := &github.SecretScanningAlertListOptions{
		State:             opts.State,
		ListCursorOptions: github.ListCursorOptions{First: opts.PerPage},
	}
	for {
		alerts, resp, err := c.Client.SecretScanning.ListAlertsForRepo(ctx, c.owner, c.repo, reqOpts)
		if err != nil {
			if isNotFound(err) || isSecretScanningDisabled(err) {
				return allAlerts, nil
			}
			return nil, fmt.Errorf("failed to fetch secret scanning alerts: %w", err)
		}
		allAlerts = append(allAlerts, alerts...)
		if resp.Cursor == "" {
			break
		}
		reqOpts.ListCursorOptions.Cursor = resp.Cursor
	}
	return allAlerts, nil
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

func (c *GitHubClient) ListRepositoriesForOwner(ctx context.Context, owner string) ([]*github.Repository, error) {
	ownerInfo, _, err := c.Client.Users.Get(ctx, owner)
	if err != nil {
		return nil, fmt.Errorf("failed to get GitHub owner %s: %w", owner, err)
	}

	if ownerInfo.GetType() == "Organization" {
		return c.ListOrganizationRepositories(ctx, owner)
	}

	if c.authenticated && c.isAuthenticatedOwner(ctx, owner) {
		return c.ListAuthenticatedOwnerRepositories(ctx)
	}

	return c.ListUserRepositories(ctx, owner)
}

func (c *GitHubClient) ListOrganizationRepositories(ctx context.Context, owner string) ([]*github.Repository, error) {
	var allRepos []*github.Repository
	opts := &github.RepositoryListByOrgOptions{
		Type:      "all",
		Sort:      "full_name",
		Direction: "asc",
		ListOptions: github.ListOptions{
			PerPage: 100,
		},
	}

	for {
		repos, resp, err := c.Client.Repositories.ListByOrg(ctx, owner, opts)
		if err != nil {
			return nil, fmt.Errorf("failed to list repositories for organization %s: %w", owner, err)
		}

		allRepos = append(allRepos, repos...)
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	return allRepos, nil
}

func (c *GitHubClient) ListUserRepositories(ctx context.Context, owner string) ([]*github.Repository, error) {
	var allRepos []*github.Repository
	opts := &github.RepositoryListByUserOptions{
		Type:      "owner",
		Sort:      "full_name",
		Direction: "asc",
		ListOptions: github.ListOptions{
			PerPage: 100,
		},
	}

	for {
		repos, resp, err := c.Client.Repositories.ListByUser(ctx, owner, opts)
		if err != nil {
			return nil, fmt.Errorf("failed to list repositories for user %s: %w", owner, err)
		}

		allRepos = append(allRepos, repos...)
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	return allRepos, nil
}

func (c *GitHubClient) ListAuthenticatedOwnerRepositories(ctx context.Context) ([]*github.Repository, error) {
	var allRepos []*github.Repository
	opts := &github.RepositoryListByAuthenticatedUserOptions{
		Visibility:  "all",
		Affiliation: "owner",
		Sort:        "full_name",
		Direction:   "asc",
		ListOptions: github.ListOptions{
			PerPage: 100,
		},
	}

	for {
		repos, resp, err := c.Client.Repositories.ListByAuthenticatedUser(ctx, opts)
		if err != nil {
			return nil, fmt.Errorf("failed to list repositories for authenticated user: %w", err)
		}

		allRepos = append(allRepos, repos...)
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	return allRepos, nil
}

func (c *GitHubClient) isAuthenticatedOwner(ctx context.Context, owner string) bool {
	user, _, err := c.Client.Users.Get(ctx, "")
	if err != nil {
		c.ScrapeContext.Logger.V(3).Infof("failed to get authenticated GitHub user: %v", err)
		return false
	}

	return strings.EqualFold(user.GetLogin(), owner)
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
