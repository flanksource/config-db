package scrapers

import (
	"fmt"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
	"github.com/flanksource/duty/context"
	"github.com/flanksource/duty/models"
	"github.com/flanksource/duty/postq"
	"github.com/flanksource/duty/postq/pg"
	"github.com/flanksource/duty/query"
	"github.com/flanksource/duty/shutdown"
	"github.com/samber/lo"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const event = "config-db.incremental-scrape"

const (
	// eventQueueUpdateChannel is the channel on which new events on the `event_queue` table
	// are notified.
	eventQueueUpdateChannel = "event_queue_updates"
)

func StartEventListener(ctx context.Context) {
	notifyChan := make(chan string)
	go pg.Listen(ctx, eventQueueUpdateChannel, notifyChan)

	workers := 2 // TODO: decide on this

	consumer := postq.AsyncEventConsumer{
		WatchEvents: []string{event},
		BatchSize:   1,
		Consumer: func(_ctx context.Context, events models.Events) models.Events {
			if len(events) == 0 {
				return nil
			}

			if err := incrementalScrapeFromEvent(ctx, events[0]); err != nil {
				events[0].Error = lo.ToPtr(err.Error())
				return events
			}

			return nil
		},
		EventFetcherOption: &postq.EventFetcherOption{
			MaxAttempts: 1, // retry only once
		},
		ConsumerOption: &postq.ConsumerOption{
			NumConsumers: workers,
			ErrorHandler: func(ctx context.Context, err error) bool {
				ctx.Errorf("error consuming event(%s): %v", event, err)
				return false // don't retry here. Event queue has its own retry mechanism.
			},
		},
	}

	if ec, err := consumer.EventConsumer(); err != nil {
		ctx.Errorf("failed to create event consumer: %s", err)
		shutdown.ShutdownAndExit(1, fmt.Sprintf("failed to start consumer: %v", err))
	} else {
		go ec.Listen(ctx, notifyChan)
	}
}

func incrementalScrapeFromEvent(ctx context.Context, event models.Event) error {
	var (
		scraperID = event.Properties["scraper_id"]
		configID  = event.Properties["config_id"]
	)

	config, err := query.GetCachedConfig(ctx, configID)
	if err != nil {
		return fmt.Errorf("failed to get config: %w", err)
	}

	configSpec, err := config.ConfigJSONStringMap()
	if err != nil {
		return err
	}

	obj := unstructured.Unstructured{Object: configSpec}

	var scraper models.ConfigScraper
	if err := ctx.DB().Where("id = ?", scraperID).First(&scraper).Error; err != nil {
		return fmt.Errorf("failed to get scraper: %w", err)
	}

	scrapeConfig, err := v1.ScrapeConfigFromModel(scraper)
	if err != nil {
		return err
	}

	scrapeCtx := api.NewScrapeContext(ctx).WithScrapeConfig(&scrapeConfig)

	for _, sc := range scrapeConfig.Spec.Kubernetes {
		// TODO: Which of the kubernetes spec from this scraper?
		// For now, assume there's only one.

		results, err := RunK8sObjectScraper(scrapeCtx, sc, obj.GetNamespace(), obj.GetName(), obj.GroupVersionKind())
		if err != nil {
			return err
		}

		if _, err := db.SaveResults(scrapeCtx, results); err != nil {
			return fmt.Errorf("failed to save %d results: %w", len(results), err)
		}
	}

	return nil
}
