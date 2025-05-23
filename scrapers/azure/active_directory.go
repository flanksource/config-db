package azure

import (
	"fmt"

	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty/models"
	"github.com/flanksource/duty/query"
	"github.com/flanksource/duty/types"
	"github.com/google/uuid"
	graphcore "github.com/microsoftgraph/msgraph-sdk-go-core"
	"github.com/microsoftgraph/msgraph-sdk-go/applications"
	msgraphModels "github.com/microsoftgraph/msgraph-sdk-go/models"
	"github.com/microsoftgraph/msgraph-sdk-go/users"
	"github.com/samber/lo"
)

// Include types for Azure Active Directory
const (
	IncludeAppRegistrations   = "appRegistrations"
	IncludeUsers              = "users"
	IncludeGroups             = "groups"
	IncludeRoles              = "roles"
	IncludeAuthMethods        = "authMethods"
	IncludeAccessReviews      = "accessReviews"
	IncludeEnterpriseApps     = "enterpriseApps"
	IncludeAppRoleAssignments = "appRoleAssignments"
)

const (
	EnterpriseApplicationType = "EnterpriseApplication"
)

func (azure *Scraper) scrapeActiveDirectory() (v1.ScrapeResults, error) {
	results := v1.ScrapeResults{}
	results = append(results, azure.fetchUsers()...)
	results = append(results, azure.fetchGroups()...)

	results = append(results, azure.fetchAppRegistrations()...)
	results = append(results, azure.fetchEnterpriseApplications()...)
	results = append(results, azure.fetchAllAppRoleAssignments(azure.config.AppRoleAssignments)...)

	results = append(results, azure.fetchAuthMethods()...)
	return results, nil
}

func (azure Scraper) fetchAllAppRoleAssignments(selector types.ResourceSelectors) v1.ScrapeResults {
	if !azure.config.Includes(IncludeAppRoleAssignments) && len(azure.config.Include) > 0 {
		return nil
	}

	if len(selector) == 0 {
		// We'll never fetch role assignments for all apps.
		// A selector must be provided.
		return nil
	}

	selectors := lo.Map(selector, func(s types.ResourceSelector, _ int) types.ResourceSelector {
		s.Types = []string{ConfigTypePrefix + EnterpriseApplicationType}
		return s
	})

	var results v1.ScrapeResults
	appIDs, err := query.FindConfigIDsByResourceSelector(azure.ctx.DutyContext(), -1, selectors...)
	if err != nil {
		azure.ctx.Logger.Errorf("failed to find config IDs by resource selector: %v", err)
		return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to find config IDs by resource selector: %w", err)})
	}

	// TODO: make this work with enterprise applications that were fetched in this run.
	// v1.ScrapeResult must be made types.ResourceSelectable
	for _, appID := range appIDs {
		if configAccesses := azure.fetchAppRoleAssignments(appID); len(configAccesses) > 0 {
			results = append(results, configAccesses...)
		}
	}

	return results
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
	appID := lo.FromPtr(app.GetAppId())
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

func (azure Scraper) fetchAppRoleAssignments(spID uuid.UUID) v1.ScrapeResults {
	var results v1.ScrapeResults
	assignments, err := azure.graphClient.ServicePrincipals().ByServicePrincipalId(spID.String()).AppRoleAssignedTo().Get(azure.ctx, nil)
	if err != nil {
		return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to fetch app role assignments for service principal %s: %w", spID, err)})
	}

	assignmentIterator, err := graphcore.NewPageIterator[msgraphModels.AppRoleAssignmentable](assignments, azure.graphClient.GetAdapter(), nil)
	if err != nil {
		return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to create assignment iterator for service principal %s: %w", spID, err)})
	}

	var result v1.ScrapeResult
	err = assignmentIterator.Iterate(azure.ctx, func(assignment msgraphModels.AppRoleAssignmentable) bool {
		principalType := lo.FromPtr(assignment.GetPrincipalType())
		assignmentID := lo.FromPtr(assignment.GetId())

		switch principalType {
		case "User":
			result.ConfigAccess = append(result.ConfigAccess, models.ConfigAccess{
				ID:             assignmentID,
				ExternalUserID: assignment.GetPrincipalId(),
				ExternalRoleID: assignment.GetResourceId(),
				ConfigID:       spID,
				ScraperID:      lo.FromPtr(azure.ctx.ScrapeConfig().GetPersistedID()),
				CreatedAt:      lo.FromPtr(assignment.GetCreatedDateTime()),
				DeletedAt:      assignment.GetDeletedDateTime(),
			})
		case "Group":
			result.ConfigAccess = append(result.ConfigAccess, models.ConfigAccess{
				ID:              assignmentID,
				ExternalGroupID: assignment.GetPrincipalId(),
				ExternalRoleID:  assignment.GetResourceId(),
				ConfigID:        spID,
				ScraperID:       lo.FromPtr(azure.ctx.ScrapeConfig().GetPersistedID()),
				CreatedAt:       lo.FromPtr(assignment.GetCreatedDateTime()),
				DeletedAt:       assignment.GetDeletedDateTime(),
			})
		default:
			logger.Warnf("unknown principal type %s for app role assignment %s", principalType, assignmentID)
		}

		return true
	})
	if err != nil {
		return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to iterate through app role assignments: %w", err)})
	}

	results = append(results, result)
	return results
}

// fetchEnterpriseApplications gets all enterprise applications (service principals) and their assigned users
func (azure Scraper) fetchEnterpriseApplications() v1.ScrapeResults {
	if !azure.config.Includes(IncludeEnterpriseApps) {
		return nil
	}

	azure.ctx.Logger.V(3).Infof("fetching enterprise applications for tenant %s", azure.config.TenantID)

	var results v1.ScrapeResults

	servicePrincipals, err := azure.graphClient.ServicePrincipals().Get(azure.ctx, nil)
	if err != nil {
		return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to fetch service principals: %w", err)})
	}

	pageIterator, err := graphcore.NewPageIterator[msgraphModels.ServicePrincipalable](servicePrincipals, azure.graphClient.GetAdapter(), msgraphModels.CreateServicePrincipalCollectionResponseFromDiscriminatorValue)
	if err != nil {
		return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to create page iterator: %w", err)})
	}

	err = pageIterator.Iterate(azure.ctx, func(sp msgraphModels.ServicePrincipalable) bool {
		spID := lo.FromPtr(sp.GetId())
		appID := lo.FromPtr(sp.GetAppId())
		displayName := *sp.GetDisplayName()

		if orgID := sp.GetAppOwnerOrganizationId(); orgID == nil {
			return true
		} else if orgID.String() != azure.config.TenantID {
			return true // there are a lot of built-in service principals. Only process the ones for this tenant
		}

		result := v1.ScrapeResult{
			BaseScraper: azure.config.BaseScraper,
			ID:          spID,
			Name:        displayName,
			Config:      sp.GetBackingStore().Enumerate(),
			ConfigClass: EnterpriseApplicationType,
			Type:        ConfigTypePrefix + EnterpriseApplicationType,
			RelationshipResults: []v1.RelationshipResult{{
				RelatedConfigID: spID,
				ConfigID:        appID,
				Relationship:    "AppServicePrincipal",
			}},
		}
		results = append(results, result)

		return true
	})

	if err != nil {
		return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to iterate through service principals: %w", err)})
	}

	return results
}

// fetchUsers gets Azure AD users in a tenant.
func (azure Scraper) fetchUsers() v1.ScrapeResults {
	if !azure.config.Includes(IncludeUsers) {
		return nil
	}

	azure.ctx.Logger.V(3).Infof("fetching users for tenant %s", azure.config.TenantID)

	var results v1.ScrapeResults

	// Specify the fields to select
	queryParams := &users.UsersRequestBuilderGetQueryParameters{
		Select: []string{"id", "displayName", "givenName", "mail", "createdDateTime", "deletedDateTime"},
	}
	requestConfig := &users.UsersRequestBuilderGetRequestConfiguration{
		QueryParameters: queryParams,
	}

	users, err := azure.graphClient.Users().Get(azure.ctx, requestConfig) // Pass requestConfig here
	if err != nil {
		return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to fetch users: %w", err)})
	}

	pageIterator, err := graphcore.NewPageIterator[msgraphModels.Userable](users, azure.graphClient.GetAdapter(), nil)
	if err != nil {
		return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to create page iterator: %w", err)})
	}

	err = pageIterator.Iterate(azure.ctx, func(user msgraphModels.Userable) bool {
		scrapeResult, err := azure.userToScrapeResult(user)
		if err != nil {
			azure.ctx.Logger.Errorf("failed to convert user to scrape result: %v", err)
			return true
		}

		results = append(results, scrapeResult)
		return true
	})

	if err != nil {
		return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to iterate through pages: %w", err)})
	}

	return results
}

func (azure Scraper) userToScrapeResult(user msgraphModels.Userable) (v1.ScrapeResult, error) {
	displayName := *user.GetDisplayName()

	userID, err := uuid.Parse(lo.FromPtr(user.GetId()))
	if err != nil {
		azure.ctx.Logger.Errorf("failed to parse user ID %s: %v", lo.FromPtr(user.GetId()), err)
		return v1.ScrapeResult{}, err
	}

	externalUser := models.ExternalUser{
		ID:        userID,
		Name:      displayName,
		ScraperID: lo.FromPtr(azure.ctx.ScrapeConfig().GetPersistedID()),
		AccountID: azure.config.TenantID,
		UserType:  "User",
		Email:     user.GetMail(),
		CreatedAt: lo.FromPtr(user.GetCreatedDateTime()),
		DeletedAt: user.GetDeletedDateTime(),
	}

	return v1.ScrapeResult{
		BaseScraper:   azure.config.BaseScraper,
		ExternalUsers: []models.ExternalUser{externalUser},
	}, nil
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
		scrapeResult, err := azure.groupToScrapeResult(group)
		if err != nil {
			azure.ctx.Logger.Errorf("failed to convert group to scrape result: %v", err)
			continue
		}

		results = append(results, scrapeResult)
	}

	pageIterator, err := graphcore.NewPageIterator[msgraphModels.Groupable](groups, azure.graphClient.GetAdapter(), nil)
	if err != nil {
		return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to create page iterator: %w", err)})
	}

	err = pageIterator.Iterate(azure.ctx, func(group msgraphModels.Groupable) bool {
		result, err := azure.groupToScrapeResult(group)
		if err != nil {
			azure.ctx.Logger.Errorf("failed to convert group to scrape result: %v", err)
			return true
		}

		if members, err := azure.fetchGroupMembers(lo.FromPtr(group.GetId())); err != nil {
			azure.ctx.Logger.Errorf("failed to fetch group members: %s", err)
		} else if len(members) > 0 {
			result.ExternalUserGroups = members
		}

		results = append(results, result)
		return true
	})
	if err != nil {
		return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to iterate through pages: %w", err)})
	}

	return results
}

func (azure Scraper) groupToScrapeResult(group msgraphModels.Groupable) (v1.ScrapeResult, error) {
	groupID, err := uuid.Parse(lo.FromPtr(group.GetId()))
	if err != nil {
		return v1.ScrapeResult{}, fmt.Errorf("failed to parse group ID %s: %w", lo.FromPtr(group.GetId()), err)
	}

	externalGroup := models.ExternalGroup{
		ID:        groupID,
		AccountID: azure.config.TenantID,
		ScraperID: lo.FromPtr(azure.ctx.ScrapeConfig().GetPersistedID()),
		Name:      lo.FromPtr(group.GetDisplayName()),
		CreatedAt: lo.FromPtr(group.GetCreatedDateTime()),
		DeletedAt: group.GetDeletedDateTime(),
	}

	if gt := group.GetGroupTypes(); len(gt) > 0 {
		externalGroup.GroupType = gt[0]
	}

	return v1.ScrapeResult{
		ExternalGroups: []models.ExternalGroup{externalGroup},
	}, nil
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

		results = append(results, v1.ScrapeResult{
			BaseScraper: azure.config.BaseScraper,
			ScraperLess: true,
			ID:          methodID,
			Name:        methodID,
			Config:      method.GetBackingStore().Enumerate(),
			Status:      lo.Ternary(lo.FromPtr(method.GetState()) == msgraphModels.ENABLED_AUTHENTICATIONMETHODSTATE, "Enabled", "Disabled"),
			Health:      lo.Ternary(lo.FromPtr(method.GetState()) == msgraphModels.ENABLED_AUTHENTICATIONMETHODSTATE, models.HealthHealthy, models.HealthUnknown),
			ConfigClass: "AuthenticationMethod",
			Type:        ConfigTypePrefix + "AuthenticationMethod",
		})
	}

	return results
}

// fetchGroupMembers gets members of an Azure AD group.
func (azure Scraper) fetchGroupMembers(groupID string) ([]models.ExternalUserGroup, error) {
	if !azure.config.Includes(IncludeUsers) || !azure.config.Includes(IncludeGroups) {
		return nil, nil
	}

	groupUUID, err := uuid.Parse(groupID)
	if err != nil {
		return nil, fmt.Errorf("failed to parse group ID %s: %w", groupID, err)
	}

	var results []models.ExternalUserGroup
	members, err := azure.graphClient.Groups().ByGroupId(groupID).Members().Get(azure.ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch group members: %w", err)
	}

	pageIterator, err := graphcore.NewPageIterator[msgraphModels.DirectoryObjectable](members, azure.graphClient.GetAdapter(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create page iterator: %w", err)
	}

	err = pageIterator.Iterate(azure.ctx, func(member msgraphModels.DirectoryObjectable) bool {
		memberID, err := uuid.Parse(lo.FromPtr(member.GetId()))
		if err != nil {
			azure.ctx.Logger.Errorf("failed to parse azure group member ID %s: %v", lo.FromPtr(member.GetId()), err)
			return true
		}

		ug := models.ExternalUserGroup{
			ExternalUserID:  memberID,
			ExternalGroupID: groupUUID,
			// CreatedAt: , // TODO: The API doesn't return created date
			// DeletedAt: member.GetDeletedDateTime(), // TODO: The API doesn't return deleted date
		}
		results = append(results, ug)
		return true
	})
	if err != nil {
		return nil, fmt.Errorf("failed to iterate through pages: %w", err)
	}

	return results, nil
}
