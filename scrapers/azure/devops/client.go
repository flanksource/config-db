package devops

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/go-resty/resty/v2"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
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

func (p Pipeline) GetLabels() map[string]string {
	var labels = map[string]string{}

	for k, v := range p.TemplateParameters {
		labels[k] = fmt.Sprintf("%v", v)

	}
	for k, v := range p.Variables {
		labels[k] = v.Value
	}
	return labels
}

// GetID returns a stable ID for a pipeline, independent of revision.
// The API URL contains a ?revision=N query parameter that changes on each update,
// so we prefer the web link (stable browser URL) and strip any query string as a fallback.
func (p Pipeline) GetID() string {
	if web, ok := p.Links["web"]; ok && web.Href != "" {
		return web.Href
	}
	if i := strings.IndexByte(p.URL, '?'); i >= 0 {
		return p.URL[:i]
	}
	return p.URL
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
	*resty.Client
	api.ScrapeContext
}

func NewAzureDevopsClient(ctx api.ScrapeContext, ado v1.AzureDevops) (*AzureDevopsClient, error) {
	var token string
	if connection, err := ctx.HydrateConnectionByURL(ado.ConnectionName); err != nil {
		return nil, fmt.Errorf("failed to find connection: %w", err)
	} else if connection != nil {
		token = connection.Password
		ado.Organization = connection.Username
	} else {
		token, err = ctx.GetEnvValueFromCache(ado.PersonalAccessToken, ctx.GetNamespace())
		if err != nil {
			return nil, err
		}
	}

	client := resty.New().
		SetBaseURL(fmt.Sprintf("https://dev.azure.com/%s", ado.Organization)).
		SetBasicAuth(ado.Organization, token)

	return &AzureDevopsClient{
		ScrapeContext: ctx,
		Client:        client,
	}, nil
}

func (ado *AzureDevopsClient) GetPipelines(ctx context.Context, project string) ([]Pipeline, error) {
	var response Pipelines
	_, err := ado.R().SetContext(ctx).SetResult(&response).Get(fmt.Sprintf("/%s/_apis/pipelines", project))
	if err != nil {
		return nil, err
	}

	return response.Value, nil
}

func (ado *AzureDevopsClient) GetPipelineRuns(ctx context.Context, project string, pipeline Pipeline) ([]Run, error) {
	var runs Runs
	_, err := ado.R().SetContext(ctx).SetResult(&runs).Get(fmt.Sprintf("/%s/_apis/pipelines/%d/runs", project, pipeline.ID))

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
	var projects Projects
	_, err := ado.R().SetContext(ctx).SetResult(&projects).Get("/_apis/projects")

	if err != nil {
		return nil, err
	}

	return projects.Value, nil
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
	RepositoryName string `json:"repositoryName,omitempty"`
	RepositoryURL  string `json:"repositoryUrl,omitempty"`
	DefaultBranch  string `json:"defaultBranch,omitempty"`
}

// GetPipelineWithDefinition fetches a pipeline with its build definition details
func (ado *AzureDevopsClient) GetPipelineWithDefinition(ctx context.Context, project string, pipelineID int) (*PipelineDefinition, error) {
	var pipeline Pipeline
	_, err := ado.R().SetContext(ctx).SetResult(&pipeline).Get(fmt.Sprintf("/%s/_apis/pipelines/%d", project, pipelineID))
	if err != nil {
		return nil, fmt.Errorf("failed to get pipeline: %w", err)
	}

	var definition BuildDefinition
	_, err = ado.R().SetContext(ctx).SetResult(&definition).Get(fmt.Sprintf("/%s/_apis/build/definitions/%d", project, pipelineID))
	if err != nil {
		return nil, fmt.Errorf("failed to get build definition: %w", err)
	}

	pipelineDef := &PipelineDefinition{
		Pipeline: pipeline,
	}

	if definition.Process != nil {
		pipelineDef.YamlPath = definition.Process.YamlPath
	}

	if definition.Repository != nil {
		pipelineDef.RepositoryName = definition.Repository.Name
		pipelineDef.RepositoryURL = definition.Repository.URL
		pipelineDef.DefaultBranch = definition.Repository.DefaultBranch
	}

	return pipelineDef, nil
}

// GetBuildTimeline gets the timeline/steps for a specific build
func (ado *AzureDevopsClient) GetBuildTimeline(ctx context.Context, project string, buildID int) (*Timeline, error) {
	var timeline Timeline
	resp, err := ado.R().SetContext(ctx).SetResult(&timeline).Get(fmt.Sprintf("/%s/_apis/build/builds/%d/timeline", project, buildID))
	if err != nil {
		return nil, fmt.Errorf("failed to get build timeline: %w", err)
	}
	if resp.StatusCode() == 404 {
		return nil, nil
	}
	return &timeline, nil
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
			json.Unmarshal(stepsJSON, &stepsMap)
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
	var response PipelineApprovals
	_, err := ado.R().SetContext(ctx).SetResult(&response).
		SetQueryParam("api-version", "7.1-preview.1").
		SetQueryParam("$expand", "steps").
		Get(fmt.Sprintf("/%s/_apis/pipelines/approvals", project))
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
	var response buildArtifacts
	_, err := ado.R().SetContext(ctx).SetResult(&response).
		SetQueryParam("api-version", "7.1").
		Get(fmt.Sprintf("/%s/_apis/build/builds/%d/artifacts", project, buildID))
	if err != nil {
		return nil, fmt.Errorf("failed to get build artifacts: %w", err)
	}
	return response.Value, nil
}

// GetTestRuns fetches test runs for a build and aggregates the counts
func (ado *AzureDevopsClient) GetTestRuns(ctx context.Context, project string, buildID int) (*TestRunSummary, error) {
	var response testRuns
	_, err := ado.R().SetContext(ctx).SetResult(&response).
		SetQueryParam("buildId", fmt.Sprintf("%d", buildID)).
		SetQueryParam("includeRunDetails", "true").
		SetQueryParam("api-version", "7.1").
		Get(fmt.Sprintf("/%s/_apis/test/runs", project))
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
	req := ado.R().SetContext(ctx).SetResult(&Builds{}).
		SetQueryParam("definitions", fmt.Sprintf("%d", definitionID)).
		SetQueryParam("api-version", "7.1")
	if !since.IsZero() {
		req = req.SetQueryParam("minTime", since.UTC().Format(time.RFC3339))
	}
	resp, err := req.Get(fmt.Sprintf("/%s/_apis/build/builds", project))
	if err != nil {
		return nil, fmt.Errorf("failed to get builds: %w", err)
	}
	return resp.Result().(*Builds).Value, nil
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

// GetPipelinePermissions gets ACL permissions for a pipeline
func (ado *AzureDevopsClient) GetPipelinePermissions(ctx context.Context, project string, projectID string, pipelineID int) ([]AccessControlList, error) {
	token := fmt.Sprintf("%s/%d", projectID, pipelineID)

	var acls AccessControlLists
	_, err := ado.R().SetContext(ctx).SetResult(&acls).
		SetQueryParam("api-version", "7.1").
		SetQueryParam("token", token).
		SetQueryParam("includeExtendedInfo", "true").
		Get(fmt.Sprintf("/_apis/accesscontrollists/%s", BuildSecurityNamespaceID))
	if err != nil {
		return nil, fmt.Errorf("failed to get pipeline permissions: %w", err)
	}
	return acls.Value, nil
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

			// Parse identity type from descriptor
			// Format: Microsoft.TeamFoundation.Identity;S-1-9-... or vssgp.Uy0xLT...
			if len(descriptor) > 5 {
				if descriptor[0:5] == "vssgp" {
					perm.IdentityType = "group"
				} else {
					perm.IdentityType = "user"
				}
			}

			permissions = append(permissions, perm)
		}
	}

	return permissions
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

// GetIdentitiesByDescriptor resolves identity descriptors to full identity info
func (ado *AzureDevopsClient) GetIdentitiesByDescriptor(ctx context.Context, descriptors []string) ([]ResolvedIdentity, error) {
	if len(descriptors) == 0 {
		return nil, nil
	}

	vsspsClient := resty.New().
		SetBaseURL(fmt.Sprintf("https://vssps.dev.azure.com/%s", ado.ScrapeContext.ScrapeConfig().Spec.AzureDevops[0].Organization)).
		SetBasicAuth(ado.Client.UserInfo.Username, ado.Client.UserInfo.Password)

	var identities ResolvedIdentities
	_, err := vsspsClient.R().SetContext(ctx).
		SetResult(&identities).
		SetQueryParam("api-version", "7.1").
		SetQueryParam("descriptors", joinDescriptors(descriptors)).
		Get("/_apis/identities")
	if err != nil {
		return nil, fmt.Errorf("failed to resolve identities: %w", err)
	}
	return identities.Value, nil
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

// GetPipelineSecurityRoles gets the build definition security roles (who can queue, admin, etc.)
func (ado *AzureDevopsClient) GetPipelineSecurityRoles(ctx context.Context, project string, pipelineID int) ([]PipelineRole, error) {
	var roles PipelineRoles
	_, err := ado.R().SetContext(ctx).SetResult(&roles).
		SetQueryParam("api-version", "7.1-preview.1").
		Get(fmt.Sprintf("/%s/_apis/build/definitions/%d/authorizedresources", project, pipelineID))
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

// AzureDevopsReleaseClient talks to vsrm.dev.azure.com for classic release pipelines.
type AzureDevopsReleaseClient struct {
	*resty.Client
	api.ScrapeContext
}

// NewAzureDevopsReleaseClient creates a release client pointing to vsrm.dev.azure.com.
func NewAzureDevopsReleaseClient(ctx api.ScrapeContext, ado v1.AzureDevops) (*AzureDevopsReleaseClient, error) {
	var token string
	if connection, err := ctx.HydrateConnectionByURL(ado.ConnectionName); err != nil {
		return nil, fmt.Errorf("failed to find connection: %w", err)
	} else if connection != nil {
		token = connection.Password
		ado.Organization = connection.Username
	} else {
		token, err = ctx.GetEnvValueFromCache(ado.PersonalAccessToken, ctx.GetNamespace())
		if err != nil {
			return nil, err
		}
	}
	client := resty.New().
		SetBaseURL(fmt.Sprintf("https://vsrm.dev.azure.com/%s", ado.Organization)).
		SetBasicAuth(ado.Organization, token)
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

type ReleaseEnvironment struct {
	ID                  int               `json:"id"`
	Name                string            `json:"name"`
	Status              string            `json:"status"`
	Rank                int               `json:"rank"`
	CreatedOn           time.Time         `json:"createdOn"`
	ModifiedOn          time.Time         `json:"modifiedOn"`
	PreDeployApprovals  []ReleaseApproval `json:"preDeployApprovals,omitempty"`
	PostDeployApprovals []ReleaseApproval `json:"postDeployApprovals,omitempty"`
}

type Release struct {
	ID           int                  `json:"id"`
	Name         string               `json:"name"`
	Status       string               `json:"status"`
	CreatedOn    time.Time            `json:"createdOn"`
	CreatedBy    *IdentityRef         `json:"createdBy,omitempty"`
	Environments []ReleaseEnvironment `json:"environments,omitempty"`
	Links        map[string]Link      `json:"_links,omitempty"`
}

type Releases struct {
	Count int       `json:"count"`
	Value []Release `json:"value"`
}

// GetReleaseDefinitions returns all classic release definitions for a project.
func (ado *AzureDevopsReleaseClient) GetReleaseDefinitions(ctx context.Context, project string) ([]ReleaseDefinition, error) {
	var response ReleaseDefinitions
	_, err := ado.R().SetContext(ctx).SetResult(&response).
		SetQueryParam("api-version", "7.1").
		Get(fmt.Sprintf("/%s/_apis/release/definitions", project))
	if err != nil {
		return nil, fmt.Errorf("failed to get release definitions: %w", err)
	}
	return response.Value, nil
}

// GetReleases returns releases for a definition with environments expanded.
// No time filter is applied at the API level because a release created before
// the cutoff can still have environments that were deployed after it.
// Callers should filter by environment ModifiedOn instead.
func (ado *AzureDevopsReleaseClient) GetReleases(ctx context.Context, project string, definitionID int) ([]Release, error) {
	resp, err := ado.R().SetContext(ctx).SetResult(&Releases{}).
		SetQueryParam("api-version", "7.1").
		SetQueryParam("definitionId", fmt.Sprintf("%d", definitionID)).
		SetQueryParam("$expand", "environments,approvals").
		Get(fmt.Sprintf("/%s/_apis/release/releases", project))
	if err != nil {
		return nil, fmt.Errorf("failed to get releases: %w", err)
	}
	return resp.Result().(*Releases).Value, nil
}
