package devops

import (
	"fmt"
	"sort"
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
	TemplateParameters map[string]string   `json:"templateParameters,omitempty"`
	Runs               []v1.ChangeResult   `json:"-"`
}

func (p Pipeline) GetTags() map[string]string {
	var tags = p.TemplateParameters
	for k, v := range p.Variables {
		tags[k] = v.Value
	}
	return tags
}

// GetID returns a repeatable ID for a pipeline with variables / parameters
func (p Pipeline) GetID() string {

	s := p.Name

	if len(p.TemplateParameters) > 0 {
		s += "/"
		params := []string{}
		for k, v := range p.TemplateParameters {
			params = append(params, fmt.Sprintf("%s=%s", k, v))
		}
		sort.Strings(params)
		s += fmt.Sprintf("/%s", strings.Join(params, " "))
	}
	return s
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
	TemplateParameters map[string]string   `json:"templateParameters,omitempty"`
	State              string              `json:"state"`
	Result             string              `json:"result"`
	CreatedDate        time.Time           `json:"createdDate"`
	FinishedDate       time.Time           `json:"finishedDate"`
	//Duration in milliseconds
	Duration int    `json:"duration"`
	URL      string `json:"url"`
	ID       int    `json:"id"`
	Name     string `json:"name"`
}

func (r Run) GetTags() map[string]string {
	var tags = r.TemplateParameters
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
	if connection, err := ctx.HydrateConnection(ado.ConnectionName); err != nil {
		return nil, fmt.Errorf("failed to find connection: %w", err)
	} else if connection != nil {
		token = connection.Password
		ado.Organization = connection.Username
	} else {
		token, err = ctx.GetEnvValueFromCache(ado.PersonalAccessToken)
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

func (ado *AzureDevopsClient) GetPipelines(project string) ([]Pipeline, error) {
	var response Pipelines
	_, err := ado.R().SetResult(&response).Get(fmt.Sprintf("/%s/_apis/pipelines", project))
	if err != nil {
		return nil, err
	}

	return response.Value, nil
}

func (ado *AzureDevopsClient) GetPipelineRuns(project string, pipeline Pipeline) ([]Run, error) {
	var runs Runs
	_, err := ado.R().SetResult(&runs).Get(fmt.Sprintf("/%s/_apis/pipelines/%d/runs", project, pipeline.ID))

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

func (ado *AzureDevopsClient) GetProjects() ([]Project, error) {
	var projects Projects
	_, err := ado.R().SetResult(&projects).Get("/_apis/projects")

	if err != nil {
		return nil, err
	}

	return projects.Value, nil
}
