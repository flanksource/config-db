package gcp

import (
	"fmt"
	"strings"

	v1 "github.com/flanksource/config-db/api/v1"
	iam "google.golang.org/api/iam/v1"
)

// roleScope classifies a role id by the IAM Admin endpoint that resolves it.
func roleScope(role string) string {
	switch {
	case strings.HasPrefix(role, "roles/"):
		return "predefined"
	case strings.HasPrefix(role, "projects/") && strings.Contains(role, "/roles/"):
		return "project"
	case strings.HasPrefix(role, "organizations/") && strings.Contains(role, "/roles/"):
		return "organization"
	}
	return "unknown"
}

// enrichRoleConfigs augments each GCP::IAMRole config item's Config with the
// role definition (title, description, permissions, stage) from the IAM Admin
// API. Best-effort: a missing IAM Admin permission degrades to binding-derived
// config rather than failing the RBAC scrape — every failure is logged loudly.
func enrichRoleConfigs(ctx *GCPContext, roleConfigs []v1.ScrapeResult) {
	if len(roleConfigs) == 0 {
		return
	}

	svc, err := iam.NewService(ctx, ctx.ClientOpts...)
	if err != nil {
		ctx.Warnf("gcp iam: role enrichment disabled, failed to create IAM Admin client: %v", err)
		return
	}

	for i := range roleConfigs {
		role := roleConfigs[i].ID
		def, err := getRoleDefinition(ctx, svc, role)
		if err != nil {
			ctx.Warnf("gcp iam: failed to enrich role %s: %v", role, err)
			continue
		}

		config, ok := roleConfigs[i].Config.(map[string]any)
		if !ok {
			continue
		}
		config["title"] = def.Title
		config["description"] = def.Description
		config["stage"] = def.Stage
		config["includedPermissions"] = def.IncludedPermissions
		roleConfigs[i].Description = def.Title
	}
}

func getRoleDefinition(ctx *GCPContext, svc *iam.Service, role string) (*iam.Role, error) {
	switch roleScope(role) {
	case "predefined":
		return svc.Roles.Get(role).Context(ctx).Do()
	case "project":
		return svc.Projects.Roles.Get(role).Context(ctx).Do()
	case "organization":
		return svc.Organizations.Roles.Get(role).Context(ctx).Do()
	}
	return nil, fmt.Errorf("unrecognized role id format: %s", role)
}
