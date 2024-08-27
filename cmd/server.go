package cmd

import (
	"context"
	"fmt"

	"net/http"
	"net/url"
	"slices"
	"time"

	commonsCtx "github.com/flanksource/commons/context"
	"github.com/flanksource/commons/logger"
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
	"github.com/flanksource/config-db/jobs"
	"github.com/flanksource/duty"
	dutyAPI "github.com/flanksource/duty/api"
	dutyContext "github.com/flanksource/duty/context"
	dutyEcho "github.com/flanksource/duty/echo"
	"github.com/flanksource/duty/postgrest"
	"github.com/flanksource/duty/shutdown"

	"github.com/labstack/echo-contrib/echoprometheus"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	prom "github.com/prometheus/client_golang/prometheus"
	"github.com/spf13/cobra"
	"go.opentelemetry.io/contrib/instrumentation/github.com/labstack/echo/otelecho"
	"go.opentelemetry.io/otel"

	"github.com/flanksource/config-db/scrapers"
)

// Serve ...
var Serve = &cobra.Command{
	Use: "serve",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, closer, err := duty.Start("config-db", duty.SkipMigrationByDefaultMode)
		if err != nil {
			logger.Fatalf("Failed to initialize db: %v", err.Error())
		}
		AddShutdownHook(closer)

		dutyCtx := dutyContext.NewContext(ctx, commonsCtx.WithTracer(otel.GetTracerProvider().Tracer(otelServiceName)))
		api.DefaultContext = api.NewScrapeContext(dutyCtx)

		dedupWindow := api.DefaultContext.Properties().Duration("changes.dedup.window", time.Hour)
		if err := db.InitChangeFingerprintCache(api.DefaultContext, dedupWindow); err != nil {
			return fmt.Errorf("failed to initialize change fingerprint cache: %w", err)
		}

		serve(args)
		return nil
	},
}

func serve(configFiles []string) {
	e := echo.New()

	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			ctx := api.DefaultContext.Wrap(c.Request().Context())
			c.SetRequest(c.Request().WithContext(ctx))
			return next(c)
		}
	})

	dutyEcho.AddDebugHandlers(api.DefaultContext.DutyContext(), e, func(next echo.HandlerFunc) echo.HandlerFunc { return next })
	e.Use(otelecho.Middleware("config-db", otelecho.WithSkipper(telemetryURLSkipper)))

	if logger.IsTraceEnabled() {
		echoLogConfig := middleware.DefaultLoggerConfig
		echoLogConfig.Skipper = telemetryURLSkipper
		e.Use(middleware.LoggerWithConfig(echoLogConfig))
	}

	if !dutyAPI.DefaultConfig.Postgrest.Disable {
		forward(e, "/db", postgrest.PostgRESTEndpoint(dutyAPI.DefaultConfig))
		forward(e, "/live", postgrest.PostgRESTAdminEndpoint(dutyAPI.DefaultConfig))
		forward(e, "/ready", postgrest.PostgRESTAdminEndpoint(dutyAPI.DefaultConfig))
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
		Registerer:                prom.DefaultRegisterer,
		Skipper:                   telemetryURLSkipper,
		DoNotUseRequestPathFor404: true,
	}))

	e.GET("/metrics", echoprometheus.NewHandlerWithConfig(echoprometheus.HandlerConfig{
		Gatherer: prom.DefaultGatherer,
	}))

	go startScraperCron(configFiles)

	go jobs.ScheduleJobs(api.DefaultContext.DutyContext())
	shutdown.AddHook(jobs.Stop)

	shutdown.AddHook(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
		defer cancel()

		if err := e.Shutdown(ctx); err != nil {
			e.Logger.Fatal(err)
		}
	})

	shutdown.WaitForSignal()

	if err := e.Start(fmt.Sprintf(":%d", httpPort)); err != nil && err != http.ErrServerClosed {
		e.Logger.Fatal(err)
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

// telemetryURLSkipper ignores metrics route on some middleware
func telemetryURLSkipper(c echo.Context) bool {
	pathsToSkip := []string{"/live", "/ready", "/metrics"}
	return slices.Contains(pathsToSkip, c.Path())
}
