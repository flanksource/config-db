package azure

import (
	"encoding/base64"
	"fmt"
	"sort"
	"strings"

	"github.com/flanksource/commons/collections"
	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty/models"
	"github.com/flanksource/duty/query"
	"github.com/flanksource/duty/types"
	"github.com/google/uuid"
	"github.com/lib/pq"
	kiota "github.com/microsoft/kiota-abstractions-go"
	graphcore "github.com/microsoftgraph/msgraph-sdk-go-core"
	"github.com/microsoftgraph/msgraph-sdk-go/applications"
	graphgroups "github.com/microsoftgraph/msgraph-sdk-go/groups"
	msgraphModels "github.com/microsoftgraph/msgraph-sdk-go/models"
	"github.com/microsoftgraph/msgraph-sdk-go/serviceprincipals"
	"github.com/microsoftgraph/msgraph-sdk-go/users"
	"github.com/samber/lo"
)

// Include types for Entra
const (
	IncludeAuthMethods = "authMethods"
	IncludeAppRoles    = "appRoles"
	IncludeEntra       = "entra"
)

const (
	EnterpriseApplicationType = "EnterpriseApplication"
)

const graphIDInFilterChunkSize = 15

func (azure *Scraper) scrapeEntra() (v1.ScrapeResults, error) {
	if !azure.config.Includes(IncludeEntra) {
		return nil, nil
	}

	if azure.config.Entra == nil {
		azure.config.Entra = &v1.Entra{}
	}

	results := v1.ScrapeResults{}
	results = append(results, azure.fetchUsers(azure.config.Entra.Users)...)
	results = append(results, azure.fetchGroups(azure.config.Entra.Groups)...)

	results = append(results, azure.fetchAppRegistrations(azure.config.Entra.AppRegistrations)...)
	results = append(results, azure.fetchEnterpriseApplications(azure.config.Entra.EnterpriseApps)...)
	results = append(results, azure.fetchAllAppRoleAssignments(azure.config.Entra.AppRoleAssignments)...)

	results = append(results, azure.fetchAuthMethods()...)
	return results, nil
}

func (azure Scraper) fetchAppRoles(appObjectID string) v1.ScrapeResults {
	if !azure.config.Includes(IncludeAppRoles) {
		return nil
	}

	azure.ctx.Logger.V(3).Infof("fetching app roles for app %s", appObjectID)

	var results v1.ScrapeResults
	app, err := azure.graphClient.Applications().ByApplicationId(appObjectID).Get(azure.ctx, nil)
	if err != nil {
		return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to fetch application: %w", err)})
	}

	appRoles := app.GetAppRoles()
	for _, role := range appRoles {
		if role.GetId() == nil {
			continue
		}

		// AWS-SSO app roles are represented from assignment metadata, where
		// the target AWS account and IAM role ARN are both available.
		if _, _, ok := parseAWSSSOAppRoleValue(lo.FromPtr(role.GetValue())); ok {
			continue
		}

		results = append(results, v1.ScrapeResult{
			BaseScraper: azure.config.BaseScraper,
			ExternalRoles: []models.ExternalRole{
				{
					ID:          lo.FromPtr(role.GetId()),
					Tenant:      azure.config.TenantID,
					ScraperID:   azure.ctx.ScrapeConfig().GetPersistedID(),
					Name:        lo.FromPtr(role.GetDisplayName()),
					Description: lo.FromPtr(role.GetDescription()),
				},
			},
		})
	}

	return results
}

func (azure Scraper) fetchAllAppRoleAssignments(selectors types.ResourceSelectors) v1.ScrapeResults {
	if len(selectors) == 0 {
		// We'll never fetch role assignments for all apps.
		// A selector must be provided.
		return nil
	}

	var results v1.ScrapeResults
	// Per-selector loop: on this selector the `name` field is overloaded to
	// hold a role-displayName filter (Role1,Role2,!role3). It is NOT an
	// app-name filter, so we strip it before resolving apps; app selection
	// is driven by Types/TagSelector/etc. on the same selector.
	for _, sel := range selectors {
		roleFilter := splitRoleFilter(sel.Name)
		sel.Name = ""
		sel.Types = []string{ConfigTypePrefix + EnterpriseApplicationType}

		appIDs, err := query.FindConfigIDsByResourceSelector(azure.ctx.DutyContext(), -1, sel)
		if err != nil {
			azure.ctx.Logger.Errorf("failed to find config IDs by resource selector: %v", err)
			results = append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to find config IDs by resource selector: %w", err)})
			continue
		}

		// TODO: make this work with enterprise applications that were fetched in this run.
		// v1.ScrapeResult must be made types.ResourceSelectable
		for _, appID := range appIDs {
			if configAccesses := azure.fetchAppRoleAssignments(appID, roleFilter); len(configAccesses) > 0 {
				results = append(results, configAccesses...)
			}
		}
	}

	return results
}

// selectorRoleFilter resolves the role-displayName filter to apply when
// scraping role assignments for sp during the AppRegistration auto-fan-out
// path. It walks the user-configured appRoleAssignments selectors and returns
// the first one whose non-name predicates (Types/TagSelector/etc.) match sp;
// `name` on those selectors is the role filter, not an app filter, so it's
// stripped before matching. Returns nil when no selector matches, which
// preserves the legacy unfiltered fan-out.
func selectorRoleFilter(selectors []types.ResourceSelector, sp v1.ScrapeResult) []string {
	for _, sel := range selectors {
		match := sel
		match.Name = ""
		if !(types.ResourceSelectors{match}).Matches(sp) {
			continue
		}
		return splitRoleFilter(sel.Name)
	}
	return nil
}

// splitRoleFilter parses the comma-separated role-displayName filter from a
// selector's `name` field (e.g. "Admin,Reader,!Guest"). Returns nil for an
// empty input so callers can use len() == 0 as the "no filter" sentinel.
// Whitespace and `!` exclusion are handled downstream by collections.MatchItems.
func splitRoleFilter(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// fetchAppRegistrations gets Azure App Registrations in a tenant.
func (azure Scraper) fetchAppRegistrations(selectors types.ResourceSelectors) v1.ScrapeResults {
	if len(selectors) == 0 {
		return nil
	}

	azure.ctx.Logger.V(3).Infof("fetching app registrations for tenant %s", azure.config.TenantID)

	var results v1.ScrapeResults

	requestParameters := &applications.ApplicationsRequestBuilderGetQueryParameters{
		Select: []string{"id", "appId", "displayName", "passwordCredentials", "keyCredentials"},
	}
	requestConfig := &applications.ApplicationsRequestBuilderGetRequestConfiguration{
		QueryParameters: requestParameters,
	}

	apps, err := azure.graphClient.Applications().Get(azure.ctx, requestConfig)
	if err != nil {
		return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to fetch app registrations: %w", err)})
	}

	pageIterator, err := graphcore.NewPageIterator[*msgraphModels.Application](apps, azure.graphClient.GetAdapter(), applications.CreateDeltaGetResponseFromDiscriminatorValue)
	if err != nil {
		return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to create page iterator: %w", err)})
	}

	err = pageIterator.Iterate(azure.ctx, func(app *msgraphModels.Application) bool {
		appScrapeResult := azure.appToScrapeResult(app)
		if !selectors.Matches(appScrapeResult) {
			return true
		}
		results = append(results, appScrapeResult)

		appRegAppID := lo.FromPtr(app.GetAppId())
		for _, pc := range app.GetPasswordCredentials() {
			results = append(results, azure.passwordCredentialToScrapeResult(pc, appRegAppID))
		}

		for _, kc := range app.GetKeyCredentials() {
			results = append(results, azure.keyCredentialToScrapeResult(kc, appRegAppID))
		}

		results = append(results, azure.fetchAppRoles(lo.FromPtr(app.GetId()))...)

		// Auto-scrape the matching Enterprise Application (service principal)
		// and its role assignments. Graph exposes role assignments only on the
		// SP side, so fetching the AppRegistration without its SP would never
		// yield config_access rows.
		if spResult, spID, ok := azure.fetchServicePrincipalByAppID(appRegAppID); ok {
			results = append(results, spResult)
			var roleFilter []string
			if azure.config.Entra != nil {
				roleFilter = selectorRoleFilter(azure.config.Entra.AppRoleAssignments, spResult)
			}
			results = append(results, azure.fetchAppRoleAssignments(spID, roleFilter)...)
		}
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

func (azure Scraper) passwordCredentialToScrapeResult(cred msgraphModels.PasswordCredentialable, appRegAppID string) v1.ScrapeResult {
	return v1.ScrapeResult{
		BaseScraper: azure.config.BaseScraper,
		ID:          lo.FromPtr(cred.GetKeyId()).String(),
		Name:        lo.FromPtr(cred.GetDisplayName()),
		ConfigClass: "ClientSecret",
		Type:        ConfigTypePrefix + "AppRegistration::ClientSecret",
		Config:      cred.GetBackingStore().Enumerate(),
		Parents: []v1.ConfigExternalKey{
			{
				ExternalID: appRegAppID,
				Type:       ConfigTypePrefix + "AppRegistration",
			},
		},
	}
}

func (azure Scraper) keyCredentialToScrapeResult(cred msgraphModels.KeyCredentialable, appRegAppID string) v1.ScrapeResult {
	config := cred.GetBackingStore().Enumerate()

	// Key ID and custom key identifier are base64 encoded as they contain non-unicode characters.
	if keyID, ok := config["keyId"].([]byte); ok {
		config["keyId"] = base64.StdEncoding.EncodeToString(keyID)
	}
	if customKeyIdentifier, ok := config["customKeyIdentifier"].([]byte); ok {
		config["customKeyIdentifier"] = base64.StdEncoding.EncodeToString(customKeyIdentifier)
	}

	return v1.ScrapeResult{
		BaseScraper: azure.config.BaseScraper,
		ID:          lo.FromPtr(cred.GetKeyId()).String(),
		Name:        lo.FromPtr(cred.GetDisplayName()),
		ConfigClass: "ClientCertificate",
		Type:        ConfigTypePrefix + "AppRegistration::Certificate",
		Config:      config,
		Parents: []v1.ConfigExternalKey{
			{
				ExternalID: appRegAppID,
				Type:       ConfigTypePrefix + "AppRegistration",
			},
		},
	}
}

// fetchServicePrincipalByAppID resolves the service principal whose appId equals
// appID (i.e. the Enterprise Application matching an AppRegistration). Returns
// ok=false (without error) when the tenant has no SP for this app — that's
// expected for multi-tenant apps owned by a different tenant.
func (azure Scraper) fetchServicePrincipalByAppID(appID string) (v1.ScrapeResult, uuid.UUID, bool) {
	filter := fmt.Sprintf("appId eq '%s'", appID)
	resp, err := azure.graphClient.ServicePrincipals().Get(azure.ctx, &serviceprincipals.ServicePrincipalsRequestBuilderGetRequestConfiguration{
		QueryParameters: &serviceprincipals.ServicePrincipalsRequestBuilderGetQueryParameters{Filter: &filter},
	})
	if err != nil {
		logger.Warnf("failed to look up service principal for appId %s: %v", appID, err)
		return v1.ScrapeResult{}, uuid.Nil, false
	}
	sps := resp.GetValue()
	if len(sps) == 0 {
		return v1.ScrapeResult{}, uuid.Nil, false
	}
	sp := sps[0]
	spID, err := uuid.Parse(lo.FromPtr(sp.GetId()))
	if err != nil {
		logger.Warnf("service principal for appId %s has non-uuid id %q: %v", appID, lo.FromPtr(sp.GetId()), err)
		return v1.ScrapeResult{}, uuid.Nil, false
	}
	return azure.servicePrincipalToScrapeResult(sp), spID, true
}

func (azure Scraper) servicePrincipalToScrapeResult(sp msgraphModels.ServicePrincipalable) v1.ScrapeResult {
	spID := lo.FromPtr(sp.GetId())
	appID := lo.FromPtr(sp.GetAppId())
	displayName := lo.FromPtr(sp.GetDisplayName())

	return v1.ScrapeResult{
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
}

// awsSSOLink captures the AWS IAM role + SAML provider ARNs parsed from an
// Azure AppRole's `value` in the Microsoft AWS-SSO gallery-app pattern.
type awsSSOLink struct {
	AccountID string
	RoleName  string
	RoleARN   string
	SAMLARN   string
}

// fetchServicePrincipalRoleMetadata fetches the service principal's appRoles
// in a single Graph call and returns:
//   - awsLinks: appRoleID -> awsSSOLink for roles whose `value` parses as an
//     AWS role + SAML provider pair (non-matching roles are absent).
//   - roleNames: appRoleID -> displayName for every role with an ID, used to
//     evaluate the role-displayName filter in fetchAppRoleAssignments.
func (azure Scraper) fetchServicePrincipalRoleMetadata(spID uuid.UUID) (map[string]awsSSOLink, map[string]string, error) {
	sp, err := azure.graphClient.ServicePrincipals().ByServicePrincipalId(spID.String()).Get(azure.ctx, nil)
	if err != nil {
		return nil, nil, err
	}
	links := map[string]awsSSOLink{}
	names := map[string]string{}
	for _, role := range sp.GetAppRoles() {
		if role.GetId() == nil {
			continue
		}
		roleID := role.GetId().String()
		names[roleID] = lo.FromPtr(role.GetDisplayName())
		if roleARN, samlARN, ok := parseAWSSSOAppRoleValue(lo.FromPtr(role.GetValue())); ok {
			accountID, _ := awsIAMResourceAccount(roleARN, "role/")
			roleName := awsIAMRoleName(roleARN)
			links[roleID] = awsSSOLink{AccountID: accountID, RoleName: roleName, RoleARN: roleARN, SAMLARN: samlARN}
		}
	}
	return links, names, nil
}

// shouldEmitAssignment encapsulates the role-displayName filter check applied
// inside fetchAppRoleAssignments' iterator. With no filter every assignment
// passes (preserves the legacy "member" alias fallback for null roles); when a
// filter is set, assignments without a resolvable role displayName are dropped
// and remaining ones are matched against the filter via collections.MatchItems
// (which handles `Role1,Role2,!role3` semantics including glob and exclusion).
func shouldEmitAssignment(roleFilter []string, displayName string, hasRole bool) bool {
	if len(roleFilter) == 0 {
		return true
	}
	if !hasRole || displayName == "" {
		return false
	}
	return collections.MatchItems(displayName, roleFilter...)
}

func appendAWSSSOAssignment(result *v1.ScrapeResult, base v1.ExternalConfigAccess, assignmentID string, spID uuid.UUID, link awsSSOLink, displayName string, scraperID *uuid.UUID, emittedRoles map[string]struct{}) {
	source := lo.ToPtr(fmt.Sprintf("azure-sso via sp:%s saml:%s", spID, link.SAMLARN))

	if _, ok := emittedRoles[link.RoleARN]; !ok {
		result.ExternalRoles = append(result.ExternalRoles, models.ExternalRole{
			Tenant:      link.AccountID,
			ScraperID:   scraperID,
			Aliases:     pq.StringArray{link.RoleARN},
			RoleType:    "IAMRole",
			Name:        link.RoleName,
			Description: displayName,
		})
		emittedRoles[link.RoleARN] = struct{}{}
	}

	accountAccess := base
	accountAccess.ID = assignmentID
	accountAccess.ConfigExternalID = v1.ExternalID{ConfigType: v1.AWSAccount, ExternalID: link.AccountID, ScraperID: "all"}
	accountAccess.ExternalRoleAliases = []string{link.RoleARN}
	accountAccess.Source = source
	result.ConfigAccess = append(result.ConfigAccess, accountAccess)
}

func (azure Scraper) fetchAppRoleAssignments(spID uuid.UUID, roleFilter []string) v1.ScrapeResults {
	var results v1.ScrapeResults

	awsLinks, roleNames, err := azure.fetchServicePrincipalRoleMetadata(spID)
	if err != nil {
		// A failure to enumerate app roles only removes the cross-cloud
		// fan-out and the role-displayName filter input; the primary
		// assignment rows still emit when no filter is set. When a filter
		// IS set, an empty roleNames map causes shouldEmitAssignment to
		// drop everything — that's the right failure mode (loud, no
		// silent unfiltered scrape).
		logger.Warnf("failed to fetch app roles for service principal %s (continuing without AWS-SSO fan-out): %v", spID, err)
		awsLinks = map[string]awsSSOLink{}
		roleNames = map[string]string{}
	}

	q := &serviceprincipals.ItemAppRoleAssignedToRequestBuilderGetRequestConfiguration{
		QueryParameters: &serviceprincipals.ItemAppRoleAssignedToRequestBuilderGetQueryParameters{
			Select: []string{"id", "principalId", "principalType", "appRoleId", "resourceId", "createdDateTime", "deletedDateTime"},
		},
	}
	assignments, err := azure.graphClient.ServicePrincipals().ByServicePrincipalId(spID.String()).AppRoleAssignedTo().Get(azure.ctx, q)
	if err != nil {
		return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to fetch app role assignments for service principal %s: %w", spID, err)})
	}

	assignmentIterator, err := graphcore.NewPageIterator[msgraphModels.AppRoleAssignmentable](assignments, azure.graphClient.GetAdapter(), msgraphModels.CreateAppRoleAssignmentCollectionResponseFromDiscriminatorValue)
	if err != nil {
		return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to create assignment iterator for service principal %s: %w", spID, err)})
	}

	var result v1.ScrapeResult
	scraperID := azure.ctx.ScrapeConfig().GetPersistedID()
	emittedRoles := map[string]struct{}{}
	awsSSOGroupIDs := map[uuid.UUID]struct{}{}

	err = assignmentIterator.Iterate(azure.ctx, func(assignment msgraphModels.AppRoleAssignmentable) bool {
		principalType := lo.FromPtr(assignment.GetPrincipalType())
		assignmentID := lo.FromPtr(assignment.GetId())

		base := v1.ExternalConfigAccess{
			OwnerScraperID: scraperID,
			CreatedAt:      lo.FromPtr(assignment.GetCreatedDateTime()),
			DeletedAt:      assignment.GetDeletedDateTime(),
		}
		switch principalType {
		case "User":
			base.ExternalUserID = assignment.GetPrincipalId()
		case "Group":
			base.ExternalGroupID = assignment.GetPrincipalId()
		default:
			logger.Warnf("unknown principal type %s for app role assignment %s", principalType, assignmentID)
			return true
		}

		appRoleID := assignment.GetAppRoleId()
		hasRole := appRoleID != nil && appRoleID.String() != uuid.Nil.String()
		var displayName string
		if hasRole {
			displayName = roleNames[appRoleID.String()]
		}
		if !shouldEmitAssignment(roleFilter, displayName, hasRole) {
			return true
		}

		if link, ok := awsLinks[lo.FromPtr(appRoleID).String()]; ok && appRoleID != nil {
			// AWS-SSO assignment: access is scoped to the AWS account, with
			// the target IAM role modelled as the ExternalRole permission.
			appendAWSSSOAssignment(&result, base, assignmentID, spID, link, displayName, scraperID, emittedRoles)
			if base.ExternalGroupID != nil {
				awsSSOGroupIDs[*base.ExternalGroupID] = struct{}{}
			}
			return true
		}

		// Non-AWS-SSO: access is to the Enterprise App itself.
		ca := base
		ca.ID = assignmentID
		ca.ConfigID = spID
		if hasRole {
			ca.ExternalRoleID = appRoleID
		} else {
			ca.ExternalRoleAliases = []string{"member"}
		}
		result.ConfigAccess = append(result.ConfigAccess, ca)
		return true
	})
	if err != nil {
		return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to iterate through app role assignments: %w", err)})
	}

	if len(awsSSOGroupIDs) > 0 {
		if fanout, err := azure.fetchAWSSSOAssignedGroups(awsSSOGroupIDs); err != nil {
			logger.Warnf("failed to fan out AWS-SSO assigned groups for service principal %s: %v", spID, err)
		} else {
			result.ExternalGroups = append(result.ExternalGroups, fanout.ExternalGroups...)
			result.ExternalUsers = append(result.ExternalUsers, fanout.ExternalUsers...)
			result.ExternalUserGroups = append(result.ExternalUserGroups, fanout.ExternalUserGroups...)
		}
	}

	results = append(results, result)
	return results
}

func (azure Scraper) fetchAWSSSOAssignedGroups(groupIDs map[uuid.UUID]struct{}) (v1.ScrapeResult, error) {
	var result v1.ScrapeResult
	if len(groupIDs) == 0 {
		return result, nil
	}

	ids := make([]string, 0, len(groupIDs))
	for id := range groupIDs {
		ids = append(ids, id.String())
	}
	sort.Strings(ids)

	seenGroups := map[uuid.UUID]struct{}{}
	seenUsers := map[uuid.UUID]struct{}{}
	seenMemberships := map[string]struct{}{}

	for _, chunk := range chunkStrings(ids, graphIDInFilterChunkSize) {
		groups, err := azure.fetchGroupsByIDs(chunk)
		if err != nil {
			return result, err
		}

		for _, group := range groups {
			groupID, ok := parseGraphUUID(lo.FromPtr(group.GetId()), "group")
			if !ok {
				continue
			}
			if _, ok := seenGroups[groupID]; !ok {
				result.ExternalGroups = append(result.ExternalGroups, azure.groupToExternalGroup(group, groupID))
				seenGroups[groupID] = struct{}{}
			}

			users, memberships, err := azure.fetchDirectUserMembers(groupID)
			if err != nil {
				logger.Warnf("failed to fetch direct user members for Azure group %s: %v", groupID, err)
				continue
			}
			for _, user := range users {
				if _, ok := seenUsers[user.ID]; ok {
					continue
				}
				result.ExternalUsers = append(result.ExternalUsers, user)
				seenUsers[user.ID] = struct{}{}
			}
			for _, membership := range memberships {
				if membership.ExternalUserID == nil || membership.ExternalGroupID == nil {
					continue
				}
				key := membership.ExternalUserID.String() + "/" + membership.ExternalGroupID.String()
				if _, ok := seenMemberships[key]; ok {
					continue
				}
				result.ExternalUserGroups = append(result.ExternalUserGroups, membership)
				seenMemberships[key] = struct{}{}
			}
		}
	}

	return result, nil
}

func (azure Scraper) fetchGroupsByIDs(ids []string) ([]msgraphModels.Groupable, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	top := int32(100)
	count := true
	filter := graphIDInFilter(ids)
	requestConfig := &graphgroups.GroupsRequestBuilderGetRequestConfiguration{
		Headers: graphAdvancedQueryHeaders(),
		QueryParameters: &graphgroups.GroupsRequestBuilderGetQueryParameters{
			Count:  &count,
			Filter: &filter,
			Select: []string{"id", "createdDateTime", "displayName", "deletedDateTime"},
			Top:    &top,
		},
	}

	groups, err := azure.graphClient.Groups().Get(azure.ctx, requestConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch Azure groups by ID filter %q: %w", filter, err)
	}

	var results []msgraphModels.Groupable
	pageIterator, err := graphcore.NewPageIterator[msgraphModels.Groupable](groups, azure.graphClient.GetAdapter(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Azure group page iterator: %w", err)
	}
	if err := pageIterator.Iterate(azure.ctx, func(group msgraphModels.Groupable) bool {
		results = append(results, group)
		return true
	}); err != nil {
		return nil, fmt.Errorf("failed to iterate Azure group pages: %w", err)
	}

	return results, nil
}

func (azure Scraper) fetchDirectUserMembers(groupID uuid.UUID) ([]models.ExternalUser, []v1.ExternalUserGroup, error) {
	top := int32(999)
	count := true
	requestConfig := &graphgroups.ItemMembersGraphUserRequestBuilderGetRequestConfiguration{
		Headers: graphAdvancedQueryHeaders(),
		QueryParameters: &graphgroups.ItemMembersGraphUserRequestBuilderGetQueryParameters{
			Count:  &count,
			Select: []string{"id", "displayName", "deletedDateTime", "employeeId", "mail", "mailNickname", "onPremisesDomainName", "onPremisesSamAccountName", "userPrincipalName"},
			Top:    &top,
		},
	}

	members, err := azure.graphClient.Groups().ByGroupId(groupID.String()).Members().GraphUser().Get(azure.ctx, requestConfig)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to fetch direct user members: %w", err)
	}

	var users []models.ExternalUser
	var memberships []v1.ExternalUserGroup
	pageIterator, err := graphcore.NewPageIterator[msgraphModels.Userable](members, azure.graphClient.GetAdapter(), nil)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create direct user members page iterator: %w", err)
	}
	if err := pageIterator.Iterate(azure.ctx, func(user msgraphModels.Userable) bool {
		userID, ok := parseGraphUUID(lo.FromPtr(user.GetId()), "user")
		if !ok {
			return true
		}
		displayName := lo.FromPtr(user.GetDisplayName())
		if strings.TrimSpace(displayName) == "" {
			return true
		}

		users = append(users, azure.userMemberToExternalUser(user, userID))
		userIDCopy := userID
		groupIDCopy := groupID
		memberships = append(memberships, v1.ExternalUserGroup{
			ExternalUserID:  &userIDCopy,
			ExternalGroupID: &groupIDCopy,
		})
		return true
	}); err != nil {
		return nil, nil, fmt.Errorf("failed to iterate direct user members: %w", err)
	}

	return users, memberships, nil
}

func (azure Scraper) groupToExternalGroup(group msgraphModels.Groupable, id uuid.UUID) models.ExternalGroup {
	return models.ExternalGroup{
		ID:        id,
		Tenant:    azure.config.TenantID,
		ScraperID: lo.FromPtr(azure.ctx.ScrapeConfig().GetPersistedID()),
		Name:      lo.FromPtr(group.GetDisplayName()),
		CreatedAt: lo.FromPtr(group.GetCreatedDateTime()),
		DeletedAt: group.GetDeletedDateTime(),
		GroupType: "security",
	}
}

func (azure Scraper) userMemberToExternalUser(user msgraphModels.Userable, id uuid.UUID) models.ExternalUser {
	mail := user.GetMail()
	return models.ExternalUser{
		ID:        id,
		Name:      lo.FromPtr(user.GetDisplayName()),
		ScraperID: lo.FromPtr(azure.ctx.ScrapeConfig().GetPersistedID()),
		Tenant:    azure.config.TenantID,
		UserType:  "human",
		Email:     mail,
		Aliases:   pq.StringArray(azureUserAliases(user)),
		CreatedAt: lo.FromPtr(user.GetCreatedDateTime()),
		DeletedAt: user.GetDeletedDateTime(),
	}
}

func azureUserAliases(user msgraphModels.Userable) []string {
	aliases := []string{
		lo.FromPtr(user.GetMail()),
		lo.FromPtr(user.GetUserPrincipalName()),
		lo.FromPtr(user.GetOnPremisesSamAccountName()),
		lo.FromPtr(user.GetMailNickname()),
		lo.FromPtr(user.GetEmployeeId()),
	}
	if nickname := strings.TrimSpace(lo.FromPtr(user.GetMailNickname())); nickname != "" {
		aliases = append(aliases, `OMCORE\`+nickname)
	}
	return compactUniqueStrings(aliases)
}

func graphIDInFilter(ids []string) string {
	quoted := make([]string, 0, len(ids))
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" {
			quoted = append(quoted, "'"+strings.ReplaceAll(id, "'", "''")+"'")
		}
	}
	return fmt.Sprintf("id in (%s)", strings.Join(quoted, ","))
}

func graphAdvancedQueryHeaders() *kiota.RequestHeaders {
	headers := kiota.NewRequestHeaders()
	headers.Add("ConsistencyLevel", "eventual")
	return headers
}

func chunkStrings(items []string, size int) [][]string {
	if size <= 0 || len(items) == 0 {
		return nil
	}
	var chunks [][]string
	for start := 0; start < len(items); start += size {
		end := start + size
		if end > len(items) {
			end = len(items)
		}
		chunks = append(chunks, items[start:end])
	}
	return chunks
}

func compactUniqueStrings(items []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}

func parseGraphUUID(id, kind string) (uuid.UUID, bool) {
	parsed, err := uuid.Parse(id)
	if err != nil {
		logger.Warnf("failed to parse Azure %s ID %q: %v", kind, id, err)
		return uuid.Nil, false
	}
	return parsed, true
}

// fetchEnterpriseApplications gets all enterprise applications (service principals) and their assigned users
func (azure Scraper) fetchEnterpriseApplications(selectors types.ResourceSelectors) v1.ScrapeResults {
	if len(selectors) == 0 {
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
		if orgID := sp.GetAppOwnerOrganizationId(); orgID == nil {
			return true
		} else if orgID.String() != azure.config.TenantID {
			return true // there are a lot of built-in service principals. Only process the ones for this tenant
		}

		result := azure.servicePrincipalToScrapeResult(sp)
		if !selectors.Matches(result) {
			return true
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
func (azure Scraper) fetchUsers(selectors types.ResourceSelectors) v1.ScrapeResults {
	if len(selectors) == 0 {
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
		scrapeResult, err := azure.userToScrapeResult(user, selectors)
		if err != nil {
			azure.ctx.Logger.Errorf("failed to convert user to scrape result: %v", err)
			return true
		} else if len(scrapeResult.ExternalUsers) > 0 {
			results = append(results, scrapeResult)
		}

		return true
	})

	if err != nil {
		return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to iterate through pages: %w", err)})
	}

	return results
}

func (azure Scraper) userToScrapeResult(user msgraphModels.Userable, selector types.ResourceSelectors) (v1.ScrapeResult, error) {
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
		Tenant:    azure.config.TenantID,
		UserType:  "User",
		Email:     user.GetMail(),
		CreatedAt: lo.FromPtr(user.GetCreatedDateTime()),
		DeletedAt: user.GetDeletedDateTime(),
	}

	if !selector.Matches(externalUser) {
		return v1.ScrapeResult{}, nil
	}

	return v1.ScrapeResult{
		BaseScraper:   azure.config.BaseScraper,
		ExternalUsers: []models.ExternalUser{externalUser},
	}, nil
}

// fetchGroups gets Azure AD groups in a tenant.
func (azure Scraper) fetchGroups(selectors types.ResourceSelectors) v1.ScrapeResults {
	if len(selectors) == 0 {
		return nil
	}

	azure.ctx.Logger.V(3).Infof("fetching groups for tenant %s", azure.config.TenantID)

	var results v1.ScrapeResults
	groups, err := azure.graphClient.Groups().Get(azure.ctx, nil)
	if err != nil {
		return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to fetch groups: %w", err)})
	}

	pageIterator, err := graphcore.NewPageIterator[msgraphModels.Groupable](groups, azure.graphClient.GetAdapter(), nil)
	if err != nil {
		return append(results, v1.ScrapeResult{Error: fmt.Errorf("failed to create page iterator: %w", err)})
	}

	err = pageIterator.Iterate(azure.ctx, func(group msgraphModels.Groupable) bool {
		result, err := azure.groupToScrapeResult(group, selectors)
		if err != nil {
			azure.ctx.Logger.Errorf("failed to convert group to scrape result: %v", err)
			return true
		} else if len(result.ExternalGroups) == 0 {
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

func (azure Scraper) groupToScrapeResult(group msgraphModels.Groupable, selector types.ResourceSelectors) (v1.ScrapeResult, error) {
	groupID, err := uuid.Parse(lo.FromPtr(group.GetId()))
	if err != nil {
		return v1.ScrapeResult{}, fmt.Errorf("failed to parse group ID %s: %w", lo.FromPtr(group.GetId()), err)
	}

	externalGroup := models.ExternalGroup{
		ID:        groupID,
		Tenant:    azure.config.TenantID,
		ScraperID: lo.FromPtr(azure.ctx.ScrapeConfig().GetPersistedID()),
		Name:      lo.FromPtr(group.GetDisplayName()),
		CreatedAt: lo.FromPtr(group.GetCreatedDateTime()),
		DeletedAt: group.GetDeletedDateTime(),
	}

	if gt := group.GetGroupTypes(); len(gt) > 0 {
		externalGroup.GroupType = gt[0]
	}

	if !selector.Matches(externalGroup) {
		return v1.ScrapeResult{}, nil
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
func (azure Scraper) fetchGroupMembers(groupID string) ([]v1.ExternalUserGroup, error) {
	groupUUID, err := uuid.Parse(groupID)
	if err != nil {
		return nil, fmt.Errorf("failed to parse group ID %s: %w", groupID, err)
	}

	var results []v1.ExternalUserGroup
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

		groupUUIDCopy := groupUUID
		results = append(results, v1.ExternalUserGroup{
			ExternalUserID:  &memberID,
			ExternalGroupID: &groupUUIDCopy,
		})
		return true
	})
	if err != nil {
		return nil, fmt.Errorf("failed to iterate through pages: %w", err)
	}

	return results, nil
}
