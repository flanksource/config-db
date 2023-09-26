package jobs

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

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

type LastPushedConfigResult struct {
	ConfigID   uuid.UUID
	AnalysisID uuid.UUID
	ChangeID   uuid.UUID
}

// UpstreamPushJob pushes scrape config results to the upstream server
type UpstreamPushJob struct {
	status LastPushedConfigResult

	initiated bool
}

// init initializes the last pushed ids ...
func (t *UpstreamPushJob) init(ctx api.ScrapeContext, config upstream.UpstreamConfig) error {
	endpoint, err := url.JoinPath(config.Host, "upstream", "scrapeconfig", "status", config.AgentName)
	if err != nil {
		return fmt.Errorf("error creating url endpoint for host %s: %w", config.Host, err)
	}

	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("error creating new http request: %w", err)
	}

	req.SetBasicAuth(config.Username, config.Password)

	httpClient := &http.Client{}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("error making request: %w", err)
	}
	defer resp.Body.Close()

	if err := json.NewDecoder(resp.Body).Decode(&t.status); err != nil {
		return fmt.Errorf("error decoding JSON response: %w", err)
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
		if err := t.init(ctx, api.UpstreamConfig); err != nil {
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
	pushData := &upstream.PushData{AgentName: api.UpstreamConfig.AgentName}
	if err := ctx.DB().Where("id > ?", t.status.ConfigID).Find(&pushData.ConfigItems).Error; err != nil {
		return err
	}

	if err := ctx.DB().Where("id > ?", t.status.AnalysisID).Find(&pushData.ConfigAnalysis).Error; err != nil {
		return err
	}

	if err := ctx.DB().Where("id > ?", t.status.ChangeID).Find(&pushData.ConfigChanges).Error; err != nil {
		return err
	}

	if pushData.Count() == 0 {
		return nil
	}

	logger.Tracef("pushing %d config scrape results to upstream", pushData.Count())
	if err := upstream.Push(ctx, api.UpstreamConfig, pushData); err != nil {
		return fmt.Errorf("error pushing to upstream: %w", err)
	}

	if len(pushData.ConfigItems) > 0 {
		t.status.ConfigID = pushData.ConfigItems[len(pushData.ConfigItems)-1].ID
	}

	if len(pushData.ConfigAnalysis) > 0 {
		t.status.AnalysisID = pushData.ConfigAnalysis[len(pushData.ConfigAnalysis)-1].ID
	}

	if len(pushData.ConfigChanges) > 0 {
		id := pushData.ConfigChanges[len(pushData.ConfigChanges)-1].ID
		parsed, err := uuid.Parse(id)
		if err != nil {
			return err
		}

		t.status.ChangeID = parsed
	}

	return nil
}
