package gcp

import (
	"fmt"
	"strings"

	asset "cloud.google.com/go/asset/apiv1"
	"cloud.google.com/go/asset/apiv1/assetpb"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty/models"
	"github.com/lib/pq"
	"github.com/samber/lo"
	"google.golang.org/api/iterator"
)

const iamPolicySource = "gcp-iam-policy"

// gcpPrincipal is a classified IAM policy member. Alias is the globally-unique
// identifier used for save-time entity resolution (bare email for real
// identities, the verbatim member token for the special all-users / domain
// forms). IsGroup routes the principal to external_groups vs external_users.
type gcpPrincipal struct {
	Type    string
	Alias   string
	Name    string
	IsGroup bool
}

// parseGCPMember classifies an IAM binding member string.
// See https://cloud.google.com/iam/docs/principal-identifiers
func parseGCPMember(member string) (gcpPrincipal, bool) {
	switch {
	case strings.HasPrefix(member, "user:"):
		email := strings.TrimPrefix(member, "user:")
		return gcpPrincipal{Type: "User", Alias: email, Name: email}, true
	case strings.HasPrefix(member, "serviceAccount:"):
		email := strings.TrimPrefix(member, "serviceAccount:")
		return gcpPrincipal{Type: "ServiceAccount", Alias: email, Name: email}, true
	case strings.HasPrefix(member, "group:"):
		email := strings.TrimPrefix(member, "group:")
		return gcpPrincipal{Type: "Group", Alias: email, Name: email, IsGroup: true}, true
	case strings.HasPrefix(member, "domain:"):
		domain := strings.TrimPrefix(member, "domain:")
		return gcpPrincipal{Type: "Domain", Alias: member, Name: domain, IsGroup: true}, true
	case member == "allUsers":
		return gcpPrincipal{Type: "AllUsers", Alias: member, Name: member, IsGroup: true}, true
	case member == "allAuthenticatedUsers":
		return gcpPrincipal{Type: "AllAuthenticatedUsers", Alias: member, Name: member, IsGroup: true}, true
	}

	return gcpPrincipal{}, false
}

// iamScope identifies the account an IAM scrape is rooted at.
type iamScope struct {
	// Tenant is stamped on every discovered identity as its account_id. It is
	// the organization whenever one is reachable, so the same principal resolves
	// to one account across every project it appears in.
	Tenant string
	// Project is the configured project id, empty for organization-scoped scrapes.
	Project string
	// Root owns roles that belong to neither a single project nor a single
	// organization, i.e. GCP's predefined roles.
	Root v1.ConfigExternalKey
}

// scopeFor resolves the scope of one asset-inventory root. organization may be
// empty, in which case a project falls back to tenanting by itself.
func scopeFor(parent, organization string) iamScope {
	project := projectFromParent(parent)
	scope := iamScope{
		Tenant:  project,
		Project: project,
		Root:    v1.ConfigExternalKey{Type: v1.GCPProject, ExternalID: project},
	}

	if organization != "" {
		scope.Tenant = organization
	}

	if project == "" {
		scope.Root = v1.ConfigExternalKey{
			Type:       "GCP::" + organizationConfigClass,
			ExternalID: resourceManagerPrefix + organizationPrefix + organization,
		}
	}

	return scope
}

// resolveOrganization finds the organization identities should be tenanted by,
// preferring the configured one, then the resource hierarchy, then the ancestry
// Cloud Asset Inventory reports on every asset. The last needs no Cloud Resource
// Manager permission, so a narrowly-scoped scrape service account still tenants
// by organization.
func resolveOrganization(config v1.GCP, hierarchy resourceHierarchy, assets []*assetpb.Asset) string {
	if organization := config.OrganizationID(); organization != "" {
		return organization
	}
	if hierarchy.OrganizationID != "" {
		return hierarchy.OrganizationID
	}
	return organizationFromAssets(assets)
}

// iamAccessResult is the deduplicated view of a project's IAM policy bindings.
type iamAccessResult struct {
	// RoleConfigs are GCP::IAMRole config items (one per distinct bound role)
	// carrying role→resource IAMBinding relationships.
	RoleConfigs []v1.ScrapeResult
	Roles       []models.ExternalRole
	Users       []models.ExternalUser
	Groups      []models.ExternalGroup
	// Access holds one row per (resource, principal, role).
	Access []v1.ExternalConfigAccess
	// GroupEmails are the real Google-group emails eligible for membership
	// expansion (excludes domain / all-users pseudo-principals).
	GroupEmails []string
}

// buildIAMAccess collapses IAM policy bindings across all assets into role
// config items, external identities, and per-(resource, principal, role) grant edges.
// Pure and unit-tested; persistence resolves everything by alias.
func buildIAMAccess(assets []*assetpb.Asset, scope iamScope) iamAccessResult {
	var res iamAccessResult

	roleIdx := make(map[string]int)
	seenRoleResource := make(map[string]struct{})
	seenAccess := make(map[string]struct{})
	seenUser := make(map[string]struct{})
	seenGroup := make(map[string]struct{})

	for _, a := range assets {
		if a.IamPolicy == nil || a.Name == "" {
			continue
		}

		resourceType := fmt.Sprintf("GCP::%s", parseGCPConfigClass(a.AssetType))

		for _, binding := range a.IamPolicy.Bindings {
			role := binding.Role
			if role == "" {
				continue
			}

			idx, ok := roleIdx[role]
			if !ok {
				idx = len(res.RoleConfigs)
				roleIdx[role] = idx
				res.RoleConfigs = append(res.RoleConfigs, newRoleConfig(role, scope))
				res.Roles = append(res.Roles, models.ExternalRole{
					Aliases:  pq.StringArray{role},
					Name:     roleShortName(role),
					Tenant:   scope.Tenant,
					RoleType: roleType(role),
				})
			}

			if rrKey := role + "\x00" + resourceType + "\x00" + a.Name; !contains(seenRoleResource, rrKey) {
				seenRoleResource[rrKey] = struct{}{}
				res.RoleConfigs[idx].RelationshipResults = append(res.RoleConfigs[idx].RelationshipResults,
					v1.RelationshipResult{
						ConfigExternalID:  v1.ExternalID{ConfigType: v1.IAMRole, ExternalID: role},
						RelatedExternalID: v1.ExternalID{ConfigType: resourceType, ExternalID: a.Name, ScraperID: "all"},
						Relationship:      "IAMBinding",
					})
			}

			for _, member := range binding.Members {
				p, ok := parseGCPMember(member)
				if !ok {
					continue
				}

				if accessKey := resourceType + "\x00" + a.Name + "\x00" + p.Alias + "\x00" + role; contains(seenAccess, accessKey) {
					continue
				} else {
					seenAccess[accessKey] = struct{}{}
				}

				access := v1.ExternalConfigAccess{
					ConfigExternalID:    v1.ExternalID{ConfigType: resourceType, ExternalID: a.Name},
					ExternalRoleAliases: []string{role},
					Source:              lo.ToPtr(iamPolicySource),
				}

				if p.IsGroup {
					access.ExternalGroupAliases = []string{p.Alias}
					if !contains(seenGroup, p.Alias) {
						seenGroup[p.Alias] = struct{}{}
						res.Groups = append(res.Groups, models.ExternalGroup{
							Aliases:   pq.StringArray{p.Alias},
							Name:      p.Name,
							Tenant:    scope.Tenant,
							GroupType: p.Type,
						})
						if p.Type == "Group" {
							res.GroupEmails = append(res.GroupEmails, p.Alias)
						}
					}
				} else {
					access.ExternalUserAliases = []string{p.Alias}
					if !contains(seenUser, p.Alias) {
						seenUser[p.Alias] = struct{}{}
						user := models.ExternalUser{
							Aliases:  pq.StringArray{p.Alias},
							Name:     p.Name,
							Tenant:   scope.Tenant,
							UserType: p.Type,
						}
						if strings.Contains(p.Alias, "@") {
							user.Email = lo.ToPtr(p.Alias)
						}
						res.Users = append(res.Users, user)
					}
				}

				res.Access = append(res.Access, access)
			}
		}
	}

	return res
}

func contains(set map[string]struct{}, key string) bool {
	_, ok := set[key]
	return ok
}

// newRoleConfig builds the GCP::IAMRole config item for a bound role. Config is
// a non-nil map so the item is a real config (not metadata-only); enrichRoleConfigs
// augments it with the role's title / permissions from the IAM Admin API.
//
// A custom role names the project or organization that defines it, and is
// parented there. Predefined roles belong to no single resource and hang off the
// scrape root.
func newRoleConfig(role string, scope iamScope) v1.ScrapeResult {
	kind := "custom"
	if strings.HasPrefix(role, "roles/") {
		kind = "predefined"
	}

	config := map[string]any{
		"name": role,
		"type": kind,
	}
	parent := scope.Root

	switch owner, id := roleOwner(role); owner {
	case "project":
		config["project"] = id
		parent = v1.ConfigExternalKey{Type: v1.GCPProject, ExternalID: id}
	case "organization":
		config["organization"] = id
		parent = v1.ConfigExternalKey{
			Type:       "GCP::" + organizationConfigClass,
			ExternalID: resourceManagerPrefix + organizationPrefix + id,
		}
	default:
		if scope.Project != "" {
			config["project"] = scope.Project
		}
	}

	result := v1.ScrapeResult{
		ID:          role,
		Name:        roleShortName(role),
		ConfigClass: "IAMRole",
		Type:        v1.IAMRole,
		Aliases:     []string{role},
		Config:      config,
	}
	if parent.ExternalID != "" {
		result.Parents = []v1.ConfigExternalKey{parent}
	}

	return result
}

// roleOwner returns the kind of resource that defines a custom role and its id,
// e.g. ("project", "gcp-proj-1") for projects/gcp-proj-1/roles/customViewer.
// Predefined roles have no owner.
func roleOwner(role string) (string, string) {
	prefix, _, found := strings.Cut(role, "/roles/")
	if !found {
		return "", ""
	}

	if id, ok := strings.CutPrefix(prefix, "projects/"); ok {
		return "project", id
	}
	if id, ok := strings.CutPrefix(prefix, organizationPrefix); ok {
		return "organization", id
	}
	return "", ""
}

// roleShortName returns the last path segment of a role id
// (roles/storage.admin -> storage.admin, projects/p/roles/x -> x).
func roleShortName(role string) string {
	if i := strings.LastIndex(role, "/"); i >= 0 {
		return role[i+1:]
	}
	return role
}

func roleType(role string) string {
	if strings.HasPrefix(role, "roles/") {
		return "Global"
	}
	return "Custom"
}

type iamPolicyFetchers struct {
	listAssets     func(*GCPContext, v1.GCP, string) ([]*assetpb.Asset, error)
	fetchHierarchy func(*GCPContext, v1.GCP, string) (resourceHierarchy, error)
	enrichRoles    func(*GCPContext, []v1.ScrapeResult)
}

// iamPolicyResult is a completed IAM-policy scrape.
type iamPolicyResult struct {
	Results v1.ScrapeResults
	// Scope is the resolved account the discovered identities belong to, reused
	// by group-membership expansion so it stamps the same tenant.
	Scope iamScope
	// GroupEmails are the real Google groups discovered in bindings, eligible for
	// membership expansion by the caller.
	GroupEmails []string
}

// FetchIAMPolicies reads Cloud Asset Inventory IAM policies beneath parent and
// emits role config items, external identities, and grant edges.
func (scraper Scraper) FetchIAMPolicies(ctx *GCPContext, config v1.GCP, parent string) (iamPolicyResult, error) {
	return scraper.fetchIAMPolicies(ctx, config, parent, iamPolicyFetchers{
		listAssets:     listIAMPolicyAssets,
		fetchHierarchy: fetchResourceManagerHierarchy,
		enrichRoles:    enrichRoleConfigs,
	})
}

func (Scraper) fetchIAMPolicies(ctx *GCPContext, config v1.GCP, parent string, fetchers iamPolicyFetchers) (iamPolicyResult, error) {
	assets, err := fetchers.listAssets(ctx, config, parent)
	if err != nil {
		return iamPolicyResult{}, err
	}

	// A hierarchy that cannot be read costs the organization and folder config
	// items, so it is reported as a scraper error rather than only logged. The
	// organization id itself is still recovered from the parent chain or the
	// asset ancestry, so identities stay tenanted correctly.
	var hierarchyResults v1.ScrapeResults
	hierarchy, err := fetchers.fetchHierarchy(ctx, config, parent)
	if err != nil {
		hierarchyResults.Errorf(err, "failed to read the GCP resource hierarchy above %s, its organization and folder config items will be missing", parent)
	} else if results, policies, err := buildResourceManagerHierarchy(hierarchy.Project, hierarchy.Nodes, config.BaseScraper); err != nil {
		hierarchyResults.Errorf(err, "invalid GCP resource hierarchy above %s, its organization and folder config items will be missing", parent)
	} else {
		hierarchyResults = results
		assets = append(assets, policies...)
	}

	scope := scopeFor(parent, resolveOrganization(config, hierarchy, assets))
	access := buildIAMAccess(assets, scope)
	fetchers.enrichRoles(ctx, access.RoleConfigs)

	results := hierarchyResults
	for i := range access.RoleConfigs {
		access.RoleConfigs[i].BaseScraper = config.BaseScraper
		results = append(results, access.RoleConfigs[i])
	}

	results = append(results, v1.ScrapeResult{
		BaseScraper:    config.BaseScraper,
		ExternalRoles:  access.Roles,
		ExternalUsers:  access.Users,
		ExternalGroups: access.Groups,
		ConfigAccess:   access.Access,
	})

	return iamPolicyResult{Results: results, Scope: scope, GroupEmails: access.GroupEmails}, nil
}

func listIAMPolicyAssets(ctx *GCPContext, config v1.GCP, parent string) ([]*assetpb.Asset, error) {
	req := &assetpb.ListAssetsRequest{
		Parent:      parent,
		ContentType: assetpb.ContentType_IAM_POLICY,
		PageSize:    1000,
	}

	assetClient, err := asset.NewClient(ctx, ctx.ClientOpts...)
	if err != nil {
		return nil, fmt.Errorf("error creating asset client for IAM policies: %w", err)
	}
	defer func() {
		if err := assetClient.Close(); err != nil {
			ctx.Warnf("gcp iam policies: failed to close asset client: %v", err)
		}
	}()

	var assets []*assetpb.Asset
	it := assetClient.ListAssets(ctx, req)
	for {
		a, err := it.Next()
		if err == iterator.Done {
			break
		} else if err != nil {
			return nil, fmt.Errorf("error listing IAM policies: %w", err)
		}
		assets = append(assets, a)
	}

	return assets, nil
}
