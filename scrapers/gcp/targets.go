// Resolves a GCP scraper config into the Cloud Asset Inventory roots to scrape,
// narrowing an organization to the configured projects that actually belong to it.
package gcp

import (
	"fmt"
	"slices"
	"strings"

	asset "cloud.google.com/go/asset/apiv1"
	"cloud.google.com/go/asset/apiv1/assetpb"
	"google.golang.org/api/iterator"

	v1 "github.com/flanksource/config-db/api/v1"
)

// resolveParents returns the asset-inventory roots to list from. An organization
// with no configured projects is listed in one pass; configured projects are
// listed one at a time so only what was asked for is fetched.
func resolveParents(ctx *GCPContext, config v1.GCP) ([]string, error) {
	configured := config.ConfiguredProjects()

	if !config.IsOrgScoped() {
		return qualifyProjects(configured), nil
	}

	organization := v1.OrganizationPrefix + config.OrganizationID()
	if len(configured) == 0 {
		return []string{organization}, nil
	}

	organizationProjects, err := listOrganizationProjects(ctx, organization)
	if err != nil {
		return nil, fmt.Errorf("failed to list projects in %s: %w", organization, err)
	}

	return qualifyProjects(intersectProjects(organizationProjects, configured, ctx.Warnf)), nil
}

// intersectProjects keeps the configured projects that belong to the
// organization, warning about each one that does not so a typo or a project
// moved out of the organization is visible rather than silently scraped as
// nothing.
func intersectProjects(organizationProjects, configured []string, warn func(format string, args ...any)) []string {
	var projects []string
	for _, project := range configured {
		if slices.Contains(organizationProjects, project) {
			projects = append(projects, project)
			continue
		}
		warn("gcp: skipping project %s, it does not belong to the configured organization", project)
	}
	return projects
}

func qualifyProjects(projects []string) []string {
	parents := make([]string, 0, len(projects))
	for _, project := range projects {
		parents = append(parents, v1.ProjectPrefix+project)
	}
	return parents
}

// projectFromParent returns the project id an asset-inventory root refers to,
// empty for an organization root.
func projectFromParent(parent string) string {
	id, ok := strings.CutPrefix(parent, v1.ProjectPrefix)
	if !ok {
		return ""
	}
	return id
}

// listOrganizationProjects returns the ids of every project beneath the
// organization, read from the asset inventory so no extra API surface is needed.
func listOrganizationProjects(ctx *GCPContext, organization string) ([]string, error) {
	assetClient, err := asset.NewClient(ctx, ctx.ClientOpts...)
	if err != nil {
		return nil, fmt.Errorf("error creating asset client: %w", err)
	}
	defer func() {
		if err := assetClient.Close(); err != nil {
			ctx.Warnf("gcp: failed to close asset client: %v", err)
		}
	}()

	req := &assetpb.ListAssetsRequest{
		Parent:      organization,
		ContentType: assetpb.ContentType_RESOURCE,
		AssetTypes:  []string{projectAssetType},
		PageSize:    1000,
	}

	resolver := newProjectResolver("")
	var projects []string
	it := assetClient.ListAssets(ctx, req)
	for {
		a, err := it.Next()
		if err == iterator.Done {
			break
		} else if err != nil {
			return nil, fmt.Errorf("error listing projects: %w", err)
		}

		resolver.record(a)
		if id := resolver.resolve(a.Ancestors); id != "" {
			projects = append(projects, id)
		}
	}

	return projects, nil
}
