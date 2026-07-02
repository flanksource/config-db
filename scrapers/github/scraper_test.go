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

		It("dedupes overlapping selectors and exact repositories", func() {
			repos := []*gogithub.Repository{
				{Name: gogithub.Ptr("mission-control"), Owner: &gogithub.User{Login: gogithub.Ptr("flanksource")}},
				{Name: gogithub.Ptr("mission-control-ui"), Owner: &gogithub.User{Login: gogithub.Ptr("flanksource")}},
			}

			seen := make(map[string]struct{})
			var resolved []v1.GitHubRepository
			for _, repo := range matchingRepositoryConfigs("flanksource", "*control", repos) {
				resolved = appendRepositoryConfig(resolved, seen, repo)
			}
			for _, repo := range matchingRepositoryConfigs("flanksource", "mission*", repos) {
				resolved = appendRepositoryConfig(resolved, seen, repo)
			}
			resolved = appendRepositoryConfig(resolved, seen, v1.GitHubRepository{Owner: "flanksource", Repo: "duty"})
			resolved = appendRepositoryConfig(resolved, seen, v1.GitHubRepository{Owner: "FlankSource", Repo: "MISSION-CONTROL"})

			Expect(resolved).To(Equal([]v1.GitHubRepository{
				{Owner: "flanksource", Repo: "mission-control"},
				{Owner: "flanksource", Repo: "mission-control-ui"},
				{Owner: "flanksource", Repo: "duty"},
			}))
		})
	})

	Context("OpenSSF code scanning dedupe", func() {
		newCodeScanningAlert := func(number int, toolName, ruleID, ruleDescription string) *gogithub.Alert {
			alert := &gogithub.Alert{
				Number:          gogithub.Ptr(number),
				State:           gogithub.Ptr("open"),
				RuleID:          gogithub.Ptr(ruleID),
				RuleDescription: gogithub.Ptr(ruleDescription),
				Rule: &gogithub.Rule{
					ID:                    gogithub.Ptr(ruleID),
					Description:           gogithub.Ptr(ruleDescription),
					Severity:              gogithub.Ptr("high"),
					SecuritySeverityLevel: gogithub.Ptr("high"),
				},
				MostRecentInstance: &gogithub.MostRecentInstance{
					Message: &gogithub.Message{Text: gogithub.Ptr("finding message")},
				},
			}
			if toolName != "" {
				alert.Tool = &gogithub.Tool{Name: gogithub.Ptr(toolName)}
			}
			return alert
		}

		codeScanningAnalyses := func(results v1.ScrapeResults) []*v1.AnalysisResult {
			var analyses []*v1.AnalysisResult
			for _, result := range results {
				if result.AnalysisResult != nil && result.AnalysisResult.Source == "GitHub Code Scanning" {
					analyses = append(analyses, result.AnalysisResult)
				}
			}
			return analyses
		}

		It("detects Scorecard-origin alerts by tool name only", func() {
			scorecardAlert := newCodeScanningAlert(32, "Scorecard", "VulnerabilitiesID", "Vulnerabilities")
			Expect(isOpenSSFCodeScanningAlert(scorecardAlert)).To(BeTrue())

			checkName, ok := openSSFCodeScanningCheckName(scorecardAlert)
			Expect(ok).To(BeTrue())
			Expect(checkName).To(Equal("Vulnerabilities"))

			openssfScorecardAlert := newCodeScanningAlert(33, "OpenSSF Scorecard", "VulnerabilitiesID", "Vulnerabilities")
			Expect(isOpenSSFCodeScanningAlert(openssfScorecardAlert)).To(BeTrue())

			codeQLAlert := newCodeScanningAlert(34, "CodeQL", "VulnerabilitiesID", "Vulnerabilities")
			Expect(isOpenSSFCodeScanningAlert(codeQLAlert)).To(BeFalse())
		})

		It("skips only Scorecard-origin code scanning alerts when OpenSSF API data exists", func() {
			alerts := &allAlerts{codeScanning: []*gogithub.Alert{
				newCodeScanningAlert(32, "Scorecard", "VulnerabilitiesID", "Vulnerabilities"),
				newCodeScanningAlert(33, "CodeQL", "go/sql-injection", "SQL injection"),
			}}

			var results v1.ScrapeResults
			ctx := api.NewScrapeContext(dutyCtx.New())
			createAlertAnalyses(ctx, &results, "github/acme/repo", alerts, true)

			analyses := codeScanningAnalyses(results)
			Expect(analyses).To(HaveLen(1))
			Expect(analyses[0].Analyzer).To(Equal("go/sql-injection"))
		})

		It("keeps Scorecard-origin code scanning alerts when OpenSSF API data is missing", func() {
			alerts := &allAlerts{codeScanning: []*gogithub.Alert{
				newCodeScanningAlert(32, "Scorecard", "VulnerabilitiesID", "Vulnerabilities"),
			}}

			var results v1.ScrapeResults
			ctx := api.NewScrapeContext(dutyCtx.New())
			createAlertAnalyses(ctx, &results, "github/acme/repo", alerts, false)

			analyses := codeScanningAnalyses(results)
			Expect(analyses).To(HaveLen(1))
			Expect(analyses[0].Analyzer).To(Equal("VulnerabilitiesID"))
		})

		It("enriches OpenSSF analyses with matching GitHub code scanning URLs", func() {
			alerts := &allAlerts{codeScanning: []*gogithub.Alert{
				newCodeScanningAlert(32, "Scorecard", "VulnerabilitiesID", "Vulnerabilities"),
			}}
			urls := codeScanningURLsByOpenSSFCheckName("github/acme/repo", alerts)
			Expect(urls).To(HaveKeyWithValue("Vulnerabilities", "https://github.com/acme/repo/security/code-scanning/32"))

			scorecard := &ScorecardResponse{Checks: []CheckResult{{
				Name:   "Vulnerabilities",
				Score:  0,
				Reason: "project has vulnerabilities",
			}}}

			var results v1.ScrapeResults
			ctx := api.NewScrapeContext(dutyCtx.New())
			createScorecardAnalyses(ctx, &results, "github/acme/repo", v1.GitHubRepository{}, scorecard, urls)

			Expect(results).To(HaveLen(1))
			var found bool
			for _, property := range results[0].AnalysisResult.Properties {
				if property.Name == "GitHub Code Scanning Alert" {
					found = true
					Expect(property.Text).To(Equal("https://github.com/acme/repo/security/code-scanning/32"))
				}
			}
			Expect(found).To(BeTrue())
		})

		It("excludes skipped Scorecard-origin alerts from repository alert counts", func() {
			alerts := &allAlerts{codeScanning: []*gogithub.Alert{
				newCodeScanningAlert(32, "Scorecard", "VulnerabilitiesID", "Vulnerabilities"),
				newCodeScanningAlert(33, "CodeQL", "go/sql-injection", "SQL injection"),
			}}

			counts := alerts.countsExcludingOpenSSFCodeScanning()
			Expect(counts.high).To(Equal(1))
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
					for _, property := range a.AnalysisResult.Properties {
						if property.Name == "Tool" {
							Expect(property.Text).ToNot(MatchRegexp("(?i)scorecard"),
								"Scorecard-origin code scanning alert %q should be deduped", a.AnalysisResult.Summary)
						}
					}
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
