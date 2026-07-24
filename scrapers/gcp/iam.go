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
	cloudresourcemanager "google.golang.org/api/cloudresourcemanager/v3"
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
func buildIAMAccess(assets []*assetpb.Asset, project string) iamAccessResult {
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
				res.RoleConfigs = append(res.RoleConfigs, newRoleConfig(role, project))
				res.Roles = append(res.Roles, models.ExternalRole{
					Aliases:  pq.StringArray{role},
					Name:     roleShortName(role),
					Tenant:   project,
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
							Tenant:    project,
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
							Tenant:   project,
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
func newRoleConfig(role, project string) v1.ScrapeResult {
	kind := "custom"
	if strings.HasPrefix(role, "roles/") {
		kind = "predefined"
	}

	return v1.ScrapeResult{
		ID:          role,
		Name:        roleShortName(role),
		ConfigClass: "IAMRole",
		Type:        v1.IAMRole,
		Aliases:     []string{role},
		Config: map[string]any{
			"name":    role,
			"type":    kind,
			"project": project,
		},
		Parents: []v1.ConfigExternalKey{{Type: v1.GCPProject, ExternalID: project}},
	}
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
	listAssets     func(*GCPContext, v1.GCP) ([]*assetpb.Asset, error)
	fetchHierarchy func(*GCPContext, v1.GCP) (*cloudresourcemanager.Project, []resourceManagerNode, error)
	enrichRoles    func(*GCPContext, []v1.ScrapeResult)
}

// FetchIAMPolicies reads Cloud Asset Inventory IAM policies for the project and
// emits role config items, external identities, and grant edges. The returned
// group emails are the real Google groups discovered in bindings, for optional
// membership expansion by the caller.
func (scraper Scraper) FetchIAMPolicies(ctx *GCPContext, config v1.GCP) (v1.ScrapeResults, []string, error) {
	return scraper.fetchIAMPolicies(ctx, config, iamPolicyFetchers{
		listAssets:     listIAMPolicyAssets,
		fetchHierarchy: fetchResourceManagerHierarchy,
		enrichRoles:    enrichRoleConfigs,
	})
}

func (Scraper) fetchIAMPolicies(ctx *GCPContext, config v1.GCP, fetchers iamPolicyFetchers) (v1.ScrapeResults, []string, error) {
	assets, err := fetchers.listAssets(ctx, config)
	if err != nil {
		return nil, nil, err
	}

	var hierarchyResults v1.ScrapeResults
	project, nodes, err := fetchers.fetchHierarchy(ctx, config)
	if err != nil {
		ctx.Warnf("gcp iam policies: resource hierarchy unavailable: %v", err)
	} else if hierarchy, policies, err := buildResourceManagerHierarchy(project, nodes, config.BaseScraper); err != nil {
		ctx.Warnf("gcp iam policies: invalid resource hierarchy: %v", err)
	} else {
		hierarchyResults = hierarchy
		assets = append(assets, policies...)
	}

	access := buildIAMAccess(assets, config.Project)
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

	return results, access.GroupEmails, nil
}

func listIAMPolicyAssets(ctx *GCPContext, config v1.GCP) ([]*assetpb.Asset, error) {
	req := &assetpb.ListAssetsRequest{
		Parent:      fmt.Sprintf("projects/%s", config.Project),
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
