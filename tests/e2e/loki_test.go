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

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
	dbmodels "github.com/flanksource/config-db/db/models"
	"github.com/flanksource/config-db/scrapers"
)

var _ = ginkgo.Describe("Logs Scraper - Loki", ginkgo.Ordered, func() {
	var (
		lokiURL               string
		server                *httptest.Server
		scrapeConfig          *v1.ScrapeConfig
		configScraper         models.ConfigScraper
		postgresConfigScraper models.ConfigScraper
	)

	ginkgo.BeforeAll(func() {
		lokiURL = os.Getenv("LOKI_URL")
		if lokiURL == "" {
			lokiURL = "http://localhost:3100"
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
			resp, err := http.Get(lokiURL + "/ready")
			if err != nil {
				return err
			}
			defer func() {
				if err := resp.Body.Close(); err != nil {
					logger.Errorf("failed to close response body: %v", err)
				}
			}()

			if resp.StatusCode != 200 {
				return fmt.Errorf("loki not ready, status: %d", resp.StatusCode)
			}

			return nil
		}, 30*time.Second, 2*time.Second).Should(Succeed())

		fixturePath := filepath.Join("testdata", "loki.yaml")
		configs, err := v1.ParseConfigs(fixturePath)
		Expect(err).NotTo(HaveOccurred())
		Expect(configs).To(HaveLen(1))

		scrapeConfig = &configs[0]

		for i := range scrapeConfig.Spec.Logs {
			if scrapeConfig.Spec.Logs[i].Loki != nil {
				scrapeConfig.Spec.Logs[i].Loki.URL = lokiURL
			}
		}

		configScraper, err = db.PersistScrapeConfigFromFile(DefaultContext, *scrapeConfig)
		Expect(err).NotTo(HaveOccurred())

		postgresSpec := v1.ScrapeConfig{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "postgres-scraper",
				Namespace: "default",
				UID:       "59f5ef3a-9399-45c5-92e3-43100869b5d7",
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

		configItemID, _ := uuid.Parse("fdee1b15-4579-499e-adc5-2817735ec3f6")
		configItem := &dbmodels.ConfigItem{
			ID:         configItemID.String(),
			ExternalID: []string{configItemID.String(), "postgres-0"},
			Type:       "Database::PostgreSQL",
			Name:       lo.ToPtr("postgres-0"),
			Config:     lo.ToPtr(`{"name":"postgres-0","port":5432,"host":"localhost"}`),
			ScraperID:  &postgresConfigScraper.ID,
		}

		scraperCtx := api.NewScrapeContext(DefaultContext)
		err = db.CreateConfigItem(scraperCtx, configItem)
		Expect(err).NotTo(HaveOccurred())

		err = injectTestLogs(lokiURL)
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
		err = DefaultContext.DB().Where("config_id = ?", "fdee1b15-4579-499e-adc5-2817735ec3f6").Find(&configChanges).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(configChanges).To(HaveLen(3))

		for _, change := range configChanges {
			Expect(change.ChangeType).To(Equal("ConfigReload"))
			Expect(change.Summary).To(ContainSubstring("changed from"))
		}
	})
})

type LogEntry struct {
	Timestamp int64
	Message   string
}

type LogStream struct {
	Labels  map[string]string
	Entries []LogEntry
}

func (ls LogStream) ToLokiFormat() map[string]any {
	values := make([][]string, len(ls.Entries))
	for i, entry := range ls.Entries {
		values[i] = []string{fmt.Sprintf("%d", entry.Timestamp), entry.Message}
	}

	return map[string]any{
		"stream": ls.Labels,
		"values": values,
	}
}

func injectTestLogs(lokiURL string) error {
	now := time.Now().UnixNano()

	streams := []LogStream{
		{
			Labels: map[string]string{
				"job":   "app",
				"level": "info",
				"host":  "test-host-1",
			},
			Entries: []LogEntry{
				{Timestamp: now, Message: "Configuration reloaded: database.max_connections changed from 100 to 200"},
				{Timestamp: now + 1000000, Message: "Configuration reloaded: server.timeout changed from 30s to 60s"},
				{Timestamp: now + 2000000, Message: "Configuration reloaded: cache.size changed from 1GB to 2GB"},
			},
		},
	}

	lokiStreams := make([]map[string]any, len(streams))
	for i, stream := range streams {
		lokiStreams[i] = stream.ToLokiFormat()
	}

	logData := map[string]any{
		"streams": lokiStreams,
	}

	jsonData, err := json.Marshal(logData)
	if err != nil {
		return fmt.Errorf("failed to marshal log data: %w", err)
	}

	resp, err := http.Post(
		lokiURL+"/loki/api/v1/push",
		"application/json",
		bytes.NewBuffer(jsonData),
	)
	if err != nil {
		return fmt.Errorf("failed to push logs to loki: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			logger.Errorf("failed to close response body: %v", err)
		}
	}()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("failed to push logs, status code: %d", resp.StatusCode)
	}

	time.Sleep(2 * time.Second)
	return nil
}
