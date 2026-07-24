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

func fetchResourceManagerHierarchy(ctx *GCPContext, config v1.GCP) (*cloudresourcemanager.Project, []resourceManagerNode, error) {
	service, err := cloudresourcemanager.NewService(ctx, ctx.ClientOpts...)
	if err != nil {
		return nil, nil, fmt.Errorf("create Cloud Resource Manager client: %w", err)
	}

	projectName := config.Project
	if !strings.HasPrefix(projectName, "projects/") {
		projectName = "projects/" + projectName
	}
	project, err := service.Projects.Get(projectName).Context(ctx).Do()
	if err != nil {
		return nil, nil, fmt.Errorf("get GCP project hierarchy root %s: %w", projectName, err)
	}

	var nodes []resourceManagerNode
	visited := make(map[string]struct{})
	for parent := project.Parent; parent != ""; {
		if _, ok := visited[parent]; ok {
			return nil, nil, fmt.Errorf("cyclic GCP resource hierarchy at %s", parent)
		}
		visited[parent] = struct{}{}

		node, err := fetchResourceManagerNode(ctx, service, parent)
		if err != nil {
			return nil, nil, err
		}
		nodes = append(nodes, node)

		metadata, err := node.metadata()
		if err != nil {
			return nil, nil, err
		}
		parent = metadata.Parent
	}

	return project, nodes, nil
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

func buildResourceManagerHierarchy(project *cloudresourcemanager.Project, nodes []resourceManagerNode, base v1.BaseScraper) (v1.ScrapeResults, []*assetpb.Asset, error) {
	if project == nil || project.Name == "" {
		return nil, nil, fmt.Errorf("GCP project hierarchy root has no resource name")
	}
	if _, err := resourceManagerMetadataForName(project.Name); err != nil {
		return nil, nil, err
	}

	expectedName := project.Parent
	var results v1.ScrapeResults
	var policies []*assetpb.Asset
	for _, node := range nodes {
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
