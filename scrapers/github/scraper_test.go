package github

import (
	"encoding/json"
	"os"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	dutyCtx "github.com/flanksource/duty/context"
	"github.com/flanksource/duty/models"
	gogithub "github.com/google/go-github/v73/github"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("GitHubScraper", func() {
	Context("repository selectors", func() {
		DescribeTable("detects selector syntax",
			func(repo string, expected bool) {
				Expect(isRepositorySelector(repo)).To(Equal(expected))
			},
			Entry("exact repo", "duty", false),
			Entry("wildcard", "*", true),
			Entry("prefix wildcard", "config-*", true),
			Entry("comma-separated allow-list", "duty,config-db", true),
			Entry("exclusion-only", "!archive-*", true),
		)

		It("splits MatchItems repo patterns", func() {
			Expect(splitRepositoryPatterns("duty, config-*, !config-test")).To(Equal([]string{"duty", "config-*", "!config-test"}))
		})

		It("selects matching repositories and ignores archived repositories", func() {
			repos := []*gogithub.Repository{
				{Name: gogithub.Ptr("config-db"), Owner: &gogithub.User{Login: gogithub.Ptr("flanksource")}},
				{Name: gogithub.Ptr("config-test"), Owner: &gogithub.User{Login: gogithub.Ptr("flanksource")}},
				{Name: gogithub.Ptr("duty"), Owner: &gogithub.User{Login: gogithub.Ptr("flanksource")}},
				{Name: gogithub.Ptr("archived"), Owner: &gogithub.User{Login: gogithub.Ptr("flanksource")}, Archived: gogithub.Ptr(true)},
			}

			matched := matchingRepositoryConfigs("flanksource", "*,!config-test", repos)

			Expect(matched).To(Equal([]v1.GitHubRepository{
				{Owner: "flanksource", Repo: "config-db"},
				{Owner: "flanksource", Repo: "duty"},
			}))
		})
	})

	Context("with security and OpenSSF enabled", func() {
		It("should scrape repository config and analysis results", func() {
			if os.Getenv("GITHUB_TOKEN") == "" {
				Skip("GITHUB_TOKEN not set, skipping e2e test")
			}

			scrapeConfig := v1.ScrapeConfig{}
			scrapeConfig.Name = "github-test"
			scrapeConfig.Spec = v1.ScraperSpec{
				GitHub: []v1.GitHub{{
					Security: true,
					OpenSSF:  true,
					Repositories: []v1.GitHubRepository{
						{Owner: "flanksource", Repo: "canary-checker"},
					},
				}},
			}

			ctx := api.NewScrapeContext(dutyCtx.New()).WithScrapeConfig(&scrapeConfig)

			scraper := GithubScraper{}
			Expect(scraper.CanScrape(scrapeConfig.Spec)).To(BeTrue())

			results := scraper.Scrape(ctx)

			var configs []v1.ScrapeResult
			var analyses []v1.ScrapeResult
			for _, r := range results {
				if r.Config != nil {
					configs = append(configs, r)
				} else if r.AnalysisResult != nil {
					analyses = append(analyses, r)
				}
			}

			Expect(configs).To(HaveLen(1), "expected exactly 1 GitHub::Repository config item")

			repo := configs[0]
			Expect(repo.Type).To(Equal("GitHub::Repository"))
			Expect(repo.ID).To(Equal("github/flanksource/canary-checker"))
			Expect(repo.Name).To(Equal("flanksource/canary-checker"))
			Expect(repo.ConfigClass).To(Equal("Repository"))
			Expect(repo.Tags["owner"]).To(Equal("flanksource"))
			Expect(repo.Tags["repo"]).To(Equal("canary-checker"))
			Expect(repo.Config).ToNot(BeNil())
			Expect(repo.CreatedAt).ToNot(BeNil())
			Expect(repo.Properties).ToNot(BeEmpty())

			Expect(analyses).ToNot(BeEmpty(), "expected at least some analysis results from security and/or OpenSSF")

			var hasDependabot, hasOpenSSF bool
			for _, a := range analyses {
				Expect(a.AnalysisResult).ToNot(BeNil())
				switch a.AnalysisResult.Source {
				case "GitHub Dependabot":
					hasDependabot = true
				case "OpenSSF Scorecard":
					hasOpenSSF = true
				}
			}

			GinkgoWriter.Printf("Results: %d config items, %d analyses (dependabot=%v, openssf=%v)\n",
				len(configs), len(analyses), hasDependabot, hasOpenSSF)

			openssfCheckNames := make(map[string]bool)
			for _, a := range analyses {
				if a.AnalysisResult == nil || a.AnalysisResult.Source != "OpenSSF Scorecard" {
					continue
				}

				openssfCheckNames[a.AnalysisResult.Analyzer] = true
				Expect(a.AnalysisResult.Status).To(Equal(models.AnalysisStatusOpen))
				Expect(a.AnalysisResult.Status).ToNot(Equal("resolved"))
				Expect(a.AnalysisResult.Status).ToNot(Equal("failing"))
				Expect(a.AnalysisResult.Status).ToNot(Equal("passing"))
				Expect(a.AnalysisResult.Analysis).To(HaveKey("score"))
				score, ok := a.AnalysisResult.Analysis["score"].(int)
				Expect(ok).To(BeTrue(), "expected OpenSSF score to be an int")
				Expect(score).To(BeNumerically("<", 10))
			}
			Expect(openssfCheckNames).ToNot(BeEmpty(),
				"dedup validation requires at least one OpenSSF check to be meaningful")
			for _, a := range analyses {
				if a.AnalysisResult != nil && a.AnalysisResult.Source == "GitHub Code Scanning" {
					Expect(openssfCheckNames[a.AnalysisResult.Summary]).To(BeFalse(),
						"code scanning alert %q should be deduped (covered by OpenSSF check)", a.AnalysisResult.Summary)
				}
			}

			if os.Getenv("DEBUG") != "" {
				data, _ := json.MarshalIndent(results, "", "  ")
				GinkgoWriter.Printf("Full results:\n%s\n", string(data))
			}
		})
	})

	Context("with OpenSSF only", func() {
		It("should include Vulnerabilities check", func() {
			if os.Getenv("GITHUB_TOKEN") == "" {
				Skip("GITHUB_TOKEN not set, skipping e2e test")
			}

			scrapeConfig := v1.ScrapeConfig{}
			scrapeConfig.Name = "github-openssf-only"
			scrapeConfig.Spec = v1.ScraperSpec{
				GitHub: []v1.GitHub{{
					OpenSSF: true,
					Repositories: []v1.GitHubRepository{
						{Owner: "flanksource", Repo: "canary-checker"},
					},
				}},
			}

			ctx := api.NewScrapeContext(dutyCtx.New()).WithScrapeConfig(&scrapeConfig)
			results := GithubScraper{}.Scrape(ctx)

			var configs, analyses []v1.ScrapeResult
			for _, r := range results {
				if r.Config != nil {
					configs = append(configs, r)
				} else if r.AnalysisResult != nil {
					analyses = append(analyses, r)
				}
			}

			Expect(configs).To(HaveLen(1))
			Expect(configs[0].Type).To(Equal("GitHub::Repository"))

			var hasVulnerabilities bool
			for _, a := range analyses {
				if a.AnalysisResult != nil && a.AnalysisResult.Analyzer == "Vulnerabilities" {
					hasVulnerabilities = true
				}
			}
			Expect(hasVulnerabilities).To(BeTrue(), "Vulnerabilities check should be present")
		})
	})
})
