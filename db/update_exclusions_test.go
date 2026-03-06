// ABOUTME: Tests for generic external entity exclusion filtering.
// ABOUTME: Verifies that applyExternalEntityExclusions removes matching entities from ScrapeResults.
package db

import (
	"testing"

	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty/models"
)

func TestApplyExternalEntityExclusions(t *testing.T) {
	t.Run("excludes users by name pattern", func(t *testing.T) {
		result := &v1.ScrapeResult{
			ExternalUsers: []models.ExternalUser{
				{Name: "system:node:ip-10-0-1-5"},
				{Name: "alice"},
				{Name: "system:node:ip-10-0-1-6"},
			},
		}

		applyExternalEntityExclusions(result, v1.ScraperExclusion{
			ExternalUsers: []string{"system:node:*"},
		})

		if len(result.ExternalUsers) != 1 {
			t.Fatalf("expected 1 user, got %d", len(result.ExternalUsers))
		}
		if result.ExternalUsers[0].Name != "alice" {
			t.Fatalf("expected alice, got %s", result.ExternalUsers[0].Name)
		}
	})

	t.Run("excludes roles by exact name", func(t *testing.T) {
		result := &v1.ScrapeResult{
			ExternalRoles: []models.ExternalRole{
				{Name: "admin"},
				{Name: "system:controller:replicaset-controller"},
				{Name: "viewer"},
			},
		}

		applyExternalEntityExclusions(result, v1.ScraperExclusion{
			ExternalRoles: []string{"system:controller:replicaset-controller"},
		})

		if len(result.ExternalRoles) != 2 {
			t.Fatalf("expected 2 roles, got %d", len(result.ExternalRoles))
		}
	})

	t.Run("excludes groups by wildcard", func(t *testing.T) {
		result := &v1.ScrapeResult{
			ExternalGroups: []models.ExternalGroup{
				{Name: "system:authenticated"},
				{Name: "developers"},
				{Name: "system:unauthenticated"},
			},
		}

		applyExternalEntityExclusions(result, v1.ScraperExclusion{
			ExternalGroups: []string{"system:*"},
		})

		if len(result.ExternalGroups) != 1 {
			t.Fatalf("expected 1 group, got %d", len(result.ExternalGroups))
		}
		if result.ExternalGroups[0].Name != "developers" {
			t.Fatalf("expected developers, got %s", result.ExternalGroups[0].Name)
		}
	})

	t.Run("removes config access referencing excluded user aliases", func(t *testing.T) {
		result := &v1.ScrapeResult{
			ExternalUsers: []models.ExternalUser{
				{Name: "system:kube-proxy"},
				{Name: "alice"},
			},
			ConfigAccess: []v1.ExternalConfigAccess{
				{
					ConfigExternalID:    v1.ExternalID{ConfigType: "Test", ExternalID: "1"},
					ExternalUserAliases: []string{"system:kube-proxy"},
				},
				{
					ConfigExternalID:    v1.ExternalID{ConfigType: "Test", ExternalID: "2"},
					ExternalUserAliases: []string{"alice"},
				},
			},
		}

		applyExternalEntityExclusions(result, v1.ScraperExclusion{
			ExternalUsers: []string{"system:*"},
		})

		if len(result.ExternalUsers) != 1 {
			t.Fatalf("expected 1 user, got %d", len(result.ExternalUsers))
		}
		if len(result.ConfigAccess) != 1 {
			t.Fatalf("expected 1 config access, got %d", len(result.ConfigAccess))
		}
		if result.ConfigAccess[0].ExternalUserAliases[0] != "alice" {
			t.Fatalf("expected alice access, got %s", result.ConfigAccess[0].ExternalUserAliases[0])
		}
	})

	t.Run("removes config access referencing excluded role aliases", func(t *testing.T) {
		result := &v1.ScrapeResult{
			ConfigAccess: []v1.ExternalConfigAccess{
				{
					ConfigExternalID:    v1.ExternalID{ConfigType: "Test", ExternalID: "1"},
					ExternalRoleAliases: []string{"system:controller:job-controller"},
				},
				{
					ConfigExternalID:    v1.ExternalID{ConfigType: "Test", ExternalID: "2"},
					ExternalRoleAliases: []string{"custom-role"},
				},
			},
		}

		applyExternalEntityExclusions(result, v1.ScraperExclusion{
			ExternalRoles: []string{"system:controller:*"},
		})

		if len(result.ConfigAccess) != 1 {
			t.Fatalf("expected 1 config access, got %d", len(result.ConfigAccess))
		}
	})

	t.Run("no-op when exclusions are empty", func(t *testing.T) {
		result := &v1.ScrapeResult{
			ExternalUsers:  []models.ExternalUser{{Name: "alice"}},
			ExternalGroups: []models.ExternalGroup{{Name: "devs"}},
			ExternalRoles:  []models.ExternalRole{{Name: "admin"}},
		}

		applyExternalEntityExclusions(result, v1.ScraperExclusion{})

		if len(result.ExternalUsers) != 1 || len(result.ExternalGroups) != 1 || len(result.ExternalRoles) != 1 {
			t.Fatal("empty exclusions should not filter anything")
		}
	})

	t.Run("multiple exclusion patterns", func(t *testing.T) {
		result := &v1.ScrapeResult{
			ExternalUsers: []models.ExternalUser{
				{Name: "system:kube-proxy"},
				{Name: "system:node:ip-10-0-1-5"},
				{Name: "alice"},
				{Name: "bot-deployer"},
			},
		}

		applyExternalEntityExclusions(result, v1.ScraperExclusion{
			ExternalUsers: []string{"system:*", "bot-*"},
		})

		if len(result.ExternalUsers) != 1 {
			t.Fatalf("expected 1 user, got %d", len(result.ExternalUsers))
		}
		if result.ExternalUsers[0].Name != "alice" {
			t.Fatalf("expected alice, got %s", result.ExternalUsers[0].Name)
		}
	})
}
