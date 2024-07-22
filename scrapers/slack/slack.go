package slack

import (
	"fmt"
	"time"

	"github.com/flanksource/commons/duration"
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/samber/lo"
)

type Scraper struct{}

func (s Scraper) CanScrape(configs v1.ScraperSpec) bool {
	return len(configs.Slack) > 0
}

func (s Scraper) Scrape(ctx api.ScrapeContext) v1.ScrapeResults {
	var results v1.ScrapeResults

	for _, config := range ctx.ScrapeConfig().Spec.Slack {
		token, err := ctx.GetEnvValueFromCache(config.Token, ctx.Namespace())
		if err != nil {
			results = append(results, v1.ScrapeResult{Error: err})
			continue
		}

		client := NewSlackAPI(token)

		channelsList, err := client.ListConversations(ctx)
		if err != nil {
			results = append(results, v1.ScrapeResult{Error: err})
			continue
		}

		matchingChannels := lo.Filter(channelsList.Channels, func(channel ChannelDetail, _ int) bool {
			return config.Channels.Match(channel.Name)
		})

		for _, channel := range matchingChannels {
			results = append(results, s.scrapeChannel(ctx, config, client, channel)...)
		}
	}

	return results
}

func (s Scraper) scrapeChannel(ctx api.ScrapeContext, config v1.Slack, client *SlackAPI, channel ChannelDetail) []v1.ScrapeResult {
	var results v1.ScrapeResults

	opt := &GetConversationHistoryParameters{}
	if config.Since != "" {
		parsed, err := duration.ParseDuration(config.Since)
		if err != nil {
			results = append(results, v1.ScrapeResult{Error: fmt.Errorf("bad duration string %s: %w", config.Since, err)})
			return results
		}

		opt.Oldest = time.Now().Add(-time.Duration(parsed)).Unix()
	}

	messages, err := client.ConversationHistory(ctx, channel, opt)
	if err != nil {
		results = append(results, v1.ScrapeResult{Error: err})
		return results
	}

	for _, message := range messages {
		results = append(results, v1.ScrapeResult{
			BaseScraper: config.BaseScraper,
			Changes: []v1.ChangeResult{
				{Source: message.Channel},
			},
		})
	}

	return results
}
