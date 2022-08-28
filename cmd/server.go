package cmd

import (
	"context"
	"fmt"
	"net/url"

	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
	fs "github.com/flanksource/config-db/filesystem"
	"github.com/flanksource/config-db/query"

	"github.com/flanksource/config-db/scrapers"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/robfig/cron/v3"
	"github.com/spf13/cobra"
)

// Serve ...
var Serve = &cobra.Command{
	Use: "serve",
	Run: func(cmd *cobra.Command, args []string) {
		db.MustInit()
		e := echo.New()
		// PostgREST needs to know how it is exposed to create the correct links
		db.HTTPEndpoint = publicEndpoint + "/db"
		go db.StartPostgrest()

		url, err := url.Parse("http://localhost:3000")
		if err != nil {
			e.Logger.Fatal(err)
		}

		if logger.IsTraceEnabled() {
			e.Use(middleware.Logger())
		}

		e.GET("/query", query.Handler)

		e.Group("/db").Use(middleware.ProxyWithConfig(middleware.ProxyConfig{
			Rewrite: map[string]string{
				"^/db/*": "/$1",
			},
			Balancer: middleware.NewRoundRobinBalancer([]*middleware.ProxyTarget{
				{
					URL: url,
				},
			}),
		}))
		serve(args)
		if err := e.Start(fmt.Sprintf(":%d", httpPort)); err != nil {
			e.Logger.Fatal(err)
		}
	},
}

func serve(configFiles []string) {

	scraperConfigs, err := getConfigs(configFiles)
	if err != nil {
		logger.Fatalf(err.Error())
	}

	cron := cron.New()

	for _, scraper := range scraperConfigs {
		schedule := scraper.Schedule
		if schedule == "" {
			schedule = defaultSchedule
		}
		_scraper := scraper
		fn := func() {
			ctx := v1.ScrapeContext{Context: context.Background(), Kommons: kommonsClient, Scraper: &_scraper}
			manager := v1.Manager{
				Finder: fs.NewFileFinder(),
			}
			if results, err := scrapers.Run(ctx, manager, _scraper); err != nil {
				logger.Errorf("Failed to run scraper %s: %v", _scraper, err)
			} else if err = db.Update(ctx, results); err != nil {
				//FIXME cache results to save to db later
				logger.Errorf("Failed to update db: %v", err)
			}
		}
		if _, err := cron.AddFunc(schedule, fn); err != nil {
			logger.Errorf("failed to schedule %s using %s: %v", scraper, scraper.Schedule, err)
		}
		fn()
	}

	cron.Start()

}

func init() {
	ServerFlags(Serve.Flags())
}
