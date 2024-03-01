package jobs

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/flanksource/commons/http"
	"github.com/flanksource/commons/logger"
	"github.com/flanksource/commons/utils"
	"github.com/flanksource/config-db/api"
	"github.com/flanksource/duty/context"
	"github.com/flanksource/duty/job"
	"github.com/flanksource/duty/models"
	"github.com/flanksource/duty/upstream"
	"github.com/flanksource/postq"
	"github.com/flanksource/postq/pg"
	"gorm.io/gorm/clause"
)

var ReconcilePageSize int

const (
	EventPushQueueCreate    = "push_queue.create"
	eventQueueUpdateChannel = "event_queue_updates"
)

var SyncConfigChanges = &job.Job{
	Name:       "SyncConfigChanges",
	JobHistory: true,
	Singleton:  true,
	Retention:  job.RetentionHour,
	RunNow:     true,
	Schedule:   "@every 30s",
	Fn: func(ctx job.JobRuntime) error {
		ctx.History.ResourceType = job.ResourceTypeUpstream
		ctx.History.ResourceID = api.UpstreamConfig.Host
		count, err := upstream.SyncConfigChanges(ctx.Context, api.UpstreamConfig, ReconcilePageSize)
		ctx.History.SuccessCount = count
		return err
	},
}

var SyncConfigAnalyses = &job.Job{
	Name:       "SyncConfigAnalyses",
	JobHistory: true,
	Singleton:  true,
	Retention:  job.RetentionHour,
	RunNow:     true,
	Schedule:   "@every 30s",
	Fn: func(ctx job.JobRuntime) error {
		ctx.History.ResourceType = job.ResourceTypeUpstream
		ctx.History.ResourceID = api.UpstreamConfig.Host
		count, err := upstream.SyncConfigAnalyses(ctx.Context, api.UpstreamConfig, ReconcilePageSize)
		ctx.History.SuccessCount = count
		return err
	},
}

var ReconcileConfigScrapersAndItems = &job.Job{
	Name:       "ReconcileConfigScrapers",
	JobHistory: true,
	Singleton:  true,
	Retention:  job.RetentionDay,
	RunNow:     true,
	Schedule:   "@every 30m",
	Fn: func(ctx job.JobRuntime) error {
		ctx.History.ResourceType = job.ResourceTypeUpstream
		ctx.History.ResourceID = api.UpstreamConfig.Host

		if count, err := upstream.ReconcileTable[models.ConfigScraper](ctx.Context, api.UpstreamConfig, ReconcilePageSize); err != nil {
			ctx.History.AddError(err.Error())
		} else {
			ctx.History.SuccessCount += count
		}

		if count, err := upstream.ReconcileTable[models.ConfigItem](ctx.Context, api.UpstreamConfig, ReconcilePageSize); err != nil {
			ctx.History.AddError(err.Error())
		} else {
			ctx.History.SuccessCount += count
		}

		return nil
	},
}

var PullUpstreamConfigScrapers = &job.Job{
	Name:       "PullUpstreamConfigScrapers",
	JobHistory: true,
	Singleton:  true,
	RunNow:     true,
	Schedule:   "@every 10m",
	Retention:  job.RetentionHour,
	Fn: func(ctx job.JobRuntime) error {
		ctx.History.ResourceType = job.ResourceTypeUpstream
		ctx.History.ResourceID = api.UpstreamConfig.Host
		count, err := pullUpstreamConfigScrapers(ctx.Context, api.UpstreamConfig)
		ctx.History.SuccessCount = count
		return err
	},
}

var UpstreamJobs = []*job.Job{
	SyncConfigChanges,
	SyncConfigAnalyses,
	PullUpstreamConfigScrapers,
	ReconcileConfigScrapersAndItems,
}

// configScrapersPullLastRuntime pulls scrape configs from the upstream server
var configScrapersPullLastRuntime time.Time

func pullUpstreamConfigScrapers(ctx context.Context, config upstream.UpstreamConfig) (int, error) {
	logger.Tracef("pulling scrape configs from upstream since: %v", configScrapersPullLastRuntime)

	req := http.NewClient().BaseURL(config.Host).Auth(config.Username, config.Password).R(ctx).
		QueryParam("since", configScrapersPullLastRuntime.Format(time.RFC3339Nano)).
		QueryParam(upstream.AgentNameQueryParam, config.AgentName)

	resp, err := req.Get("upstream/scrapeconfig/pull")
	if err != nil {
		return 0, fmt.Errorf("error making request: %w", err)
	}
	defer resp.Body.Close()

	if !resp.IsOK() {
		body, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("server returned unexpected status:%s (%s)", resp.Status, body)
	}

	var scrapeConfigs []models.ConfigScraper
	if err := json.NewDecoder(resp.Body).Decode(&scrapeConfigs); err != nil {
		return 0, fmt.Errorf("error decoding JSON response: %w", err)
	}

	if len(scrapeConfigs) == 0 {
		return 0, nil
	}

	configScrapersPullLastRuntime = utils.Deref(scrapeConfigs[len(scrapeConfigs)-1].UpdatedAt)
	logger.Tracef("fetched %d scrape configs from upstream", len(scrapeConfigs))

	return len(scrapeConfigs), ctx.DB().Omit("agent_id").Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		UpdateAll: true,
	}).Create(&scrapeConfigs).Error
}

func StartUpstreamConsumer(ctx context.Context) error {
	asyncConsumer := postq.AsyncEventConsumer{
		WatchEvents: []string{EventPushQueueCreate},
		Consumer: func(_ctx postq.Context, e postq.Events) postq.Events {
			return upstream.NewPushUpstreamConsumer(api.UpstreamConfig)(ctx, e)
		},
		BatchSize: 50,
		ConsumerOption: &postq.ConsumerOption{
			NumConsumers: 5,
			ErrorHandler: func(ctx postq.Context, err error) bool {
				logger.Errorf("error consuming upstream push_queue.create events: %v", err)
				time.Sleep(time.Second)
				return true
			},
		},
	}

	consumer, err := asyncConsumer.EventConsumer()
	if err != nil {
		return err
	}

	pgNotifyChannel := make(chan string)
	go pg.Listen(ctx, eventQueueUpdateChannel, pgNotifyChannel)

	go consumer.Listen(ctx, pgNotifyChannel)
	return nil
}
