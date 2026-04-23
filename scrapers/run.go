package scrapers

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/flanksource/commons/collections/syncmap"
	"github.com/flanksource/commons/har"
	"github.com/flanksource/commons/logger"
	"github.com/flanksource/commons/timer"
	"go.opentelemetry.io/otel/attribute"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
	"github.com/flanksource/config-db/scrapers/extract"
	"github.com/flanksource/config-db/scrapers/processors"
)

type contextKey string

const (
	contextKeyScrapeStart contextKey = "scrape_start_time"
)

// Cache store to be used by watch jobs
var TempCacheStore syncmap.SyncMap[string, *api.TempCache]

type ScrapeOutput struct {
	Total            int // all configs & changes
	Summary          v1.ScrapeSummary
	Results          v1.ScrapeResults
	SnapshotPair     *v1.ScrapeSnapshotPair
	HAR              []har.Entry
	Logs             string
	RateLimitResetAt *time.Time
}

type RunScraperOptions struct {
	CaptureHAR         bool
	CaptureSnapshots   bool
	CaptureLogs        bool
	PersistRunArtifact bool
}

type RunScraperOption func(*RunScraperOptions)

func WithCaptureHAR(enabled bool) RunScraperOption {
	return func(o *RunScraperOptions) { o.CaptureHAR = enabled }
}

func WithCaptureSnapshots(enabled bool) RunScraperOption {
	return func(o *RunScraperOptions) { o.CaptureSnapshots = enabled }
}

func WithCaptureLogs(enabled bool) RunScraperOption {
	return func(o *RunScraperOptions) { o.CaptureLogs = enabled }
}

func WithPersistRunArtifact(enabled bool) RunScraperOption {
	return func(o *RunScraperOptions) { o.PersistRunArtifact = enabled }
}

func buildRunScraperOptions(opts ...RunScraperOption) RunScraperOptions {
	cfg := RunScraperOptions{}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	return cfg
}

func ensureHARCollector(ctx api.ScrapeContext, opts RunScraperOptions) api.ScrapeContext {
	if ctx.HARCollector() != nil {
		return ctx
	}
	if opts.CaptureHAR || ctx.Logger.IsTraceEnabled() {
		return ctx.WithHARCollector(har.NewCollector(har.DefaultConfig()))
	}
	return ctx
}

func RunScraper(ctx api.ScrapeContext, opts ...RunScraperOption) (*ScrapeOutput, error) {
	runOpts := buildRunScraperOptions(opts...)
	var timer = timer.NewMemoryTimer()
	ctx, err := ctx.InitTempCache()
	if err != nil {
		return nil, err
	}
	TempCacheStore.Store(ctx.ScraperID(), ctx.TempCache())

	runStart := time.Now()
	ctx = ctx.WithValue(contextKeyScrapeStart, runStart)
	ctx.Context = ctx.
		WithName(fmt.Sprintf("%s/%s", ctx.ScrapeConfig().Namespace, ctx.ScrapeConfig().Name)).
		WithNamespace(ctx.ScrapeConfig().Namespace)

	var runLogs *bytes.Buffer
	if runOpts.CaptureLogs {
		runLogs = &bytes.Buffer{}
		runLogger := logger.NewWithWriter(io.MultiWriter(os.Stderr, runLogs))
		runLogger.SetLogLevel(ctx.Logger.GetLevel())
		ctx = ctx.WithLogger(runLogger)
	}

	ctx = ensureHARCollector(ctx, runOpts)

	var beforeSnapshot *v1.ScrapeSnapshot
	if runOpts.CaptureSnapshots {
		if snap, snapErr := db.CaptureScrapeSnapshot(ctx, runStart); snapErr != nil {
			ctx.Logger.V(2).Infof("failed to capture pre-scrape snapshot: %v", snapErr)
		} else {
			beforeSnapshot = snap
		}
	}

	results, scraperErr := Run(ctx)
	if scraperErr != nil {
		return nil, fmt.Errorf("failed to run scraper %v: %w", ctx.ScrapeConfig().Name, scraperErr)
	}

	if v1.ScrapeResults(results).IsRateLimited() {
		resetAt := v1.ScrapeResults(results).GetRateLimitResetAt()
		ctx.Logger.Warnf("Scrape rate limited, skipping save/retention (reset at %v)", resetAt)
		out := &ScrapeOutput{RateLimitResetAt: resetAt, Results: results}
		if runLogs != nil {
			out.Logs = runLogs.String()
		}
		if runOpts.CaptureHAR {
			if collector := ctx.HARCollector(); collector != nil {
				out.HAR = collector.Entries()
			}
		}
		return out, nil
	}

	savedResult, err := db.SaveResults(ctx, results)
	if err != nil {
		return nil, fmt.Errorf("failed to save results: %w", err)
	}

	if err := UpdateStaleConfigItems(ctx, results); err != nil {
		return nil, fmt.Errorf("failed to update stale config items: %w", err)
	}

	var snapshotPair *v1.ScrapeSnapshotPair
	if runOpts.CaptureSnapshots {
		if afterSnapshot, snapErr := db.CaptureScrapeSnapshot(ctx, runStart); snapErr != nil {
			ctx.Logger.V(2).Infof("failed to capture post-scrape snapshot: %v", snapErr)
		} else {
			snapshotPair = &v1.ScrapeSnapshotPair{
				Before: beforeSnapshot,
				After:  afterSnapshot,
				Diff:   v1.DiffSnapshots(beforeSnapshot, afterSnapshot),
			}
		}
	}

	ctx.Logger.Debugf("Completed scrape with %s in %s", savedResult.PrettyShort(), timer.End())

	out := &ScrapeOutput{
		Total:        len(results),
		Summary:      savedResult,
		Results:      results,
		SnapshotPair: snapshotPair,
	}
	if runLogs != nil {
		out.Logs = runLogs.String()
	}
	if runOpts.CaptureHAR {
		if collector := ctx.HARCollector(); collector != nil {
			out.HAR = collector.Entries()
		}
	}
	return out, nil
}

func UpdateStaleConfigItems(ctx api.ScrapeContext, results v1.ScrapeResults) error {
	basectx, span := ctx.StartSpan("UpdateStaleConfigItems")
	defer span.End()

	ctx.Context = basectx

	persistedID := ctx.ScrapeConfig().GetPersistedID()
	if persistedID != nil {
		ctx.GetSpan().SetAttributes(
			attribute.Int("scrape.results", len(results)),
			attribute.Bool("scrape.hasError", v1.ScrapeResults(results).HasErr()),
		)

		// If error in any of the scrape results, don't delete old items
		if len(results) > 0 && !v1.ScrapeResults(results).HasErr() {
			staleTimeout := ctx.ScrapeConfig().Spec.Retention.StaleItemAge
			if _, err := DeleteStaleConfigItems(ctx.DutyContext(), staleTimeout, *persistedID); err != nil {
				return fmt.Errorf("error deleting stale config items: %w", err)
			}
		}
	}

	return nil
}

// Run ...
func Run(ctx api.ScrapeContext) ([]v1.ScrapeResult, error) {
	plugins, err := db.LoadAllPlugins(ctx.DutyContext())
	if err != nil {
		return nil, ctx.Oops().Wrapf(err, "failed to load plugins")
	}

	var results v1.ScrapeResults
	for _, scraper := range All {
		if !scraper.CanScrape(ctx.ScrapeConfig().Spec) {
			continue
		}

		ctx = ctx.WithScrapeConfig(ctx.ScrapeConfig(), plugins...)

		ctx.Debugf("Starting %s", scraper)
		for _, result := range scraper.Scrape(ctx) {
			scraped := processScrapeResult(ctx, result)

			for i := range scraped {
				if scraped[i].Error != nil {
					ctx.Errorf("Error scraping %s: %v", scraped[i].ID, scraped[i].Error)
					ctx.JobHistory().AddError(scraped[i].Error.Error())
				}
			}

			if !scraped.HasErr() {
				ctx.JobHistory().IncrSuccess()
			}

			results = append(results, scraped...)
		}
	}

	return results, nil
}

// processScrapeResult extracts possibly more configs from the result
func processScrapeResult(ctx api.ScrapeContext, result v1.ScrapeResult) v1.ScrapeResults {
	extract.ApplyAnalysisRules(&result)

	// TODO: Decide if this can be removed here. It's newly placed on func updateChange.
	// changes.ProcessRules(&result, result.BaseScraper.Transform.Change.Mapping...)

	result.Changes = extract.SummarizeChanges(result.Changes)

	if result.Config == nil {
		return []v1.ScrapeResult{result}
	}

	extractor, err := processors.NewExtractor(result.BaseScraper)
	if err != nil {
		result.Error = err
		return []v1.ScrapeResult{result}
	}

	scraped, err := extractor.Extract(ctx, result)
	if err != nil {
		result.Error = err
		return []v1.ScrapeResult{result}
	}

	if ctx.ScrapeConfig().Spec.Full {
		scraped = extract.ExtractFullMode(ctx, ctx.ScrapeConfig().GetPersistedID(), scraped)
	}

	return scraped
}
