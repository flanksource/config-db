package scrapers

import (
	"github.com/flanksource/commons/template"
	"github.com/flanksource/config-db/scrapers/azure"
	"github.com/flanksource/duty"
	"github.com/flanksource/duty/types"

	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/scrapers/aws"
	"github.com/flanksource/config-db/scrapers/azure/devops"
	"github.com/flanksource/config-db/scrapers/file"
	"github.com/flanksource/config-db/scrapers/github"
	"github.com/flanksource/config-db/scrapers/kubernetes"
	"github.com/flanksource/config-db/scrapers/sql"
)

// All is the scrappers registry
var All = []v1.Scraper{
	azure.Scraper{},
	aws.Scraper{},
	aws.CostScraper{},
	file.FileScraper{},
	kubernetes.KubernetesScraper{},
	kubernetes.KubernetesFileScraper{},
	devops.AzureDevopsScraper{},
	github.GithubActionsScraper{},
	sql.SqlScraper{},
}

func GetConnection(ctx *v1.ScrapeContext, conn *v1.Connection) (string, error) {
	// TODO: this function should not be necessary, each check should be templated out individual
	// however, the walk method only support high level values, not values from siblings.

	if conn.Authentication.IsEmpty() {
		return conn.Connection, nil
	}

	auth, err := GetAuthValues(ctx, &conn.Authentication)
	if err != nil {
		return "", err
	}

	var clone = *conn

	data := map[string]interface{}{
		"namespace": ctx.Namespace,
		"username":  auth.GetUsername(),
		"password":  auth.GetPassword(),
		"domain":    auth.GetDomain(),
	}
	templater := template.StructTemplater{
		Values: data,
		// access go values in template requires prefix everything with .
		// to support $(username) instead of $(.username) we add a function for each var
		ValueFunctions: true,
		DelimSets: []template.Delims{
			{Left: "{{", Right: "}}"},
			{Left: "$(", Right: ")"},
		},
		RequiredTag: "template",
	}
	if err := templater.Walk(clone); err != nil {
		return "", err
	}

	return clone.Connection, nil
}

func GetAuthValues(ctx *v1.ScrapeContext, auth *v1.Authentication) (*v1.Authentication, error) {
	authentication := &v1.Authentication{
		Username: types.EnvVar{
			ValueStatic: "",
		},
		Password: types.EnvVar{
			ValueStatic: "",
		},
	}
	// in case nil we are sending empty string values for username and password
	if auth == nil {
		return authentication, nil
	}
	username, err := duty.GetEnvValueFromCache(ctx.Kubernetes, auth.Username, ctx.Namespace)
	if err != nil {
		return nil, err
	}
	authentication.Username = types.EnvVar{
		ValueStatic: username,
	}
	password, err := duty.GetEnvValueFromCache(ctx.Kubernetes, auth.Password, ctx.Namespace)
	if err != nil {
		return nil, err
	}
	authentication.Password = types.EnvVar{
		ValueStatic: password,
	}
	return authentication, err
}
