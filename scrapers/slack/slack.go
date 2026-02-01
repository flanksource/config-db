package slack

import (
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/flanksource/commons/duration"
	"github.com/flanksource/config-db/pkg/api"
	v1 "github.com/flanksource/config-db/api"
	"github.com/flanksource/config-db/scrapers/changes"
	"github.com/flanksource/duty/types"
	"github.com/flanksource/gomplate/v3"
	"github.com/samber/lo"
)

var lastScrapeTime = sync.Map{}

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
		if err := client.PopulateUsers(ctx); err != nil {
			results = append(results, v1.ScrapeResult{Error: err})
			continue
		}

		channelsList, err := client.ListConversations(ctx)
		if err != nil {
			results = append(results, v1.ScrapeResult{Error: err})
			continue
		}

		matchingChannels := lo.Filter(channelsList, func(channel ChannelDetail, _ int) bool {
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

		opt.Oldest = strconv.FormatInt(time.Now().Add(-time.Duration(parsed)).Unix(), 10)
	} else {
		opt.Oldest = strconv.FormatInt(time.Now().Add(-time.Hour*24*7).Unix(), 10)
	}

	lastMessagekey := fmt.Sprintf("%s:%s", ctx.ScraperID(), channel.ID)
	if last, ok := lastScrapeTime.Load(lastMessagekey); ok {
		if last.(string) > opt.Oldest {
			opt.Oldest = last.(string)
		}
	}

	messages, err := client.ConversationHistory(ctx, channel, opt)
	if err != nil {
		results = append(results, v1.ScrapeResult{Error: err})
		return results
	}

	if len(messages) == 0 {
		return results
	}

	for _, rule := range config.Rules {
		results = append(results, processRule(ctx, config, rule, messages)...)
	}

	lastScrapeTime.Store(lastMessagekey, messages[0].Timestamp)
	return results
}

func processRule(ctx api.ScrapeContext, config v1.Slack, rule v1.SlackChangeExtractionRule, messages []Message) []v1.ScrapeResult {
	var results v1.ScrapeResults
	for _, message := range messages {
		if accept, err := filterMessage(message, rule.Filter); err != nil {
			results = append(results, v1.ScrapeResult{Error: err})
			return results // bad filter, exit early
		} else if !accept {
			continue
		}

		extractedChanges, err := changes.MapChanges(ctx.DutyContext(), rule.ChangeExtractionRule, message.Text)
		if err != nil {
			results = append(results, v1.ScrapeResult{Error: err})
			return results
		}

		results = append(results, v1.ScrapeResult{
			BaseScraper: config.BaseScraper,
			Changes:     extractedChanges,
		})
	}

	return results
}

func filterMessage(message Message, filter *v1.SlackChangeAcceptanceFilter) (bool, error) {
	if filter == nil {
		return true, nil
	}

	userMatched := matchUser(filter.User, message)
	botMatched := matchBot(filter.Bot, message)
	if !userMatched && !botMatched {
		// Must match one
		return false, nil
	}

	if filter.Expr != "" {
		output, err := gomplate.RunTemplate(message.AsMap(), gomplate.Template{Expression: string(filter.Expr)})
		if err != nil {
			return false, nil
		} else if parsed, err := strconv.ParseBool(output); err != nil {
			return false, fmt.Errorf("expected expresion to return a boolean value: %w", err)
		} else if !parsed {
			return false, nil
		}
	}

	return true, nil
}

func matchUser(match v1.SlackUserFilter, message Message) bool {
	if match.DisplayName != "" {
		if !match.DisplayName.Match(message.UserInfo.Profile.DisplayName) {
			return false
		}
	}

	if match.Name != "" {
		if !match.Name.Match(message.User) {
			return false
		}
	}

	return true
}

func matchBot(match types.MatchExpression, message Message) bool {
	if match == "" {
		return true
	}

	if match == "!*" && message.BotProfile != nil {
		return false // all bot messages should be ignored by the filter
	}

	if message.BotProfile == nil {
		return false // this isn't a message by a bot
	}

	return match.Match(message.BotProfile.Name)
}
