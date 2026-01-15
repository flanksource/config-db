// ABOUTME: Unit tests for config extraction functions.
// ABOUTME: Tests extractConfigChangesFromConfig including config_access extraction.
package scrapers

import (
	"encoding/json"
	"testing"

	v1 "github.com/flanksource/config-db/api/v1"
)

func TestExtractConfigAccess(t *testing.T) {
	testCases := []struct {
		name                  string
		input                 map[string]any
		expectError           bool
		expectedCount         int
		expectedIDs           []string
		expectedUserAliases   [][]string
		expectedRoleAliases   [][]string
		expectedGroupAliases  [][]string
	}{
		{
			name: "extracts config_access with external_user_aliases",
			input: map[string]any{
				"config": map[string]any{
					"name": "Test Org",
				},
				"config_access": []map[string]any{
					{
						"id": "access-001",
						"external_config_id": map[string]any{
							"config_type": "Organization",
							"external_id": "test-org",
						},
						"external_user_aliases": []string{"alice", "alice@example.com"},
					},
					{
						"id": "access-002",
						"external_config_id": map[string]any{
							"config_type": "Organization",
							"external_id": "test-org",
						},
						"external_user_aliases": []string{"bob"},
					},
				},
			},
			expectError:   false,
			expectedCount: 2,
			expectedIDs:   []string{"access-001", "access-002"},
			expectedUserAliases: [][]string{
				{"alice", "alice@example.com"},
				{"bob"},
			},
		},
		{
			name: "extracts config_access with external_role_aliases",
			input: map[string]any{
				"config": map[string]any{
					"name": "Test Org",
				},
				"config_access": []map[string]any{
					{
						"id": "access-003",
						"external_config_id": map[string]any{
							"config_type": "Organization",
							"external_id": "test-org",
						},
						"external_user_aliases": []string{"alice"},
						"external_role_aliases": []string{"admin", "owner"},
					},
				},
			},
			expectError:   false,
			expectedCount: 1,
			expectedIDs:   []string{"access-003"},
			expectedUserAliases: [][]string{
				{"alice"},
			},
			expectedRoleAliases: [][]string{
				{"admin", "owner"},
			},
		},
		{
			name: "extracts config_access with external_group_aliases",
			input: map[string]any{
				"config": map[string]any{
					"name": "Test Org",
				},
				"config_access": []map[string]any{
					{
						"id": "access-004",
						"external_config_id": map[string]any{
							"config_type": "Organization",
							"external_id": "test-org",
						},
						"external_user_aliases":  []string{"alice"},
						"external_group_aliases": []string{"admins", "super-users"},
					},
				},
			},
			expectError:   false,
			expectedCount: 1,
			expectedIDs:   []string{"access-004"},
			expectedUserAliases: [][]string{
				{"alice"},
			},
			expectedGroupAliases: [][]string{
				{"admins", "super-users"},
			},
		},
		{
			name: "extracts empty config_access",
			input: map[string]any{
				"config": map[string]any{
					"name": "Test Org",
				},
				"config_access": []map[string]any{},
			},
			expectError:   false,
			expectedCount: 0,
		},
		{
			name: "handles missing config_access",
			input: map[string]any{
				"config": map[string]any{
					"name": "Test Org",
				},
			},
			expectError:   false,
			expectedCount: 0,
		},
		{
			name: "extracts config_access with external_config_id",
			input: map[string]any{
				"config_access": []map[string]any{
					{
						"id": "access-003",
						"external_config_id": map[string]any{
							"config_type": "Database",
							"external_id": "db-123",
						},
						"external_user_aliases": []string{"admin"},
					},
				},
			},
			expectError:   false,
			expectedCount: 1,
			expectedIDs:   []string{"access-003"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := extractConfigChangesFromConfig(tc.input)

			if tc.expectError && err == nil {
				t.Errorf("expected error but got none")
				return
			}
			if !tc.expectError && err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if len(result.ConfigAccess) != tc.expectedCount {
				t.Errorf("expected %d config access entries, got %d", tc.expectedCount, len(result.ConfigAccess))
				return
			}

			if tc.expectedCount > 0 {
				for i, expectedID := range tc.expectedIDs {
					if result.ConfigAccess[i].ID != expectedID {
						t.Errorf("expected ID %s at index %d, got %s", expectedID, i, result.ConfigAccess[i].ID)
					}
				}

				// Verify user aliases are extracted
				for i, expectedAliases := range tc.expectedUserAliases {
					if i >= len(result.ConfigAccess) {
						break
					}
					gotAliases := result.ConfigAccess[i].ExternalUserAliases
					if len(gotAliases) != len(expectedAliases) {
						t.Errorf("expected %d user aliases at index %d, got %d", len(expectedAliases), i, len(gotAliases))
						continue
					}
					for j, alias := range expectedAliases {
						if gotAliases[j] != alias {
							t.Errorf("expected user alias %s at index %d,%d, got %s", alias, i, j, gotAliases[j])
						}
					}
				}

				// Verify role aliases are extracted
				for i, expectedAliases := range tc.expectedRoleAliases {
					if i >= len(result.ConfigAccess) {
						break
					}
					gotAliases := result.ConfigAccess[i].ExternalRoleAliases
					if len(gotAliases) != len(expectedAliases) {
						t.Errorf("expected %d role aliases at index %d, got %d", len(expectedAliases), i, len(gotAliases))
						continue
					}
					for j, alias := range expectedAliases {
						if gotAliases[j] != alias {
							t.Errorf("expected role alias %s at index %d,%d, got %s", alias, i, j, gotAliases[j])
						}
					}
				}

				// Verify group aliases are extracted
				for i, expectedAliases := range tc.expectedGroupAliases {
					if i >= len(result.ConfigAccess) {
						break
					}
					gotAliases := result.ConfigAccess[i].ExternalGroupAliases
					if len(gotAliases) != len(expectedAliases) {
						t.Errorf("expected %d group aliases at index %d, got %d", len(expectedAliases), i, len(gotAliases))
						continue
					}
					for j, alias := range expectedAliases {
						if gotAliases[j] != alias {
							t.Errorf("expected group alias %s at index %d,%d, got %s", alias, i, j, gotAliases[j])
						}
					}
				}
			}
		})
	}
}

func TestExtractConfigAccessExternalConfigID(t *testing.T) {
	input := map[string]any{
		"config_access": []map[string]any{
			{
				"id": "access-001",
				"external_config_id": map[string]any{
					"config_type": "Organization",
					"external_id": "org-123",
				},
				"external_user_aliases": []string{"user1"},
			},
		},
	}

	result, err := extractConfigChangesFromConfig(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.ConfigAccess) != 1 {
		t.Fatalf("expected 1 config access entry, got %d", len(result.ConfigAccess))
	}

	ca := result.ConfigAccess[0]
	if ca.ConfigExternalID.ConfigType != "Organization" {
		t.Errorf("expected config_type 'Organization', got '%s'", ca.ConfigExternalID.ConfigType)
	}
	if ca.ConfigExternalID.ExternalID != "org-123" {
		t.Errorf("expected ExternalID 'org-123', got '%s'", ca.ConfigExternalID.ExternalID)
	}
}

func TestExtractConfigAccessJSONRoundTrip(t *testing.T) {
	// Simulate the JSON that would come from a file scraper
	jsonData := `{
		"id": "test-org",
		"config": {
			"name": "Test Organization"
		},
		"external_users": [
			{
				"name": "Alice",
				"aliases": ["alice", "alice@example.com"]
			}
		],
		"config_access": [
			{
				"id": "access-001",
				"external_config_id": {
					"config_type": "Organization",
					"external_id": "test-org"
				},
				"external_user_aliases": ["alice", "alice@example.com"]
			}
		]
	}`

	var configMap map[string]any
	if err := json.Unmarshal([]byte(jsonData), &configMap); err != nil {
		t.Fatalf("failed to unmarshal JSON: %v", err)
	}

	result, err := extractConfigChangesFromConfig(configMap)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify external users are extracted
	if len(result.ExternalUsers) != 1 {
		t.Errorf("expected 1 external user, got %d", len(result.ExternalUsers))
	}

	// Verify config access is extracted
	if len(result.ConfigAccess) != 1 {
		t.Fatalf("expected 1 config access entry, got %d", len(result.ConfigAccess))
	}

	ca := result.ConfigAccess[0]
	if ca.ID != "access-001" {
		t.Errorf("expected ID 'access-001', got '%s'", ca.ID)
	}

	if ca.ConfigExternalID.ConfigType != "Organization" {
		t.Errorf("expected config_type 'Organization', got '%s'", ca.ConfigExternalID.ConfigType)
	}

	if ca.ConfigExternalID.ExternalID != "test-org" {
		t.Errorf("expected ExternalID 'test-org', got '%s'", ca.ConfigExternalID.ExternalID)
	}

	expectedAliases := []string{"alice", "alice@example.com"}
	if len(ca.ExternalUserAliases) != len(expectedAliases) {
		t.Errorf("expected %d aliases, got %d", len(expectedAliases), len(ca.ExternalUserAliases))
	} else {
		for i, alias := range expectedAliases {
			if ca.ExternalUserAliases[i] != alias {
				t.Errorf("expected alias '%s' at index %d, got '%s'", alias, i, ca.ExternalUserAliases[i])
			}
		}
	}
}

// Verify that ExternalConfigAccess can be unmarshaled from JSON correctly
func TestExternalConfigAccessUnmarshal(t *testing.T) {
	jsonData := `[
		{
			"id": "access-001",
			"external_config_id": {
				"config_type": "Bucket",
				"external_id": "s3://my-bucket"
			},
			"external_user_aliases": ["admin", "admin@example.com"],
			"external_role_aliases": ["bucket-reader", "bucket-writer"],
			"external_group_aliases": ["storage-team", "cloud-ops"]
		}
	]`

	var configAccesses []v1.ExternalConfigAccess
	if err := json.Unmarshal([]byte(jsonData), &configAccesses); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if len(configAccesses) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(configAccesses))
	}

	ca := configAccesses[0]
	if ca.ID != "access-001" {
		t.Errorf("expected ID 'access-001', got '%s'", ca.ID)
	}

	if ca.ConfigExternalID.ConfigType != "Bucket" {
		t.Errorf("expected config_type 'Bucket', got '%s'", ca.ConfigExternalID.ConfigType)
	}

	if ca.ConfigExternalID.ExternalID != "s3://my-bucket" {
		t.Errorf("expected ExternalID 's3://my-bucket', got '%s'", ca.ConfigExternalID.ExternalID)
	}

	if len(ca.ExternalUserAliases) != 2 {
		t.Errorf("expected 2 user aliases, got %d", len(ca.ExternalUserAliases))
	}

	if len(ca.ExternalRoleAliases) != 2 {
		t.Errorf("expected 2 role aliases, got %d", len(ca.ExternalRoleAliases))
	} else {
		expectedRoleAliases := []string{"bucket-reader", "bucket-writer"}
		for i, alias := range expectedRoleAliases {
			if ca.ExternalRoleAliases[i] != alias {
				t.Errorf("expected role alias '%s' at index %d, got '%s'", alias, i, ca.ExternalRoleAliases[i])
			}
		}
	}

	if len(ca.ExternalGroupAliases) != 2 {
		t.Errorf("expected 2 group aliases, got %d", len(ca.ExternalGroupAliases))
	} else {
		expectedGroupAliases := []string{"storage-team", "cloud-ops"}
		for i, alias := range expectedGroupAliases {
			if ca.ExternalGroupAliases[i] != alias {
				t.Errorf("expected group alias '%s' at index %d, got '%s'", alias, i, ca.ExternalGroupAliases[i])
			}
		}
	}
}
