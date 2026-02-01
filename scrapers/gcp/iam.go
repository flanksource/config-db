package gcp

import (
	"fmt"
	"strings"
	"time"

	asset "cloud.google.com/go/asset/apiv1"
	"cloud.google.com/go/asset/apiv1/assetpb"
	v1 "github.com/flanksource/config-db/api"
	"github.com/flanksource/duty/models"
	"github.com/google/uuid"
	"github.com/samber/lo"
	"google.golang.org/api/iterator"
)

func parseGCPMember(member string) (userType, name, email string, found bool) {
	if after, ok := strings.CutPrefix(member, "user:"); ok {
		email = after
		return "User", email, email, true
	} else if after, ok := strings.CutPrefix(member, "serviceAccount:"); ok {
		email = after
		return "ServiceAccount", email, email, true
	} else if after, ok := strings.CutPrefix(member, "group:"); ok {
		name = after
		return "Group", name, name, true
	}

	return "", member, "", false
}

// FetchIAMPolicies scrapes external users and roles.
func (Scraper) FetchIAMPolicies(ctx *GCPContext, config v1.GCP) (v1.ScrapeResults, error) {
	var results v1.ScrapeResults

	req := &assetpb.ListAssetsRequest{
		Parent:      fmt.Sprintf("projects/%s", config.Project),
		ContentType: assetpb.ContentType_IAM_POLICY,
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

	// Track unique roles and users to avoid duplicates
	uniqueRoles := make(map[uuid.UUID]models.ExternalRole)
	uniqueUsers := make(map[uuid.UUID]models.ExternalUser)
	var configAccesses []v1.ExternalConfigAccess

	it := assetClient.ListAssets(ctx, req)
	for {
		asset, err := it.Next()
		if err == iterator.Done {
			break
		} else if err != nil {
			return nil, fmt.Errorf("error listing IAM policies: %w", err)
		}

		if asset.IamPolicy == nil {
			continue
		}

		// Extract resource ID from asset name (e.g., "//compute.googleapis.com/projects/project-id/zones/us-central1-a/instances/instance-name")
		resourceID := asset.Name
		if resourceID == "" {
			continue
		}

		for _, binding := range asset.IamPolicy.Bindings {
			// bind.Role could be
			// global: roles/cloudasset.owner
			// custom: projects/aditya-461913/roles/mycustomroleaditya (project scoped)
			roleID := generateConsistentID(binding.Role)
			if _, exists := uniqueRoles[roleID]; !exists {
				role := models.ExternalRole{
					ID:        roleID,
					Name:      binding.Role,
					AccountID: config.Project,
					ScraperID: ctx.ScrapeConfig().GetPersistedID(),
					RoleType:  lo.Ternary(strings.HasPrefix(binding.Role, "roles/"), "Global", "Custom"),
				}

				if strings.HasPrefix(binding.Role, "roles/") {
					role.RoleType = "Global"
				} else {
					role.RoleType = "Custom"

					// FIXME: Only custom roles should be tied to an account and scraper
				}

				uniqueRoles[roleID] = role
			}

			for _, member := range binding.Members {
				userType, name, email, found := parseGCPMember(member)
				if !found {
					continue
				}

				userID := generateConsistentID(email)
				if _, exists := uniqueUsers[userID]; !exists {
					externalUser := models.ExternalUser{
						ID:        userID,
						Name:      name,
						ScraperID: lo.FromPtr(ctx.ScrapeConfig().GetPersistedID()),
						AccountID: config.Project,
						CreatedAt: time.Now(), // We don't have this information
						UserType:  userType,
					}
					if email != "" {
						externalUser.Email = &email
					}
					uniqueUsers[userID] = externalUser
				}
			}
		}
	}

	var externalRoles []models.ExternalRole
	for _, role := range uniqueRoles {
		externalRoles = append(externalRoles, role)
	}

	var externalUsers []models.ExternalUser
	for _, user := range uniqueUsers {
		externalUsers = append(externalUsers, user)
	}

	results = append(results, v1.ScrapeResult{
		BaseScraper:   config.BaseScraper,
		ExternalRoles: externalRoles,
		ExternalUsers: externalUsers,
		ConfigAccess:  configAccesses,
	})

	return results, nil
}
