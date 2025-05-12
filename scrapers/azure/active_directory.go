package azure

import (
	"fmt"
	"strings"
	"time"

	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty/types"
	graphcore "github.com/microsoftgraph/msgraph-sdk-go-core"
	"github.com/microsoftgraph/msgraph-sdk-go/applications"
	"github.com/microsoftgraph/msgraph-sdk-go/auditlogs"
	msgraphModels "github.com/microsoftgraph/msgraph-sdk-go/models"
	"github.com/samber/lo"
)

// Include types for Azure Active Directory
const (
	IncludeAppRegistrations = "appRegistrations"
	IncludeUsers            = "users"
	IncludeGroups           = "groups"
	IncludeRoles            = "roles"
	IncludeAuthMethods      = "authMethods"
)

func (azure *Scraper) scrapeActiveDirectory() (v1.ScrapeResults, error) {
	results := v1.ScrapeResults{}
	results = append(results, azure.fetchAppRegistrations()...)
	results = append(results, azure.fetchUsers()...)
	results = append(results, azure.fetchGroups()...)
	results = append(results, azure.fetchRoles()...)
	results = append(results, azure.fetchAuthMethods()...)
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

// fetchLastLogin gets sign-in activity logs for a user
func (azure Scraper) fetchLastLogin(userID string) (*time.Time, error) {
	azure.ctx.Logger.V(3).Infof("fetching sign-in logs for user %s", userID)

	requestConfig := &auditlogs.SignInsRequestBuilderGetRequestConfiguration{
		QueryParameters: &auditlogs.SignInsRequestBuilderGetQueryParameters{
			Filter: lo.ToPtr(fmt.Sprintf("userId eq '%s'", userID)),
			Top:    lo.ToPtr(int32(1)), // Get last 1 sign-in
		},
	}

	signIns, err := azure.graphClient.AuditLogs().SignIns().Get(azure.ctx, requestConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch sign-in logs: %w", err)
	}

	if len(signIns.GetValue()) == 0 {
		return nil, nil
	}

	latestLogin := signIns.GetValue()[0].GetCreatedDateTime()
	return latestLogin, nil
}

func (azure Scraper) userToScrapeResult(user msgraphModels.Userable) v1.ScrapeResult {
	userID := lo.FromPtr(user.GetId())
	displayName := *user.GetDisplayName()

	latestLogin, err := azure.fetchLastLogin(userID)
	if err != nil {
		azure.ctx.Logger.Errorf("failed to fetch sign-in logs for user %s: %v", userID, err)
	}

	return v1.ScrapeResult{
		BaseScraper:    azure.config.BaseScraper,
		ID:             userID,
		Name:           displayName,
		Config:         user.GetBackingStore().Enumerate(),
		ConfigClass:    "User",
		Type:           ConfigTypePrefix + "User",
		LatestActivity: latestLogin,
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
		result := azure.groupToScrapeResult(group)
		members, err := azure.fetchGroupMembers(lo.FromPtr(group.GetId()))
		if err != nil {
			azure.ctx.Logger.Errorf("failed to fetch group members: %s", err)
		} else if len(members) > 0 {
			result.RelationshipResults = members
		}

		results = append(results, result)
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

// fetchRoles gets Azure AD roles in a tenant.
func (azure Scraper) fetchRoles() v1.ScrapeResults {
	if !azure.config.Includes(IncludeRoles) {
		return nil
	}

	azure.ctx.Logger.V(3).Infof("fetching roles for tenant %s", azure.config.TenantID)

	var results v1.ScrapeResults
	roles, err := azure.graphClient.RoleManagement().Directory().RoleDefinitions().Get(azure.ctx, nil)
	if err != nil {
		return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to fetch roles: %w", err)})
	}

	for _, role := range roles.GetValue() {
		results = append(results, azure.roleToScrapeResult(role))
	}

	pageIterator, err := graphcore.NewPageIterator[msgraphModels.UnifiedRoleDefinitionable](roles, azure.graphClient.GetAdapter(), nil)
	if err != nil {
		return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to create page iterator: %w", err)})
	}

	err = pageIterator.Iterate(azure.ctx, func(role msgraphModels.UnifiedRoleDefinitionable) bool {
		results = append(results, azure.roleToScrapeResult(role))
		return true
	})
	if err != nil {
		return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to iterate through pages: %w", err)})
	}

	return results
}

func (azure Scraper) roleToScrapeResult(role msgraphModels.UnifiedRoleDefinitionable) v1.ScrapeResult {
	roleID := lo.FromPtr(role.GetId())
	displayName := *role.GetDisplayName()

	return v1.ScrapeResult{
		BaseScraper: azure.config.BaseScraper,
		ID:          roleID,
		Name:        displayName,
		Config:      role.GetBackingStore().Enumerate(),
		ConfigClass: "Role",
		ScraperLess: lo.FromPtr(role.GetIsBuiltIn()), // built-in roles are common across tenants (i.e. they have the same global uid). They should be made scraper less just like aws regions.
		Type:        ConfigTypePrefix + "Role",
	}
}

// fetchGroupMembers gets members of an Azure AD group.
func (azure Scraper) fetchGroupMembers(groupID string) (v1.RelationshipResults, error) {
	if !azure.config.Includes(IncludeUsers) || !azure.config.Includes(IncludeGroups) {
		return nil, nil
	}

	var results v1.RelationshipResults
	members, err := azure.graphClient.Groups().ByGroupId(groupID).Members().Get(azure.ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch group members: %w", err)
	}

	pageIterator, err := graphcore.NewPageIterator[msgraphModels.DirectoryObjectable](members, azure.graphClient.GetAdapter(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create page iterator: %w", err)
	}

	err = pageIterator.Iterate(azure.ctx, func(member msgraphModels.DirectoryObjectable) bool {
		results = append(results, v1.RelationshipResult{
			RelatedExternalID: v1.ExternalID{ExternalID: lo.FromPtr(member.GetId()), ConfigType: ConfigTypePrefix + "User"},
			ConfigExternalID:  v1.ExternalID{ExternalID: groupID, ConfigType: ConfigTypePrefix + "Group"},
			Relationship:      "GroupUser",
		})
		return true
	})
	if err != nil {
		return nil, fmt.Errorf("failed to iterate through pages: %w", err)
	}

	return results, nil
}

// fetchAuthMethods gets authentication methods configured in Azure AD.
func (azure Scraper) fetchAuthMethods() v1.ScrapeResults {
	if !azure.config.Includes(IncludeAuthMethods) {
		return nil
	}

	azure.ctx.Logger.V(3).Infof("fetching authentication methods for tenant %s", azure.config.TenantID)

	var results v1.ScrapeResults
	authMethods, err := azure.graphClient.Policies().AuthenticationMethodsPolicy().Get(azure.ctx, nil)
	if err != nil {
		return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to fetch authentication methods: %w", err)})
	}

	methods := authMethods.GetAuthenticationMethodConfigurations()
	for _, method := range methods {
		methodID := lo.FromPtr(method.GetId())
		methodType := lo.FromPtr(method.GetOdataType())

		// Extract the method name from the OData type
		// Example: "#microsoft.graph.fido2AuthenticationMethodConfiguration"
		methodName := strings.TrimPrefix(methodType, "#microsoft.graph.")
		methodName = strings.TrimSuffix(methodName, "AuthenticationMethodConfiguration")
		methodName = strings.ToUpper(methodName)

		results = append(results, v1.ScrapeResult{
			BaseScraper: azure.config.BaseScraper,
			ScraperLess: true,
			ID:          methodID,
			Name:        methodName,
			Config:      method.GetBackingStore().Enumerate(),
			ConfigClass: "AuthenticationMethod",
			Type:        ConfigTypePrefix + "AuthenticationMethod",
		})
	}

	return results
}
