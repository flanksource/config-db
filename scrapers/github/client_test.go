package github

import (
	"os"
	"testing"

	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/kommons"
)

var testGithubApiClient = func() (*GitHubActionsClient, error) {
	textCtx := new(v1.ScrapeContext)
	ghToken := os.Getenv("GH_ACCESS_TOKEN")
	testGh := v1.GitHubActions{
		Owner:               "flanksource",
		Repository:          "config-db",
		PersonalAccessToken: kommons.EnvVar{Value: ghToken},
	}
	client, err := NewGitHubActionsClient(textCtx, testGh)
	if err != nil {
		return nil, err
	}

	return client, nil
}

var client *GitHubActionsClient
var workflows []Workflow

func init() {
	var err error
	client, err = testGithubApiClient()
	if err != nil {
		panic(err)
	}
}

func TestGetWorkFlows(t *testing.T) {
	var err error
	workflows, err = client.GetWorkflows()
	if err != nil {
		t.Fatalf("error was not expected %v", err)
	}
	// (TODO: basebandit) we could probably assert that there is something in the returned workflows slice
}

func TestGetWorkFlowRuns(t *testing.T) {
	for _, workflow := range workflows {
		_, err := client.GetWorkflowRuns(workflow.ID, 1)
		if err != nil {
			t.Fatalf("error was not expected %v", err)
		}

		// (TODO: basebandit) we could probably assert that there is something in the returned runs slice

	}
}
