package scrapers

import (
	"encoding/json"

	v1 "github.com/flanksource/config-db/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("extractConfigChangesFromConfig", func() {
	type testCase struct {
		input                map[string]any
		expectError          bool
		expectedCount        int
		expectedIDs          []string
		expectedUserAliases  [][]string
		expectedRoleAliases  [][]string
		expectedGroupAliases [][]string
	}

	DescribeTable("config_access extraction",
		func(tc testCase) {
			result, err := extractConfigChangesFromConfig(tc.input)

			if tc.expectError {
				Expect(err).To(HaveOccurred())
				return
			}
			Expect(err).ToNot(HaveOccurred())
			Expect(result.ConfigAccess).To(HaveLen(tc.expectedCount))

			for i, expectedID := range tc.expectedIDs {
				Expect(result.ConfigAccess[i].ID).To(Equal(expectedID))
			}
			for i, expectedAliases := range tc.expectedUserAliases {
				Expect(result.ConfigAccess[i].ExternalUserAliases).To(Equal(expectedAliases))
			}
			for i, expectedAliases := range tc.expectedRoleAliases {
				Expect(result.ConfigAccess[i].ExternalRoleAliases).To(Equal(expectedAliases))
			}
			for i, expectedAliases := range tc.expectedGroupAliases {
				Expect(result.ConfigAccess[i].ExternalGroupAliases).To(Equal(expectedAliases))
			}
		},
		Entry("with external_user_aliases", testCase{
			input: map[string]any{
				"config": map[string]any{"name": "Test Org"},
				"config_access": []map[string]any{
					{"id": "access-001", "external_config_id": map[string]any{"config_type": "Organization", "external_id": "test-org"}, "external_user_aliases": []string{"alice", "alice@example.com"}},
					{"id": "access-002", "external_config_id": map[string]any{"config_type": "Organization", "external_id": "test-org"}, "external_user_aliases": []string{"bob"}},
				},
			},
			expectedCount:       2,
			expectedIDs:         []string{"access-001", "access-002"},
			expectedUserAliases: [][]string{{"alice", "alice@example.com"}, {"bob"}},
		}),
		Entry("with external_role_aliases", testCase{
			input: map[string]any{
				"config": map[string]any{"name": "Test Org"},
				"config_access": []map[string]any{
					{"id": "access-003", "external_config_id": map[string]any{"config_type": "Organization", "external_id": "test-org"}, "external_user_aliases": []string{"alice"}, "external_role_aliases": []string{"admin", "owner"}},
				},
			},
			expectedCount:       1,
			expectedIDs:         []string{"access-003"},
			expectedUserAliases: [][]string{{"alice"}},
			expectedRoleAliases: [][]string{{"admin", "owner"}},
		}),
		Entry("with external_group_aliases", testCase{
			input: map[string]any{
				"config": map[string]any{"name": "Test Org"},
				"config_access": []map[string]any{
					{"id": "access-004", "external_config_id": map[string]any{"config_type": "Organization", "external_id": "test-org"}, "external_user_aliases": []string{"alice"}, "external_group_aliases": []string{"admins", "super-users"}},
				},
			},
			expectedCount:        1,
			expectedIDs:          []string{"access-004"},
			expectedUserAliases:  [][]string{{"alice"}},
			expectedGroupAliases: [][]string{{"admins", "super-users"}},
		}),
		Entry("with user, role and group aliases combined", testCase{
			input: map[string]any{
				"config": map[string]any{"name": "Test Org"},
				"config_access": []map[string]any{
					{"id": "access-005", "external_config_id": map[string]any{"config_type": "Organization", "external_id": "test-org"}, "external_user_aliases": []string{"alice", "alice@example.com"}, "external_role_aliases": []string{"admin", "owner"}, "external_group_aliases": []string{"admins", "super-users"}},
				},
			},
			expectedCount:        1,
			expectedIDs:          []string{"access-005"},
			expectedUserAliases:  [][]string{{"alice", "alice@example.com"}},
			expectedRoleAliases:  [][]string{{"admin", "owner"}},
			expectedGroupAliases: [][]string{{"admins", "super-users"}},
		}),
		Entry("with empty config_access", testCase{
			input: map[string]any{
				"config":        map[string]any{"name": "Test Org"},
				"config_access": []map[string]any{},
			},
			expectedCount: 0,
		}),
		Entry("with missing config_access", testCase{
			input:         map[string]any{"config": map[string]any{"name": "Test Org"}},
			expectedCount: 0,
		}),
		Entry("with external_config_id", testCase{
			input: map[string]any{
				"config_access": []map[string]any{
					{"id": "access-003", "external_config_id": map[string]any{"config_type": "Database", "external_id": "db-123"}, "external_user_aliases": []string{"admin"}},
				},
			},
			expectedCount: 1,
			expectedIDs:   []string{"access-003"},
		}),
	)

	It("should extract external_config_id fields correctly", func() {
		input := map[string]any{
			"config_access": []map[string]any{
				{"id": "access-001", "external_config_id": map[string]any{"config_type": "Organization", "external_id": "org-123"}, "external_user_aliases": []string{"user1"}},
			},
		}

		result, err := extractConfigChangesFromConfig(input)
		Expect(err).ToNot(HaveOccurred())
		Expect(result.ConfigAccess).To(HaveLen(1))
		Expect(result.ConfigAccess[0].ConfigExternalID.ConfigType).To(Equal("Organization"))
		Expect(result.ConfigAccess[0].ConfigExternalID.ExternalID).To(Equal("org-123"))
	})

	It("should handle JSON round-trip through extractConfigChangesFromConfig", func() {
		jsonData := `{
			"id": "test-org",
			"config": {"name": "Test Organization"},
			"external_users": [{"name": "Alice", "aliases": ["alice", "alice@example.com"]}],
			"config_access": [{"id": "access-001", "external_config_id": {"config_type": "Organization", "external_id": "test-org"}, "external_user_aliases": ["alice", "alice@example.com"]}]
		}`

		var configMap map[string]any
		Expect(json.Unmarshal([]byte(jsonData), &configMap)).To(Succeed())

		result, err := extractConfigChangesFromConfig(configMap)
		Expect(err).ToNot(HaveOccurred())
		Expect(result.ExternalUsers).To(HaveLen(1))
		Expect(result.ConfigAccess).To(HaveLen(1))

		ca := result.ConfigAccess[0]
		Expect(ca.ID).To(Equal("access-001"))
		Expect(ca.ConfigExternalID.ConfigType).To(Equal("Organization"))
		Expect(ca.ConfigExternalID.ExternalID).To(Equal("test-org"))
		Expect(ca.ExternalUserAliases).To(Equal([]string{"alice", "alice@example.com"}))
	})
})

var _ = Describe("ExternalConfigAccess unmarshal", func() {
	It("should unmarshal all alias fields from JSON", func() {
		jsonData := `[{
			"id": "access-001",
			"external_config_id": {"config_type": "Bucket", "external_id": "s3://my-bucket"},
			"external_user_aliases": ["admin", "admin@example.com"],
			"external_role_aliases": ["bucket-reader", "bucket-writer"],
			"external_group_aliases": ["storage-team", "cloud-ops"]
		}]`

		var configAccesses []v1.ExternalConfigAccess
		Expect(json.Unmarshal([]byte(jsonData), &configAccesses)).To(Succeed())
		Expect(configAccesses).To(HaveLen(1))

		ca := configAccesses[0]
		Expect(ca.ID).To(Equal("access-001"))
		Expect(ca.ConfigExternalID.ConfigType).To(Equal("Bucket"))
		Expect(ca.ConfigExternalID.ExternalID).To(Equal("s3://my-bucket"))
		Expect(ca.ExternalUserAliases).To(Equal([]string{"admin", "admin@example.com"}))
		Expect(ca.ExternalRoleAliases).To(Equal([]string{"bucket-reader", "bucket-writer"}))
		Expect(ca.ExternalGroupAliases).To(Equal([]string{"storage-team", "cloud-ops"}))
	})
})
