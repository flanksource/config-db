package scrapers

import (
	"fmt"
	"time"

	"github.com/flanksource/commons/timer"
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
	"go.opentelemetry.io/otel/attribute"
)

type contextKey string

const (
	contextKeyScrapeStart contextKey = "scrape_start_time"
)

type ScrapeOutput struct {
	Total   int // all configs & changes
	Summary map[string]v1.ConfigTypeScrapeSummary
}

func RunScraper(ctx api.ScrapeContext) (*ScrapeOutput, error) {
	var timer = timer.NewMemoryTimer()
	ctx, err := ctx.InitTempCache()
	if err != nil {
		return nil, err
	}

	ctx = ctx.WithValue(contextKeyScrapeStart, time.Now())
	ctx.Context = ctx.
		WithName(fmt.Sprintf("%s/%s", ctx.ScrapeConfig().Namespace, ctx.ScrapeConfig().Name)).
		WithNamespace(ctx.ScrapeConfig().Namespace)

	results, scraperErr := Run(ctx)
	if scraperErr != nil {
		return nil, fmt.Errorf("failed to run scraper %v: %w", ctx.ScrapeConfig().Name, scraperErr)
	}

	savedResult, err := db.SaveResults(ctx, results)
	if err != nil {
		return nil, fmt.Errorf("failed to save results: %w", err)
	}

	if err := UpdateStaleConfigItems(ctx, results); err != nil {
		return nil, fmt.Errorf("failed to update stale config items: %w", err)
	}

	ctx.Logger.Debugf("Completed scrape with %s in %s", savedResult, timer.End())

	return &ScrapeOutput{
		Total:   len(results),
		Summary: savedResult,
	}, nil
}

func UpdateStaleConfigItems(ctx api.ScrapeContext, results v1.ScrapeResults) error {
	basectx, span := ctx.StartSpan("UpdateStaleConfigItems")
	defer span.End()

	ctx.Context = basectx

	persistedID := ctx.ScrapeConfig().GetPersistedID()
	if persistedID != nil {
		ctx.GetSpan().SetAttributes(
			attribute.Int("scrape.results", len(results)),
			attribute.Bool("scrape.hasError", v1.ScrapeResults(results).HasErr()),
		)

		// If error in any of the scrape results, don't delete old items
		if len(results) > 0 && !v1.ScrapeResults(results).HasErr() {
			if err := DeleteStaleConfigItems(ctx, *persistedID); err != nil {
				return fmt.Errorf("error deleting stale config items: %w", err)
			}
		}
	}

	return nil
}
