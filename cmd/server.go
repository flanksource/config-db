package cmd

import (
	"context"
	"fmt"
	"net/url"

	"github.com/flanksource/commons/logger"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/spf13/cobra"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
	"github.com/flanksource/config-db/jobs"
	"github.com/flanksource/config-db/query"
	"github.com/flanksource/config-db/scrapers"
	"github.com/flanksource/config-db/scrapers/kubernetes"
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

	if agentName != "" {
		agent, err := db.FindAgentByName(context.Background(), agentName)
		if err != nil {
			logger.Fatalf("error searching for agent (name=%s): %v", agentName, err)
		} else if agent == nil {
			logger.Fatalf("agent not found (name=%s)", agentName)
		} else {
			agentID = agent.ID
		}
	}

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
	for _, scrapeConfig := range scraperConfigsFiles {
		_, err := db.PersistScrapeConfigFromFile(scrapeConfig)
		if err != nil {
			logger.Fatalf("Error persisting scrape config to db: %v", err)
		}
	}

	scraperConfigsDB, err := db.GetScrapeConfigsOfAgent(agentID)
	if err != nil {
		logger.Fatalf("error getting configs from database: %v", err)
	}

	logger.Infof("Starting %d scrapers", len(scraperConfigsDB))
	for _, scraper := range scraperConfigsDB {
		_scraper, err := v1.ScrapeConfigFromModel(scraper)
		if err != nil {
			logger.Fatalf("Error parsing config scraper: %v", err)
		}
		scrapers.AddToCron(_scraper)

		fn := func() {
			ctx := api.NewScrapeContext(context.Background(), _scraper)
			if _, err := scrapers.RunScraper(ctx); err != nil {
				logger.Errorf("Error running scraper(id=%s): %v", scraper.ID, err)
			}
		}
		go scrapers.AtomicRunner(scraper.ID.String(), fn)()

		for _, k := range _scraper.Spec.Kubernetes {
			ctx := api.NewScrapeContext(context.Background(), _scraper)
			go exitOnError(kubernetes.WatchEvents(ctx, k, kubernetesChangeEventConsumer), "error watching events")
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

func exitOnError(err error, description string) {
	if err != nil {
		logger.Fatalf("%s %v", description, err)
	}
}

func kubernetesChangeEventConsumer(ctx *v1.ScrapeContext, config v1.Kubernetes, resourcesPerKind map[string]map[string]*kubernetes.InvolvedObject) {
	var resourceIDs []string
	for kind, resources := range resourcesPerKind {
		for _, r := range resources {
			resourceIDs = append(resourceIDs, kubernetes.ItemID{Kind: kind, Name: r.Name, Namespace: r.Namespace}.Encode())
		}
	}

	if _, err := scrapers.RunTargettedScraper(ctx, kubernetes.KubernetesScraper{}, config, resourceIDs); err != nil {
		logger.Errorf("Error running scraper(id=%s): %v", ctx.ScrapeConfig.GetUID(), err)
	}
}
