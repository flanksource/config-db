package devops

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	commonsHTTP "github.com/flanksource/commons/http"
	"github.com/flanksource/commons/logger"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty/connection"
	"github.com/flanksource/duty/types"
)

type Project struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	URL            string    `json:"url"`
	State          string    `json:"state"`
	Revision       int       `json:"revision"`
	Visibility     string    `json:"visibility"`
	LastUpdateTime time.Time `json:"lastUpdateTime"`
}

type Projects struct {
	Count int       `json:"count"`
	Value []Project `json:"value"`
}

type Link struct {
	Href string `json:"href"`
}

type Pipeline struct {
	Links              map[string]Link     `json:"_links,omitempty"`
	URL                string              `json:"url"`
	ID                 int                 `json:"id"`
	Revision           int                 `json:"revision"`
	Name               string              `json:"name"`
	Folder             string              `json:"folder"`
	Variables          map[string]Variable `json:"variables,omitempty"`
	TemplateParameters map[string]any      `json:"templateParameters,omitempty"`
	Configuration      *PipelineConfig     `json:"configuration,omitempty"`
	Runs               []v1.ChangeResult   `json:"-"`
}

type PipelineConfig struct {
	Type       string      `json:"type,omitempty"`
	Path       string      `json:"path,omitempty"`
	Repository *Repository `json:"repository,omitempty"`
}

type Repository struct {
	ID            string `json:"id,omitempty"`
	Name          string `json:"name,omitempty"`
	Type          string `json:"type,omitempty"`
	URL           string `json:"url,omitempty"`
	DefaultBranch string `json:"defaultBranch,omitempty"`
}

type GitRepository struct {
	ID            string      `json:"id"`
	Name          string      `json:"name"`
	URL           string      `json:"url"`
	RemoteURL     string      `json:"remoteUrl"`
	SSHURL        string      `json:"sshUrl"`
	WebURL        string      `json:"webUrl"`
	DefaultBranch string      `json:"defaultBranch"`
	Size          int64       `json:"size"`
	IsDisabled    bool        `json:"isDisabled"`
	IsFork        bool        `json:"isFork"`
	Project       *ProjectRef `json:"project,omitempty"`
}

type ProjectRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type GitRepositories struct {
	Count int             `json:"count"`
	Value []GitRepository `json:"value"`
}

func (p Pipeline) GetLabels() map[string]string {
	return map[string]string{}
}

// GetID returns a stable ID for a pipeline, independent of revision.
// Only the volatile ?revision=N param is stripped; ?definitionId=N is preserved
// since it distinguishes pipelines within the same project.
func (p Pipeline) GetID() string {
	href := ""
	if web, ok := p.Links["web"]; ok && web.Href != "" {
		href = web.Href
	} else {
		href = p.URL
	}
	u, err := url.Parse(href)
	if err != nil {
		return href
	}
	q := u.Query()
	q.Del("revision")
	u.RawQuery = q.Encode()
	return u.String()
}

type Pipelines struct {
	Count int        `json:"count"`
	Value []Pipeline `json:"value"`
}

type Variable struct {
	Value string `json:"value"`
}

type Run struct {
	Links              map[string]Link     `json:"_links,omitempty"`
	Variables          map[string]Variable `json:"variables,omitempty"`
	TemplateParameters map[string]any      `json:"templateParameters,omitempty"`
	State              string              `json:"state"`
	Result             string              `json:"result"`
	CreatedDate        time.Time           `json:"createdDate"`
	FinishedDate       time.Time           `json:"finishedDate"`
	//Duration in milliseconds
	Duration int    `json:"duration"`
	URL      string `json:"url"`
	ID       int    `json:"id"`
	Name     string `json:"name"`
	// Resources contains the repositories and other resources used by the run
	Resources *RunResources `json:"resources,omitempty"`
}

type RunResources struct {
	Repositories map[string]RunRepository `json:"repositories,omitempty"`
	Pipelines    map[string]RunPipeline   `json:"pipelines,omitempty"`
}

type RunRepository struct {
	RefName string `json:"refName,omitempty"`
	Version string `json:"version,omitempty"`
}

type RunPipeline struct {
	ID      int    `json:"id,omitempty"`
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
}

func (r Run) GetTags() map[string]string {
	var tags = map[string]string{}
	for k, v := range r.TemplateParameters {
		tags[k] = fmt.Sprintf("%v", v)
	}
	for k, v := range r.Variables {
		tags[k] = v.Value
	}
	return tags
}

type Runs struct {
	Count int   `json:"count"`
	Value []Run `json:"value"`
}

type AzureDevopsClient struct {
	*commonsHTTP.Client
	api.ScrapeContext
	Organization string
	token        string
}

// resolveOrgAndToken extracts the organization and personal access token from
// a connection or the AzureDevops config. This is shared by both the main
// and release client constructors to avoid duplicating auth/org bootstrap logic.
func resolveOrgAndToken(ctx api.ScrapeContext, ado *v1.AzureDevops) (org, token string, err error) {
	if connection, connErr := ctx.HydrateConnectionByURL(ado.ConnectionName); connErr != nil {
		return "", "", fmt.Errorf("failed to find connection: %w", connErr)
	} else if connection != nil {
		token = connection.Password
		ado.Organization = connection.Username
	} else {
		token, err = ctx.GetEnvValueFromCache(ado.PersonalAccessToken, ctx.GetNamespace())
		if err != nil {
			return "", "", err
		}
	}
	return ado.Organization, token, nil
}

func NewAzureDevopsClient(ctx api.ScrapeContext, ado v1.AzureDevops) (*AzureDevopsClient, error) {
	org, token, err := resolveOrgAndToken(ctx, &ado)
	if err != nil {
		return nil, err
	}

	client, err := newADOHTTPClient(ctx, fmt.Sprintf("https://dev.azure.com/%s", org), org, token)
	if err != nil {
		return nil, err
	}

	return &AzureDevopsClient{
		ScrapeContext: ctx,
		Client:        client,
		Organization:  org,
		token:         token,
	}, nil
}

// newADOHTTPClient builds a commons HTTP client for Azure DevOps endpoints,
// wiring authentication and feature-aware observability through duty's
// connection layer.
func newADOHTTPClient(ctx api.ScrapeContext, baseURL, org, token string) (*commonsHTTP.Client, error) {
	conn := connection.HTTPConnection{
		HTTPBasicAuth: types.HTTPBasicAuth{
			Authentication: types.Authentication{
				Username: types.EnvVar{ValueStatic: org},
				Password: types.EnvVar{ValueStatic: token},
			},
		},
	}
	client, err := connection.CreateHTTPClient(ctx, conn, types.WithFeature("azure.devops"))
	if err != nil {
		return nil, err
	}
	client.BaseURL(baseURL)
	return client, nil
}

// get is a convenience wrapper that performs a GET request and unmarshals the JSON response.
func get[T any](client *commonsHTTP.Client, ctx context.Context, path string, params ...string) (*T, *commonsHTTP.Response, error) {
	req := client.R(ctx)
	for i := 0; i+1 < len(params); i += 2 {
		req = req.QueryParam(params[i], params[i+1])
	}
	resp, err := req.Get(path)
	if err != nil {
		return nil, resp, err
	}
	if !resp.IsOK() {
		body, _ := resp.AsString()
		return nil, resp, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, body)
	}
	var result T
	if err := resp.Into(&result); err != nil {
		return nil, resp, fmt.Errorf("failed to decode response: %w", err)
	}
	return &result, resp, nil
}

func (ado *AzureDevopsClient) GetPipelines(ctx context.Context, project string) ([]Pipeline, error) {
	response, _, err := get[Pipelines](ado.Client, ctx, fmt.Sprintf("/%s/_apis/pipelines", project))
	if err != nil {
		return nil, err
	}

	pipelines := response.Value
	for _, pipeline := range pipelines {
		pipeline.Folder = strings.TrimPrefix(pipeline.Folder, "/")
		pipeline.Folder = strings.TrimPrefix(pipeline.Folder, "\\")
	}

	return pipelines, nil
}

func (ado *AzureDevopsClient) GetPipelineRuns(ctx context.Context, project string, pipeline Pipeline) ([]Run, error) {
	runs, _, err := get[Runs](ado.Client, ctx, fmt.Sprintf("/%s/_apis/pipelines/%d/runs", project, pipeline.ID))
	if err != nil {
		return nil, err
	}
	var results []Run
	for _, run := range runs.Value {
		if !run.FinishedDate.IsZero() {
			run.Duration = int(run.FinishedDate.Sub(run.CreatedDate).Milliseconds())
		}
		results = append(results, run)
	}
	return results, nil
}

func (ado *AzureDevopsClient) GetProjects(ctx context.Context) ([]Project, error) {
	projects, _, err := get[Projects](ado.Client, ctx, "/_apis/projects")
	if err != nil {
		return nil, err
	}
	return projects.Value, nil
}

func (ado *AzureDevopsClient) GetRepositories(ctx context.Context, project string) ([]GitRepository, error) {
	repos, _, err := get[GitRepositories](ado.Client, ctx, fmt.Sprintf("/%s/_apis/git/repositories", project), "api-version", "7.1")
	if err != nil {
		return nil, err
	}
	return repos.Value, nil
}

// Timeline represents the timeline of a build
type Timeline struct {
	Records []TimelineRecord `json:"records"`
	ID      string           `json:"id"`
	URL     string           `json:"url"`
}

// TimelineRecord represents an entry in a build's timeline
type TimelineRecord struct {
	ID           string          `json:"id"`
	ParentID     string          `json:"parentId,omitempty"`
	Type         string          `json:"type"`
	Name         string          `json:"name"`
	StartTime    *time.Time      `json:"startTime,omitempty"`
	FinishTime   *time.Time      `json:"finishTime,omitempty"`
	State        string          `json:"state"`
	Result       string          `json:"result,omitempty"`
	Order        int             `json:"order"`
	ErrorCount   int             `json:"errorCount"`
	WarningCount int             `json:"warningCount"`
	Log          *LogReference   `json:"log,omitempty"`
	Task         *TaskReference  `json:"task,omitempty"`
	Issues       []TimelineIssue `json:"issues,omitempty"`
	WorkerName   string          `json:"workerName,omitempty"`
	Identifier   string          `json:"identifier,omitempty"`
}

type LogReference struct {
	ID   int    `json:"id"`
	Type string `json:"type"`
	URL  string `json:"url"`
}

type TaskReference struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version"`
}

type TimelineIssue struct {
	Type     string `json:"type"`
	Category string `json:"category"`
	Message  string `json:"message"`
}

// JobStepSummary is a simplified representation of a job step for change details
type JobStepSummary struct {
	Name         string     `json:"name"`
	Type         string     `json:"type"`
	State        string     `json:"state"`
	Result       string     `json:"result,omitempty"`
	StartTime    *time.Time `json:"startTime,omitempty"`
	FinishTime   *time.Time `json:"finishTime,omitempty"`
	Duration     string     `json:"duration,omitempty"`
	ErrorCount   int        `json:"errorCount,omitempty"`
	WarningCount int        `json:"warningCount,omitempty"`
	LogURL       string     `json:"logUrl,omitempty"`
	WorkerName   string     `json:"workerName,omitempty"`
}

// BuildDefinition represents a build/pipeline definition with YAML configuration
type BuildDefinition struct {
	ID         int                                `json:"id"`
	Name       string                             `json:"name"`
	URL        string                             `json:"url"`
	Path       string                             `json:"path"`
	Revision   int                                `json:"revision"`
	Repository *BuildRepository                   `json:"repository,omitempty"`
	Process    *BuildProcess                      `json:"process,omitempty"`
	Variables  map[string]BuildDefinitionVariable `json:"variables,omitempty"`
}

type BuildRepository struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Type          string `json:"type"`
	URL           string `json:"url"`
	DefaultBranch string `json:"defaultBranch"`
}

type BuildProcess struct {
	Type     int    `json:"type"`
	YamlPath string `json:"yamlFilename,omitempty"`
}

type BuildDefinitionVariable struct {
	Value         string `json:"value"`
	IsSecret      bool   `json:"isSecret"`
	AllowOverride bool   `json:"allowOverride"`
}

// PipelineDefinition is the enriched pipeline with YAML definition
type PipelineDefinition struct {
	Pipeline
	YamlPath       string `json:"yamlPath,omitempty"`
	YamlContent    string `json:"yamlContent,omitempty"`
	RepositoryName string `json:"repositoryName,omitempty"`
	RepositoryURL  string `json:"repositoryUrl,omitempty"`
	DefaultBranch  string `json:"defaultBranch,omitempty"`
}

// GetPipelineWithDefinition fetches a pipeline with its build definition details
func (ado *AzureDevopsClient) GetPipelineWithDefinition(ctx context.Context, project string, pipelineID int) (*PipelineDefinition, error) {
	pipeline, _, err := get[Pipeline](ado.Client, ctx, fmt.Sprintf("/%s/_apis/pipelines/%d", project, pipelineID))
	if err != nil {
		return nil, fmt.Errorf("failed to get pipeline: %w", err)
	}

	definition, _, err := get[BuildDefinition](ado.Client, ctx, fmt.Sprintf("/%s/_apis/build/definitions/%d", project, pipelineID))
	if err != nil {
		return nil, fmt.Errorf("failed to get build definition: %w", err)
	}

	pipelineDef := &PipelineDefinition{
		Pipeline: *pipeline,
	}

	if definition.Process != nil {
		pipelineDef.YamlPath = definition.Process.YamlPath
	}

	if definition.Repository != nil {
		pipelineDef.RepositoryName = definition.Repository.Name
		pipelineDef.RepositoryURL = definition.Repository.URL
		pipelineDef.DefaultBranch = definition.Repository.DefaultBranch

		if pipelineDef.YamlPath != "" && definition.Repository.ID != "" {
			content, err := ado.GetRepositoryFile(ctx, project, definition.Repository.ID, pipelineDef.YamlPath, definition.Repository.DefaultBranch)
			if err != nil {
				logger.Warnf("failed to fetch pipeline YAML %s: %v", pipelineDef.YamlPath, err)
			} else {
				pipelineDef.YamlContent = content
			}
		}
	}

	return pipelineDef, nil
}

// GetRepositoryFile fetches a file's content from an Azure DevOps Git repository.
// It requests raw text via Accept: text/plain so the response body is the file content directly.
func (ado *AzureDevopsClient) GetRepositoryFile(ctx context.Context, project, repoID, path, branch string) (string, error) {
	resp, err := ado.Client.R(ctx).
		Header("Accept", "text/plain").
		QueryParam("path", path).
		QueryParam("includeContent", "true").
		QueryParam("recursionLevel", "0").
		QueryParam("versionDescriptor.version", strings.TrimPrefix(branch, "refs/heads/")).
		QueryParam("versionDescriptor.versionOptions", "0").
		QueryParam("versionDescriptor.versionType", "0").
		Get(fmt.Sprintf("/%s/_apis/git/repositories/%s/Items", project, repoID))
	if err != nil {
		return "", fmt.Errorf("failed to get repository file: %w", err)
	}
	if !resp.IsOK() {
		body, _ := resp.AsString()
		return "", fmt.Errorf("unexpected status %d: %s", resp.StatusCode, body)
	}
	return resp.AsString()
}

// GetBuildTimeline gets the timeline/steps for a specific build
func (ado *AzureDevopsClient) GetBuildTimeline(ctx context.Context, project string, buildID int) (*Timeline, error) {
	timeline, resp, err := get[Timeline](ado.Client, ctx, fmt.Sprintf("/%s/_apis/build/builds/%d/timeline", project, buildID))
	if err != nil {
		if resp != nil && resp.StatusCode == 404 {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get build timeline: %w", err)
	}
	return timeline, nil
}

// GetJobStepsSummary extracts a summary of job steps from the timeline
func GetJobStepsSummary(timeline *Timeline, webURL string) []JobStepSummary {
	if timeline == nil || len(timeline.Records) == 0 {
		return nil
	}

	var steps []JobStepSummary
	for _, record := range timeline.Records {
		// Only include Task and Job types for the summary
		if record.Type != "Task" && record.Type != "Job" && record.Type != "Stage" {
			continue
		}

		step := JobStepSummary{
			Name:         record.Name,
			Type:         record.Type,
			State:        record.State,
			Result:       record.Result,
			StartTime:    record.StartTime,
			FinishTime:   record.FinishTime,
			ErrorCount:   record.ErrorCount,
			WarningCount: record.WarningCount,
			WorkerName:   record.WorkerName,
		}

		if record.Log != nil {
			step.LogURL = record.Log.URL
		}

		if record.StartTime != nil && record.FinishTime != nil {
			duration := record.FinishTime.Sub(*record.StartTime)
			step.Duration = duration.String()
		}

		steps = append(steps, step)
	}

	return steps
}

// ApprovalPipelineRef links a project-level approval to its pipeline run
type ApprovalPipelineRef struct {
	ID int `json:"id"` // pipeline run ID
}

// PipelineApproval represents an environment approval gate for a pipeline run
type PipelineApproval struct {
	ID                   string               `json:"id"`
	Status               string               `json:"status"`
	CreatedOn            time.Time            `json:"createdOn"`
	LastModifiedOn       time.Time            `json:"lastModifiedOn"`
	Instructions         string               `json:"instructions,omitempty"`
	MinRequiredApprovers int                  `json:"minRequiredApprovers,omitempty"`
	Steps                []ApprovalStep       `json:"steps,omitempty"`
	BlockedApprovers     []IdentityRef        `json:"blockedApprovers,omitempty"`
	Pipeline             *ApprovalPipelineRef `json:"pipeline,omitempty"`
}

type ApprovalStep struct {
	AssignedApprover IdentityRef  `json:"assignedApprover"`
	ActualApprover   *IdentityRef `json:"actualApprover,omitempty"`
	Status           string       `json:"status"`
	Comment          string       `json:"comment,omitempty"`
	LastModifiedOn   time.Time    `json:"lastModifiedOn"`
	InitiatedOn      time.Time    `json:"initiatedOn"`
}

type PipelineApprovals struct {
	Count int                `json:"count"`
	Value []PipelineApproval `json:"value"`
}

// TestRunSummary holds aggregated test counts for a build
type TestRunSummary struct {
	TotalCount   int `json:"totalCount"`
	PassedCount  int `json:"passedCount"`
	FailedCount  int `json:"failedCount"`
	SkippedCount int `json:"skippedCount"`
	ErrorCount   int `json:"errorCount"`
}

type testRunEntry struct {
	TotalTests   int `json:"totalTests"`
	PassedTests  int `json:"passedTests"`
	FailedTests  int `json:"failedTests"`
	SkippedTests int `json:"skippedTests"`
	ErrorTests   int `json:"errorTests"`
}

type testRuns struct {
	Count int            `json:"count"`
	Value []testRunEntry `json:"value"`
}

// BuildArtifact represents an artifact produced by a build
type BuildArtifact struct {
	ID       int               `json:"id"`
	Name     string            `json:"name"`
	Resource *ArtifactResource `json:"resource,omitempty"`
}

type ArtifactResource struct {
	Type        string `json:"type"`
	DownloadURL string `json:"downloadUrl,omitempty"`
	URL         string `json:"url,omitempty"`
}

type buildArtifacts struct {
	Count int             `json:"count"`
	Value []BuildArtifact `json:"value"`
}

// RunDetails contains the full details of a pipeline run including steps
type RunDetails struct {
	Run
	URL         string             `json:"url,omitempty"`
	RequestedBy *IdentityRef       `json:"requestedBy,omitempty"`
	Steps       []JobStepSummary   `json:"steps,omitempty"`
	Parameters  map[string]any     `json:"parameters,omitempty"`
	Approvals   []PipelineApproval `json:"approvals,omitempty"`
	Artifacts   []BuildArtifact    `json:"artifacts,omitempty"`
	Tests       *TestRunSummary    `json:"tests,omitempty"`
}

// PipelinePermissions represents the permissions for a pipeline
type PipelinePermissions struct {
	Pipelines    []PipelinePermission    `json:"pipelines,omitempty"`
	AllPipelines *AllPipelinesPermission `json:"allPipelines,omitempty"`
}

type PipelinePermission struct {
	ID         int  `json:"id"`
	Authorized bool `json:"authorized"`
}

type AllPipelinesPermission struct {
	Authorized     bool         `json:"authorized"`
	AuthorizedBy   *IdentityRef `json:"authorizedBy,omitempty"`
	AuthorizedDate *time.Time   `json:"authorizedOn,omitempty"`
}

// IdentityRef represents an Azure DevOps identity
type IdentityRef struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	UniqueName  string `json:"uniqueName"`
	Descriptor  string `json:"descriptor,omitempty"`
	ImageURL    string `json:"imageUrl,omitempty"`
	IsContainer bool   `json:"isContainer,omitempty"`
}

// Build represents a build with requestedBy info
type Build struct {
	ID            int                 `json:"id"`
	BuildNumber   string              `json:"buildNumber"`
	Status        string              `json:"status"`
	Result        string              `json:"result,omitempty"`
	QueueTime     *time.Time          `json:"queueTime,omitempty"`
	StartTime     *time.Time          `json:"startTime,omitempty"`
	FinishTime    *time.Time          `json:"finishTime,omitempty"`
	RequestedBy   *IdentityRef        `json:"requestedBy,omitempty"`
	RequestedFor  *IdentityRef        `json:"requestedFor,omitempty"`
	SourceBranch  string              `json:"sourceBranch,omitempty"`
	SourceVersion string              `json:"sourceVersion,omitempty"`
	Definition    *BuildDefinitionRef `json:"definition,omitempty"`
	Links         map[string]Link     `json:"_links,omitempty"`
}

type BuildDefinitionRef struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type Builds struct {
	Count int     `json:"count"`
	Value []Build `json:"value"`
}

// AccessControlList represents Azure DevOps ACL
type AccessControlList struct {
	InheritPermissions bool                          `json:"inheritPermissions"`
	Token              string                        `json:"token"`
	AcesDictionary     map[string]AccessControlEntry `json:"acesDictionary"`
}

type AccessControlEntry struct {
	Descriptor   string                     `json:"descriptor"`
	Allow        int                        `json:"allow"`
	Deny         int                        `json:"deny"`
	ExtendedInfo *AccessControlExtendedInfo `json:"extendedInfo,omitempty"`
}

type AccessControlExtendedInfo struct {
	EffectiveAllow int `json:"effectiveAllow"`
	EffectiveDeny  int `json:"effectiveDeny"`
}

type AccessControlLists struct {
	Count int                 `json:"count"`
	Value []AccessControlList `json:"value"`
}

// Identity represents an Azure DevOps identity from the identities API
type Identity struct {
	ID                  string         `json:"id"`
	Descriptor          string         `json:"descriptor"`
	ProviderDisplayName string         `json:"providerDisplayName"`
	CustomDisplayName   string         `json:"customDisplayName"`
	IsActive            bool           `json:"isActive"`
	SubjectDescriptor   string         `json:"subjectDescriptor"`
	Properties          map[string]any `json:"properties,omitempty"`
}

type Identities struct {
	Count int        `json:"count"`
	Value []Identity `json:"value"`
}

// ToJSON converts RunDetails to a JSON map for change details
func (rd RunDetails) ToJSON() map[string]any {
	result := map[string]any{
		"id":          rd.ID,
		"name":        rd.Name,
		"state":       rd.State,
		"result":      rd.Result,
		"createdDate": rd.CreatedDate,
	}

	if !rd.FinishedDate.IsZero() {
		result["finishedDate"] = rd.FinishedDate
		result["duration"] = rd.Duration
	}

	if rd.Links != nil {
		if web, ok := rd.Links["web"]; ok {
			result["webUrl"] = web.Href
		}
	}

	if rd.RequestedBy != nil {
		result["requestedBy"] = rd.RequestedBy
	}

	if len(rd.Parameters) > 0 {
		result["parameters"] = rd.Parameters
	}

	if len(rd.Steps) > 0 {
		stepsJSON, err := json.Marshal(rd.Steps)
		if err == nil {
			var stepsMap []map[string]any
			_ = json.Unmarshal(stepsJSON, &stepsMap)
			result["steps"] = stepsMap
		}
	}

	if rd.Resources != nil && len(rd.Resources.Repositories) > 0 {
		result["repositories"] = rd.Resources.Repositories
	}

	if rd.Resources != nil && len(rd.Resources.Pipelines) > 0 {
		result["pipelines"] = rd.Resources.Pipelines
	}

	if len(rd.Approvals) > 0 {
		result["approvals"] = rd.Approvals
	}

	if len(rd.Artifacts) > 0 {
		result["artifacts"] = rd.Artifacts
	}

	if rd.Tests != nil {
		result["tests"] = rd.Tests
	}

	return result
}

// GetProjectApprovals fetches all approvals for a project and returns a map keyed by pipeline run ID.
func (ado *AzureDevopsClient) GetProjectApprovals(ctx context.Context, project string) (map[int][]PipelineApproval, error) {
	response, _, err := get[PipelineApprovals](ado.Client, ctx, fmt.Sprintf("/%s/_apis/pipelines/approvals", project),
		"api-version", "7.1-preview.1", "$expand", "steps")
	if err != nil {
		return nil, fmt.Errorf("failed to get project approvals: %w", err)
	}
	byRunID := make(map[int][]PipelineApproval, len(response.Value))
	for _, a := range response.Value {
		if a.Pipeline != nil {
			byRunID[a.Pipeline.ID] = append(byRunID[a.Pipeline.ID], a)
		}
	}
	return byRunID, nil
}

// GetBuildArtifacts fetches artifacts produced by a build
func (ado *AzureDevopsClient) GetBuildArtifacts(ctx context.Context, project string, buildID int) ([]BuildArtifact, error) {
	response, _, err := get[buildArtifacts](ado.Client, ctx, fmt.Sprintf("/%s/_apis/build/builds/%d/artifacts", project, buildID),
		"api-version", "7.1")
	if err != nil {
		return nil, fmt.Errorf("failed to get build artifacts: %w", err)
	}
	return response.Value, nil
}

// GetTestRuns fetches test runs for a build and aggregates the counts
func (ado *AzureDevopsClient) GetTestRuns(ctx context.Context, project string, buildID int) (*TestRunSummary, error) {
	response, _, err := get[testRuns](ado.Client, ctx, fmt.Sprintf("/%s/_apis/test/runs", project),
		"buildId", fmt.Sprintf("%d", buildID), "includeRunDetails", "true", "api-version", "7.1")
	if err != nil {
		return nil, fmt.Errorf("failed to get test runs: %w", err)
	}
	if response.Count == 0 {
		return nil, nil
	}
	summary := &TestRunSummary{}
	for _, run := range response.Value {
		summary.TotalCount += run.TotalTests
		summary.PassedCount += run.PassedTests
		summary.FailedCount += run.FailedTests
		summary.SkippedCount += run.SkippedTests
		summary.ErrorCount += run.ErrorTests
	}
	return summary, nil
}

// GetBuilds gets builds for a specific definition with requestedBy info.
// When since is non-zero only builds updated after that time are returned.
func (ado *AzureDevopsClient) GetBuilds(ctx context.Context, project string, definitionID int, since time.Time) ([]Build, error) {
	params := []string{"definitions", fmt.Sprintf("%d", definitionID), "api-version", "7.1"}
	if !since.IsZero() {
		params = append(params, "minTime", since.UTC().Format(time.RFC3339))
	}
	response, _, err := get[Builds](ado.Client, ctx, fmt.Sprintf("/%s/_apis/build/builds", project), params...)
	if err != nil {
		return nil, fmt.Errorf("failed to get builds: %w", err)
	}
	return response.Value, nil
}

// Build permission bits for Azure DevOps Build namespace
const (
	// Permission to view builds
	BuildPermissionViewBuilds = 1
	// Permission to edit build pipeline
	BuildPermissionEditBuildPipeline = 2
	// Permission to delete builds
	BuildPermissionDeleteBuilds = 8
	// Permission to queue builds
	BuildPermissionQueueBuilds = 128
	// Permission to stop builds
	BuildPermissionStopBuilds = 256
	// Permission to administer build permissions
	BuildPermissionAdministerBuildPermissions = 16384
)

// BuildSecurityNamespaceID is the GUID for the Build security namespace
const BuildSecurityNamespaceID = "33344d9c-fc72-4d6f-aba5-fa317101a7e9"

// GitSecurityNamespaceID is the GUID for the Git Repositories security namespace
const GitSecurityNamespaceID = "2e9eb7ed-3c0a-47d4-87c1-0ffdd275fd87"

const (
	GitPermissionRead         = 2
	GitPermissionContribute   = 4
	GitPermissionForcePush    = 8
	GitPermissionCreateBranch = 16
	GitPermissionCreateTag    = 32
	GitPermissionManageNotes  = 64
	GitPermissionCreateRepo   = 256
	GitPermissionDeleteRepo   = 512
	GitPermissionRenameRepo   = 1024
	GitPermissionManagePerms  = 8192
	GitPermissionPolicyExempt = 32768
)

// GetPipelinePermissions gets ACL permissions for a pipeline
func (ado *AzureDevopsClient) GetPipelinePermissions(ctx context.Context, project string, projectID string, pipelineID int) ([]AccessControlList, error) {
	token := fmt.Sprintf("%s/%d", projectID, pipelineID)
	acls, _, err := get[AccessControlLists](ado.Client, ctx, fmt.Sprintf("/_apis/accesscontrollists/%s", BuildSecurityNamespaceID),
		"api-version", "7.1", "token", token, "includeExtendedInfo", "true")
	if err != nil {
		return nil, fmt.Errorf("failed to get pipeline permissions: %w", err)
	}
	return acls.Value, nil
}

func (ado *AzureDevopsClient) GetRepositoryPermissions(ctx context.Context, projectID, repoID string) ([]AccessControlList, error) {
	token := fmt.Sprintf("repoV2/%s/%s", projectID, repoID)
	acls, _, err := get[AccessControlLists](ado.Client, ctx, fmt.Sprintf("/_apis/accesscontrollists/%s", GitSecurityNamespaceID),
		"api-version", "7.1", "token", token, "includeExtendedInfo", "true")
	if err != nil {
		return nil, fmt.Errorf("failed to get repository permissions: %w", err)
	}
	return acls.Value, nil
}

type GitPermissionInfo struct {
	IdentityDescriptor string
	IdentityType       string // "user", "group"
	Permissions        []string
}

var gitPermissionBits = []struct {
	Bit  int
	Name string
}{
	{GitPermissionRead, "Read"},
	{GitPermissionContribute, "Contribute"},
	{GitPermissionForcePush, "ForcePush"},
	{GitPermissionCreateBranch, "CreateBranch"},
	{GitPermissionCreateTag, "CreateTag"},
	{GitPermissionManageNotes, "ManageNotes"},
	{GitPermissionCreateRepo, "CreateRepository"},
	{GitPermissionDeleteRepo, "DeleteRepository"},
	{GitPermissionRenameRepo, "RenameRepository"},
	{GitPermissionManagePerms, "ManagePermissions"},
	{GitPermissionPolicyExempt, "PolicyExempt"},
}

func ParseGitPermissions(acls []AccessControlList) []GitPermissionInfo {
	var perms []GitPermissionInfo
	for _, acl := range acls {
		for descriptor, ace := range acl.AcesDictionary {
			effectiveAllow := ace.Allow
			if ace.ExtendedInfo != nil {
				effectiveAllow = ace.ExtendedInfo.EffectiveAllow
			}
			var permissions []string
			for _, bit := range gitPermissionBits {
				if (effectiveAllow & bit.Bit) != 0 {
					permissions = append(permissions, bit.Name)
				}
			}
			if len(permissions) == 0 {
				continue
			}
			identityType := "user"
			if strings.HasPrefix(descriptor, vssgpPrefix) {
				identityType = "group"
			} else if strings.HasPrefix(descriptor, tfIdentityPrefix) {
				identityType = "unknown"
			}
			perms = append(perms, GitPermissionInfo{
				IdentityDescriptor: descriptor,
				IdentityType:       identityType,
				Permissions:        permissions,
			})
		}
	}
	return perms
}

// ResolveGitRoles is a convenience wrapper for ResolveRoles with the "Git" prefix.
func ResolveGitRoles(permissions []string, roleMapping map[string][]string) []string {
	return ResolveRoles("Git", permissions, roleMapping)
}

// PermissionInfo represents a resolved permission entry
type PermissionInfo struct {
	IdentityDescriptor string
	IdentityName       string
	IdentityID         string
	IdentityType       string // "user", "group"
	CanQueue           bool
	CanAdmin           bool
	CanView            bool
	Allow              int
	Deny               int
}

// ParsePermissions extracts permission info from ACLs
func ParsePermissions(acls []AccessControlList) []PermissionInfo {
	var permissions []PermissionInfo

	for _, acl := range acls {
		for descriptor, ace := range acl.AcesDictionary {
			effectiveAllow := ace.Allow
			if ace.ExtendedInfo != nil {
				effectiveAllow = ace.ExtendedInfo.EffectiveAllow
			}

			perm := PermissionInfo{
				IdentityDescriptor: descriptor,
				Allow:              ace.Allow,
				Deny:               ace.Deny,
				CanView:            (effectiveAllow & BuildPermissionViewBuilds) != 0,
				CanQueue:           (effectiveAllow & BuildPermissionQueueBuilds) != 0,
				CanAdmin:           (effectiveAllow & BuildPermissionAdministerBuildPermissions) != 0,
			}

			if strings.HasPrefix(descriptor, vssgpPrefix) {
				perm.IdentityType = "group"
			} else if strings.HasPrefix(descriptor, tfIdentityPrefix) {
				perm.IdentityType = "unknown"
			} else {
				perm.IdentityType = "user"
			}

			permissions = append(permissions, perm)
		}
	}

	return permissions
}

var buildPermissionBits = []struct {
	Bit  int
	Name string
}{
	{BuildPermissionViewBuilds, "ViewBuilds"},
	{BuildPermissionEditBuildPipeline, "EditBuildPipeline"},
	{BuildPermissionDeleteBuilds, "DeleteBuilds"},
	{BuildPermissionQueueBuilds, "QueueBuilds"},
	{BuildPermissionStopBuilds, "StopBuilds"},
	{BuildPermissionAdministerBuildPermissions, "AdministerBuildPermissions"},
}

func ParseBuildPermissions(acls []AccessControlList) []GitPermissionInfo {
	var perms []GitPermissionInfo
	for _, acl := range acls {
		for descriptor, ace := range acl.AcesDictionary {
			effectiveAllow := ace.Allow
			if ace.ExtendedInfo != nil {
				effectiveAllow = ace.ExtendedInfo.EffectiveAllow
			}
			var permissions []string
			for _, bit := range buildPermissionBits {
				if (effectiveAllow & bit.Bit) != 0 {
					permissions = append(permissions, bit.Name)
				}
			}
			if len(permissions) == 0 {
				continue
			}
			identityType := "user"
			if strings.HasPrefix(descriptor, vssgpPrefix) {
				identityType = "group"
			} else if strings.HasPrefix(descriptor, tfIdentityPrefix) {
				identityType = "unknown"
			}
			perms = append(perms, GitPermissionInfo{
				IdentityDescriptor: descriptor,
				IdentityType:       identityType,
				Permissions:        permissions,
			})
		}
	}
	return perms
}

// ReleaseSecurityNamespaceID is the GUID for the ReleaseManagement security namespace
const ReleaseSecurityNamespaceID = "c788c23e-1b46-4162-8f5e-d7585343b5de"

const (
	ReleasePermissionViewReleaseDefinition            = 1
	ReleasePermissionEditReleaseDefinition            = 2
	ReleasePermissionDeleteReleaseDefinition          = 4
	ReleasePermissionManageDeployments                = 8
	ReleasePermissionManageReleaseApprovers           = 16
	ReleasePermissionManageReleases                   = 32
	ReleasePermissionViewReleases                     = 64
	ReleasePermissionCreateReleases                   = 128
	ReleasePermissionEditReleaseEnvironment           = 256
	ReleasePermissionDeleteReleaseEnvironment         = 512
	ReleasePermissionAdministerReleasePermissions     = 1024
	ReleasePermissionDeleteReleases                   = 2048
	ReleasePermissionManageDefinitionReleaseApprovers = 4096
)

var releasePermissionBits = []struct {
	Bit  int
	Name string
}{
	{ReleasePermissionViewReleaseDefinition, "ViewReleaseDefinition"},
	{ReleasePermissionEditReleaseDefinition, "EditReleaseDefinition"},
	{ReleasePermissionDeleteReleaseDefinition, "DeleteReleaseDefinition"},
	{ReleasePermissionManageDeployments, "ManageDeployments"},
	{ReleasePermissionManageReleaseApprovers, "ManageReleaseApprovers"},
	{ReleasePermissionManageReleases, "ManageReleases"},
	{ReleasePermissionViewReleases, "ViewReleases"},
	{ReleasePermissionCreateReleases, "CreateReleases"},
	{ReleasePermissionEditReleaseEnvironment, "EditReleaseEnvironment"},
	{ReleasePermissionDeleteReleaseEnvironment, "DeleteReleaseEnvironment"},
	{ReleasePermissionAdministerReleasePermissions, "AdministerReleasePermissions"},
	{ReleasePermissionDeleteReleases, "DeleteReleases"},
	{ReleasePermissionManageDefinitionReleaseApprovers, "ManageDefinitionReleaseApprovers"},
}

func ParseReleasePermissions(acls []AccessControlList) []GitPermissionInfo {
	var perms []GitPermissionInfo
	for _, acl := range acls {
		for descriptor, ace := range acl.AcesDictionary {
			effectiveAllow := ace.Allow
			if ace.ExtendedInfo != nil {
				effectiveAllow = ace.ExtendedInfo.EffectiveAllow
			}
			var permissions []string
			for _, bit := range releasePermissionBits {
				if (effectiveAllow & bit.Bit) != 0 {
					permissions = append(permissions, bit.Name)
				}
			}
			if len(permissions) == 0 {
				continue
			}
			identityType := "user"
			if strings.HasPrefix(descriptor, vssgpPrefix) {
				identityType = "group"
			} else if strings.HasPrefix(descriptor, tfIdentityPrefix) {
				identityType = "unknown"
			}
			perms = append(perms, GitPermissionInfo{
				IdentityDescriptor: descriptor,
				IdentityType:       identityType,
				Permissions:        permissions,
			})
		}
	}
	return perms
}

func (ado *AzureDevopsClient) GetReleasePermissions(ctx context.Context, projectID string, definitionID int) ([]AccessControlList, error) {
	token := fmt.Sprintf("%s/%d", projectID, definitionID)
	acls, _, err := get[AccessControlLists](ado.Client, ctx, fmt.Sprintf("/_apis/accesscontrollists/%s", ReleaseSecurityNamespaceID),
		"api-version", "7.1", "token", token, "includeExtendedInfo", "true")
	if err != nil {
		return nil, fmt.Errorf("failed to get release permissions: %w", err)
	}
	return acls.Value, nil
}

var DefaultRoles = map[string][]string{
	"Viewer": {
		"Git:Read",
		"Pipeline:ViewBuilds",
		"Release:ViewReleases",
		"Release:ViewReleaseDefinition",
	},
	"Developer": {
		"Git:Contribute",
		"Git:CreateBranch",
		"Git:CreateTag",
		"Pipeline:QueueBuilds",
		"Pipeline:EditBuildPipeline",
		"Release:CreateReleases",
	},
	"Admin": {
		"Git:ManagePermissions",
		"Git:DeleteRepository",
		"Git:ForcePush",
		"Pipeline:AdministerBuildPermissions",
		"Pipeline:DeleteBuilds",
		"Release:AdministerReleasePermissions",
		"Release:DeleteReleases",
		"Release:DeleteReleaseDefinition",
	},
	"Releaser": {
		"Release:ManageDeployments",
		"Release:ManageReleases",
		"Release:ManageReleaseApprovers",
		"Release:EditReleaseEnvironment",
	},
}

// ResolveRoles maps permissions to role names using a prefixed role mapping.
// It filters roleMapping to entries with the given prefix (e.g. "Git", "Pipeline", "Release"),
// then matches if the identity has ANY of the permissions for that role.
// Uses DefaultRoles when roleMapping is empty.
func ResolveRoles(prefix string, permissions []string, roleMapping map[string][]string) []string {
	if len(roleMapping) == 0 {
		roleMapping = DefaultRoles
	}

	permSet := make(map[string]bool, len(permissions))
	for _, p := range permissions {
		permSet[p] = true
	}

	pfx := prefix + ":"
	var matched []string
	for roleName, rolePerms := range roleMapping {
		for _, rp := range rolePerms {
			if !strings.HasPrefix(rp, pfx) {
				continue
			}
			perm := rp[len(pfx):]
			if permSet[perm] {
				matched = append(matched, roleName)
				break
			}
		}
	}
	return matched
}

// ResolvedIdentity represents a resolved Azure DevOps identity
type ResolvedIdentity struct {
	ID                  string                      `json:"id"`
	Descriptor          string                      `json:"descriptor"`
	SubjectDescriptor   string                      `json:"subjectDescriptor"`
	ProviderDisplayName string                      `json:"providerDisplayName"`
	IsActive            bool                        `json:"isActive"`
	IsContainer         bool                        `json:"isContainer"`
	MemberOf            []string                    `json:"memberOf,omitempty"`
	Members             []string                    `json:"members,omitempty"`
	MasterId            string                      `json:"masterId,omitempty"`
	Properties          map[string]IdentityProperty `json:"properties,omitempty"`
}

type IdentityProperty struct {
	Type  string `json:"$type"`
	Value string `json:"$value"`
}

type ResolvedIdentities struct {
	Count int                `json:"count"`
	Value []ResolvedIdentity `json:"value"`
}

// GetIdentitiesByDescriptor resolves identity descriptors to full identity info.
// System/service descriptors (svc., s2s.) are filtered out as they cannot be resolved.
func (ado *AzureDevopsClient) GetIdentitiesByDescriptor(ctx context.Context, descriptors []string) ([]ResolvedIdentity, error) {
	if len(descriptors) == 0 {
		return nil, nil
	}

	// _apis/identities accepts two parameter forms with different descriptor
	// vocabularies: ?descriptors= takes the legacy Microsoft.TeamFoundation.*
	// forms, while ?subjectDescriptors= takes the modern Graph forms (vssgp.,
	// aad., aadgp., svc.). Sending Graph-form values via ?descriptors=
	// silently returns {"value": [null, null, ...]} — no error, no warning,
	// just nulls. Routing each descriptor to the right parameter recovers
	// user/membership emission from the Graph/Memberships flow in
	// scrapeGroups (whose memberDescriptor values are always Graph form) and
	// resolves service principals (svc.) which the previous "filter out and
	// skip" policy left as zero-value records.
	var legacy, subject []string
	for _, d := range descriptors {
		if !isValidDescriptor(d) {
			ado.Logger.V(4).Infof("skipping unresolvable descriptor %q", d)
			continue
		}
		switch {
		case strings.HasPrefix(d, tfIdentityPrefix),
			strings.HasPrefix(d, tfServiceIdentityPrefix),
			strings.HasPrefix(d, claimsIdentityPrefix):
			legacy = append(legacy, d)
		default:
			// vssgp., aadgp., aad., svc. — Graph forms only after validation.
			subject = append(subject, d)
		}
	}
	if len(legacy) == 0 && len(subject) == 0 {
		return nil, nil
	}

	vsspsClient, err := ado.vsspsClient()
	if err != nil {
		return nil, fmt.Errorf("failed to build vssps client: %w", err)
	}

	// Branch failures are non-fatal: a 4xx from one branch must not block
	// whatever the other branch resolved. The caller (e.g. fetchRepoPermissions)
	// folds an error return into "drop the whole repo's permission rows" — we
	// only want to do that when nothing was resolved at all.
	var combined []ResolvedIdentity
	var firstErr error
	tolerable := func(resp *commonsHTTP.Response) bool {
		// 400 (malformed/unresolvable batch) and 404 (none of the descriptors
		// match an identity) are recoverable: log and continue.
		return resp != nil && (resp.StatusCode == 400 || resp.StatusCode == 404)
	}

	if len(legacy) > 0 {
		identities, resp, err := get[ResolvedIdentities](vsspsClient, ctx, "/_apis/identities",
			"api-version", "7.1", "descriptors", joinDescriptors(legacy))
		switch {
		case err != nil && tolerable(resp):
			ado.Logger.Warnf("ADO rejected %d legacy descriptors with HTTP %d: %v — continuing without them",
				len(legacy), resp.StatusCode, err)
		case err != nil:
			firstErr = fmt.Errorf("failed to resolve legacy identities: %w", err)
		default:
			for _, id := range identities.Value {
				if id.Descriptor != "" {
					combined = append(combined, id)
				}
			}
		}
	}
	if len(subject) > 0 {
		identities, resp, err := get[ResolvedIdentities](vsspsClient, ctx, "/_apis/identities",
			"api-version", "7.1", "subjectDescriptors", joinDescriptors(subject))
		switch {
		case err != nil && tolerable(resp):
			ado.Logger.Warnf("ADO rejected %d subject descriptors with HTTP %d: %v — continuing without them",
				len(subject), resp.StatusCode, err)
		case err != nil:
			if firstErr == nil {
				firstErr = fmt.Errorf("failed to resolve subject identities: %w", err)
			}
		default:
			for _, id := range identities.Value {
				if id.Descriptor != "" {
					combined = append(combined, id)
				}
			}
		}
	}
	if len(combined) == 0 && firstErr != nil {
		return nil, firstErr
	}
	return combined, nil
}

func joinDescriptors(descriptors []string) string {
	result := ""
	for i, d := range descriptors {
		if i > 0 {
			result += ","
		}
		result += d
	}
	return result
}

const (
	tfIdentityPrefix        = "Microsoft.TeamFoundation.Identity;"
	tfServiceIdentityPrefix = "Microsoft.TeamFoundation.ServiceIdentity;"
	claimsIdentityPrefix    = "Microsoft.IdentityModel.Claims.ClaimsIdentity;"
	vssgpPrefix             = "vssgp."
	aadgpPrefix             = "aadgp."
	aadPrefix               = "aad."
	svcPrefix               = "svc."
	s2sPrefix               = "s2s."
)

// isValidDescriptor reports whether `d` is a recognised, non-empty-payload ADO
// identity descriptor that can be sent to `_apis/identities`. ADO's parser
// rejects an entire batch with HTTP 400 ("The string must have at least one
// character. Parameter name: descriptors element.IdentityType") if any element
// has an empty IdentityType — caused by:
//   - the empty string,
//   - a known prefix with nothing after the separator (e.g. "vssgp.", "Microsoft.TeamFoundation.Identity;"),
//   - any other shape ADO doesn't classify (s2s., bare strings, future Graph forms with no payload).
// Filtering these out client-side prevents the 400 from blowing up an entire
// repo / pipeline ACL scrape.
func isValidDescriptor(d string) bool {
	if d == "" {
		return false
	}
	hasPayloadAfter := func(d, prefix string) bool {
		return len(d) > len(prefix) && strings.HasPrefix(d, prefix)
	}
	switch {
	case hasPayloadAfter(d, tfIdentityPrefix),
		hasPayloadAfter(d, tfServiceIdentityPrefix),
		hasPayloadAfter(d, claimsIdentityPrefix),
		hasPayloadAfter(d, vssgpPrefix),
		hasPayloadAfter(d, aadgpPrefix),
		hasPayloadAfter(d, aadPrefix),
		hasPayloadAfter(d, svcPrefix):
		return true
	}
	// s2s. and unknown forms — ADO can't resolve them; skip rather than risk a 400.
	return false
}

// b64decodeAny tolerates both standard and raw (unpadded) base64 encodings,
// since ADO emits the unpadded form for vssgp./aad./aadgp./svc. but historical
// payloads occasionally surface the padded variant.
func b64decodeAny(s string) ([]byte, bool) {
	if b, err := base64.RawStdEncoding.DecodeString(s); err == nil {
		return b, true
	}
	if b, err := base64.StdEncoding.DecodeString(s); err == nil {
		return b, true
	}
	return nil, false
}

// DescriptorToSID extracts the SID from either descriptor format.
// "vssgp.BASE64" / "aadgp.BASE64" -> decoded SID,
// "Microsoft.TeamFoundation.Identity;SID" -> SID.
func DescriptorToSID(descriptor string) string {
	if strings.HasPrefix(descriptor, tfIdentityPrefix) {
		return descriptor[len(tfIdentityPrefix):]
	}
	for _, prefix := range []string{vssgpPrefix, aadgpPrefix} {
		if strings.HasPrefix(descriptor, prefix) {
			if b, ok := b64decodeAny(descriptor[len(prefix):]); ok {
				return string(b)
			}
			return ""
		}
	}
	return ""
}

// DecodedDescriptor is the canonical, decoded form of an ADO descriptor.
// Exactly one of the value fields is populated; Form indicates which.
type DecodedDescriptor struct {
	Form string // "tfidentity", "service", "claims", "aad-user", "aad-group", "service-principal", "unknown"
	// SID is set for vssgp./aadgp./Microsoft.TeamFoundation.Identity; descriptors.
	SID string
	// AADObjectID is the AAD object id (lower-case, hyphenated GUID) for aad. descriptors.
	AADObjectID string
	// Email is set when the descriptor encodes an email address (claims identities).
	Email string
	// ServiceLabel is the human-readable label for service/service-principal descriptors.
	ServiceLabel string
	// ServicePayload is the inner OWNER:TYPE:GUID payload shared by the legacy
	// Microsoft.TeamFoundation.ServiceIdentity; form and the Graph svc. form —
	// the natural canonical alias that lets the two forms merge via overlap.
	ServicePayload string
	// Raw is the original descriptor string.
	Raw string
}

// DecodeDescriptor classifies a descriptor and decodes it down to its
// canonical primitive (SID, AAD object id, email, or service label).
//
// projectName is used only for service-principal labels — when the descriptor
// payload contains a project GUID, we substitute the human-readable project
// name for it (e.g. "Build Service (OIPA)" instead of "Build Service (8c6b...)").
func DecodeDescriptor(descriptor, projectName string) DecodedDescriptor {
	d := DecodedDescriptor{Raw: descriptor, Form: "unknown"}
	if descriptor == "" {
		return d
	}

	switch {
	case strings.HasPrefix(descriptor, tfIdentityPrefix):
		d.Form = "tfidentity"
		d.SID = descriptor[len(tfIdentityPrefix):]

	case strings.HasPrefix(descriptor, tfServiceIdentityPrefix):
		d.Form = "service"
		d.ServicePayload = descriptor[len(tfServiceIdentityPrefix):]
		d.ServiceLabel = serviceIdentityLabel(d.ServicePayload, projectName)

	case strings.HasPrefix(descriptor, claimsIdentityPrefix):
		d.Form = "claims"
		// Payload is "DOMAIN\email" or "DOMAIN\username". Take everything after
		// the last backslash; if it's an email, that's the canonical user id.
		payload := descriptor[len(claimsIdentityPrefix):]
		if idx := strings.LastIndex(payload, `\`); idx >= 0 && idx+1 < len(payload) {
			d.Email = payload[idx+1:]
		} else {
			d.Email = payload
		}

	case strings.HasPrefix(descriptor, vssgpPrefix):
		d.Form = "tfidentity"
		if b, ok := b64decodeAny(descriptor[len(vssgpPrefix):]); ok {
			d.SID = string(b)
		}

	case strings.HasPrefix(descriptor, aadgpPrefix):
		d.Form = "aad-group"
		if b, ok := b64decodeAny(descriptor[len(aadgpPrefix):]); ok {
			d.SID = string(b)
		}

	case strings.HasPrefix(descriptor, aadPrefix):
		d.Form = "aad-user"
		if b, ok := b64decodeAny(descriptor[len(aadPrefix):]); ok {
			d.AADObjectID = canonicalAADObjectID(b)
		}

	case strings.HasPrefix(descriptor, svcPrefix):
		d.Form = "service-principal"
		if b, ok := b64decodeAny(descriptor[len(svcPrefix):]); ok {
			d.ServicePayload = string(b)
			d.ServiceLabel = serviceIdentityLabel(d.ServicePayload, projectName)
		}

	case strings.HasPrefix(descriptor, s2sPrefix):
		d.Form = "service-principal"
	}

	return d
}

// canonicalAADObjectID converts the AAD object-id bytes (which historically
// arrive as the ASCII GUID string itself rather than a 16-byte UUID) to a
// lower-case hyphenated GUID. We accept both shapes and normalise.
func canonicalAADObjectID(raw []byte) string {
	s := strings.ToLower(strings.TrimSpace(string(raw)))
	if isUUIDLike(s) {
		return s
	}
	if len(raw) == 16 {
		// Big-endian byte slice → standard UUID string.
		var u [16]byte
		copy(u[:], raw)
		return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", u[0:4], u[4:6], u[6:8], u[8:10], u[10:16])
	}
	return ""
}

// CanonicalIdentityValue returns the single most useful value for cross-system
// matching — preferring email > AAD object id > SID > service payload >
// service label > raw. This is the value that should appear as a primary alias
// on every entity so AAD-scraper rows and ADO-scraper rows merge.
func CanonicalIdentityValue(descriptor, projectName string) string {
	d := DecodeDescriptor(descriptor, projectName)
	switch {
	case d.Email != "":
		return d.Email
	case d.AADObjectID != "":
		return d.AADObjectID
	case d.SID != "":
		return d.SID
	case d.ServicePayload != "":
		return d.ServicePayload
	case d.ServiceLabel != "":
		return d.ServiceLabel
	}
	return descriptor
}

// SIDToTFIdentity converts a SID to the Microsoft.TeamFoundation.Identity; form.
func SIDToTFIdentity(sid string) string {
	if sid == "" {
		return ""
	}
	return tfIdentityPrefix + sid
}

// SIDToVssgp converts a SID to the vssgp. form.
func SIDToVssgp(sid string) string {
	if sid == "" {
		return ""
	}
	return vssgpPrefix + base64.RawStdEncoding.EncodeToString([]byte(sid))
}

// NormalizeDescriptor returns the canonical vssgp. form of a descriptor.
// Both "vssgp.X" and "Microsoft.TeamFoundation.Identity;SID" normalize to the same "vssgp.X".
// Non-identity descriptors are returned as-is.
func NormalizeDescriptor(descriptor string) string {
	sid := DescriptorToSID(descriptor)
	if sid == "" {
		return descriptor
	}
	return SIDToVssgp(sid)
}

// DescriptorAliases returns the canonical primitive(s) a descriptor decodes
// to — never the raw descriptor or its legacy synonyms. The canonical forms
// (email / AAD object id / SID / service payload / service label) are what
// merge ADO and AAD rows by alias overlap; the descriptor synonyms add no
// extra information and only inflate the alias set.
//
// When the descriptor is in a form we don't recognise (no decode produces any
// primitive), the raw descriptor is returned as the only alias — that is the
// only situation where the caller has nothing better to use.
func DescriptorAliases(descriptor string) []string {
	if descriptor == "" {
		return nil
	}
	d := DecodeDescriptor(descriptor, "")
	var out []string
	if d.SID != "" {
		out = append(out, d.SID)
	}
	if d.AADObjectID != "" {
		out = append(out, d.AADObjectID)
	}
	if d.Email != "" {
		out = append(out, d.Email)
	}
	if d.ServicePayload != "" {
		out = append(out, d.ServicePayload)
	}
	if d.ServiceLabel != "" {
		out = append(out, d.ServiceLabel)
	}
	if len(out) == 0 {
		return []string{descriptor}
	}
	return uniqueNonEmpty(out)
}

// identityAliases assembles the canonical alias list for a resolved identity:
// the email (already extracted from identity properties by the caller) plus
// each descriptor's canonical decoded primitives. The raw descriptors are
// intentionally NOT included — they are the unique-and-redundant synonyms of
// the canonical forms produced by DescriptorAliases.
func identityAliases(identity ResolvedIdentity, extraEmail string) []string {
	out := []string{extraEmail}
	out = append(out, DescriptorAliases(identity.Descriptor)...)
	out = append(out, DescriptorAliases(identity.SubjectDescriptor)...)
	return uniqueNonEmpty(out)
}

func uniqueNonEmpty(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

// BuildIdentityMap creates a lookup map that maps any descriptor form (vssgp.,
// Microsoft.TeamFoundation.Identity;, aad./aadgp./svc., SubjectDescriptor) plus
// the canonical primitive (SID/AAD GUID/email/service payload) to the resolved
// identity. ACL responses key permissions by one descriptor form but the
// resolved-identity API may return a different form, so the lookup needs both.
func BuildIdentityMap(identities []ResolvedIdentity) map[string]ResolvedIdentity {
	m := make(map[string]ResolvedIdentity, len(identities)*4)
	addDescriptorKeys := func(descriptor string, id ResolvedIdentity) {
		if descriptor == "" {
			return
		}
		m[descriptor] = id
		// Add the matching SID-form synonym so a caller with the OTHER
		// descriptor form lands on the same identity. This is the only
		// place the legacy synonyms are useful — they don't belong in the
		// persisted alias set.
		if sid := DescriptorToSID(descriptor); sid != "" {
			m[SIDToVssgp(sid)] = id
			m[SIDToTFIdentity(sid)] = id
		}
		// Canonical primitives (SID / email / AAD id / service payload).
		for _, alias := range DescriptorAliases(descriptor) {
			m[alias] = id
		}
	}
	for _, id := range identities {
		addDescriptorKeys(id.Descriptor, id)
		addDescriptorKeys(id.SubjectDescriptor, id)
	}
	return m
}

func isUUIDLike(s string) bool {
	return len(s) == 36 && s[8] == '-' && s[13] == '-' && s[18] == '-' && s[23] == '-'
}

// serviceIdentityLabel extracts the type and replaces the resource GUID with projectName if available.
func serviceIdentityLabel(payload string, projectName string) string {
	parts := strings.SplitN(payload, ":", 3)
	if len(parts) != 3 {
		return ""
	}
	label := projectName
	if label == "" {
		label = parts[2]
	}
	return parts[1] + " Service (" + label + ")"
}

// ResolvedIdentityName returns the best display name for a resolved identity.
// projectName is used to replace GUIDs in service identity names (e.g. "Build Service (OIPA)").
func ResolvedIdentityName(identity ResolvedIdentity, projectName string) string {
	name := identity.ProviderDisplayName

	for _, desc := range []string{identity.Descriptor, identity.SubjectDescriptor} {
		decoded := DecodeDescriptor(desc, projectName)
		if decoded.ServiceLabel != "" {
			return decoded.ServiceLabel
		}
	}

	if name != "" && strings.Contains(name, "@") {
		return "Service Principal (" + name + ")"
	}
	if isUUIDLike(name) {
		return "Service Account (" + name + ")"
	}
	return name
}

// GetPipelineSecurityRoles gets the build definition security roles (who can queue, admin, etc.)
func (ado *AzureDevopsClient) GetPipelineSecurityRoles(ctx context.Context, project string, pipelineID int) ([]PipelineRole, error) {
	roles, _, err := get[PipelineRoles](ado.Client, ctx, fmt.Sprintf("/%s/_apis/build/definitions/%d/authorizedresources", project, pipelineID),
		"api-version", "7.1-preview.1")
	if err != nil {
		return nil, fmt.Errorf("failed to get pipeline security roles: %w", err)
	}
	return roles.Value, nil
}

type PipelineRoles struct {
	Count int            `json:"count"`
	Value []PipelineRole `json:"value"`
}

type PipelineRole struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	Name       string `json:"name"`
	Authorized bool   `json:"authorized"`
}

// GraphGroup represents an Azure DevOps group from the Graph API
type GraphGroup struct {
	Descriptor    string `json:"descriptor"`
	DisplayName   string `json:"displayName"`
	Description   string `json:"description,omitempty"`
	PrincipalName string `json:"principalName"`
	Origin        string `json:"origin"`
	OriginID      string `json:"originId"`
	Domain        string `json:"domain,omitempty"`
	MailAddress   string `json:"mailAddress,omitempty"`
}

type GraphGroups struct {
	Count int          `json:"count"`
	Value []GraphGroup `json:"value"`
}

type GraphMembership struct {
	ContainerDescriptor string `json:"containerDescriptor"`
	MemberDescriptor    string `json:"memberDescriptor"`
}

type GraphMemberships struct {
	Count int               `json:"count"`
	Value []GraphMembership `json:"value"`
}

func (ado *AzureDevopsClient) vsspsClient() (*commonsHTTP.Client, error) {
	return newADOHTTPClient(ado.ScrapeContext, fmt.Sprintf("https://vssps.dev.azure.com/%s", ado.Organization), ado.Organization, ado.token)
}

func (ado *AzureDevopsClient) GetGroups(ctx context.Context) ([]GraphGroup, error) {
	client, err := ado.vsspsClient()
	if err != nil {
		return nil, fmt.Errorf("failed to build vssps client: %w", err)
	}
	var all []GraphGroup
	var prevToken string
	continuationToken := ""

	for {
		if err := ctx.Err(); err != nil {
			return all, err
		}
		params := []string{"api-version", "7.1-preview.1"}
		if continuationToken != "" {
			params = append(params, "continuationToken", continuationToken)
		}

		groups, resp, err := get[GraphGroups](client, ctx, "/_apis/graph/groups", params...)
		if err != nil {
			return all, fmt.Errorf("failed to list groups: %w", err)
		}
		all = append(all, groups.Value...)

		continuationToken = resp.Header.Get("X-MS-ContinuationToken")
		if continuationToken == "" || continuationToken == prevToken {
			break
		}
		prevToken = continuationToken
	}

	return all, nil
}

func (ado *AzureDevopsClient) GetGroupMembers(ctx context.Context, groupDescriptor string) ([]GraphMembership, error) {
	client, err := ado.vsspsClient()
	if err != nil {
		return nil, fmt.Errorf("failed to build vssps client: %w", err)
	}
	var all []GraphMembership
	var prevToken string
	continuationToken := ""
	path := fmt.Sprintf("/_apis/graph/Memberships/%s", groupDescriptor)

	for {
		if err := ctx.Err(); err != nil {
			return all, err
		}
		params := []string{"api-version", "7.1-preview.1", "direction", "Down"}
		if continuationToken != "" {
			params = append(params, "continuationToken", continuationToken)
		}
		memberships, resp, err := get[GraphMemberships](client, ctx, path, params...)
		if err != nil {
			return all, fmt.Errorf("failed to get group members: %w", err)
		}
		all = append(all, memberships.Value...)

		continuationToken = resp.Header.Get("X-MS-ContinuationToken")
		if continuationToken == "" || continuationToken == prevToken {
			break
		}
		prevToken = continuationToken
	}

	return all, nil
}

// AzureDevopsReleaseClient talks to vsrm.dev.azure.com for classic release pipelines.
type AzureDevopsReleaseClient struct {
	*commonsHTTP.Client
	api.ScrapeContext
}

// NewAzureDevopsReleaseClient creates a release client pointing to vsrm.dev.azure.com.
func NewAzureDevopsReleaseClient(ctx api.ScrapeContext, ado v1.AzureDevops) (*AzureDevopsReleaseClient, error) {
	org, token, err := resolveOrgAndToken(ctx, &ado)
	if err != nil {
		return nil, err
	}
	client, err := newADOHTTPClient(ctx, fmt.Sprintf("https://vsrm.dev.azure.com/%s", org), org, token)
	if err != nil {
		return nil, err
	}
	return &AzureDevopsReleaseClient{ScrapeContext: ctx, Client: client}, nil
}

type ReleaseDefinition struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	Path string `json:"path,omitempty"`
}

type ReleaseDefinitions struct {
	Count int                 `json:"count"`
	Value []ReleaseDefinition `json:"value"`
}

// ReleaseApproval is a single pre- or post-deploy approval instance on a release environment.
// IsAutomated==true entries are system-generated and carry no useful information.
type ReleaseApproval struct {
	ID           int          `json:"id"`
	ApprovalType string       `json:"approvalType"` // "preDeploy" | "postDeploy"
	Status       string       `json:"status"`       // "approved" | "rejected" | "pending" | "skipped" | ...
	IsAutomated  bool         `json:"isAutomated"`
	Approver     *IdentityRef `json:"approver,omitempty"`
	ApprovedBy   *IdentityRef `json:"approvedBy,omitempty"`
	Comments     string       `json:"comments,omitempty"`
}

type DeployStep struct {
	ID              int          `json:"id"`
	Status          string       `json:"status"`
	OperationStatus string       `json:"operationStatus"`
	Attempt         int          `json:"attempt"`
	RequestedBy     *IdentityRef `json:"requestedBy,omitempty"`
	RequestedFor    *IdentityRef `json:"requestedFor,omitempty"`
	QueuedOn        *time.Time   `json:"queuedOn,omitempty"`
	LastModifiedOn  *time.Time   `json:"lastModifiedOn,omitempty"`
}

type ConfigurationVariable struct {
	Value    string `json:"value"`
	IsSecret bool   `json:"isSecret,omitempty"`
}

type ArtifactSourceRef struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
}

type ReleaseArtifact struct {
	SourceID            string                       `json:"sourceId,omitempty"`
	Type                string                       `json:"type,omitempty"`
	Alias               string                       `json:"alias,omitempty"`
	IsPrimary           bool                         `json:"isPrimary,omitempty"`
	DefinitionReference map[string]ArtifactSourceRef `json:"definitionReference,omitempty"`
}

type ReleaseEnvironment struct {
	ID                  int                              `json:"id"`
	Name                string                           `json:"name"`
	Status              string                           `json:"status"`
	Rank                int                              `json:"rank"`
	TriggerReason       string                           `json:"triggerReason,omitempty"`
	Variables           map[string]ConfigurationVariable `json:"variables,omitempty"`
	CreatedOn           time.Time                        `json:"createdOn"`
	ModifiedOn          time.Time                        `json:"modifiedOn"`
	PreDeployApprovals  []ReleaseApproval                `json:"preDeployApprovals,omitempty"`
	PostDeployApprovals []ReleaseApproval                `json:"postDeployApprovals,omitempty"`
	DeploySteps         []DeployStep                     `json:"deploySteps,omitempty"`
}

type Release struct {
	ID           int                              `json:"id"`
	Name         string                           `json:"name"`
	Status       string                           `json:"status"`
	Reason       string                           `json:"reason,omitempty"`
	Description  string                           `json:"description,omitempty"`
	CreatedOn    time.Time                        `json:"createdOn"`
	CreatedBy    *IdentityRef                     `json:"createdBy,omitempty"`
	Variables    map[string]ConfigurationVariable `json:"variables,omitempty"`
	Artifacts    []ReleaseArtifact                `json:"artifacts,omitempty"`
	Environments []ReleaseEnvironment             `json:"environments,omitempty"`
	Links        map[string]Link                  `json:"_links,omitempty"`
}

type Releases struct {
	Count int       `json:"count"`
	Value []Release `json:"value"`
}

// GetReleaseDefinitions returns all classic release definitions for a project.
func (ado *AzureDevopsReleaseClient) GetReleaseDefinitions(ctx context.Context, project string) ([]ReleaseDefinition, error) {
	response, _, err := get[ReleaseDefinitions](ado.Client, ctx, fmt.Sprintf("/%s/_apis/release/definitions", project),
		"api-version", "7.1")
	if err != nil {
		return nil, fmt.Errorf("failed to get release definitions: %w", err)
	}
	releases := response.Value
	for i := range releases {
		releases[i].Path = strings.TrimPrefix(releases[i].Path, "/")
		releases[i].Path = strings.TrimPrefix(releases[i].Path, "\\")
	}
	return releases, nil
}

// GetReleases returns releases for a definition with environments expanded.
// No time filter is applied at the API level because a release created before
// the cutoff can still have environments that were deployed after it.
// Callers should filter by environment ModifiedOn instead.
func (ado *AzureDevopsReleaseClient) GetReleases(ctx context.Context, project string, definitionID int) ([]Release, error) {
	response, _, err := get[Releases](ado.Client, ctx, fmt.Sprintf("/%s/_apis/release/releases", project),
		"api-version", "7.1", "definitionId", fmt.Sprintf("%d", definitionID), "$expand", "environments,approvals")
	if err != nil {
		return nil, fmt.Errorf("failed to get releases: %w", err)
	}
	return response.Value, nil
}

// GetReleaseDefinition fetches the full release definition JSON for use as config.
func (ado *AzureDevopsReleaseClient) GetReleaseDefinition(ctx context.Context, project string, definitionID int) (map[string]any, error) {
	response, _, err := get[map[string]any](ado.Client, ctx,
		fmt.Sprintf("/%s/_apis/release/definitions/%d", project, definitionID),
		"api-version", "7.1")
	if err != nil {
		return nil, fmt.Errorf("failed to get release definition %d: %w", definitionID, err)
	}
	return *response, nil
}
