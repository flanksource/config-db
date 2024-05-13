package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/flanksource/commons/logger"
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
	"github.com/flanksource/config-db/jobs"
	"github.com/flanksource/duty"
	dutyContext "github.com/flanksource/duty/context"
	"github.com/labstack/echo-contrib/echoprometheus"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	prom "github.com/prometheus/client_golang/prometheus"
	"github.com/spf13/cobra"
	"go.opentelemetry.io/contrib/instrumentation/github.com/labstack/echo/otelecho"

	"github.com/flanksource/config-db/scrapers"
)

// Serve ...
var Serve = &cobra.Command{
	Use: "serve",
	RunE: func(cmd *cobra.Command, args []string) error {
		api.DefaultContext = api.NewScrapeContext(db.MustInit())

		if ok, err := duty.HasMigrationsRun(cmd.Context(), api.DefaultContext.Pool()); err != nil {
			return fmt.Errorf("failed to check if migrations have run: %w", err)
		} else if !ok {
			return errors.New("Migrations not run, waiting for mission-control pod to start")
		}

		if err := dutyContext.LoadPropertiesFromFile(api.DefaultContext.DutyContext(), propertiesFile); err != nil {
			return fmt.Errorf("failed to load properties: %v", err)
		}

		serve(context.Background(), args)
		return nil
	},
}

func serve(ctx context.Context, configFiles []string) {
	e := echo.New()
	e.Use(otelecho.Middleware("config-db", otelecho.WithSkipper(tracingURLSkipper)))

	// PostgREST needs to know how it is exposed to create the correct links
	db.HTTPEndpoint = publicEndpoint + "/db"

	if logger.IsTraceEnabled() {
		echoLogConfig := middleware.DefaultLoggerConfig
		echoLogConfig.Skipper = tracingURLSkipper
		e.Use(middleware.LoggerWithConfig(echoLogConfig))
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

	e.POST("/run/:id", scrapers.RunNowHandler)

	e.Use(echoprometheus.NewMiddlewareWithConfig(echoprometheus.MiddlewareConfig{
		Registerer: prom.DefaultRegisterer,
	}))

	e.GET("/metrics", echoprometheus.NewHandlerWithConfig(echoprometheus.HandlerConfig{
		Gatherer: prom.DefaultGatherer,
	}))

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
		_, err := db.PersistScrapeConfigFromFile(api.DefaultContext, scrapeConfig)
		if err != nil {
			logger.Fatalf("Error persisting scrape config to db: %v", err)
		}
	}

	scrapers.SyncScrapeConfigs(api.DefaultContext)

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

// tracingURLSkipper ignores metrics route on some middleware
func tracingURLSkipper(c echo.Context) bool {
	pathsToSkip := []string{"/health", "/metrics"}
	for _, p := range pathsToSkip {
		if strings.HasPrefix(c.Path(), p) {
			return true
		}
	}
	return false
}
