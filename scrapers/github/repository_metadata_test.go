package github

import (
	"os"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/scrapers/processors"
	dutyCtx "github.com/flanksource/duty/context"
	"github.com/flanksource/duty/types"
	gogithub "github.com/google/go-github/v73/github"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"
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

	It("adds fork, license, and archive properties through the GitHub plugin", func() {
		pluginYAML, err := os.ReadFile("../../fixtures/plugins/github.yaml")
		Expect(err).ToNot(HaveOccurred())

		var plugin v1.ScrapePlugin
		Expect(yaml.Unmarshal(pluginYAML, &plugin)).To(Succeed())

		repo := &gogithub.Repository{
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
			v1.BaseScraper{}.ApplyPlugins(plugin.Spec),
			nil,
			nil,
		)
		Expect(result.Config.(*gogithub.Repository).License.Body).To(BeNil())

		extractor, err := processors.NewExtractor(result.BaseScraper)
		Expect(err).ToNot(HaveOccurred())

		extracted, err := extractor.Extract(api.NewScrapeContext(dutyCtx.New()), result)
		Expect(err).ToNot(HaveOccurred())
		Expect(extracted).To(HaveLen(1))
		Expect(extracted[0].Properties[1:]).To(Equal(types.Properties{
			{Name: "Forked", Type: "badge", Text: "true", Links: []types.Link{}},
			{Name: "License", Type: "badge", Text: "Apache-2.0", Links: []types.Link{}},
			{Name: "Archived", Type: "badge", Text: "false", Links: []types.Link{}},
		}))
	})
})
