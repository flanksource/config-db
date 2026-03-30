package devops

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("mapAuditEntryToConfig", func() {
	org := "myorg"

	It("maps Pipeline area with PipelineId to pipeline config", func() {
		entry := AuditLogEntry{
			Area:        "Pipelines",
			ProjectName: "MyProject",
			Data:        map[string]any{"PipelineId": float64(42)},
		}
		externalID, configType := mapAuditEntryToConfig(org, entry)
		Expect(configType).To(Equal(PipelineType))
		Expect(externalID).To(Equal("azuredevops://myorg/MyProject/pipeline/42"))
	})

	It("maps Release area with PipelineId to release config", func() {
		entry := AuditLogEntry{
			Area:        "Release",
			ProjectName: "MyProject",
			Data:        map[string]any{"PipelineId": float64(10)},
		}
		externalID, configType := mapAuditEntryToConfig(org, entry)
		Expect(configType).To(Equal(ReleaseType))
		Expect(externalID).To(Equal("azuredevops://myorg/MyProject/release/10"))
	})

	It("maps Git area with RepoId to repository config", func() {
		entry := AuditLogEntry{
			Area:        "Git",
			ProjectName: "MyProject",
			Data:        map[string]any{"RepoId": "abc-123"},
		}
		externalID, configType := mapAuditEntryToConfig(org, entry)
		Expect(configType).To(Equal(RepositoryType))
		Expect(externalID).To(Equal("azuredevops://myorg/MyProject/repository/abc-123"))
	})

	It("maps Permissions area with RepositoryIdFromToken to repository config", func() {
		entry := AuditLogEntry{
			Area: "Permissions",
			Data: map[string]any{
				"RepositoryIdFromToken": "repo-uuid",
				"ProjectNameFromToken":  "MyProject",
			},
		}
		externalID, configType := mapAuditEntryToConfig(org, entry)
		Expect(configType).To(Equal(RepositoryType))
		Expect(externalID).To(Equal("azuredevops://myorg/MyProject/repository/repo-uuid"))
	})

	It("maps Policy area with RepoId to repository config", func() {
		entry := AuditLogEntry{
			Area:        "Policy",
			ProjectName: "MyProject",
			Data:        map[string]any{"RepoId": "policy-repo-uuid"},
		}
		externalID, configType := mapAuditEntryToConfig(org, entry)
		Expect(configType).To(Equal(RepositoryType))
		Expect(externalID).To(Equal("azuredevops://myorg/MyProject/repository/policy-repo-uuid"))
	})

	It("returns empty for unknown area", func() {
		entry := AuditLogEntry{
			Area:        "Token",
			ProjectName: "MyProject",
			Data:        map[string]any{"DisplayName": "my-pat"},
		}
		externalID, configType := mapAuditEntryToConfig(org, entry)
		Expect(configType).To(BeEmpty())
		Expect(externalID).To(BeEmpty())
	})

	It("returns empty when PipelineId is missing", func() {
		entry := AuditLogEntry{
			Area:        "Pipelines",
			ProjectName: "MyProject",
			Data:        map[string]any{},
		}
		externalID, configType := mapAuditEntryToConfig(org, entry)
		Expect(configType).To(BeEmpty())
		Expect(externalID).To(BeEmpty())
	})

	It("returns empty when project is missing", func() {
		entry := AuditLogEntry{
			Area: "Pipelines",
			Data: map[string]any{"PipelineId": float64(42)},
		}
		externalID, configType := mapAuditEntryToConfig(org, entry)
		Expect(configType).To(BeEmpty())
		Expect(externalID).To(BeEmpty())
	})
})

var _ = Describe("isExcluded", func() {
	exclusions := []string{"AuditLog.AccessLog", "Pipelines.Deployment"}

	It("excludes exact match", func() {
		Expect(isExcluded("AuditLog.AccessLog", exclusions)).To(BeTrue())
	})

	It("excludes prefix match", func() {
		Expect(isExcluded("Pipelines.DeploymentJobCompleted", exclusions)).To(BeTrue())
	})

	It("does not exclude non-matching", func() {
		Expect(isExcluded("Git.RepositoryCreated", exclusions)).To(BeFalse())
	})

	It("does not exclude with empty exclusions", func() {
		Expect(isExcluded("AuditLog.AccessLog", nil)).To(BeFalse())
	})
})

var _ = Describe("auditSeverity", func() {
	It("returns high for remove category", func() {
		Expect(auditSeverity("remove", "Git")).To(Equal("high"))
	})

	It("returns medium for Security area", func() {
		Expect(auditSeverity("modify", "Security")).To(Equal("medium"))
	})

	It("returns medium for Token area", func() {
		Expect(auditSeverity("create", "Token")).To(Equal("medium"))
	})

	It("returns info for normal events", func() {
		Expect(auditSeverity("create", "Pipelines")).To(Equal("info"))
	})
})

var _ = Describe("dataInt", func() {
	It("handles float64 from JSON", func() {
		v, ok := dataInt(map[string]any{"x": float64(42)}, "x")
		Expect(ok).To(BeTrue())
		Expect(v).To(Equal(42))
	})

	It("handles int", func() {
		v, ok := dataInt(map[string]any{"x": 42}, "x")
		Expect(ok).To(BeTrue())
		Expect(v).To(Equal(42))
	})

	It("returns false for missing key", func() {
		_, ok := dataInt(map[string]any{}, "x")
		Expect(ok).To(BeFalse())
	})

	It("returns false for string value", func() {
		_, ok := dataInt(map[string]any{"x": "not-a-number"}, "x")
		Expect(ok).To(BeFalse())
	})
})
