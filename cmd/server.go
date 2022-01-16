package cmd

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/confighub/api/v1"
	"github.com/flanksource/confighub/db"
	"github.com/flanksource/confighub/kube"

	"github.com/flanksource/confighub/scrapers"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/robfig/cron/v3"
	"github.com/spf13/cobra"
)

var Serve = &cobra.Command{
	Use: "serve",
	Run: func(cmd *cobra.Command, args []string) {
		if err := db.Init(db.ConnectionString); err != nil {
			logger.Errorf("Failed to initialize the db: %v", err)
		}
		e := echo.New()
		e.GET("/", func(c echo.Context) error {
			return c.String(http.StatusOK, "Hello, World!")
		})
		// PostgREST needs to know how it is exposed to create the correct links
		db.HttpEndpoint = publicEndpoint + "/db"
		go db.StartPostgrest()

		url, err := url.Parse("http://localhost:3000")
		if err != nil {
			e.Logger.Fatal(err)
		}

		e.Use(middleware.Logger())

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

	kommonsClient, err := kube.NewKommonsClient()
	if err != nil {
		logger.Errorf("failed to get kubernetes client: %v", err)
	}
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
			if results, err := scrapers.Run(ctx, _scraper); err != nil {
				logger.Errorf("Failed to run scraper %s: %v", _scraper, err)
			} else if err = db.Update(ctx, results); err != nil {
				//FIXME cache results to save to db later
				logger.Errorf("Failed to update db: %v", err)
			}
		}
		cron.AddFunc(schedule, fn)
		fn()
	}

	cron.Start()

}

func init() {
	ServerFlags(Serve.Flags())
}
