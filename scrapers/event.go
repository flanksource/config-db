package scrapers

import (
	"fmt"
	"time"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty/context"
	"github.com/flanksource/duty/models"
	"github.com/flanksource/duty/postq"
	"github.com/flanksource/duty/postq/pg"
	"github.com/flanksource/duty/shutdown"
	"github.com/samber/lo"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const event = "config-db.incremental-scrape"

const (
	// eventQueueUpdateChannel is the channel on which new events on the `event_queue` table
	// are notified.
	eventQueueUpdateChannel = "event_queue_updates"
)

func StartEventListener(ctx context.Context) {
	var notifyChan chan string
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
			Timeout:      time.Second * 10,
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

		name      = event.Properties["name"]
		namespace = event.Properties["namespace"]
		group     = event.Properties["group"]
		version   = event.Properties["version"]
		kind      = event.Properties["kind"]
	)

	var scraper models.ConfigScraper
	if err := ctx.DB().Where("id = ?", scraperID).First(&scraper).Error; err != nil {
		return err
	}

	scrapeConfig, err := v1.ScrapeConfigFromModel(scraper)
	if err != nil {
		return err
	}

	scrapeCtx := api.NewScrapeContext(ctx).WithScrapeConfig(&scrapeConfig)

	for _, sc := range scrapeConfig.Spec.Kubernetes {
		// TODO: Which of the kubernetes spec from this scraper?
		// For now, assume there's only one.

		gvk := schema.GroupVersionKind{Group: group, Version: version, Kind: kind}
		results, err := RunK8sObjectScraper(scrapeCtx, sc, namespace, name, gvk)
		if err != nil {
			return err
		}

		// TODO: Save the result
		fmt.Println(len(results))
	}

	return nil
}
