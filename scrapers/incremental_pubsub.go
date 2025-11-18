package scrapers

import (
	"fmt"
	"sync"
	"time"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
	pubsubscraper "github.com/flanksource/config-db/scrapers/pubsub"
	"github.com/flanksource/duty/job"
	dutypubsub "github.com/flanksource/duty/pubsub"
	"github.com/samber/lo"
)

func consumePubSubJobKey(id string) string {
	return id + "-consume-pubsub"
}

// ConsumeKubernetesWatchJobFunc returns a job that consumes kubernetes objects received from shared informers
// for the given config of the scrapeconfig.
func ConsumePubSubJobFunc(sc api.ScrapeContext, config v1.PubSub) *job.Job {
	return &job.Job{
		Name:         "ConsumePubSubJobFunc",
		Context:      sc.DutyContext().WithObject(sc.ScrapeConfig().ObjectMeta),
		JobHistory:   true,
		Singleton:    true,
		Retention:    job.RetentionFailed,
		Schedule:     "@every 1m",
		ResourceID:   string(sc.ScrapeConfig().GetUID()),
		ID:           fmt.Sprintf("%s/%s", sc.ScrapeConfig().Namespace, sc.ScrapeConfig().Name),
		ResourceType: job.ResourceTypeScraper,
		Fn: func(jobCtx job.JobRuntime) error {
			plugins, err := db.LoadAllPlugins(jobCtx.Context)
			if err != nil {
				return fmt.Errorf("failed to load plugins: %w", err)
			}

			config := config.DeepCopy()
			config.BaseScraper = config.BaseScraper.ApplyPlugins(plugins...)

			sc := sc.WithScrapeConfig(sc.ScrapeConfig(), plugins...).AsIncrementalScrape()

			queueConfig := config.QueueConfig

			subscription, err := dutypubsub.Subscribe(jobCtx.Context, queueConfig)
			if err != nil {
				return fmt.Errorf("error opening subscription for %s: %w", queueConfig.GetQueue(), err)
			}
			defer subscription.Shutdown(jobCtx.Context) //nolint:errcheck

			messageCh := make(chan pubsubscraper.PubSubMessage, 1000)
			var wg sync.WaitGroup
			wg.Add(1)
			var results v1.ScrapeResults
			go func() {
				defer wg.Done()
				for msg := range messageCh {
					results = append(results, v1.ScrapeResult{
						BaseScraper: config.BaseScraper,
						Config:      msg,
					})
				}
			}()

			maxMessages := lo.CoalesceOrEmpty(config.MaxMessages, 2000)
			if err := pubsubscraper.ListenToSubscription(jobCtx.Context, subscription, messageCh, 10*time.Second, maxMessages); err != nil {
				// Only log error but continue with consume so that acked messages are not lost
				jobCtx.Errorf("error while receiving from pubsub[%s]: %v", queueConfig.GetQueue(), err)
			}

			wg.Wait()
			return consumePubSubResults(jobCtx, *sc.ScrapeConfig(), results)
		},
	}
}

func consumePubSubResults(ctx job.JobRuntime, scrapeConfig v1.ScrapeConfig, results v1.ScrapeResults) error {
	cc := api.NewScrapeContext(ctx.Context).WithScrapeConfig(&scrapeConfig).WithJobHistory(ctx.History).AsIncrementalScrape()
	cc.Context = cc.Context.WithoutName().WithName(fmt.Sprintf("watch[%s/%s]", cc.GetNamespace(), cc.GetName()))

	var processedResults v1.ScrapeResults
	for _, r := range results {
		scraped := processScrapeResult(cc, r)
		for i := range scraped {
			if scraped[i].Error != nil {
				ctx.History.AddError(scraped[i].Error.Error())
			} else {
				ctx.History.SuccessCount++
				processedResults = append(processedResults, scraped[i])
			}
		}
	}

	if summary, err := db.SaveResults(cc, processedResults); err != nil {
		return fmt.Errorf("failed to save %d results: %w", len(results), err)
	} else {
		ctx.History.AddDetails("scrape_summary", summary)
	}
	return nil
}
