package e2e

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"time"

	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
	"github.com/flanksource/config-db/scrapers"
	"github.com/flanksource/duty/models"
	"github.com/labstack/echo/v4"
	ginkgo "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("GitHub Scraper - Insights", ginkgo.Ordered, func() {
	var (
		server        *httptest.Server
		configScraper models.ConfigScraper
	)

	ginkgo.BeforeAll(func() {
		if os.Getenv("GITHUB_TOKEN") == "" {
			ginkgo.Skip("GITHUB_TOKEN not set, skipping GitHub insights e2e test")
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

		fixturePath := filepath.Join("..", "..", "fixtures", "github.yaml")
		configs, err := v1.ParseConfigs(fixturePath)
		Expect(err).NotTo(HaveOccurred())
		Expect(configs).To(HaveLen(1))

		configScraper, err = db.PersistScrapeConfigFromFile(DefaultContext, configs[0])
		Expect(err).NotTo(HaveOccurred())

		scrapers.InitSemaphoreWeights(DefaultContext)
	})

	ginkgo.AfterAll(func() {
		if server != nil {
			server.Close()
		}
	})

	ginkgo.It("should run github scraper and persist insights", func() {
		runURL := fmt.Sprintf("%s/run/%s", server.URL, configScraper.ID)
		resp, err := http.Post(runURL, "application/json", nil)
		Expect(err).NotTo(HaveOccurred())
		defer func() {
			if err := resp.Body.Close(); err != nil {
				logger.Errorf("failed to close response body: %v", err)
			}
		}()

		Expect(resp.StatusCode).To(Equal(http.StatusOK))

		Eventually(func(g Gomega) {
			var latest models.JobHistory
			err := DefaultContext.DB().
				Where("resource_id = ?", configScraper.ID.String()).
				Order("created_at DESC").
				First(&latest).Error
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(latest.Status).To(Equal(models.StatusSuccess))
		}, 2*time.Minute, 2*time.Second).Should(Succeed())

		var analyses []models.ConfigAnalysis
		err = DefaultContext.DB().
			Where("scraper_id = ?", configScraper.ID.String()).
			Order("last_observed DESC NULLS LAST").
			Find(&analyses).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(analyses).NotTo(BeEmpty())

		var latestExpectedSource *models.ConfigAnalysis
		for i := range analyses {
			analysis := analyses[i]
			if analysis.Source != "OpenSSF Scorecard" && analysis.Source != "GitHub Dependabot" {
				continue
			}
			latestExpectedSource = &analysis
			break
		}

		Expect(latestExpectedSource).NotTo(BeNil(), "expected at least one analysis from OpenSSF Scorecard or GitHub Dependabot")
		Expect(latestExpectedSource.AnalysisType).To(Equal(models.AnalysisTypeSecurity))
		Expect(latestExpectedSource.Severity).To(Or(
			Equal(models.SeverityCritical),
			Equal(models.SeverityHigh),
			Equal(models.SeverityMedium),
			Equal(models.SeverityLow),
			Equal(models.SeverityInfo),
		))
		Expect(latestExpectedSource.Status).NotTo(BeEmpty())
		Expect(latestExpectedSource.FirstObserved).NotTo(BeNil())
		Expect(latestExpectedSource.LastObserved).NotTo(BeNil())
	})
})
