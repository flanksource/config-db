package github

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	dutyCtx "github.com/flanksource/duty/context"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGitHubScraper(t *testing.T) {
	if os.Getenv("GITHUB_TOKEN") == "" {
		t.Skip("GITHUB_TOKEN not set, skipping e2e test")
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
	require.True(t, scraper.CanScrape(scrapeConfig.Spec))

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

	require.Len(t, configs, 1, "expected exactly 1 GitHub::Repository config item")

	repo := configs[0]
	assert.Equal(t, "GitHub::Repository", repo.Type)
	assert.Equal(t, "github/flanksource/canary-checker", repo.ID)
	assert.Equal(t, "flanksource/canary-checker", repo.Name)
	assert.Equal(t, "Repository", repo.ConfigClass)
	assert.Equal(t, "flanksource", repo.Tags["owner"])
	assert.Equal(t, "canary-checker", repo.Tags["repo"])
	assert.NotNil(t, repo.Config)
	assert.NotNil(t, repo.CreatedAt)
	assert.NotEmpty(t, repo.Properties)

	assert.NotEmpty(t, analyses, "expected at least some analysis results from security and/or OpenSSF")

	var hasDependabot, hasOpenSSF bool
	for _, a := range analyses {
		require.NotNil(t, a.AnalysisResult)
		switch a.AnalysisResult.Source {
		case "GitHub Dependabot":
			hasDependabot = true
		case "OpenSSF Scorecard":
			hasOpenSSF = true
		}
	}

	t.Logf("Results: %d config items, %d analyses (dependabot=%v, openssf=%v)",
		len(configs), len(analyses), hasDependabot, hasOpenSSF)

	// Verify deduplication: Code Scanning alerts matching OpenSSF check names should be dropped.
	// OpenSSF results are always kept (richer data: scores, details).
	openssfCheckNames := make(map[string]bool)
	for _, a := range analyses {
		if a.AnalysisResult != nil && a.AnalysisResult.Source == "OpenSSF Scorecard" {
			openssfCheckNames[a.AnalysisResult.Analyzer] = true
		}
	}
	for _, a := range analyses {
		if a.AnalysisResult != nil && a.AnalysisResult.Source == "GitHub Code Scanning" {
			assert.False(t, openssfCheckNames[a.AnalysisResult.Summary],
				"code scanning alert %q should be deduped (covered by OpenSSF check)", a.AnalysisResult.Summary)
		}
	}

	// Dump results for manual inspection
	if os.Getenv("DEBUG") != "" {
		data, _ := json.MarshalIndent(results, "", "  ")
		t.Logf("Full results:\n%s", string(data))
	}
}

func TestGitHubScraperOpenSSFOnly(t *testing.T) {
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

	if os.Getenv("GITHUB_TOKEN") == "" {
		t.Skip("GITHUB_TOKEN not set, skipping e2e test")
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

	require.Len(t, configs, 1)
	assert.Equal(t, "GitHub::Repository", configs[0].Type)

	// All OpenSSF checks should be present (dedup only filters code scanning alerts, not OpenSSF)
	var hasVulnerabilities bool
	for _, a := range analyses {
		if a.AnalysisResult != nil && a.AnalysisResult.Analyzer == "Vulnerabilities" {
			hasVulnerabilities = true
		}
	}
	assert.True(t, hasVulnerabilities, "Vulnerabilities check should be present")
}
