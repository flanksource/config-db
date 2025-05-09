package azure

import (
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty/types"
	msgraphsdkgo "github.com/microsoftgraph/msgraph-sdk-go"
	graphcore "github.com/microsoftgraph/msgraph-sdk-go-core"
	"github.com/microsoftgraph/msgraph-sdk-go/applications"
	msgraphModels "github.com/microsoftgraph/msgraph-sdk-go/models"
)

// Include types for Azure Active Directory
const (
	IncludeActiveDirectory  = "activeDirectory"
	IncludeAppRegistrations = "appRegistrations"
)

func (azure Scraper) scrapeActiveDirectory() v1.ScrapeResults {
	if !azure.config.Includes(IncludeActiveDirectory) {
		return nil
	}

	results := v1.ScrapeResults{}
	results = append(results, azure.fetchAppRegistrations()...)
	return results
}

func (azure Scraper) getGraphClient() (*msgraphsdkgo.GraphServiceClient, error) {
	graphCred, err := azidentity.NewClientSecretCredential(azure.config.TenantID, azure.config.ClientID.ValueStatic, azure.config.ClientSecret.ValueStatic, nil)
	if err != nil {
		return nil, err
	}

	return msgraphsdkgo.NewGraphServiceClientWithCredentials(graphCred, []string{"https://graph.microsoft.com/.default"})
}

// fetchAppRegistrations gets Azure App Registrations in a tenant.
func (azure Scraper) fetchAppRegistrations() v1.ScrapeResults {
	if !azure.config.Includes(IncludeAppRegistrations) {
		return nil
	}

	azure.ctx.Logger.V(3).Infof("fetching app registrations for tenant %s", azure.config.TenantID)

	var results v1.ScrapeResults
	graphClient, err := azure.getGraphClient()
	if err != nil {
		return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to create graph client: %w", err)})
	}

	// Get apps with pagination
	apps, err := graphClient.Applications().Get(azure.ctx, nil)
	if err != nil {
		return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to fetch app registrations: %w", err)})
	}

	// Process the first page
	for _, app := range apps.GetValue() {
		results = append(results, azure.appToScrapeResult(app.(*msgraphModels.Application)))
	}

	// Process additional pages if they exist
	pageIterator, err := graphcore.NewPageIterator[*msgraphModels.Application](apps, graphClient.GetAdapter(), applications.CreateDeltaGetResponseFromDiscriminatorValue)
	if err != nil {
		return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to create page iterator: %w", err)})
	}

	err = pageIterator.Iterate(azure.ctx, func(app *msgraphModels.Application) bool {
		results = append(results, azure.appToScrapeResult(app))

		return true
	})

	if err != nil {
		return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to iterate through pages: %w", err)})
	}

	return results
}

func (azure Scraper) appToScrapeResult(app *msgraphModels.Application) v1.ScrapeResult {
	appID := *app.GetId()
	displayName := *app.GetDisplayName()

	return v1.ScrapeResult{
		BaseScraper: azure.config.BaseScraper,
		ID:          fmt.Sprintf("/tenants/%s/applications/%s", azure.config.TenantID, appID),
		Name:        displayName,
		Config:      app,
		ConfigClass: "AppRegistration",
		Type:        ConfigTypePrefix + "AppRegistration",
		Properties: []*types.Property{
			{
				Name: "URL",
				Icon: ConfigTypePrefix + "AppRegistration",
				Links: []types.Link{
					{
						Text: types.Text{Label: "Console"},
						URL:  fmt.Sprintf("https://portal.azure.com/#blade/Microsoft_AAD_RegisteredApps/ApplicationMenuBlade/Overview/appId/%s", *app.GetAppId()),
					},
				},
			},
		},
		Tags: []v1.Tag{
			{
				Name:  "appID",
				Value: *app.GetAppId(),
			},
		},
	}
}
