package github

import (
	"fmt"
	"time"

	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/go-resty/resty/v2"
)

// Workflow represents a gitHub actions workflow object.
// see https://docs.github.com/en/rest/actions/workflows?apiVersion=2022-11-28#get-a-workflow
type Workflow struct {
	ID        int               `json:"id"`
	NodeID    string            `json:"node_id"`
	Name      string            `json:"name"`
	Path      string            `json:"path"`
	State     string            `json:"state"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
	Url       string            `json:"url"`
	HtmlURL   string            `json:"html_url"`
	BadgeURL  string            `json:"badge_url"`
	Runs      []v1.ChangeResult `json:"-"`
}

// GetID returns a repeatable ID for a workflow with id / name
func (w Workflow) GetID() string { return fmt.Sprintf("%d/%s", w.ID, w.Name) }

// Workflows is a list of gitHub actions workflows
type Workflows struct {
	Count int        `json:"total_count"`
	Value []Workflow `json:"workflows"`
}

// Run is a gitHub actions workflow runs for a repository.
// see https://docs.github.com/en/rest/actions/workflow-runs?apiVersion=2022-11-28#list-workflow-runs-for-a-repository
type Run struct {
	ID                  int               `json:"id"`
	Name                string            `json:"name"`
	NodeID              string            `json:"node_id"`
	CheckSuiteID        int               `json:"check_suite_id"`
	CheckSuiteNodeID    string            `json:"check_suite_node_id"`
	HeadBranch          string            `json:"head_branch"`
	HeadSHA             string            `json:"head_sha"`
	Path                string            `json:"path"`
	RunNumber           int               `json:"run_number"`
	Event               string            `json:"event"`
	DisplayTitle        string            `json:"display_title"`
	Status              string            `json:"status"`
	Conclusion          any               `json:"conclusion"`
	WorkflowID          int               `json:"workflow_id"`
	URL                 string            `json:"url"`
	HtmlURL             string            `json:"html_url"`
	PullRequests        any               `json:"pull_requests"`
	CreatedAt           time.Time         `json:"created_at"`
	UpdatedAt           time.Time         `json:"updated_at"`
	Actor               map[string]string `json:"actor"`
	RunAttempt          int               `json:"run_attempt"`
	ReferencedWorkflows any               `json:"referenced_workflows"`
	RunStartedAt        time.Time         `json:"run_started_at"`
	TriggeringActor     map[string]string `json:"triggering_actor"`
	HeadCommit          map[string]string `json:"head_commit"`
	Repository          map[string]string `json:"repository"`
	HeadRepository      map[string]string `json:"head_repository"`
}

type Runs struct {
	Count int   `json:"total_count"`
	Value []Run `json:"workflow_runs"`
}

type GitHubActionsClient struct {
	*resty.Client
	*v1.ScrapeContext
}

func NewGitHubActionsClient(ctx *v1.ScrapeContext, gha v1.GitHubActions) (*GitHubActionsClient, error) {
	client := resty.New().
	 	SetHeader("Accept", "application/vnd.github+json").
		SetBaseURL(fmt.Sprintf("https://api.github.com/repos/%s/%s", gha.Owner, gha.Repository)).
		SetBasicAuth(gha.Owner, gha.PersonalAccessToken.Value)

	return &GitHubActionsClient{
		ScrapeContext: ctx,
		Client:        client,
	}, nil
}

func (gh *GitHubActionsClient) GetWorkflows() ([]Workflow, error) {
	var response Workflows
	_, err := gh.R().SetResult(&response).Get("/actions/workflows")
	if err != nil {
		return nil, err
	}

	return response.Value, nil
}

func (gh *GitHubActionsClient) GetWorkflowRuns(id int) ([]Run, error) {
	var response Runs
	_, err := gh.R().SetResult(&response).Get(fmt.Sprintf("/actions/runs/%v", id))
	if err != nil {
		return nil, err
	}

	return response.Value, nil
}

func (gh *GitHubActionsClient) GetAllWorkflowRuns() ([]Run, error) {
	var response Runs
	_, err := gh.R().SetResult(&response).Get("/actions/runs")
	if err != nil {
		return nil, err
	}

	return response.Value, nil
}
