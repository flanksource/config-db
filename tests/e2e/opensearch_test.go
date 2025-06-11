package e2e

import (
	"os"
	"path/filepath"
	"time"

	"github.com/flanksource/duty/models"
	ginkgo "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/flanksource/config-db/db"
	dbmodels "github.com/flanksource/config-db/db/models"
	"github.com/flanksource/config-db/scrapers"
)

var _ = ginkgo.Describe("Logs Scraper - OpenSearch", ginkgo.Ordered, func() {
	var (
		opensearchURL string
		testSetup     TestSetup
		mysqlSetup    struct {
			ConfigScraper models.ConfigScraper
			ConfigItem    dbmodels.ConfigItem
		}
	)

	ginkgo.BeforeAll(func() {
		opensearchURL = os.Getenv("OPENSEARCH_URL")
		if opensearchURL == "" {
			opensearchURL = "http://localhost:9200"
		}

		testSetup.Server = createTestServer(DefaultContext)
		waitForService(opensearchURL, "/_cluster/health", 30*time.Second)

		fixturePath := filepath.Join("testdata", "opensearch.yaml")
		var err error
		testSetup.ScrapeConfig, err = loadScrapeConfig(fixturePath)
		Expect(err).NotTo(HaveOccurred())

		for i := range testSetup.ScrapeConfig.Spec.Logs {
			if testSetup.ScrapeConfig.Spec.Logs[i].OpenSearch != nil {
				testSetup.ScrapeConfig.Spec.Logs[i].OpenSearch.Address = opensearchURL
			}
		}

		testSetup.ConfigScraper, err = db.PersistScrapeConfigFromFile(DefaultContext, *testSetup.ScrapeConfig)
		Expect(err).NotTo(HaveOccurred())

		mysqlSetup.ConfigScraper, mysqlSetup.ConfigItem, err = createDummyScraperAndConfig(
			DefaultContext,
			"mysql-0",
			"45c381f0-7f73-40d3-88f3-5653a7c518d1",
			"Database::MySQL",
		)
		Expect(err).NotTo(HaveOccurred())

		err = injectTestLogsToOpenSearch(opensearchURL)
		Expect(err).NotTo(HaveOccurred())

		scrapers.InitSemaphoreWeights(DefaultContext)
	})

	ginkgo.AfterAll(func() {
		if testSetup.Server != nil {
			testSetup.Server.Close()
		}
	})

	ginkgo.It("should trigger logs scraper via /run endpoint", func() {
		triggerScraper(testSetup.Server.URL, testSetup.ConfigScraper.ID)
		verifyJobHistory(DefaultContext, testSetup.ConfigScraper.ID, 20*time.Second)
		verifyConfigChanges(DefaultContext, mysqlSetup.ConfigItem.ID, 3)
	})
})
