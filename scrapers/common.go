package scrapers

import (
	"github.com/flanksource/config-db/api"
	"github.com/flanksource/config-db/scrapers/azure"
	"github.com/flanksource/config-db/scrapers/trivy"
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
var All = []api.Scraper{
	azure.Scraper{},
	aws.Scraper{},
	aws.CostScraper{},
	file.FileScraper{},
	kubernetes.KubernetesScraper{},
	kubernetes.KubernetesFileScraper{},
	devops.AzureDevopsScraper{},
	github.GithubActionsScraper{},
	sql.SqlScraper{},
	trivy.Scanner{},
}

func GetAuthValues(ctx api.ScrapeContext, auth *v1.Authentication) (*v1.Authentication, error) {
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
	username, err := ctx.GetEnvValueFromCache(auth.Username, ctx.GetNamespace())
	if err != nil {
		return nil, err
	}
	authentication.Username = types.EnvVar{
		ValueStatic: username,
	}
	password, err := ctx.GetEnvValueFromCache(auth.Password, ctx.GetNamespace())
	if err != nil {
		return nil, err
	}
	authentication.Password = types.EnvVar{
		ValueStatic: password,
	}
	return authentication, err
}
