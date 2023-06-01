package cmd

import (
	"fmt"
	"net/url"

	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
	"github.com/flanksource/config-db/db/models"
	"github.com/flanksource/config-db/jobs"
	"github.com/flanksource/config-db/query"

	"github.com/flanksource/config-db/scrapers"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/spf13/cobra"
)

// Serve ...
var Serve = &cobra.Command{
	Use: "serve",
	Run: func(cmd *cobra.Command, args []string) {
		serve(args)
	},
}

func serve(configFiles []string) {
	db.MustInit()
	e := echo.New()
	// PostgREST needs to know how it is exposed to create the correct links
	db.HTTPEndpoint = publicEndpoint + "/db"

	if logger.IsTraceEnabled() {
		e.Use(middleware.Logger())
	}
	if !disablePostgrest {
		go db.StartPostgrest()
		forward(e, "/db", db.PostgRESTEndpoint())
		forward(e, "/live", db.PostgRESTAdminEndpoint())
		forward(e, "/ready", db.PostgRESTAdminEndpoint())
	} else {
		e.GET("/live", func(c echo.Context) error {
			return c.String(200, "OK")
		})

		e.GET("/ready", func(c echo.Context) error {
			return c.String(200, "OK")
		})
	}

	e.GET("/query", query.Handler)
	e.POST("/run/:id", scrapers.RunNowHandler)

	// Run this in a goroutine to make it non-blocking for server start
	go startScraperCron(configFiles)

	go jobs.ScheduleJobs()

	if err := e.Start(fmt.Sprintf(":%d", httpPort)); err != nil {
		e.Logger.Fatal(err)
	}
}

func startScraperCron(configFiles []string) {
	scraperConfigsFiles, err := v1.ParseConfigs(configFiles...)
	if err != nil {
		logger.Fatalf("error parsing config files: %v", err)
	}

	logger.Infof("Persisting %d config files", len(scraperConfigsFiles))
	for _, scraper := range scraperConfigsFiles {
		_, err := db.PersistScrapeConfigFromFile(scraper)
		if err != nil {
			logger.Fatalf("Error persisting scrape config to db: %v", err)
		}
	}

	scraperConfigsDB, err := db.GetScrapeConfigs()
	if err != nil {
		logger.Fatalf("error getting configs from database: %v", err)
	}

	logger.Infof("Starting %d scrapers", len(scraperConfigsDB))
	for _, scraper := range scraperConfigsDB {
		_scraper, err := models.V1ConfigScraper(scraper)
		if err != nil {
			logger.Fatalf("Error parsing config scraper: %v", err)
		}
		scrapers.AddToCron(_scraper, scraper.ID.String())

		fn := func() {
			if _, err := scrapers.RunScraper(_scraper); err != nil {
				logger.Errorf("Error running scraper(id=%s): %v", _scraper.ID, err)
			}
		}
		go scrapers.AtomicRunner(_scraper.ID, fn)()
	}
}

func forward(e *echo.Echo, prefix string, target string) {
	targetURL, err := url.Parse(target)
	if err != nil {
		e.Logger.Fatal(err)
	}
	e.Group(prefix).Use(middleware.ProxyWithConfig(middleware.ProxyConfig{
		Rewrite: map[string]string{
			fmt.Sprintf("^%s/*", prefix): "/$1",
		},
		Balancer: middleware.NewRoundRobinBalancer([]*middleware.ProxyTarget{
			{
				URL: targetURL,
			},
		}),
	}))
}

func init() {
	ServerFlags(Serve.Flags())
}
