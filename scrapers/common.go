package scrapers

import (
	"github.com/flanksource/config-db/scrapers/azure"
	"time"

	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/scrapers/aws"
	"github.com/flanksource/config-db/scrapers/azure/devops"
	"github.com/flanksource/config-db/scrapers/file"
	"github.com/flanksource/config-db/scrapers/github"
	"github.com/flanksource/config-db/scrapers/kubernetes"
	"github.com/flanksource/config-db/scrapers/sql"
	"github.com/flanksource/kommons"
	"github.com/flanksource/kommons/ktemplate"
)

// All is the scrappers registry
var All = []v1.Scraper{
	azure.Scraper{},
	aws.Scraper{},
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
	client, err := ctx.Kommons.GetClientset()
	if err != nil {
		return "", err
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
	templater := ktemplate.StructTemplater{
		Clientset: client,
		Values:    data,
		// access go values in template requires prefix everything with .
		// to support $(username) instead of $(.username) we add a function for each var
		ValueFunctions: true,
		DelimSets: []ktemplate.Delims{
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
		Username: kommons.EnvVar{
			Value: "",
		},
		Password: kommons.EnvVar{
			Value: "",
		},
	}
	// in case nil we are sending empty string values for username and password
	if auth == nil {
		return authentication, nil
	}
	_, username, err := ctx.Kommons.GetEnvValueFromCache(auth.Username, ctx.Namespace, 5*time.Minute)
	if err != nil {
		return nil, err
	}
	authentication.Username = kommons.EnvVar{
		Value: username,
	}
	_, password, err := ctx.Kommons.GetEnvValueFromCache(auth.Password, ctx.Namespace, 120*time.Second)
	if err != nil {
		return nil, err
	}
	authentication.Password = kommons.EnvVar{
		Value: password,
	}
	return authentication, err
}
