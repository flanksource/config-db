package pubsub

import (
	"fmt"
	"time"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty/context"
	dutypubsub "github.com/flanksource/duty/pubsub"
	"github.com/samber/lo"
	gocloudpubsub "gocloud.dev/pubsub"
)

type Scraper struct{}

func (Scraper) CanScrape(configs v1.ScraperSpec) bool {
	return len(configs.PubSub) > 0
}

func (ps Scraper) Scrape(ctx api.ScrapeContext) v1.ScrapeResults {
	allResults := v1.ScrapeResults{}
	for _, config := range ctx.ScrapeConfig().Spec.PubSub {
		results, err := fetchFromPubSub(ctx.Context, config)
		if err != nil {
			allResults.Errorf(err, "failed to fetch from pubsub")
			continue
		}
		allResults = append(allResults, results...)
	}
	return allResults
}

func fetchFromPubSub(ctx context.Context, config v1.PubSub) (v1.ScrapeResults, error) {
	var results v1.ScrapeResults
	queueConfig := config.QueueConfig

	subscription, err := dutypubsub.Subscribe(ctx, queueConfig)
	if err != nil {
		return nil, fmt.Errorf("error opening subscription for %s: %w", queueConfig.GetQueue(), err)
	}

	defer subscription.Shutdown(ctx) //nolint:errcheck

	limit := lo.CoalesceOrEmpty(config.MaxMessages, 2000)
	msgs, err := ListenWithTimeout(ctx, subscription, 10*time.Second, limit)
	if err != nil {
		return nil, fmt.Errorf("error listening to subscription %s: %w", queueConfig.GetQueue(), err)
	}

	for _, msg := range msgs {
		results = append(results, v1.ScrapeResult{
			BaseScraper: config.BaseScraper,
			Config:      msg,
		})
	}

	return results, nil
}

func ListenWithTimeout(ctx context.Context, subscription *gocloudpubsub.Subscription, timeout time.Duration, limit int) ([]string, error) {
	timeoutCh := make(chan bool, 1)
	messageCh := make(chan string, 1)
	errorCh := make(chan error, 1)

	var messages []string

	for {
		// Reset after each iteration
		timer := time.AfterFunc(timeout, func() {
			timeoutCh <- true
		})

		// Listen for messages in a goroutine
		go func() {
			msg, err := subscription.Receive(ctx)
			if err != nil {
				errorCh <- err
				return
			}
			messageCh <- string(msg.Body)
			msg.Ack()
		}()

		// Wait for either a message, error, or timeout
		select {
		case <-ctx.Done():
			return messages, nil
		case msg := <-messageCh:
			// Stop the timer since we got a message
			timer.Stop()
			messages = append(messages, msg)
			if len(messages) >= limit {
				return messages, nil
			}
		case err := <-errorCh:
			timer.Stop()
			return messages, err
		case <-timeoutCh:
			return messages, nil
		}
	}
}
