package github

import (
	"context"
	"fmt"
	"time"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/google/go-github/v73/github"
	"golang.org/x/oauth2"
)

// GitHubSecurityClient wraps the GitHub client for security-related operations
type GitHubSecurityClient struct {
	*github.Client
	api.ScrapeContext
	owner string
	repo  string
}

// NewGitHubSecurityClient creates a new GitHub Security API client
func NewGitHubSecurityClient(ctx api.ScrapeContext, config v1.GitHubSecurity, owner, repo string) (*GitHubSecurityClient, error) {
	var token string
	if connection, err := ctx.HydrateConnectionByURL(config.ConnectionName); err != nil {
		return nil, fmt.Errorf("failed to hydrate connection: %w", err)
	} else if connection != nil {
		token = connection.Password
	} else {
		token, err = ctx.GetEnvValueFromCache(config.PersonalAccessToken, ctx.Namespace())
		if err != nil {
			return nil, fmt.Errorf("failed to get token: %w", err)
		}
	}

	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	)
	tc := oauth2.NewClient(ctx, ts)
	client := github.NewClient(tc)

	return &GitHubSecurityClient{
		ScrapeContext: ctx,
		Client:        client,
		owner:         owner,
		repo:          repo,
	}, nil
}

// AlertListOptions contains options for listing alerts with filtering
type AlertListOptions struct {
	State      string
	Severity   string
	Page       int
	PerPage    int
	CreatedAt  string // For time-based filtering
}

// GetDependabotAlerts fetches Dependabot alerts for the repository
func (c *GitHubSecurityClient) GetDependabotAlerts(ctx context.Context, opts AlertListOptions) ([]*github.DependabotAlert, *github.Response, error) {
	reqOpts := &github.ListAlertsOptions{
		State: &opts.State,
		ListOptions: github.ListOptions{
			Page:    opts.Page,
			PerPage: opts.PerPage,
		},
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

// GetCodeScanningAlerts fetches code scanning alerts for the repository
func (c *GitHubSecurityClient) GetCodeScanningAlerts(ctx context.Context, opts AlertListOptions) ([]*github.Alert, *github.Response, error) {
	reqOpts := &github.AlertListOptions{
		State: opts.State,
		ListOptions: github.ListOptions{
			Page:    opts.Page,
			PerPage: opts.PerPage,
		},
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

// GetSecretScanningAlerts fetches secret scanning alerts for the repository
func (c *GitHubSecurityClient) GetSecretScanningAlerts(ctx context.Context, opts AlertListOptions) ([]*github.SecretScanningAlert, *github.Response, error) {
	reqOpts := &github.SecretScanningAlertListOptions{
		State: opts.State,
		ListOptions: github.ListOptions{
			Page:    opts.Page,
			PerPage: opts.PerPage,
		},
	}

	alerts, resp, err := c.Client.SecretScanning.ListAlertsForRepo(ctx, c.owner, c.repo, reqOpts)
	if err != nil {
		return nil, resp, fmt.Errorf("failed to fetch secret scanning alerts: %w", err)
	}

	return alerts, resp, nil
}

// GetSecurityAdvisories fetches repository security advisories
func (c *GitHubSecurityClient) GetSecurityAdvisories(ctx context.Context, opts AlertListOptions) ([]*github.SecurityAdvisory, *github.Response, error) {
	reqOpts := &github.ListRepositorySecurityAdvisoriesOptions{
		State: opts.State,
	}

	advisories, resp, err := c.Client.SecurityAdvisories.ListRepositorySecurityAdvisories(ctx, c.owner, c.repo, reqOpts)
	if err != nil {
		return nil, resp, fmt.Errorf("failed to fetch security advisories: %w", err)
	}

	return advisories, resp, nil
}

// CheckRateLimit checks the current rate limit status
func (c *GitHubSecurityClient) CheckRateLimit(ctx context.Context) (*github.RateLimits, error) {
	rateLimits, _, err := c.Client.RateLimit.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get rate limit: %w", err)
	}
	return rateLimits, nil
}

// ShouldPauseForRateLimit determines if we should pause due to rate limiting
func (c *GitHubSecurityClient) ShouldPauseForRateLimit(ctx context.Context) (bool, time.Duration, error) {
	rateLimits, err := c.CheckRateLimit(ctx)
	if err != nil {
		return false, 0, err
	}

	c.ScrapeContext.Logger.V(3).Infof("GitHub rate limit: remaining=%d, limit=%d, reset=%s",
		rateLimits.Core.Remaining, rateLimits.Core.Limit, rateLimits.Core.Reset.Format(time.RFC3339))

	const threshold = 100
	if rateLimits.Core.Remaining < threshold {
		resetTime := rateLimits.Core.Reset.Time
		waitDuration := time.Until(resetTime)
		c.ScrapeContext.Warnf("approaching rate limit: %d remaining, reset at %v (in %v)",
			rateLimits.Core.Remaining, resetTime, waitDuration)
		return true, waitDuration, nil
	}

	return false, 0, nil
}
