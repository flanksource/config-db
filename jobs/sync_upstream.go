package jobs

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/flanksource/commons/http"
	"github.com/flanksource/commons/logger"
	"github.com/flanksource/commons/utils"
	"github.com/flanksource/duty/context"
	"github.com/flanksource/duty/job"
	"github.com/flanksource/duty/models"
	"github.com/flanksource/duty/upstream"
	"gorm.io/gorm/clause"

	"github.com/flanksource/config-db/api"
)

var ReconcilePageSize int

var tablesToReconcile = []string{
	"config_scrapers",
	"config_items",
	"config_changes", "config_analysis",
	"config_relationships",
}

var ReconcileConfigs = &job.Job{
	Name:       "ReconcileConfigs",
	Schedule:   "@every 1m",
	Retention:  job.RetentionBalanced,
	Singleton:  true,
	JobHistory: true,
	RunNow:     true,
	Fn: func(ctx job.JobRuntime) error {
		ctx.History.ResourceType = job.ResourceTypeUpstream
		ctx.History.ResourceID = api.UpstreamConfig.Host

		client := upstream.NewUpstreamClient(api.UpstreamConfig)
		summary := upstream.ReconcileSome(ctx.Context, client, ReconcilePageSize, tablesToReconcile...)

		ctx.History.AddDetails("summary", summary)
		ctx.History.SuccessCount, ctx.History.ErrorCount = summary.GetSuccessFailure()
		if summary.Error() != nil {
			ctx.History.AddDetails("errors", summary.Error())
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
	Retention:  job.RetentionFew,
	Fn: func(ctx job.JobRuntime) error {
		ctx.History.ResourceType = job.ResourceTypeUpstream
		ctx.History.ResourceID = api.UpstreamConfig.Host
		count, err := pullUpstreamConfigScrapers(ctx.Context, api.UpstreamConfig)
		ctx.History.SuccessCount = count
		return err
	},
}

var UpstreamJobs = []*job.Job{
	PullUpstreamConfigScrapers,
	ReconcileConfigs,
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
	defer resp.Body.Close() // nolint:errcheck

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
