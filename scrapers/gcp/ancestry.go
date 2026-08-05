// Resolves asset ancestry into config relationships: the project that owns each
// asset, and the resource hierarchy links between organizations, folders and
// projects discovered by an organization-scoped scrape.
package gcp

import (
	"strings"

	"cloud.google.com/go/asset/apiv1/assetpb"

	v1 "github.com/flanksource/config-db/api/v1"
)

const (
	projectAssetType      = "cloudresourcemanager.googleapis.com/Project"
	folderAssetType       = "cloudresourcemanager.googleapis.com/Folder"
	organizationAssetType = "cloudresourcemanager.googleapis.com/Organization"

	organizationPrefix      = "organizations/"
	organizationConfigClass = "ResourceManager::Organization"
)

// isResourceManagerNode reports whether an asset is a node of the resource
// hierarchy, whose parent comes from its ancestry rather than from a relationship
// resolver.
func isResourceManagerNode(assetType string) bool {
	switch assetType {
	case projectAssetType, folderAssetType, organizationAssetType:
		return true
	}
	return false
}

// resourceManagerParent returns the immediate hierarchy parent of an asset from
// its ancestry, nil for an organization (which has no parent). Ancestry starts
// with the asset itself, so the parent is the second entry.
func resourceManagerParent(ancestors []string) (*v1.ConfigExternalKey, error) {
	if len(ancestors) < 2 {
		return nil, nil
	}

	metadata, err := resourceManagerMetadataForName(ancestors[1])
	if err != nil {
		return nil, err
	}

	return &v1.ConfigExternalKey{
		Type:       "GCP::" + metadata.ConfigClass,
		ExternalID: resourceManagerPrefix + metadata.Name,
	}, nil
}

// assetAncestry is the ancestry of a scraped asset, retained so the owning
// project can be attached once the whole listing has been seen.
type assetAncestry struct {
	ancestors       []string
	isHierarchyNode bool
}

// projectResolver maps a project number to its project id. Cloud Asset
// Inventory reports ancestry as project numbers ("projects/123456789") while
// config items are keyed by project id ("gcp-proj-1"); the Project assets in the
// same listing carry both, so the mapping is built as assets stream past.
type projectResolver struct {
	fallback string
	ids      map[string]string
}

// newProjectResolver returns a resolver that falls back to the given project id
// for assets with no resolvable ancestor project. Org-scoped scrapes pass an
// empty fallback.
func newProjectResolver(fallback string) *projectResolver {
	return &projectResolver{fallback: fallback, ids: map[string]string{}}
}

// record learns the number→id mapping from a project asset, ignoring every
// other asset type.
func (r *projectResolver) record(asset *assetpb.Asset) {
	if asset.AssetType != projectAssetType || asset.Resource == nil || asset.Resource.Data == nil {
		return
	}

	fields := asset.Resource.Data.Fields
	projectID := fields["projectId"].GetStringValue()
	if projectID == "" {
		return
	}

	number := fields["projectNumber"].GetStringValue()
	if number == "" {
		// The v3 Cloud Resource Manager representation carries the number in
		// the resource name instead: projects/123456789.
		number = strings.TrimPrefix(trimResourceManagerPrefix(asset.Name), "projects/")
		if name := fields["name"].GetStringValue(); strings.HasPrefix(name, "projects/") {
			number = strings.TrimPrefix(name, "projects/")
		}
	}
	if number == "" {
		return
	}

	r.ids[number] = projectID
}

// resolve returns the project id owning an asset with the given ancestry.
func (r *projectResolver) resolve(ancestors []string) string {
	number := projectNumberFromAncestors(ancestors)
	if number == "" {
		return r.fallback
	}
	if id, ok := r.ids[number]; ok {
		return id
	}
	if r.fallback != "" {
		return r.fallback
	}
	// Better to attribute the asset to the bare number than to drop the
	// association entirely when the project asset was filtered out of the scrape.
	return number
}

// organizationFromAncestors returns the organization number an asset lives under,
// empty for an asset with no organization. Cloud Asset Inventory reports the full
// ancestry on every asset, so this needs no Cloud Resource Manager permission.
func organizationFromAncestors(ancestors []string) string {
	for _, ancestor := range ancestors {
		if number, ok := strings.CutPrefix(ancestor, organizationPrefix); ok {
			return number
		}
	}
	return ""
}

// organizationFromAssets returns the first organization found in the ancestry of
// any listed asset.
func organizationFromAssets(assets []*assetpb.Asset) string {
	for _, asset := range assets {
		if organization := organizationFromAncestors(asset.Ancestors); organization != "" {
			return organization
		}
	}
	return ""
}

// projectNumberFromAncestors returns the number of the closest ancestor project,
// empty for assets that live above the project level (folders, organizations).
// Ancestry starts at the closest ancestor: ["projects/123", "folders/5", "organizations/1"].
func projectNumberFromAncestors(ancestors []string) string {
	for _, ancestor := range ancestors {
		if number, ok := strings.CutPrefix(ancestor, "projects/"); ok {
			return number
		}
	}
	return ""
}

func trimResourceManagerPrefix(name string) string {
	return strings.TrimPrefix(name, resourceManagerPrefix)
}
