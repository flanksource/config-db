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
	"github.com/flanksource/duty"
	dutyAPI "github.com/flanksource/duty/api"
	dutyContext "github.com/flanksource/duty/context"
	dutyEcho "github.com/flanksource/duty/echo"
	"github.com/flanksource/duty/postgrest"
	"github.com/flanksource/duty/postq/pg"
	"github.com/flanksource/duty/shutdown"
	"github.com/labstack/echo-contrib/echoprometheus"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	prom "github.com/prometheus/client_golang/prometheus"
	"github.com/spf13/cobra"
	"go.opentelemetry.io/contrib/instrumentation/github.com/labstack/echo/otelecho"
	"go.opentelemetry.io/otel"

	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
	"github.com/flanksource/config-db/jobs"
	"github.com/flanksource/config-db/scrapers"
	"github.com/flanksource/config-db/utils"
)

const (
	externalUserCacheRefreshInterval = 6 * time.Hour
	tableListenerReadyTimeout        = 10 * time.Second
)

// Serve ...
var Serve = &cobra.Command{
	Use: "serve",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, closer, err := duty.Start(app, duty.SkipMigrationByDefaultMode)
		if err != nil {
			return fmt.Errorf("failed to initialize db: %w", err)
		}
		shutdown.AddHook(closer)

		dutyCtx := dutyContext.NewContext(ctx, commonsCtx.WithTracer(otel.GetTracerProvider().Tracer(otelServiceName)))

		dedupWindow := ctx.Properties().Duration("changes.dedup.window", time.Hour)
		if err := db.InitChangeFingerprintCache(ctx, dedupWindow); err != nil {
			return fmt.Errorf("failed to initialize change fingerprint cache: %w", err)
		}
		if err := startTableUpdatesHandler(dutyCtx); err != nil {
			return fmt.Errorf("failed to initialize table notification listener: %w", err)
		}
		if err := db.WarmExternalEntityCaches(dutyCtx); err != nil {
			return fmt.Errorf("failed to initialize external entity caches: %w", err)
		}

		registerJobs(dutyCtx, args)
		scrapers.StartEventListener(ctx)
		serve(dutyCtx)

		return nil
	},
}

// startTableUpdatesHandler waits until PostgreSQL confirms the LISTEN is active.
// Warming caches only after this handshake closes the startup gap between the
// initial database snapshot and notification registration.
func startTableUpdatesHandler(ctx dutyContext.Context) error {
	ready := make(chan struct{})
	go tableUpdatesHandler(ctx, ready)

	probeTicker := time.NewTicker(100 * time.Millisecond)
	defer probeTicker.Stop()
	timeout := time.NewTimer(tableListenerReadyTimeout)
	defer timeout.Stop()

	var lastErr error
	for {
		lastErr = ctx.DB().Exec(
			"SELECT pg_notify('table_activity', 'external_user_cache_listener_ready')",
		).Error
		select {
		case <-ready:
			return nil
		case <-probeTicker.C:
			continue
		case <-timeout.C:
			return fmt.Errorf("timed out waiting for table_activity listener (last probe error: %v)", lastErr)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// tableUpdatesHandler handles all "table_activity" pg notifications.
func tableUpdatesHandler(ctx dutyContext.Context, ready chan<- struct{}) {
	notifyRouter := pg.NewNotifyRouter()
	pluginUpdates := notifyRouter.GetOrCreateChannel("scrape_plugins")
	// Source aliases and derived-index rows can change together during a merge.
	// Route both notifications through one signal-mode channel so bursts collapse
	// into a single source-of-truth cache refresh. A requested size of zero gives
	// the Duty router one pending signal slot.
	aliasUpdates := notifyRouter.GetOrCreateBufferedChannel(0, "external_users", "external_user_aliases")
	listenerReady := notifyRouter.GetOrCreateBufferedChannel(0, "external_user_cache_listener_ready")
	go notifyRouter.Run(ctx, "table_activity")

	select {
	case <-listenerReady:
		close(ready)
	case <-ctx.Done():
		return
	}

	refreshTicker := time.NewTicker(externalUserCacheRefreshInterval)
	defer refreshTicker.Stop()

	refreshExternalUsers := func(reason string) {
		ctx.Logger.V(3).Infof("reloading external user caches: %s", reason)
		if err := db.RefreshExternalUserCaches(ctx); err != nil {
			// Refresh builds a new generation before swapping, so the previous
			// cache remains usable when a notification or periodic reload fails.
			logger.Errorf("failed to reload external user caches (%s): %v", reason, err)
		}
	}

	for {
		select {
		case <-pluginUpdates:
			ctx.Logger.V(3).Infof("reloading plugins")
			if _, err := db.ReloadAllScrapePlugins(ctx); err != nil {
				logger.Errorf("failed to reload plugins: %v", err)
			}
		case <-aliasUpdates:
			refreshExternalUsers("table notification")
		case <-refreshTicker.C:
			refreshExternalUsers("periodic refresh")
		case <-ctx.Done():
			return
		}
	}
}

func registerJobs(ctx dutyContext.Context, configFiles []string) {
	go startScraperCron(ctx, configFiles)
	shutdown.AddHook(scrapers.Stop)

	go jobs.ScheduleJobs(ctx)
	shutdown.AddHook(jobs.Stop)
}

// serve runs an echo http server
func serve(ctx dutyContext.Context) {
	e := echo.New()

	dutyEcho.AddDebugHandlers(ctx, e, func(next echo.HandlerFunc) echo.HandlerFunc { return next })
	e.GET("/", utils.MemsizeEchoHandler)
	e.POST("/scan", utils.MemsizeEchoHandler)
	e.GET("/report*", utils.MemsizeEchoHandler)
	e.GET("/debug/memsize", utils.MemsizeEchoHandler)
	e.POST("/debug/memsize/scan*", utils.MemsizeEchoHandler)
	e.GET("/debug/memsize/report*", utils.MemsizeEchoHandler)

	e.Use(otelecho.Middleware(app, otelecho.WithSkipper(telemetryURLSkipper)))

	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.SetRequest(c.Request().WithContext(ctx.Wrap(c.Request().Context())))
			return next(c)
		}
	})

	if logger.IsTraceEnabled() {
		echoLogConfig := middleware.DefaultLoggerConfig
		echoLogConfig.Skipper = telemetryURLSkipper

		//nolint:staticcheck // SA1019 ignore.
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

	shutdown.AddHook(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
		defer cancel()

		if err := e.Shutdown(ctx); err != nil {
			e.Logger.Fatal(err)
		}
	})

	if err := e.Start(fmt.Sprintf(":%d", httpPort)); err != nil && err != http.ErrServerClosed {
		e.Logger.Fatal(err)
	}
}

func startScraperCron(ctx dutyContext.Context, configFiles []string) {
	scraperConfigsFiles, err := v1.ParseConfigs(configFiles...)
	if err != nil {
		logger.Fatalf("error parsing config files: %v", err)
	}

	logger.Infof("Persisting %d config files", len(scraperConfigsFiles))
	for _, scrapeConfig := range scraperConfigsFiles {
		_, err := db.PersistScrapeConfigFromFile(ctx, scrapeConfig)
		if err != nil {
			logger.Fatalf("Error persisting scrape config to db: %v", err)
		}
	}

	scrapers.SyncScrapeConfigs(ctx)
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
