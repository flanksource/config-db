package github

import (
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty/types"
	gogithub "github.com/google/go-github/v73/github"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("GitHub repository metadata", func() {
	It("adds every repository topic as a namespaced tag", func() {
		repo := &gogithub.Repository{
			Topics: []string{"golang", "observability"},
		}

		result := buildRepositoryResult(repo, v1.GitHubRepository{Owner: "acme", Repo: "telemetry"}, v1.BaseScraper{}, nil, nil)

		Expect(result.Tags).To(Equal(v1.JSONStringMap{
			"owner":               "acme",
			"repo":                "telemetry",
			"topic/golang":        "true",
			"topic/observability": "true",
		}))
	})

	It("adds fork, license, and archive properties by default", func() {
		repositoryURL := "https://github.com/acme/telemetry"
		repo := &gogithub.Repository{
			HTMLURL:  gogithub.Ptr(repositoryURL),
			Fork:     gogithub.Ptr(true),
			Archived: gogithub.Ptr(false),
			License: &gogithub.License{
				SPDXID: gogithub.Ptr("Apache-2.0"),
				Body:   gogithub.Ptr("full license body"),
			},
		}
		result := buildRepositoryResult(
			repo,
			v1.GitHubRepository{Owner: "acme", Repo: "telemetry"},
			v1.BaseScraper{},
			nil,
			nil,
		)
		Expect(result.Config.(*gogithub.Repository).License.Body).To(BeNil())
		Expect(result.Properties).To(Equal(types.Properties{
			{Name: "URL", Type: "url", Text: repositoryURL, Links: []types.Link{{URL: repositoryURL, Type: "url"}}},
			{Name: "Forked", Type: "badge", Text: "true"},
			{Name: "License", Type: "badge", Text: "Apache-2.0"},
			{Name: "Archived", Type: "badge", Text: "false"},
		}))
	})

	It("retains repository governance and security settings for audits", func() {
		repo := &gogithub.Repository{
			Visibility:               gogithub.Ptr("internal"),
			Private:                  gogithub.Ptr(true),
			DefaultBranch:            gogithub.Ptr("main"),
			IsTemplate:               gogithub.Ptr(false),
			AllowForking:             gogithub.Ptr(false),
			AllowAutoMerge:           gogithub.Ptr(false),
			AllowUpdateBranch:        gogithub.Ptr(true),
			AllowMergeCommit:         gogithub.Ptr(false),
			AllowSquashMerge:         gogithub.Ptr(true),
			AllowRebaseMerge:         gogithub.Ptr(false),
			DeleteBranchOnMerge:      gogithub.Ptr(true),
			WebCommitSignoffRequired: gogithub.Ptr(true),
			HasIssues:                gogithub.Ptr(true),
			HasProjects:              gogithub.Ptr(false),
			HasWiki:                  gogithub.Ptr(false),
			HasDiscussions:           gogithub.Ptr(true),
			Permissions:              map[string]bool{"admin": true},
			CustomProperties:         map[string]any{"data_classification": "internal"},
			SecurityAndAnalysis: &gogithub.SecurityAndAnalysis{
				AdvancedSecurity:             &gogithub.AdvancedSecurity{Status: gogithub.Ptr("enabled")},
				SecretScanning:               &gogithub.SecretScanning{Status: gogithub.Ptr("enabled")},
				SecretScanningPushProtection: &gogithub.SecretScanningPushProtection{Status: gogithub.Ptr("enabled")},
				DependabotSecurityUpdates:    &gogithub.DependabotSecurityUpdates{Status: gogithub.Ptr("enabled")},
			},
		}

		config := buildRepositoryResult(
			repo,
			v1.GitHubRepository{Owner: "acme", Repo: "telemetry"},
			v1.BaseScraper{},
			nil,
			nil,
		).Config.(*gogithub.Repository)

		Expect(config).To(Equal(repo))
	})
})
