package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"

	dutyContext "github.com/flanksource/duty/context"
	"github.com/flanksource/duty/models"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
	dbmodels "github.com/flanksource/config-db/db/models"
	"github.com/flanksource/config-db/scrapers"
)

type TestSetup struct {
	Server        *httptest.Server
	ScrapeConfig  *v1.ScrapeConfig
	ConfigScraper models.ConfigScraper
	ConfigItem    dbmodels.ConfigItem
}

func createTestServer(ctx dutyContext.Context) *httptest.Server {
	e := echo.New()
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.SetRequest(c.Request().WithContext(ctx.Wrap(c.Request().Context())))
			return next(c)
		}
	})
	e.POST("/run/:id", scrapers.RunNowHandler)
	return httptest.NewServer(e)
}

func waitForService(url, healthPath string, timeout time.Duration) {
	Eventually(func() error {
		resp, err := http.Get(url + healthPath)
		if err != nil {
			return err
		}
		defer resp.Body.Close()

		if resp.StatusCode != 200 {
			return fmt.Errorf("service not ready, status: %d", resp.StatusCode)
		}
		return nil
	}, timeout, time.Second).Should(Succeed())
}

func loadScrapeConfig(fixturePath string) (*v1.ScrapeConfig, error) {
	configs, err := v1.ParseConfigs(fixturePath)
	if err != nil {
		return nil, err
	}
	Expect(configs).To(HaveLen(1))
	return &configs[0], nil
}

func createDummyScraperAndConfig(ctx dutyContext.Context, scraperName, configID, configType string) (models.ConfigScraper, dbmodels.ConfigItem, error) {
	scrapeSpec := v1.ScrapeConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      scraperName + "-scraper",
			Namespace: "default",
			UID:       types.UID(uuid.New().String()),
		},
		Spec: v1.ScraperSpec{
			SQL: []v1.SQL{
				{
					Connection: v1.Connection{
						Connection: "dummy://connection-string",
					},
					Query: "SELECT dummy FROM table",
				},
			},
		},
	}

	configScraper, err := db.PersistScrapeConfigFromFile(ctx, scrapeSpec)
	if err != nil {
		return models.ConfigScraper{}, dbmodels.ConfigItem{}, err
	}

	configItemID := uuid.MustParse(configID)
	configItem := dbmodels.ConfigItem{
		ID:         configItemID.String(),
		ExternalID: []string{configItemID.String(), scraperName},
		Type:       configType,
		Name:       lo.ToPtr(scraperName),
		Config:     lo.ToPtr(fmt.Sprintf(`{"name":"%s","port":5432,"host":"localhost"}`, scraperName)),
		ScraperID:  &configScraper.ID,
	}

	scraperCtx := api.NewScrapeContext(ctx)
	err = db.CreateConfigItem(scraperCtx, &configItem)
	if err != nil {
		return models.ConfigScraper{}, dbmodels.ConfigItem{}, err
	}

	return configScraper, configItem, nil
}

func triggerScraper(serverURL string, scraperID uuid.UUID) {
	runURL := fmt.Sprintf("%s/run/%s", serverURL, scraperID)
	resp, err := http.Post(runURL, "application/json", nil)
	Expect(err).NotTo(HaveOccurred())
	defer resp.Body.Close()
	Expect(resp.StatusCode).To(Equal(200))
}

func verifyJobHistory(ctx dutyContext.Context, scraperID uuid.UUID, timeout time.Duration) {
	Eventually(func(g Gomega) {
		var jobHistories []models.JobHistory
		err := ctx.DB().Where("resource_id = ?", scraperID.String()).
			Order("created_at DESC").
			Find(&jobHistories).Error
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(jobHistories).To(HaveLen(1))
		g.Expect(jobHistories[0].Status).To(Equal(models.StatusSuccess))
	}, timeout, 1*time.Second).Should(Succeed())
}

func verifyConfigChanges(ctx dutyContext.Context, configItemID string, expectedCount int) {
	var configChanges []dbmodels.ConfigChange
	err := ctx.DB().Where("config_id = ?", configItemID).Find(&configChanges).Error
	Expect(err).NotTo(HaveOccurred())
	Expect(configChanges).To(HaveLen(expectedCount))

	for _, change := range configChanges {
		Expect(change.ChangeType).To(Equal("ConfigReload"))
		Expect(change.Summary).To(ContainSubstring("changed from"))
	}
}

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

func getTestLogMessages() []string {
	return []string{
		"Configuration reloaded: database.max_connections changed from 100 to 200",
		"Configuration reloaded: server.timeout changed from 30s to 60s",
		"Configuration reloaded: cache.size changed from 1GB to 2GB",
	}
}

func injectTestLogsToLoki(lokiURL string) error {
	now := time.Now().UnixNano()
	messages := getTestLogMessages()

	entries := make([]LogEntry, len(messages))
	for i, msg := range messages {
		entries[i] = LogEntry{
			Timestamp: now + int64(i*1000000),
			Message:   msg,
		}
	}

	stream := LogStream{
		Labels: map[string]string{
			"job":   "app",
			"level": "info",
			"host":  "test-host-1",
		},
		Entries: entries,
	}

	logData := map[string]any{
		"streams": []map[string]any{stream.ToLokiFormat()},
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
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("failed to push logs, status code: %d", resp.StatusCode)
	}

	time.Sleep(2 * time.Second)
	return nil
}

func injectTestLogsToOpenSearch(opensearchURL string) error {
	now := time.Now()
	messages := getTestLogMessages()

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
		return err
	}
	defer resp.Body.Close()

	for i, msg := range messages {
		doc := map[string]any{
			"@timestamp": now.Add(time.Duration(i) * time.Second).Format(time.RFC3339),
			"message":    msg,
			"level":      "info",
			"job":        "app",
			"host":       "test-host-1",
		}

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
			return err
		}
		defer resp.Body.Close()

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("failed to index document, status code: %d", resp.StatusCode)
		}
	}

	refreshURL := fmt.Sprintf("%s/%s/_refresh", opensearchURL, indexName)
	resp, err = http.Post(refreshURL, "application/json", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}
