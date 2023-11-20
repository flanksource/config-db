package jobs

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/flanksource/commons/logger"
	"github.com/flanksource/config-db/api"
	"github.com/flanksource/config-db/db"
	"github.com/flanksource/duty/models"
	"github.com/flanksource/duty/upstream"
	"github.com/flanksource/postq/pg"
	"gorm.io/gorm/clause"
)

var ReconcilePageSize int

const (
	EventPushQueueCreate    = "push_queue.create"
	eventQueueUpdateChannel = "event_queue_updates"
)

// ReconcileConfigScraperResults pushes missing scrape config results to the upstream server
func ReconcileConfigScraperResults() {
	ctx := api.DefaultContext

	jobHistory := models.NewJobHistory("PushUpstream", "Config", "")
	_ = db.PersistJobHistory(jobHistory.Start())
	defer func() { _ = db.PersistJobHistory(jobHistory.End()) }()

	reconciler := upstream.NewUpstreamReconciler(api.UpstreamConfig, ReconcilePageSize)
	if err := reconciler.SyncAfter(ctx.DutyContext(), "config_items", time.Hour*48); err != nil {
		jobHistory.AddError(err.Error())
		logger.Errorf("failed to sync table config_items: %v", err)
	} else {
		jobHistory.IncrSuccess()
	}
}

// UpstreamPullJob pulls scrape configs from the upstream server
type UpstreamPullJob struct {
	lastRuntime time.Time
}

func (t *UpstreamPullJob) Run() {
	jobHistory := models.NewJobHistory("PullUpstream", "Config", "")
	_ = db.PersistJobHistory(jobHistory.Start())
	defer func() { _ = db.PersistJobHistory(jobHistory.End()) }()

	if err := t.pull(api.DefaultContext, api.UpstreamConfig); err != nil {
		jobHistory.AddError(err.Error())
		logger.Errorf("error pulling scrape configs from upstream: %v", err)
	} else {
		jobHistory.IncrSuccess()
	}
}

func (t *UpstreamPullJob) pull(ctx api.ScrapeContext, config upstream.UpstreamConfig) error {
	logger.Tracef("pulling scrape configs from upstream since: %v", t.lastRuntime)

	endpoint, err := url.JoinPath(config.Host, "upstream", "scrapeconfig", "pull", config.AgentName)
	if err != nil {
		return fmt.Errorf("error creating url endpoint for host %s: %w", config.Host, err)
	}

	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("error creating new http request: %w", err)
	}

	req.SetBasicAuth(config.Username, config.Password)

	params := url.Values{}
	params.Add("since", t.lastRuntime.Format(time.RFC3339Nano))
	req.URL.RawQuery = params.Encode()

	httpClient := &http.Client{}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("error making request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server returned unexpected status:%s (%s)", resp.Status, body)
	}

	var scrapeConfigs []models.ConfigScraper
	if err := json.NewDecoder(resp.Body).Decode(&scrapeConfigs); err != nil {
		return fmt.Errorf("error decoding JSON response: %w", err)
	}

	if len(scrapeConfigs) == 0 {
		return nil
	}

	t.lastRuntime = scrapeConfigs[len(scrapeConfigs)-1].UpdatedAt

	logger.Tracef("fetched %d scrape configs from upstream", len(scrapeConfigs))

	return ctx.DB().Omit("agent_id").Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		UpdateAll: true,
	}).Create(&scrapeConfigs).Error
}

func StartConsumser(ctx api.ScrapeContext) error {
	consumer, err := upstream.NewPushQueueConsumer(api.UpstreamConfig).EventConsumer()
	if err != nil {
		return err
	}

	pgNotifyChannel := make(chan string)
	go pg.Listen(ctx, eventQueueUpdateChannel, pgNotifyChannel)

	go consumer.Listen(ctx, pgNotifyChannel)
	return nil
}
