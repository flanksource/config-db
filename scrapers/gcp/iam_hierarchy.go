package gcp

import (
	"fmt"
	"strings"

	"cloud.google.com/go/asset/apiv1/assetpb"
	"cloud.google.com/go/iam/apiv1/iampb"
	v1 "github.com/flanksource/config-db/api/v1"
	cloudresourcemanager "google.golang.org/api/cloudresourcemanager/v3"
)

const resourceManagerPrefix = "//cloudresourcemanager.googleapis.com/"

type resourceManagerNode struct {
	Resource any
	Policy   *cloudresourcemanager.Policy
}

type resourceManagerMetadata struct {
	Name        string
	DisplayName string
	Parent      string
	State       string
	ConfigClass string
	AssetType   string
}

// resourceHierarchy is the ancestor chain above a scraped root.
type resourceHierarchy struct {
	// Project is the hierarchy root, nil when the root is an organization.
	Project *cloudresourcemanager.Project
	Nodes   []resourceManagerNode
	// OrganizationID is the organization the root belongs to. The parent chain
	// names it before any node is read, so it survives a scrape service account
	// that may not read the organization resource itself.
	OrganizationID string
}

// fetchResourceManagerHierarchy walks upwards from parent. An organization root
// has nothing above it, so it is returned as the sole node with a nil project;
// the folders and projects beneath it arrive as assets instead.
//
// A node that cannot be read degrades the hierarchy config items but still yields
// OrganizationID, so identities stay tenanted by organization.
func fetchResourceManagerHierarchy(ctx *GCPContext, _ v1.GCP, parent string) (resourceHierarchy, error) {
	service, err := cloudresourcemanager.NewService(ctx, ctx.ClientOpts...)
	if err != nil {
		return resourceHierarchy{}, fmt.Errorf("create Cloud Resource Manager client: %w", err)
	}

	if projectFromParent(parent) == "" {
		hierarchy := resourceHierarchy{OrganizationID: strings.TrimPrefix(parent, organizationPrefix)}
		node, err := fetchResourceManagerNode(ctx, service, parent)
		if err != nil {
			return hierarchy, err
		}
		hierarchy.Nodes = []resourceManagerNode{node}
		return hierarchy, nil
	}

	project, err := service.Projects.Get(parent).Context(ctx).Do()
	if err != nil {
		return resourceHierarchy{}, fmt.Errorf("get GCP project hierarchy root %s: %w", parent, err)
	}

	hierarchy := resourceHierarchy{Project: project}
	visited := make(map[string]struct{})
	for parent := project.Parent; parent != ""; {
		if _, ok := visited[parent]; ok {
			return hierarchy, fmt.Errorf("cyclic GCP resource hierarchy at %s", parent)
		}
		visited[parent] = struct{}{}

		// Recorded before the read so an organization the caller may not read is
		// still known.
		if organization, ok := strings.CutPrefix(parent, organizationPrefix); ok {
			hierarchy.OrganizationID = organization
		}

		node, err := fetchResourceManagerNode(ctx, service, parent)
		if err != nil {
			return hierarchy, err
		}
		hierarchy.Nodes = append(hierarchy.Nodes, node)

		metadata, err := node.metadata()
		if err != nil {
			return hierarchy, err
		}
		parent = metadata.Parent
	}

	return hierarchy, nil
}

func fetchResourceManagerNode(ctx *GCPContext, service *cloudresourcemanager.Service, name string) (resourceManagerNode, error) {
	request := &cloudresourcemanager.GetIamPolicyRequest{
		Options: &cloudresourcemanager.GetPolicyOptions{RequestedPolicyVersion: 3},
	}

	switch {
	case strings.HasPrefix(name, "folders/"):
		folder, err := service.Folders.Get(name).Context(ctx).Do()
		if err != nil {
			return resourceManagerNode{}, fmt.Errorf("get GCP folder %s: %w", name, err)
		}
		policy, err := service.Folders.GetIamPolicy(name, request).Context(ctx).Do()
		if err != nil {
			return resourceManagerNode{}, fmt.Errorf("get IAM policy for GCP folder %s: %w", name, err)
		}
		return resourceManagerNode{Resource: folder, Policy: policy}, nil
	case strings.HasPrefix(name, "organizations/"):
		organization, err := service.Organizations.Get(name).Context(ctx).Do()
		if err != nil {
			return resourceManagerNode{}, fmt.Errorf("get GCP organization %s: %w", name, err)
		}
		policy, err := service.Organizations.GetIamPolicy(name, request).Context(ctx).Do()
		if err != nil {
			return resourceManagerNode{}, fmt.Errorf("get IAM policy for GCP organization %s: %w", name, err)
		}
		return resourceManagerNode{Resource: organization, Policy: policy}, nil
	default:
		return resourceManagerNode{}, fmt.Errorf("unsupported GCP resource hierarchy parent %q", name)
	}
}

// buildResourceManagerHierarchy turns the ancestor chain into config items and
// IAM-policy assets. A nil project means the scrape is rooted at an organization,
// where nodes holds just that organization and the descendants arrive as assets.
func buildResourceManagerHierarchy(project *cloudresourcemanager.Project, nodes []resourceManagerNode, base v1.BaseScraper) (v1.ScrapeResults, []*assetpb.Asset, error) {
	var projectKey *v1.ConfigExternalKey
	var expectedName string

	if project != nil {
		if project.Name == "" {
			return nil, nil, fmt.Errorf("GCP project hierarchy root has no resource name")
		}
		projectMetadata, err := resourceManagerMetadataForName(project.Name)
		if err != nil {
			return nil, nil, err
		}

		// The project itself is emitted by the asset inventory loop (see the
		// cloudresourcemanager.googleapis.com/Project mapping in types.go); link the
		// deepest ancestor to it rather than emitting a second, conflicting result.
		projectKey = &v1.ConfigExternalKey{
			Type:       "GCP::" + projectMetadata.ConfigClass,
			ExternalID: resourceManagerPrefix + projectMetadata.Name,
		}
		expectedName = project.Parent
	} else if len(nodes) > 0 {
		metadata, err := nodes[0].metadata()
		if err != nil {
			return nil, nil, err
		}
		expectedName = metadata.Name
	}

	var results v1.ScrapeResults
	var policies []*assetpb.Asset
	for i, node := range nodes {
		metadata, err := node.metadata()
		if err != nil {
			return nil, nil, err
		}
		if metadata.Name != expectedName {
			return nil, nil, fmt.Errorf("incomplete GCP resource hierarchy: expected %s, got %s", expectedName, metadata.Name)
		}

		result := v1.ScrapeResult{
			BaseScraper: base,
			ID:          metadata.Name,
			Name:        metadata.DisplayName,
			Config:      node.Resource,
			ConfigClass: metadata.ConfigClass,
			Type:        "GCP::" + metadata.ConfigClass,
			Status:      metadata.State,
			Aliases:     []string{resourceManagerPrefix + metadata.Name},
		}
		if result.Name == "" {
			result.Name = metadata.Name
		}
		if i == 0 && projectKey != nil {
			result.Children = []v1.ConfigExternalKey{*projectKey}
		}
		if metadata.Parent != "" {
			parentMetadata, err := resourceManagerMetadataForName(metadata.Parent)
			if err != nil {
				return nil, nil, err
			}
			result.Parents = []v1.ConfigExternalKey{{
				Type:       "GCP::" + parentMetadata.ConfigClass,
				ExternalID: resourceManagerPrefix + metadata.Parent,
			}}
		}
		results = append(results, result)

		if node.Policy != nil {
			policies = append(policies, &assetpb.Asset{
				Name:      resourceManagerPrefix + metadata.Name,
				AssetType: metadata.AssetType,
				IamPolicy: resourceManagerIAMPolicy(node.Policy),
			})
		}
		expectedName = metadata.Parent
	}

	if expectedName != "" {
		return nil, nil, fmt.Errorf("incomplete GCP resource hierarchy: missing %s", expectedName)
	}
	return results, policies, nil
}

func (node resourceManagerNode) metadata() (resourceManagerMetadata, error) {
	switch resource := node.Resource.(type) {
	case *cloudresourcemanager.Folder:
		metadata, err := resourceManagerMetadataForName(resource.Name)
		if err != nil {
			return resourceManagerMetadata{}, err
		}
		metadata.DisplayName = resource.DisplayName
		metadata.Parent = resource.Parent
		metadata.State = resource.State
		return metadata, nil
	case *cloudresourcemanager.Organization:
		metadata, err := resourceManagerMetadataForName(resource.Name)
		if err != nil {
			return resourceManagerMetadata{}, err
		}
		metadata.DisplayName = resource.DisplayName
		metadata.State = resource.State
		return metadata, nil
	default:
		return resourceManagerMetadata{}, fmt.Errorf("unsupported GCP resource hierarchy node %T", node.Resource)
	}
}

func resourceManagerMetadataForName(name string) (resourceManagerMetadata, error) {
	switch {
	case strings.HasPrefix(name, "projects/") && len(strings.TrimPrefix(name, "projects/")) > 0:
		return resourceManagerMetadata{Name: name, ConfigClass: "ResourceManager::Project", AssetType: "cloudresourcemanager.googleapis.com/Project"}, nil
	case strings.HasPrefix(name, "folders/") && len(strings.TrimPrefix(name, "folders/")) > 0:
		return resourceManagerMetadata{Name: name, ConfigClass: "ResourceManager::Folder", AssetType: "cloudresourcemanager.googleapis.com/Folder"}, nil
	case strings.HasPrefix(name, "organizations/") && len(strings.TrimPrefix(name, "organizations/")) > 0:
		return resourceManagerMetadata{Name: name, ConfigClass: "ResourceManager::Organization", AssetType: "cloudresourcemanager.googleapis.com/Organization"}, nil
	default:
		return resourceManagerMetadata{}, fmt.Errorf("unsupported GCP resource hierarchy name %q", name)
	}
}

func resourceManagerIAMPolicy(policy *cloudresourcemanager.Policy) *iampb.Policy {
	bindings := make([]*iampb.Binding, 0, len(policy.Bindings))
	for _, binding := range policy.Bindings {
		if binding == nil {
			continue
		}
		bindings = append(bindings, &iampb.Binding{
			Role:    binding.Role,
			Members: binding.Members,
		})
	}
	return &iampb.Policy{
		Version:  int32(policy.Version),
		Bindings: bindings,
	}
}
