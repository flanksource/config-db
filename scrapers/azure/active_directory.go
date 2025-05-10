package azure

import (
	"fmt"

	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty/types"
	graphcore "github.com/microsoftgraph/msgraph-sdk-go-core"
	"github.com/microsoftgraph/msgraph-sdk-go/applications"
	msgraphModels "github.com/microsoftgraph/msgraph-sdk-go/models"
	"github.com/samber/lo"
)

// Include types for Azure Active Directory
const (
	IncludeAppRegistrations = "appRegistrations"
	IncludeUsers            = "users"
	IncludeGroups           = "groups"
)

func (azure *Scraper) scrapeActiveDirectory() (v1.ScrapeResults, error) {
	results := v1.ScrapeResults{}
	results = append(results, azure.fetchAppRegistrations()...)
	results = append(results, azure.fetchUsers()...)
	results = append(results, azure.fetchGroups()...)
	return results, nil
}

// fetchAppRegistrations gets Azure App Registrations in a tenant.
func (azure Scraper) fetchAppRegistrations() v1.ScrapeResults {
	if !azure.config.Includes(IncludeAppRegistrations) {
		return nil
	}

	azure.ctx.Logger.V(3).Infof("fetching app registrations for tenant %s", azure.config.TenantID)

	var results v1.ScrapeResults

	apps, err := azure.graphClient.Applications().Get(azure.ctx, nil)
	if err != nil {
		return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to fetch app registrations: %w", err)})
	}

	for _, app := range apps.GetValue() {
		results = append(results, azure.appToScrapeResult(app.(*msgraphModels.Application)))
	}

	pageIterator, err := graphcore.NewPageIterator[*msgraphModels.Application](apps, azure.graphClient.GetAdapter(), applications.CreateDeltaGetResponseFromDiscriminatorValue)
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
	appID := lo.FromPtr(app.GetId())
	displayName := *app.GetDisplayName()

	return v1.ScrapeResult{
		BaseScraper: azure.config.BaseScraper,
		ID:          appID,
		Name:        displayName,
		Config:      app.GetBackingStore().Enumerate(),
		ConfigClass: "AppRegistration",
		Type:        ConfigTypePrefix + "AppRegistration",
		Properties: []*types.Property{
			{
				Name: "URL",
				Icon: ConfigTypePrefix + "AppRegistration",
				Links: []types.Link{
					{
						Text: types.Text{Label: "Console"},
						URL:  fmt.Sprintf("https://portal.azure.com/#blade/Microsoft_AAD_RegisteredApps/ApplicationMenuBlade/Overview/appId/%s", appID),
					},
				},
			},
		},
	}
}

// fetchUsers gets Azure AD users in a tenant.
func (azure Scraper) fetchUsers() v1.ScrapeResults {
	if !azure.config.Includes(IncludeUsers) {
		return nil
	}

	azure.ctx.Logger.V(3).Infof("fetching users for tenant %s", azure.config.TenantID)

	var results v1.ScrapeResults

	users, err := azure.graphClient.Users().Get(azure.ctx, nil)
	if err != nil {
		return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to fetch users: %w", err)})
	}

	for _, user := range users.GetValue() {
		results = append(results, azure.userToScrapeResult(user))
	}

	pageIterator, err := graphcore.NewPageIterator[msgraphModels.Userable](users, azure.graphClient.GetAdapter(), nil)
	if err != nil {
		return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to create page iterator: %w", err)})
	}

	err = pageIterator.Iterate(azure.ctx, func(user msgraphModels.Userable) bool {
		results = append(results, azure.userToScrapeResult(user))
		return true
	})

	if err != nil {
		return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to iterate through pages: %w", err)})
	}

	return results
}

func (azure Scraper) userToScrapeResult(user msgraphModels.Userable) v1.ScrapeResult {
	userID := lo.FromPtr(user.GetId())
	displayName := *user.GetDisplayName()

	return v1.ScrapeResult{
		BaseScraper: azure.config.BaseScraper,
		ID:          userID,
		Name:        displayName,
		Config:      user.GetBackingStore().Enumerate(),
		ConfigClass: "User",
		Type:        ConfigTypePrefix + "User",
		Properties: []*types.Property{
			{
				Name: "URL",
				Icon: ConfigTypePrefix + "User",
				Links: []types.Link{
					{
						Text: types.Text{Label: "Console"},
						URL:  fmt.Sprintf("https://portal.azure.com/#view/Microsoft_AAD_UsersAndTenants/UserProfileMenuBlade/~/overview/userId/%s/hidePreviewBanner~/true", userID),
					},
				},
			},
		},
	}
}

// fetchGroups gets Azure AD groups in a tenant.
func (azure Scraper) fetchGroups() v1.ScrapeResults {
	if !azure.config.Includes(IncludeGroups) {
		return nil
	}

	azure.ctx.Logger.V(3).Infof("fetching groups for tenant %s", azure.config.TenantID)

	var results v1.ScrapeResults
	groups, err := azure.graphClient.Groups().Get(azure.ctx, nil)
	if err != nil {
		return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to fetch groups: %w", err)})
	}

	for _, group := range groups.GetValue() {
		results = append(results, azure.groupToScrapeResult(group))
	}

	pageIterator, err := graphcore.NewPageIterator[msgraphModels.Groupable](groups, azure.graphClient.GetAdapter(), nil)
	if err != nil {
		return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to create page iterator: %w", err)})
	}

	err = pageIterator.Iterate(azure.ctx, func(group msgraphModels.Groupable) bool {
		results = append(results, azure.groupToScrapeResult(group))
		return true
	})
	if err != nil {
		return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to iterate through pages: %w", err)})
	}

	return results
}

func (azure Scraper) groupToScrapeResult(group msgraphModels.Groupable) v1.ScrapeResult {
	groupID := lo.FromPtr(group.GetId())
	displayName := *group.GetDisplayName()

	return v1.ScrapeResult{
		BaseScraper: azure.config.BaseScraper,
		ID:          groupID,
		Name:        displayName,
		Config:      group.GetBackingStore().Enumerate(),
		ConfigClass: "Group",
		Type:        ConfigTypePrefix + "Group",
		Properties: []*types.Property{
			{
				Name: "URL",
				Icon: ConfigTypePrefix + "Group",
				Links: []types.Link{
					{
						Text: types.Text{Label: "Console"},
						URL:  fmt.Sprintf("https://portal.azure.com/#view/Microsoft_AAD_UsersAndTenants/GroupMenuBlade/~/Properties/groupId/%s/hidePreviewBanner~/true", groupID),
					},
				},
			},
		},
	}
}
