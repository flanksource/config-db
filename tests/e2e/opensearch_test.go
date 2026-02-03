package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"time"

	"github.com/flanksource/commons/logger"
	"github.com/flanksource/duty/models"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	ginkgo "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1 "github.com/flanksource/config-db/api"
	"github.com/flanksource/config-db/db"
	dbmodels "github.com/flanksource/config-db/db/models"
	"github.com/flanksource/config-db/pkg/api"
	"github.com/flanksource/config-db/scrapers"
)

var _ = ginkgo.Describe("Logs Scraper - OpenSearch", ginkgo.Ordered, func() {
	var (
		opensearchURL         string
		server                *httptest.Server
		scrapeConfig          *v1.ScrapeConfig
		configScraper         models.ConfigScraper
		postgresConfigScraper models.ConfigScraper
		configItem            dbmodels.ConfigItem
	)

	ginkgo.BeforeAll(func() {
		opensearchURL = os.Getenv("OPENSEARCH_URL")
		if opensearchURL == "" {
			opensearchURL = "http://localhost:9200"
		}

		e := echo.New()
		e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
			return func(c echo.Context) error {
				c.SetRequest(c.Request().WithContext(DefaultContext.Wrap(c.Request().Context())))
				return next(c)
			}
		})
		e.POST("/run/:id", scrapers.RunNowHandler)

		server = httptest.NewServer(e)

		Eventually(func() error {
			resp, err := http.Get(opensearchURL + "/_cluster/health")
			if err != nil {
				return err
			}
			defer func() {
				if err := resp.Body.Close(); err != nil {
					logger.Errorf("failed to close response body: %v", err)
				}
			}()

			if resp.StatusCode != 200 {
				return fmt.Errorf("opensearch not ready, status: %d", resp.StatusCode)
			}

			return nil
		}, 30*time.Second, 2*time.Second).Should(Succeed())

		fixturePath := filepath.Join("testdata", "opensearch.yaml")
		configs, err := v1.ParseConfigs(fixturePath)
		Expect(err).NotTo(HaveOccurred())
		Expect(configs).To(HaveLen(1))

		scrapeConfig = &configs[0]

		for i := range scrapeConfig.Spec.Logs {
			if scrapeConfig.Spec.Logs[i].OpenSearch != nil {
				scrapeConfig.Spec.Logs[i].OpenSearch.Address = opensearchURL
			}
		}

		configScraper, err = db.PersistScrapeConfigFromFile(DefaultContext, *scrapeConfig)
		Expect(err).NotTo(HaveOccurred())

		postgresSpec := v1.ScrapeConfig{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "postgres-scraper-2",
				Namespace: "default",
				UID:       "654e45cc-d30f-4b66-b2e9-aec92def9a5b",
			},
			Spec: v1.ScraperSpec{
				SQL: []v1.SQL{
					{
						Connection: v1.Connection{
							Connection: "postgres://localhost:5432/test",
						},
						Query: "SELECT 'postgres-0' as name",
					},
				},
			},
		}

		postgresConfigScraper, err = db.PersistScrapeConfigFromFile(DefaultContext, postgresSpec)
		Expect(err).NotTo(HaveOccurred())

		configItemID := uuid.MustParse("fdee1b15-4579-499e-adc5-2817735ec3f6")
		configItem = dbmodels.ConfigItem{
			ID:         configItemID.String(),
			ExternalID: []string{configItemID.String(), "postgres-0"},
			Type:       "Database::PostgreSQL",
			Name:       lo.ToPtr("postgres-0"),
			Config:     lo.ToPtr(`{"name":"postgres-0","port":5432,"host":"localhost"}`),
			ScraperID:  &postgresConfigScraper.ID,
		}

		scraperCtx := api.NewScrapeContext(DefaultContext)
		err = db.CreateConfigItem(scraperCtx, &configItem)
		Expect(err).NotTo(HaveOccurred())

		err = injectTestLogsToOpenSearch(opensearchURL)
		Expect(err).NotTo(HaveOccurred())

		scrapers.InitSemaphoreWeights(DefaultContext)
	})

	ginkgo.AfterAll(func() {
		if server != nil {
			server.Close()
		}
	})

	ginkgo.It("should trigger logs scraper via /run endpoint", func() {
		runURL := fmt.Sprintf("%s/run/%s", server.URL, configScraper.ID)
		resp, err := http.Post(runURL, "application/json", nil)
		Expect(err).NotTo(HaveOccurred())
		defer func() {
			if err := resp.Body.Close(); err != nil {
				logger.Errorf("failed to close response body: %v", err)
			}
		}()

		Expect(resp.StatusCode).To(Equal(200))

		Eventually(func(g Gomega) {
			var jobHistories []models.JobHistory
			err := DefaultContext.DB().Where("resource_id = ?", configScraper.ID.String()).
				Order("created_at DESC").
				Find(&jobHistories).Error
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(jobHistories).To(HaveLen(1))
			g.Expect(jobHistories[0].Status).To(Equal(models.StatusSuccess))
		}, 20*time.Second, 1*time.Second).Should(Succeed())

		var configChanges []dbmodels.ConfigChange
		err = DefaultContext.DB().Where("config_id = ?", configItem.ID).Find(&configChanges).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(configChanges).To(HaveLen(3))

		for _, change := range configChanges {
			Expect(change.ChangeType).To(Equal("ConfigReload"))
			Expect(change.Summary).To(ContainSubstring("changed from"))
		}
	})
})

func injectTestLogsToOpenSearch(opensearchURL string) error {
	now := time.Now()

	// Create index if it doesn't exist
	indexName := "app-logs"
	createIndexURL := fmt.Sprintf("%s/%s", opensearchURL, indexName)

	indexMapping := map[string]any{
		"mappings": map[string]any{
			"properties": map[string]any{
				"@timestamp": map[string]any{"type": "date"},
				"message":    map[string]any{"type": "text"},
				"level":      map[string]any{"type": "keyword"},
				"job":        map[string]any{"type": "keyword"},
				"host":       map[string]any{"type": "keyword"},
			},
		},
	}

	indexData, err := json.Marshal(indexMapping)
	if err != nil {
		return fmt.Errorf("failed to marshal index mapping: %w", err)
	}

	req, err := http.NewRequest("PUT", createIndexURL, bytes.NewBuffer(indexData))
	if err != nil {
		return fmt.Errorf("failed to create index request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to create index: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			logger.Errorf("failed to close response body: %v", err)
		}
	}()

	// Inject test documents
	docs := []map[string]any{
		{
			"@timestamp": now.Format(time.RFC3339),
			"message":    "Configuration reloaded: database.max_connections changed from 100 to 200",
			"level":      "info",
			"job":        "app",
			"host":       "test-host-1",
		},
		{
			"@timestamp": now.Add(1 * time.Second).Format(time.RFC3339),
			"message":    "Configuration reloaded: server.timeout changed from 30s to 60s",
			"level":      "info",
			"job":        "app",
			"host":       "test-host-1",
		},
		{
			"@timestamp": now.Add(2 * time.Second).Format(time.RFC3339),
			"message":    "Configuration reloaded: cache.size changed from 1GB to 2GB",
			"level":      "info",
			"job":        "app",
			"host":       "test-host-1",
		},
	}

	for i, doc := range docs {
		docURL := fmt.Sprintf("%s/%s/_doc/%d", opensearchURL, indexName, i+1)
		docData, err := json.Marshal(doc)
		if err != nil {
			return fmt.Errorf("failed to marshal document: %w", err)
		}

		req, err := http.NewRequest("PUT", docURL, bytes.NewBuffer(docData))
		if err != nil {
			return fmt.Errorf("failed to create document request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("failed to index document: %w", err)
		}
		defer func() {
			if err := resp.Body.Close(); err != nil {
				logger.Errorf("failed to close response body: %v", err)
			}
		}()

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("failed to index document, status code: %d", resp.StatusCode)
		}
	}

	// Refresh index to make documents available for search
	refreshURL := fmt.Sprintf("%s/%s/_refresh", opensearchURL, indexName)
	resp, err = http.Post(refreshURL, "application/json", nil)
	if err != nil {
		return fmt.Errorf("failed to refresh index: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			logger.Errorf("failed to close response body: %v", err)
		}
	}()

	return nil
}
