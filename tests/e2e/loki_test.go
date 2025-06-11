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

var _ = ginkgo.Describe("Logs Scraper - Loki", ginkgo.Ordered, func() {
	var (
		lokiURL       string
		testSetup     TestSetup
		postgresSetup struct {
			ConfigScraper models.ConfigScraper
			ConfigItem    dbmodels.ConfigItem
		}
	)

	ginkgo.BeforeAll(func() {
		lokiURL = os.Getenv("LOKI_URL")
		if lokiURL == "" {
			lokiURL = "http://localhost:3100"
		}

		testSetup.Server = createTestServer(DefaultContext)
		waitForService(lokiURL, "/ready", 30*time.Second)

		fixturePath := filepath.Join("testdata", "loki.yaml")
		var err error
		testSetup.ScrapeConfig, err = loadScrapeConfig(fixturePath)
		Expect(err).NotTo(HaveOccurred())

		for i := range testSetup.ScrapeConfig.Spec.Logs {
			if testSetup.ScrapeConfig.Spec.Logs[i].Loki != nil {
				testSetup.ScrapeConfig.Spec.Logs[i].Loki.URL = lokiURL
			}
		}

		testSetup.ConfigScraper, err = db.PersistScrapeConfigFromFile(DefaultContext, *testSetup.ScrapeConfig)
		Expect(err).NotTo(HaveOccurred())

		postgresSetup.ConfigScraper, postgresSetup.ConfigItem, err = createDummyScraperAndConfig(
			DefaultContext,
			"postgres-0",
			"fdee1b15-4579-499e-adc5-2817735ec3f6",
			"Database::PostgreSQL",
		)
		Expect(err).NotTo(HaveOccurred())

		err = injectTestLogsToLoki(lokiURL)
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
		verifyConfigChanges(DefaultContext, postgresSetup.ConfigItem.ID, 3)
	})
})
