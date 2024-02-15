package cmd

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/flanksource/commons/logger"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/sethvargo/go-retry"
	"github.com/spf13/cobra"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
	"github.com/flanksource/config-db/jobs"
	"github.com/flanksource/config-db/query"
	dutyContext "github.com/flanksource/duty/context"
	"github.com/google/uuid"

	"github.com/flanksource/config-db/scrapers"
	"github.com/flanksource/config-db/scrapers/kubernetes"
)

// Serve ...
var Serve = &cobra.Command{
	Use: "serve",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()

		db.MustInit(ctx)

		api.DefaultContext = api.NewScrapeContext(ctx, db.DefaultDB(), db.Pool)
		if err := dutyContext.LoadPropertiesFromFile(api.DefaultContext.DutyContext(), propertiesFile); err != nil {
			return fmt.Errorf("failed to load properties: %v", err)
		}

		serve(ctx, args)
		return nil
	},
}

func serve(ctx context.Context, configFiles []string) {

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

	go startScraperCron(configFiles)

	go jobs.ScheduleJobs(api.DefaultContext.DutyContext())

	go func() {
		if err := e.Start(fmt.Sprintf(":%d", httpPort)); err != nil {
			e.Logger.Fatal(err)
		}
	}()

	<-ctx.Done()
	if err := db.StopEmbeddedPGServer(); err != nil {
		logger.Errorf("failed to stop server: %v", err)
	}

	if err := e.Shutdown(ctx); err != nil {
		logger.Errorf("failed to shutdown echo HTTP server: %v", err)
	}
}

func startScraperCron(configFiles []string) {
	scraperConfigsFiles, err := v1.ParseConfigs(configFiles...)
	if err != nil {
		logger.Fatalf("error parsing config files: %v", err)
	}

	logger.Infof("Persisting %d config files", len(scraperConfigsFiles))
	for _, scrapeConfig := range scraperConfigsFiles {
		_, err := db.PersistScrapeConfigFromFile(scrapeConfig)
		if err != nil {
			logger.Fatalf("Error persisting scrape config to db: %v", err)
		}
	}

	scraperConfigsDB, err := db.GetScrapeConfigsOfAgent(uuid.Nil)
	if err != nil {
		logger.Fatalf("error getting configs from database: %v", err)
	}

	logger.Infof("Starting %d scrapers", len(scraperConfigsDB))
	for _, scraper := range scraperConfigsDB {
		_scraper, err := v1.ScrapeConfigFromModel(scraper)
		if err != nil {
			logger.Errorf("Error parsing config scraper[%s]: %v", scraper.ID, err)
			continue
		}
		scrapers.AddToCron(_scraper)

		fn := func() {
			ctx := api.DefaultContext.WithScrapeConfig(&_scraper)
			if _, err := scrapers.RunScraper(ctx); err != nil {
				logger.Errorf("Error running scraper(id=%s): %v", scraper.ID, err)
			}
		}
		go scrapers.AtomicRunner(scraper.ID.String(), fn)()

		for _, config := range _scraper.Spec.Kubernetes {
			ctx := api.DefaultContext.WithScrapeConfig(&_scraper)
			go watchKubernetesEventsWithRetry(ctx, config)

			if err := jobs.ScheduleJob(ctx.DutyContext(), jobs.ConsumeKubernetesWatchEventsJobFunc(_scraper, config)); err != nil {
				logger.Fatalf("failed to schedule kubernetes watch event consumer job: %v", err)
			}
		}
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

func watchKubernetesEventsWithRetry(ctx api.ScrapeContext, config v1.Kubernetes) {
	const (
		timeout                 = time.Minute // how long to keep retrying before we reset and retry again
		exponentialBaseDuration = time.Second
	)

	for {
		backoff := retry.WithMaxDuration(timeout, retry.NewExponential(exponentialBaseDuration))
		err := retry.Do(ctx, backoff, func(ctxt context.Context) error {
			ctx := ctxt.(api.ScrapeContext)
			if err := kubernetes.WatchEvents(ctx, config); err != nil {
				return retry.RetryableError(err)
			}

			return nil
		})

		logger.Errorf("Failed to watch kubernetes events. name=%s namespace=%s cluster=%s: %v", config.Name, config.Namespace, config.ClusterName, err)
	}
}
