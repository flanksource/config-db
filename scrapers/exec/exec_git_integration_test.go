package exec

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/flanksource/config-db/api"
	ginkgo "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Exec Scraper - Git Checkout Integration", ginkgo.Ordered, func() {
	ginkgo.It("should checkout git repository and execute script", func() {
		// Load fixture from fixtures directory
		scrapeConfig := getConfigSpec("exec-git-checkout-test")

		// Get absolute path to config-db repository for git checkout
		repoPath, err := os.Getwd()
		Expect(err).NotTo(HaveOccurred())

		// Update checkout URL to use local repository via file:// protocol
		scrapeConfig.Spec.Exec[0].Checkout.URL = fmt.Sprintf("file://%s", repoPath)

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
		err = json.Unmarshal([]byte(configStr), &config1)
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
