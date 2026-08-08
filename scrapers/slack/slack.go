package slack

import (
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/flanksource/commons/duration"
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/scrapers/changes"
	"github.com/flanksource/duty/types"
	"github.com/flanksource/gomplate/v3"
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

		client, err := NewSlackAPI(ctx, token)
		if err != nil {
			results = append(results, v1.ScrapeResult{Error: err})
			continue
		}
		// users.list only enriches members with their name & email, so a token
		// without users:read still yields the full channel & membership map with
		// members identified by their Slack id.
		var userWarnings []v1.Warning
		if err := client.PopulateUsers(ctx); err != nil {
			userWarnings = append(userWarnings, v1.Warning{
				Error: fmt.Sprintf("failed to list users, members are identified by their slack id: %v", err),
			})
		}

		workspace, err := client.AuthTest(ctx)
		if err != nil {
			results = append(results, v1.ScrapeResult{Error: err})
			continue
		} else if workspace.ID == "" {
			results = append(results, v1.ScrapeResult{Error: fmt.Errorf("slack did not return a workspace id for the token")})
			continue
		}

		channels, err := client.ListConversations(ctx)
		if err != nil {
			results = append(results, v1.ScrapeResult{Error: err})
			continue
		}

		scrape := workspaceScrape{
			Workspace: workspace,
			Channels:  channels,
			Users:     client.Users(),
			Warnings:  userWarnings,
		}
		if config.ScrapeMembers() {
			var errs v1.ScrapeResults
			scrape.Members, errs = fetchChannelMembers(ctx, client, channels)
			results = append(results, errs...)
		}
		results = append(results, buildWorkspaceResults(config.BaseScraper, scrape)...)

		if !config.Messages {
			if len(config.Rules) > 0 {
				ctx.Logger.Warnf("slack: %d change extraction rule(s) are inert because messages are disabled", len(config.Rules))
			}
			continue
		}

		for _, channel := range channels {
			results = append(results, s.scrapeChannel(ctx, config, client, channel)...)
		}
	}

	return results
}

// fetchChannelMembers reads the membership of every channel. A channel the
// token cannot read is reported as an error result and left out of the
// membership map, so the remaining channels still get their access records.
func fetchChannelMembers(ctx api.ScrapeContext, client *SlackAPI, channels []ChannelDetail) (map[string][]string, v1.ScrapeResults) {
	var errs v1.ScrapeResults
	members := make(map[string][]string, len(channels))

	for _, channel := range channels {
		if channel.IsDirectMessage() {
			continue
		}

		channelMembers, err := client.ConversationMembers(ctx, channel)
		if err != nil {
			errs = append(errs, v1.ScrapeResult{Error: err})
			continue
		}
		members[channel.ID] = channelMembers
	}

	return members, errs
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
		if accept, err := filterMessage(ctx, message, rule.Filter); err != nil {
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

func filterMessage(ctx api.ScrapeContext, message Message, filter *v1.SlackChangeAcceptanceFilter) (bool, error) {
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
		output, err := ctx.RunTemplate(gomplate.Template{Expression: string(filter.Expr)}, message.AsMap())
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
