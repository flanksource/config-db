package github

import (
	"fmt"
	"math"
	"net/url"
	"strings"
	"sync"
	"time"

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

			changeResults, latestCompletedRun, err := processRuns(ctx, client, workflow, runs)
			if err != nil {
				results.Errorf(err, "failed to process runs with annotations for %s", workflow.GetName())
				continue
			}

			result := v1.ScrapeResult{
				ConfigClass: "Deployment",
				Config:      workflow,
				Type:        ConfigTypeWorkflow,
				ID:          fmt.Sprintf("%d/%s", workflow.GetID(), workflow.GetName()),
				Name:        fmt.Sprintf("%s/%s", config.Repository, workflow.GetName()),
				Changes:     changeResults,
				Tags: map[string]string{
					"org": config.Owner,
				},
				Aliases:    []string{fmt.Sprintf("%s/%d", workflow.GetName(), workflow.GetID())},
				CreatedAt:  lo.ToPtr(workflow.GetCreatedAt().Time),
				Properties: workflowProperties(workflow),
			}

			// The latest completed run determines the health of the workflow
			if latestCompletedRun != nil && latestCompletedRun.GetID() != 0 {
				result = result.WithHealthStatus(getHealthFromConclusion(latestCompletedRun))
			}

			results = append(results, result)
		}

		rateLimitInfo, _, err := client.Client.RateLimit.Get(ctx)
		if err != nil {
			results.Errorf(err, "failed to get rate limit info for %s", config.Repository)
			continue
		}

		ctx.Logger.V(2).Infof("github rate limit: limit=%d, remaining=%d, used=%d, reset=%s",
			rateLimitInfo.Core.Limit,
			rateLimitInfo.Core.Remaining,
			rateLimitInfo.Core.Used,
			rateLimitInfo.Core.Reset.Format(time.RFC3339),
		)
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

func runToChangeResult(workflow *github.Workflow, run Run) v1.ChangeResult {
	changeType := fmt.Sprintf("GitHubActionRun%s", lo.PascalCase(run.GetConclusion()))
	createdAt := run.GetCreatedAt().Time

	return v1.ChangeResult{
		ChangeType:       changeType,
		CreatedAt:        &createdAt,
		Severity:         run.GetConclusion(),
		ExternalID:       fmt.Sprintf("%d/%s", workflow.GetID(), workflow.GetName()),
		ConfigType:       ConfigTypeWorkflow,
		Summary:          run.Summary(),
		Source:           run.GetTriggeringActor().GetLogin(),
		Details:          v1.NewJSON(run),
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

// Run is a wrapper around github.WorkflowRun that adds duration and annotations
// and removes placeholder URLs.
type Run struct {
	*github.WorkflowRun `json:",inline"`
	Duration            time.Duration                `json:"duration"`
	Annotations         []*github.CheckRunAnnotation `json:"annotations,omitempty"`
}

func (run Run) Summary() string {
	summary := fmt.Sprintf("%s (branch: %s)", run.GetDisplayTitle(), run.GetHeadBranch())

	if run.GetStatus() == "completed" {
		duration := run.GetUpdatedAt().Sub(run.GetCreatedAt().Time)
		summary = fmt.Sprintf("%s; duration: %s", summary, duration.String())
	}

	if len(run.Annotations) > 0 {
		annotationMsg := lo.Map(run.Annotations, func(annotation *github.CheckRunAnnotation, _ int) string {
			return fmt.Sprintf("%s: %s", annotation.GetAnnotationLevel(), annotation.GetMessage())
		})

		summary = strings.Join(annotationMsg, "; ")
	}

	return summary
}

func newRun(run *github.WorkflowRun, annotations ...*github.CheckRunAnnotation) Run {
	// Sanitize the nested objects to remove dummy URLs
	run.Repository = sanitizeRepository(run.Repository)
	run.HeadRepository = sanitizeRepository(run.HeadRepository)
	run.Actor = sanitizeActor(run.Actor)
	run.TriggeringActor = sanitizeActor(run.TriggeringActor)

	return Run{
		WorkflowRun: run,
		Annotations: annotations,
		Duration:    run.GetUpdatedAt().Sub(run.GetCreatedAt().Time),
	}
}

func getHealthFromConclusion(latestCompletedRun *Run) health.HealthStatus {
	healthStatus := health.HealthStatus{
		Health: health.HealthUnknown,
		Ready:  true,
		Status: health.HealthStatusCode(lo.PascalCase(latestCompletedRun.GetConclusion())),
	}

	switch latestCompletedRun.GetConclusion() {
	case "success":
		healthStatus.Health = health.HealthHealthy
	case "failure":
		healthStatus.Health = health.HealthUnhealthy
		healthStatus.Message = latestCompletedRun.Summary()
	case "timed_out":
		healthStatus.Health = health.HealthWarning
	default:
		healthStatus.Health = health.HealthUnknown
	}

	return healthStatus
}

// processRuns transforms runs to change results with annotations for failed runs
func processRuns(ctx api.ScrapeContext, client *GitHubActionsClient, workflow *github.Workflow, runs []*github.WorkflowRun) ([]v1.ChangeResult, *Run, error) {
	changeResults := make([]v1.ChangeResult, 0, len(runs))
	var latestCompletedRun *Run
	for _, run := range runs {
		var annotations []*github.CheckRunAnnotation

		// For failed runs, try to get annotations for more details
		if run.GetConclusion() == "failure" {
			var err error
			annotations, err = client.GetWorkflowRunAnnotations(ctx, run.GetID())
			if err != nil {
				return nil, nil, fmt.Errorf("failed to get annotations for run %d: %w", run.GetID(), err)
			}
		}

		evaluatedRun := newRun(run, annotations...)
		changeResult := runToChangeResult(workflow, evaluatedRun)
		changeResults = append(changeResults, changeResult)
		if run.GetStatus() == "completed" && (latestCompletedRun == nil || run.GetRunNumber() > latestCompletedRun.GetRunNumber()) {
			latestCompletedRun = &evaluatedRun
		}
	}

	return changeResults, latestCompletedRun, nil
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

		workflowURL, err := getWorkflowURL(workflow.GetHTMLURL())
		if err == nil {
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
	joinedPath, err := url.JoinPath(owner, repo, "actions", "workflows", workflowPath)
	if err != nil {
		return htmlURL, err
	}

	return fmt.Sprintf("https://github.com/%s", joinedPath), nil
}
