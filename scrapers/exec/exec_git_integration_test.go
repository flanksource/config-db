package exec

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty/connection"
	ginkgo "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = ginkgo.Describe("Exec Scraper - Git Checkout Integration", ginkgo.Ordered, func() {
	var (
		repoPath string
	)

	ginkgo.BeforeAll(func() {
		// Get absolute path to config-db repository
		var err error
		repoPath, err = os.Getwd()
		Expect(err).NotTo(HaveOccurred())

		// Navigate to repo root (we might be in scrapers/exec/)
		for {
			if _, err := os.Stat(filepath.Join(repoPath, "go.mod")); err == nil {
				break
			}
			parent := filepath.Dir(repoPath)
			if parent == repoPath {
				ginkgo.Fail("Could not find repository root (go.mod)")
			}
			repoPath = parent
		}
	})

	ginkgo.It("should checkout git repository and execute script", func() {
		// Create test ScrapeConfig with git checkout
		scrapeConfig := v1.ScrapeConfig{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "exec-git-checkout-test",
				Namespace: "default",
			},
			Spec: v1.ScraperSpec{
				Exec: []v1.Exec{
					{
						BaseScraper: v1.BaseScraper{
							CustomScraperBase: v1.CustomScraperBase{
								ID:   "$.id",
								Type: "$.type",
								Name: "$.name",
							},
						},
						// Use an inline script that generates test JSON output
						// This script runs in the context of the cloned repository
						Script: `#!/bin/bash
# This script runs in the git checkout directory
# Verify we're in a git repo by checking for .git or go.mod
if [ ! -f go.mod ]; then
  echo "Error: Not in repository root" >&2
  exit 1
fi

# Output test config items
cat << 'EOF'
[
  {
    "id": "config-1",
    "type": "Custom::Config",
    "name": "Test Config 1",
    "config": {
      "key": "value1",
      "source": "git-checkout-test"
    }
  },
  {
    "id": "config-2",
    "type": "Custom::Config",
    "name": "Test Config 2",
    "config": {
      "key": "value2",
      "source": "git-checkout-test"
    }
  }
]
EOF
`,
						Checkout: &connection.GitConnection{
							URL:    fmt.Sprintf("file://%s", repoPath),
							Branch: "main",
						},
					},
				},
			},
		}

		// Create context using DefaultContext from suite
		scraperCtx := api.NewScrapeContext(DefaultContext).WithScrapeConfig(&scrapeConfig)

		// Execute scraper
		scraper := ExecScraper{}
		results := scraper.Scrape(scraperCtx)

		// Verify results
		Expect(results).To(HaveLen(2), "Expected 2 config items from script output")

		// Verify both results are successful
		for i, result := range results {
			Expect(result.Error).To(BeNil(), fmt.Sprintf("Result %d should not have error", i))
			Expect(result.Config).NotTo(BeNil(), fmt.Sprintf("Result %d should have config", i))
		}

		// Parse and verify first config item
		configStr, ok := results[0].Config.(string)
		Expect(ok).To(BeTrue(), "Config should be a string")
		var config1 map[string]any
		err := json.Unmarshal([]byte(configStr), &config1)
		Expect(err).NotTo(HaveOccurred())
		Expect(config1["id"]).To(Equal("config-1"))
		Expect(config1["type"]).To(Equal("Custom::Config"))
		Expect(config1["name"]).To(Equal("Test Config 1"))
		configData1 := config1["config"].(map[string]any)
		Expect(configData1["key"]).To(Equal("value1"))
		Expect(configData1["source"]).To(Equal("git-checkout-test"))

		// Parse and verify second config item
		configStr2, ok := results[1].Config.(string)
		Expect(ok).To(BeTrue(), "Config should be a string")
		var config2 map[string]any
		err = json.Unmarshal([]byte(configStr2), &config2)
		Expect(err).NotTo(HaveOccurred())
		Expect(config2["id"]).To(Equal("config-2"))
		Expect(config2["type"]).To(Equal("Custom::Config"))
		Expect(config2["name"]).To(Equal("Test Config 2"))
		configData2 := config2["config"].(map[string]any)
		Expect(configData2["key"]).To(Equal("value2"))
		Expect(configData2["source"]).To(Equal("git-checkout-test"))
	})
})
