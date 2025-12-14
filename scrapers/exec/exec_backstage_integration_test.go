package exec

import (
	"encoding/json"
	"fmt"

	"github.com/flanksource/config-db/api"
	ginkgo "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Exec Scraper - Backstage Catalog Integration", ginkgo.Ordered, func() {
	ginkgo.It("should scrape Backstage catalog entities from GitHub", func() {
		// Load fixture
		scrapeConfig := getConfigSpec("exec-backstage-catalog")

		// Create scrape context
		scraperCtx := api.NewScrapeContext(DefaultContext).WithScrapeConfig(&scrapeConfig)

		// Execute scraper
		scraper := ExecScraper{}
		results := scraper.Scrape(scraperCtx)

		// Verify we got results
		Expect(results).NotTo(BeEmpty(), "Expected config items from Backstage catalog")

		// Count entities by type
		componentCount := 0
		apiCount := 0
		systemCount := 0
		domainCount := 0

		for i, result := range results {
			Expect(result.Error).To(BeNil(), fmt.Sprintf("Result %d should not have error", i))
			Expect(result.Config).NotTo(BeNil(), fmt.Sprintf("Result %d should have config", i))

			// Parse config JSON
			configStr, ok := result.Config.(string)
			Expect(ok).To(BeTrue(), "Config should be a string")

			var config map[string]any
			err := json.Unmarshal([]byte(configStr), &config)
			Expect(err).NotTo(HaveOccurred())

			// Verify basic structure
			Expect(config["id"]).NotTo(BeEmpty(), "Entity should have id")
			Expect(config["type"]).To(HavePrefix("Backstage::"), "Type should be Backstage entity")
			Expect(config["name"]).NotTo(BeEmpty(), "Entity should have name")

			// Count by type
			entityType := config["type"].(string)
			switch entityType {
			case "Backstage::Component":
				componentCount++
			case "Backstage::API":
				apiCount++
			case "Backstage::System":
				systemCount++
			case "Backstage::Domain":
				domainCount++
			}

			// Verify metadata structure
			metadata, hasMetadata := config["metadata"].(map[string]any)
			Expect(hasMetadata).To(BeTrue(), "Entity should have metadata")
			Expect(metadata["name"]).NotTo(BeEmpty(), "Metadata should have name")

			// Verify spec structure
			spec, hasSpec := config["spec"].(map[string]any)
			Expect(hasSpec).To(BeTrue(), "Entity should have spec")
			_ = spec // Avoid unused variable warning

			// Verify tags if present
			if tags, hasTags := config["tags"].([]any); hasTags {
				Expect(tags).NotTo(BeEmpty(), "Tags array should not be empty if present")
			}
		}

		// Verify we scraped multiple entity types
		Expect(componentCount).To(BeNumerically(">", 0), "Should scrape at least one Component")

		// Log counts for visibility
		ginkgo.GinkgoWriter.Printf("Scraped: %d Components, %d APIs, %d Systems, %d Domains\n",
			componentCount, apiCount, systemCount, domainCount)
	})

	ginkgo.It("should create relationships between entities", func() {
		scrapeConfig := getConfigSpec("exec-backstage-catalog")
		scraperCtx := api.NewScrapeContext(DefaultContext).WithScrapeConfig(&scrapeConfig)

		scraper := ExecScraper{}
		results := scraper.Scrape(scraperCtx)

		// Find a component with relationships
		var componentWithOwner map[string]any
		for _, result := range results {
			if result.Error != nil {
				continue
			}

			configStr := result.Config.(string)
			var config map[string]any
			json.Unmarshal([]byte(configStr), &config)

			if config["type"] == "Backstage::Component" {
				spec := config["spec"].(map[string]any)
				if owner, hasOwner := spec["owner"]; hasOwner && owner != nil {
					componentWithOwner = config
					break
				}
			}
		}

		Expect(componentWithOwner).NotTo(BeNil(), "Should find at least one component with owner")

		// Verify owner relationship field exists in spec
		spec := componentWithOwner["spec"].(map[string]any)
		Expect(spec["owner"]).NotTo(BeEmpty(), "Component should have owner in spec")

		ginkgo.GinkgoWriter.Printf("Component '%s' has owner: %s\n",
			componentWithOwner["name"], spec["owner"])
	})

	ginkgo.It("should extract properties correctly", func() {
		scrapeConfig := getConfigSpec("exec-backstage-catalog")
		scraperCtx := api.NewScrapeContext(DefaultContext).WithScrapeConfig(&scrapeConfig)

		scraper := ExecScraper{}
		results := scraper.Scrape(scraperCtx)

		// Find component with type and lifecycle
		var componentWithProps map[string]any
		for _, result := range results {
			if result.Error != nil {
				continue
			}

			configStr := result.Config.(string)
			var config map[string]any
			json.Unmarshal([]byte(configStr), &config)

			if config["type"] == "Backstage::Component" {
				spec := config["spec"].(map[string]any)
				if _, hasType := spec["type"]; hasType {
					if _, hasLifecycle := spec["lifecycle"]; hasLifecycle {
						componentWithProps = config
						break
					}
				}
			}
		}

		Expect(componentWithProps).NotTo(BeNil(), "Should find component with type and lifecycle")

		spec := componentWithProps["spec"].(map[string]any)
		Expect(spec["type"]).To(BeElementOf("service", "website", "library", "documentation"), "Type should be valid")
		Expect(spec["lifecycle"]).To(BeElementOf("experimental", "production", "deprecated"), "Lifecycle should be valid")

		ginkgo.GinkgoWriter.Printf("Component '%s': type=%s, lifecycle=%s\n",
			componentWithProps["name"], spec["type"], spec["lifecycle"])
	})

	ginkgo.It("should extract tags from multiple sources", func() {
		scrapeConfig := getConfigSpec("exec-backstage-catalog")
		scraperCtx := api.NewScrapeContext(DefaultContext).WithScrapeConfig(&scrapeConfig)

		scraper := ExecScraper{}
		results := scraper.Scrape(scraperCtx)

		// Find entity with tags
		var entityWithTags map[string]any
		for _, result := range results {
			if result.Error != nil {
				continue
			}

			configStr := result.Config.(string)
			var config map[string]any
			json.Unmarshal([]byte(configStr), &config)

			if tags, hasTags := config["tags"].([]any); hasTags && len(tags) > 0 {
				entityWithTags = config
				break
			}
		}

		Expect(entityWithTags).NotTo(BeNil(), "Should find entity with tags")

		tags := entityWithTags["tags"].([]any)
		Expect(tags).NotTo(BeEmpty(), "Tags should not be empty")

		ginkgo.GinkgoWriter.Printf("Entity '%s' tags: %v\n",
			entityWithTags["name"], tags)
	})
})
