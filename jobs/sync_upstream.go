package jobs

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/flanksource/commons/logger"
	"github.com/flanksource/config-db/api"
	"github.com/flanksource/config-db/db"
	"github.com/flanksource/duty/models"
	"github.com/flanksource/duty/upstream"
	"github.com/google/uuid"
	"gorm.io/gorm/clause"
)

var tablesToReconcile = []string{
	"config_items",
	"config_changes",
	"config_analysis",
}

// ReconcileConfigScraperResults pushes missing scrape config results to the upstream server
func ReconcileConfigScraperResults() {
	ctx := api.DefaultContext

	jobHistory := models.NewJobHistory("PushScraperConfigResultsToUpstream", "Config", "")
	_ = db.PersistJobHistory(jobHistory.Start())
	defer func() { _ = db.PersistJobHistory(jobHistory.End()) }()

	reconciler := upstream.NewUpstreamReconciler(api.UpstreamConfig, 500)
	for _, table := range tablesToReconcile {
		if err := reconciler.Sync(ctx, table); err != nil {
			jobHistory.AddError(err.Error())
			logger.Errorf("failed to sync table %s: %v", table, err)
		} else {
			jobHistory.IncrSuccess()
		}
	}
}

// UpstreamPullJob pulls scrape configs from the upstream server
type UpstreamPullJob struct {
	lastFetchedID uuid.UUID
}

func (t *UpstreamPullJob) Run() {
	ctx := api.DefaultContext

	jobHistory := models.NewJobHistory("PullUpstreamScrapeConfigs", "Config", "")
	_ = db.PersistJobHistory(jobHistory.Start())
	defer func() { _ = db.PersistJobHistory(jobHistory.End()) }()

	if err := t.pull(ctx, api.UpstreamConfig); err != nil {
		jobHistory.AddError(err.Error())
		logger.Errorf("error pulling scrape configs from upstream: %v", err)
	} else {
		jobHistory.IncrSuccess()
	}
}

func (t *UpstreamPullJob) pull(ctx api.ScrapeContext, config upstream.UpstreamConfig) error {
	logger.Tracef("pulling scrape configs from upstream since: %v", t.lastFetchedID)

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
	params.Add("since", t.lastFetchedID.String())
	req.URL.RawQuery = params.Encode()

	httpClient := &http.Client{}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("error making request: %w", err)
	}
	defer resp.Body.Close()

	var scrapeConfigs []models.ConfigScraper
	if err := json.NewDecoder(resp.Body).Decode(&scrapeConfigs); err != nil {
		return fmt.Errorf("error decoding JSON response: %w", err)
	}

	if len(scrapeConfigs) == 0 {
		return nil
	}

	t.lastFetchedID = scrapeConfigs[len(scrapeConfigs)-1].ID

	logger.Tracef("fetched %d scrape configs from upstream", len(scrapeConfigs))

	return ctx.DB().Omit("agent_id").Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		UpdateAll: true,
	}).Create(&scrapeConfigs).Error
}

// UpstreamPushJob pushes scrape config results to the upstream server
type UpstreamPushJob struct {
	lastConfigItemID uuid.UUID
	lastAnalysisID   uuid.UUID
	lastChangeID     uuid.UUID

	initiated bool

	// MaxAge defines how far back we look into the past on startup when
	// lastRuntime is zero.
	MaxAge time.Duration
}

// init initializes the last pushed ids ...
func (t *UpstreamPushJob) init(ctx api.ScrapeContext) error {
	if err := ctx.DB().Debug().Model(&models.ConfigItem{}).Select("id").Where("NOW() - updated_at <= ?", t.MaxAge).Scan(&t.lastConfigItemID).Error; err != nil {
		return fmt.Errorf("error getting last config item id: %w", err)
	}

	if err := ctx.DB().Debug().Model(&models.ConfigAnalysis{}).Select("id").Where("NOW() - first_observed <= ?", t.MaxAge).Scan(&t.lastAnalysisID).Error; err != nil {
		return fmt.Errorf("error getting last analysis id: %w", err)
	}

	if err := ctx.DB().Debug().Model(&models.ConfigChange{}).Select("id").Where("NOW() - created_at <= ?", t.MaxAge).Scan(&t.lastChangeID).Error; err != nil {
		return fmt.Errorf("error getting last change id: %w", err)
	}

	return nil
}

func (t *UpstreamPushJob) Run() {
	ctx := api.DefaultContext

	jobHistory := models.NewJobHistory("UpstreamPushJob", "Config", "")
	_ = db.PersistJobHistory(jobHistory.Start())
	defer func() { _ = db.PersistJobHistory(jobHistory.End()) }()

	if !t.initiated {
		logger.Debugf("initializing upstream push job")
		if err := t.init(ctx); err != nil {
			jobHistory.AddError(err.Error())
			logger.Errorf("error initializing upstream push job: %v", err)
			return
		}

		t.initiated = true
	}

	if err := t.run(ctx); err != nil {
		jobHistory.AddError(err.Error())
		logger.Errorf("error pushing to upstream: %v", err)
	} else {
		jobHistory.IncrSuccess()
	}
}

func (t *UpstreamPushJob) run(ctx api.ScrapeContext) error {
	logger.Tracef("running configs upstream push job")

	pushData := &upstream.PushData{AgentName: api.UpstreamConfig.AgentName}
	if err := ctx.DB().Where("id > ?", t.lastConfigItemID).Find(&pushData.ConfigItems).Error; err != nil {
		return err
	}

	if err := ctx.DB().Where("id > ?", t.lastAnalysisID).Find(&pushData.ConfigAnalysis).Error; err != nil {
		return err
	}

	if err := ctx.DB().Where("id > ?", t.lastChangeID).Find(&pushData.ConfigChanges).Error; err != nil {
		return err
	}

	logger.Tracef("pushing %d config scrape results to upstream", pushData.Count())
	if pushData.Count() == 0 {
		return nil
	}

	if err := upstream.Push(ctx, api.UpstreamConfig, pushData); err != nil {
		return fmt.Errorf("error pushing to upstream: %w", err)
	}

	if len(pushData.ConfigItems) > 0 {
		t.lastConfigItemID = pushData.ConfigItems[len(pushData.ConfigItems)-1].ID
	}

	if len(pushData.ConfigAnalysis) > 0 {
		t.lastAnalysisID = pushData.ConfigAnalysis[len(pushData.ConfigAnalysis)-1].ID
	}

	if len(pushData.ConfigChanges) > 0 {
		id := pushData.ConfigChanges[len(pushData.ConfigChanges)-1].ID
		parsed, err := uuid.Parse(id)
		if err != nil {
			return err
		}

		t.lastChangeID = parsed
	}

	return nil
}
