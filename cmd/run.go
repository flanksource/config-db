package cmd

import (
	"bytes"
	gocontext "context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/flanksource/clicky"
	clickyapi "github.com/flanksource/clicky/api"
	"github.com/flanksource/commons/har"
	"github.com/flanksource/commons/hash"
	"github.com/flanksource/commons/logger"
	"github.com/flanksource/commons/timer"
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/cmd/scrapeui"
	"github.com/flanksource/config-db/db"
	"github.com/flanksource/config-db/scrapers"
	"github.com/flanksource/duty"
	dutyapi "github.com/flanksource/duty/api"
	"github.com/flanksource/duty/context"
	dutyEcho "github.com/flanksource/duty/echo"
	"github.com/flanksource/duty/job"
	"github.com/flanksource/duty/models"
	"github.com/flanksource/duty/shutdown"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/lib/pq"
	"github.com/spf13/cobra"
	"gorm.io/gorm"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

var outputDir string
var debugPort int
var export bool
var save bool
var uiEnabled bool
var uiPort int

// Run ...
var Run = &cobra.Command{
	Use:   "run <scraper.yaml>",
	Short: "Run scrapers and return",
	Run: func(cmd *cobra.Command, configFiles []string) {
		var logBuf bytes.Buffer
		harCollector := har.NewCollector(har.DefaultConfig())

		if logger.IsTraceEnabled() {
			logger.Tracef("Enabling HAR collection")
		}

		clicky.Flags.UseFlags()

		// Capture all logs by teeing stderr to a buffer.
		// Must happen BEFORE context.New() so contexts inherit the multiwriter logger.
		logLevel := logger.StandardLogger().GetLevel()
		logger.Use(io.MultiWriter(os.Stderr, &logBuf))
		// logger.Use() creates a fresh logger that doesn't inherit the level
		// set by UseFlags(), so re-apply the previous level.
		logger.StandardLogger().SetLogLevel(logLevel)

		logger.Infof("Scraping %v", configFiles)
		scraperConfigs, err := v1.ParseConfigs(configFiles...)
		if err != nil {
			logger.Fatalf(err.Error())
		}

		if logger.IsTraceEnabled() {
			for _, sc := range scraperConfigs {
				logger.Tracef("Scraper %s:\n%s", sc.Name, logger.Pretty(sc.Spec))
			}
		}

		dutyCtx := context.New()
		if dutyapi.DefaultConfig.ConnectionString != "" {
			c, _, err := duty.Start(app, duty.ClientOnly)
			if err != nil {
				logger.Fatalf("Failed to initialize db: %v", err.Error())
			}

			dutyCtx = c
			db.WarmExternalEntityCaches(dutyCtx)
			if blobs, err := dutyCtx.Blobs(); err != nil {
				logger.Warnf("failed to initialize blob store: %v", err)
			} else {
				api.BlobStore = blobs
			}
		}

		if debugPort >= 0 {
			e := echo.New()
			e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
				return func(c echo.Context) error {
					c.SetRequest(c.Request().WithContext(dutyCtx.Wrap(c.Request().Context())))
					return next(c)
				}
			})
			dutyEcho.AddDebugHandlers(dutyCtx, e, func(next echo.HandlerFunc) echo.HandlerFunc { return next })

			shutdown.AddHook(func() {
				ctx, cancel := gocontext.WithTimeout(gocontext.Background(), 1*time.Minute)
				defer cancel()

				if err := e.Shutdown(ctx); err != nil {
					e.Logger.Fatal(err)
				}
			})
			shutdown.WaitForSignal()

			go func() {
				if err := e.Start(fmt.Sprintf(":%d", debugPort)); err != nil && err != http.ErrServerClosed {
					e.Logger.Fatal(err)
				}
			}()
		}

		if save && dutyapi.DefaultConfig.ConnectionString != "" {
			for i := range scraperConfigs {
				if err := ensureScraper(dutyCtx, &scraperConfigs[i]); err != nil {
					logger.Fatalf("failed to ensure scraper for %s: %v", scraperConfigs[i].Name, err)
				}
			}
		}

		scrapeSpec := buildScrapeSpec(scraperConfigs)
		var uiServer *scrapeui.Server
		if uiEnabled {
			names := make([]string, len(scraperConfigs))
			for i, sc := range scraperConfigs {
				names[i] = sc.Name
			}
			uiServer = startScrapeUI(names, scrapeSpec, &logBuf)
		}

		var hasErrors bool
		var allResults v1.ScrapeResults
		var lastSummary *v1.ScrapeSummary
		var lastScrapeSummary *v1.ScrapeSummary
		snapshotPairs := map[string]*v1.ScrapeSnapshotPair{}
		progress := make([]scrapeui.ScraperProgress, len(scraperConfigs))
		for i := range scraperConfigs {
			startedAt := time.Now()
			progress[i] = scrapeui.ScraperProgress{
				Name:      scrapeui.ScraperName(scraperConfigs[i].Name),
				Status:    scrapeui.ScraperRunning,
				StartedAt: &startedAt,
			}
			if uiServer != nil {
				uiServer.UpdateScraper(scraperConfigs[i].Name, scrapeui.ScraperRunning, nil, nil, nil)
			}

			scrapeCtx, cancel, cancelTimeout := api.NewScrapeContext(dutyCtx).WithScrapeConfig(&scraperConfigs[i]).
				WithTimeout(dutyCtx.Properties().Duration("scraper.timeout", 4*time.Hour))
			defer cancelTimeout()
			shutdown.AddHook(func() { defer cancel() })

			if save && dutyapi.DefaultConfig.ConnectionString != "" {
				prev := scrapers.GetLastScrapeSummary(dutyCtx, string(scraperConfigs[i].GetUID()))
				scrapeCtx = scrapeCtx.WithLastScrapeSummary(prev)
				lastScrapeSummary = &prev
				if uiServer != nil {
					uiServer.SetLastScrapeSummary(prev)
				}
			}
			scrapeCtx = scrapeCtx.WithHARCollector(harCollector)

			results, summary, snapshotPair, err := scrapeAndStore(scrapeCtx)
			progress[i].DurationSec = time.Since(startedAt).Seconds()
			progress[i].ResultCount = len(scrapeui.MergeResults(results).Configs)
			if err != nil {
				hasErrors = true
				progress[i].Status = scrapeui.ScraperError
				progress[i].Error = err.Error()
				logger.Errorf("error scraping config: (name=%s) %+v", scraperConfigs[i].Name, err)
				if uiServer != nil {
					uiServer.UpdateScraper(scraperConfigs[i].Name, scrapeui.ScraperError, results, summary, err)
				}
			} else {
				progress[i].Status = scrapeui.ScraperComplete
				if uiServer != nil {
					uiServer.UpdateScraper(scraperConfigs[i].Name, scrapeui.ScraperComplete, results, summary, nil)
				}
			}
			if snapshotPair != nil {
				snapshotPairs[scrapeui.ScraperName(scraperConfigs[i].Name)] = snapshotPair
				if uiServer != nil {
					uiServer.SetSnapshots(scraperConfigs[i].Name, snapshotPair)
				}
			}

			scraperUID := string(scraperConfigs[i].GetUID())
			if scraperUID == "" {
				if id, err := hash.DeterministicUUID(pq.StringArray{scraperConfigs[i].Namespace, scraperConfigs[i].Name}); err == nil {
					scraperUID = id.String()
				}
			}
			if save && dutyapi.DefaultConfig.ConnectionString != "" {
				history := models.NewJobHistory(logger.StandardLogger(), "scraper", job.ResourceTypeScraper, scraperUID)
				history.Start()
				history.SuccessCount = len(results)
				if summary != nil {
					history.AddDetails("scrape_summary", *summary)
				}
				if err != nil {
					history.AddError(err.Error())
				}
				history.End()
				if persistErr := history.Persist(dutyCtx.DB()); persistErr != nil {
					logger.Warnf("failed to persist job history: %v", persistErr)
				}
				if summary != nil {
					scrapers.ScraperSummaryCache.Store(scraperUID, *summary)
				}
			}

			allResults = append(allResults, results...)
			if summary != nil {
				lastSummary = summary
			}
		}

		// Restore stderr-only logging before rendering
		logger.Use(os.Stderr)

		if uiServer != nil {
			uiServer.SetHAR(harCollector.Entries())
			uiServer.SetDone()
		}

		printOutput(allResults, lastSummary, lastScrapeSummary, snapshotPairs, scraperConfigs, progress, scrapeSpec, harCollector, logBuf.String())

		if uiServer != nil {
			sig := make(chan os.Signal, 1)
			signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
			<-sig
			return
		}

		if hasErrors {
			os.Exit(1)
		}
	},
}

// runHTMLOutput wraps scrape results for HTML rendering.
// Uses pretty:"table" tags to prevent empty slices from appearing as broken summary entries.
type runHTMLOutput struct {
	Counts             v1.CountsGrid                `json:"-"`
	SaveSummary        *v1.ScrapeSummary            `json:"-"`
	Snapshots          *v1.ScrapeSnapshotPair       `json:"snapshots,omitempty"`
	Configs            []v1.ScrapeResult            `pretty:"table"`
	Changes            []changeWithScreenshot       `pretty:"table"`
	Artifacts          []models.Artifact            `pretty:"table"`
	Analysis           []models.ConfigAnalysis      `pretty:"table"`
	ExternalRoles      []models.ExternalRole        `pretty:"table"`
	ExternalUsers      []models.ExternalUser        `pretty:"table"`
	ExternalGroups     []models.ExternalGroup       `pretty:"table"`
	ExternalUserGroups []v1.ExternalUserGroup       `pretty:"table"`
	ConfigAccess       []v1.ExternalConfigAccess    `pretty:"table"`
	ConfigAccessLogs   []v1.ExternalConfigAccessLog `pretty:"table"`
	Logs               v1.LogOutput                 `json:"-"`
	HTTPTraffic        []har.Entry                  `json:"har,omitempty"`

	// Relationships      []scrapeui.UIRelationship      `pretty:"table"`
	// ConfigMeta         map[string]scrapeui.ConfigMeta `json:",omitempty"`
}

func scrapeAndStore(ctx api.ScrapeContext) ([]v1.ScrapeResult, *v1.ScrapeSummary, *v1.ScrapeSnapshotPair, error) {
	ctx, err := ctx.InitTempCache()
	if err != nil {
		return nil, nil, nil, err
	}

	// Capture the pre-scrape global DB state so we can diff against the
	// post-scrape snapshot later. Only meaningful when a DB is configured;
	// otherwise we skip both captures entirely. Snapshot capture is
	// observability, so we log-and-continue on failure rather than aborting
	// the scrape itself.
	dbConnected := save && dutyapi.DefaultConfig.ConnectionString != ""
	runStart := time.Now()
	var beforeSnapshot *v1.ScrapeSnapshot
	if dbConnected {
		beforeSnapshot, err = db.CaptureScrapeSnapshot(ctx, runStart)
		if err != nil {
			logger.Warnf("failed to capture pre-scrape snapshot: %v", err)
		}
	}
	// beforeOnlyPair returns a partial snapshot pair with just the Before
	// snapshot populated. Used on early-exit error paths so the Snapshot tab
	// still shows the pre-scrape DB state even when the scrape itself failed
	// (e.g. the token expired before we could fetch anything).
	beforeOnlyPair := func() *v1.ScrapeSnapshotPair {
		if beforeSnapshot == nil {
			return nil
		}
		return &v1.ScrapeSnapshotPair{Before: beforeSnapshot}
	}

	timer := timer.NewMemoryTimer()
	results, err := scrapers.Run(ctx)
	if err != nil {
		return nil, nil, beforeOnlyPair(), err
	}

	// Collect any per-result scrape errors but do NOT short-circuit: a
	// partial failure (one paginated endpoint rate-limited, one tenant with
	// a permission gap) should still persist everything that did succeed.
	// The scrape error is combined with any later save error below and
	// returned to the caller, which surfaces both to the scrapeui via the
	// red error banner and still gets a populated snapshot pair.
	var scrapeErr error
	scrapeResults := v1.ScrapeResults(results)
	if scrapeResults.HasErr() {
		errs := scrapeResults.Errors()
		for _, e := range errs {
			logger.Errorf("scrape error: %s", e)
		}
		joined := strings.Join(errs, "\n---\n")
		scrapeErr = fmt.Errorf("scrape completed with %d error(s):\n%s", len(errs), joined)
	}

	logger.Infof("Scraped %d resources (%s)", len(results), timer.End())

	if outputDir != "" {
		for _, result := range results {
			if err := exportResource(result, outputDir); err != nil {
				return results, nil, beforeOnlyPair(), errors.Join(scrapeErr, fmt.Errorf("failed to export results: %w", err))
			}
		}
		logger.Infof("Exported %d resources to %s (%s)", len(results), outputDir, timer.End())
	}

	if dbConnected {
		summary, saveErr := db.SaveResults(ctx, results)
		if saveErr != nil {
			return results, nil, beforeOnlyPair(), errors.Join(scrapeErr, fmt.Errorf("failed to save results to db: %w", saveErr))
		}
		logger.Infof("Exported %d resources to DB: %s (%s)", len(results), summary.PrettyShort(), timer.End())

		afterSnapshot, captureErr := db.CaptureScrapeSnapshot(ctx, runStart)
		if captureErr != nil {
			logger.Warnf("failed to capture post-scrape snapshot: %v", captureErr)
		}
		snapshotPair := &v1.ScrapeSnapshotPair{
			Before: beforeSnapshot,
			After:  afterSnapshot,
			Diff:   v1.DiffSnapshots(beforeSnapshot, afterSnapshot),
		}
		// Log both the diff (what changed) and the After totals (final DB
		// state). On idempotent re-scrapes the diff is empty, but the After
		// counts are still useful for confirming the DB is populated.
		logger.Infof("Scrape snapshot diff: %s", snapshotPair.Diff.PrettyShort())
		if afterSnapshot != nil {
			logger.Infof("Scrape snapshot after: %s", afterSnapshot.PrettyShort())
		}

		return results, &summary, snapshotPair, scrapeErr
	}

	return results, nil, beforeOnlyPair(), scrapeErr
}

type changeWithScreenshot struct {
	v1.ChangeResult
}

func (c changeWithScreenshot) Columns() []clickyapi.ColumnDef {
	return []clickyapi.ColumnDef{
		clicky.Column("ConfigType").Build(),
		clicky.Column("ExternalID").Build(),
		clicky.Column("ChangeType").Build(),
		clicky.Column("Summary").Build(),
		clicky.Column("Severity").Build(),
		clicky.Column("CreatedAt").Build(),
	}
}

func (c changeWithScreenshot) Row() map[string]any {
	return map[string]any{
		"ConfigType": clicky.Text(c.ConfigType),
		"ExternalID": clicky.Text(c.ExternalID),
		"ChangeType": clicky.Text(c.ChangeType),
		"Summary":    clicky.Text(c.Summary),
		"Severity":   clicky.Text(c.Severity),
		"CreatedAt":  c.CreatedAt,
	}
}

func (c changeWithScreenshot) RowDetail() clickyapi.Textable {
	base := clicky.Text("")
	if c.Summary != "" {
		base = base.Append(c.Summary)
	}

	details := c.Details
	if len(details) == 0 {
		return base
	}

	var imgs string
	if arr, ok := details["artifacts"].([]any); ok {
		for _, item := range arr {
			art, ok := item.(map[string]any)
			if !ok {
				continue
			}
			artID, _ := art["artifactId"].(string)
			if artID == "" || api.BlobStore == nil {
				continue
			}
			id, err := uuid.Parse(artID)
			if err != nil {
				continue
			}
			artifactData, err := api.BlobStore.Read(id)
			if err != nil || artifactData == nil || artifactData.Content == nil {
				continue
			}
			data, err := io.ReadAll(artifactData.Content)
			artifactData.Content.Close() //nolint:errcheck
			if err != nil || len(data) == 0 {
				continue
			}
			b64 := base64.StdEncoding.EncodeToString(data)
			imgs += fmt.Sprintf(
				`<img src="data:image/png;base64,%s" style="max-width:100%%;border-radius:4px;margin:4px 0" />`,
				b64,
			)
		}
	}

	if imgs == "" {
		return base
	}
	return screenshotDetail{html: base.HTML() + imgs}
}

type screenshotDetail struct{ html string }

func (s screenshotDetail) String() string     { return "[screenshot]" }
func (s screenshotDetail) ANSI() string       { return "[screenshot]" }
func (s screenshotDetail) HTML() string       { return s.html }
func (s screenshotDetail) Markdown() string   { return "[screenshot]" }
func (s screenshotDetail) StaticHTML() string { return s.html }

func printOutput(results v1.ScrapeResults, summary, lastScrapeSummary *v1.ScrapeSummary, snapshots map[string]*v1.ScrapeSnapshotPair, scraperConfigs []v1.ScrapeConfig, progress []scrapeui.ScraperProgress, scrapeSpec any, harCollector *har.Collector, logs string) {
	if outputDir != "" {
		return
	}

	all := v1.MergeScrapeResults(results)
	var changes []changeWithScreenshot
	var artifacts []models.Artifact
	for _, c := range all.Changes {
		changes = append(changes, changeWithScreenshot{c})
		if len(c.Details) == 0 {
			continue
		}

		var artMaps []map[string]any
		if arr, ok := c.Details["artifacts"].([]any); ok {
			for _, item := range arr {
				if m, ok := item.(map[string]any); ok {
					artMaps = append(artMaps, m)
				}
			}
		}

		for _, art := range artMaps {
			filename, _ := art["name"].(string)
			checksum, _ := art["sha"].(string)
			a := models.Artifact{
				Filename: filename,
				Checksum: checksum,
			}
			if size, ok := art["size"].(float64); ok {
				a.Size = int64(size)
			}
			if artID, ok := art["artifactId"].(string); ok && artID != "" && api.BlobStore != nil {
				if id, err := uuid.Parse(artID); err == nil {
					if artifactData, err := api.BlobStore.Read(id); err == nil && artifactData != nil && artifactData.Content != nil {
						data, readErr := io.ReadAll(artifactData.Content)
						artifactData.Content.Close() //nolint:errcheck
						if readErr == nil && len(data) > 0 {
							a.ContentType = "image/png"
							a.SetContent(data, "gzip", 0) //nolint:errcheck
						}
					}
				}
			}
			artifacts = append(artifacts, a)
		}
	}

	relationships := scrapeui.BuildUIRelationships(results)
	output := runHTMLOutput{
		Counts:             v1.BuildCounts(all),
		Configs:            all.Configs,
		Changes:            changes,
		Artifacts:          artifacts,
		Analysis:           all.Analysis,
		ExternalRoles:      all.ExternalRoles,
		ExternalUsers:      all.ExternalUsers,
		ExternalGroups:     all.ExternalGroups,
		ExternalUserGroups: all.ExternalUserGroups,
		ConfigAccess:       all.ConfigAccess,
		ConfigAccessLogs:   all.ConfigAccessLogs,
		HTTPTraffic:        harCollector.Entries(),
		Logs:               v1.BuildLogOutput(logs),
	}
	output.SaveSummary = summary
	output.Snapshots = singleSnapshotPair(snapshots)

	snapshotOutput := buildRunSnapshotOutput(all, results, summary, lastScrapeSummary, snapshots, scraperConfigs, progress, scrapeSpec, relationships, harCollector.Entries(), logs)
	printRunOutput(output, snapshotOutput)
}

func buildRunSnapshotOutput(all v1.FullScrapeResults, rawResults v1.ScrapeResults, summary, lastScrapeSummary *v1.ScrapeSummary, snapshots map[string]*v1.ScrapeSnapshotPair, scraperConfigs []v1.ScrapeConfig, progress []scrapeui.ScraperProgress, scrapeSpec any, relationships []scrapeui.UIRelationship, harEntries []har.Entry, logs string) scrapeui.Snapshot {
	if len(relationships) == 0 {
		relationships = scrapeui.BuildUIRelationships(rawResults)
	}
	if len(progress) == 0 {
		progress = make([]scrapeui.ScraperProgress, len(scraperConfigs))
		for i, sc := range scraperConfigs {
			progress[i] = scrapeui.ScraperProgress{
				Name:        scrapeui.ScraperName(sc.Name),
				Status:      scrapeui.ScraperComplete,
				ResultCount: len(all.Configs),
			}
		}
	}
	if len(snapshots) == 0 {
		snapshots = nil
	}
	bi := GetBuildInfo()
	return scrapeui.Snapshot{
		Scrapers:          progress,
		Results:           all,
		Relationships:     relationships,
		ConfigMeta:        scrapeui.BuildConfigMeta(rawResults, relationships),
		Issues:            scrapeui.BuildIssues(summary),
		Counts:            scrapeui.BuildCounts(all, relationships),
		SaveSummary:       scrapeui.ConvertSaveSummary(summary),
		Snapshots:         snapshots,
		ScrapeSpec:        scrapeSpec,
		HAR:               harEntries,
		Logs:              logs,
		Done:              true,
		StartedAt:         startedAtMillis(progress),
		BuildInfo:         &scrapeui.BuildInfo{Version: bi.Version, Commit: bi.Commit, Date: bi.Date},
		LastScrapeSummary: lastScrapeSummary,
	}
}

func printRunOutput(defaultOutput runHTMLOutput, snapshotOutput scrapeui.Snapshot) {
	opts := clicky.Flags.FormatOptions
	if err := opts.ParseFormatSpec(); err != nil {
		logger.Fatalf("invalid output format: %v", err)
	}
	if len(opts.Sinks) == 0 {
		if isJSONFormat(opts) {
			clicky.MustPrint(snapshotOutput, opts)
			return
		}
		clicky.MustPrint(defaultOutput, opts)
		return
	}

	for _, sink := range opts.Sinks {
		sinkOpts := opts
		sinkOpts.Sinks = nil
		sinkOpts.Format = sink.Format
		sinkOpts.JSON, sinkOpts.YAML, sinkOpts.CSV = false, false, false
		sinkOpts.HTML, sinkOpts.Markdown, sinkOpts.Pretty = false, false, false
		sinkOpts.PDF, sinkOpts.Slack = false, false

		out := any(defaultOutput)
		if strings.EqualFold(sink.Format, "json") {
			out = snapshotOutput
		}
		if sink.File == "" {
			clicky.MustPrint(out, sinkOpts)
			continue
		}
		if err := clicky.FormatToFile(out, sinkOpts, sink.File); err != nil {
			logger.Errorf("failed to write %s output to %s: %v", sink.Format, sink.File, err)
		}
	}
}

func isJSONFormat(opts clicky.FormatOptions) bool {
	return strings.EqualFold(opts.ResolveFormat(), "json")
}

func buildScrapeSpec(scraperConfigs []v1.ScrapeConfig) any {
	specs := make([]any, len(scraperConfigs))
	for i, sc := range scraperConfigs {
		specs[i] = sc.Spec
	}
	if len(specs) == 1 {
		return specs[0]
	}
	return specs
}

func singleSnapshotPair(snapshots map[string]*v1.ScrapeSnapshotPair) *v1.ScrapeSnapshotPair {
	for _, pair := range snapshots {
		return pair
	}
	return nil
}

func startedAtMillis(progress []scrapeui.ScraperProgress) int64 {
	var earliest *time.Time
	for i := range progress {
		if progress[i].StartedAt == nil {
			continue
		}
		if earliest == nil || progress[i].StartedAt.Before(*earliest) {
			earliest = progress[i].StartedAt
		}
	}
	if earliest == nil {
		return time.Now().UnixMilli()
	}
	return earliest.UnixMilli()
}

func exportResource(resource v1.ScrapeResult, outputDir string) error {
	if resource.Config == nil && resource.AnalysisResult != nil {
		// logger.Debugf("%s/%s => %s", resource.Type, resource.ID, *resource.AnalysisResult)
		return nil
	}

	for _, change := range resource.Changes {
		outputPath := path.Join(outputDir, "changes", change.ExternalChangeID+".json")
		_ = os.MkdirAll(path.Dir(outputPath), 0755)

		data, err := db.NormalizeJSONOj(change)
		if err != nil {
			return err
		}
		// logger.Debugf("Exporting %s (%dkb)", outputPath, len(data))
		if err := os.WriteFile(outputPath, []byte(data), 0644); err != nil {
			return err
		}
	}

	if resource.Name == "" {
		return nil
	}

	outputPath := path.Join(outputDir, resource.Type, resource.Name+"-"+resource.ID[0:5]+".json")
	_ = os.MkdirAll(path.Dir(outputPath), 0755)
	data, err := db.NormalizeJSON(resource)
	if err != nil {
		return err
	}

	// logger.Debugf("Exporting %s (%dkb)", outputPath, len(data)/1024)
	return os.WriteFile(outputPath, []byte(data), 0644)
}

// ensureScraper looks up an existing scraper by name, or creates one with a
// deterministic ID so that --save mode has a valid scraper_id for FK constraints.
func ensureScraper(ctx context.Context, sc *v1.ScrapeConfig) error {
	name := sc.Name
	if name == "" {
		name = "cli-scraper"
	}

	var existing models.ConfigScraper
	err := ctx.DB().Where("name = ? AND deleted_at IS NULL", name).First(&existing).Error
	if err == nil {
		sc.ObjectMeta.UID = types.UID(existing.ID.String())
		return nil
	}
	if err != gorm.ErrRecordNotFound {
		return fmt.Errorf("failed to lookup scraper: %w", err)
	}

	id, err := hash.DeterministicUUID(pq.StringArray{name})
	if err != nil {
		return fmt.Errorf("failed to generate scraper id: %w", err)
	}

	specJSON, err := json.Marshal(sc.Spec)
	if err != nil {
		return fmt.Errorf("failed to marshal spec: %w", err)
	}

	if err := ctx.DB().Exec(`
		INSERT INTO config_scrapers (id, name, spec, source)
		VALUES (?, ?, ?, 'ConfigFile')
		ON CONFLICT (id) DO UPDATE SET
			name = EXCLUDED.name, spec = EXCLUDED.spec, deleted_at = NULL
	`, id, name, string(specJSON)).Error; err != nil {
		return fmt.Errorf("failed to create scraper: %w", err)
	}

	sc.ObjectMeta = metav1.ObjectMeta{
		Name: name,
		UID:  types.UID(id.String()),
	}
	logger.Infof("Created scraper %s with id %s", name, id)
	return nil
}

func startScrapeUI(scraperNames []string, scrapeSpec any, logBuf *bytes.Buffer) *scrapeui.Server {
	srv := scrapeui.NewServer(scraperNames, scrapeSpec, logBuf)
	bi := GetBuildInfo()
	srv.SetBuildInfo(scrapeui.BuildInfo{Version: bi.Version, Commit: bi.Commit, Date: bi.Date})
	addr := fmt.Sprintf("localhost:%d", uiPort)
	listener, err := net.Listen("tcp", addr)
	if err != nil && uiPort != 0 {
		logger.Warnf("Port %d in use, picking a free port", uiPort)
		listener, err = net.Listen("tcp", "localhost:0")
	}
	if err != nil {
		logger.Errorf("Failed to start scrape UI server: %v", err)
		return nil
	}
	port := listener.Addr().(*net.TCPAddr).Port
	url := fmt.Sprintf("http://localhost:%d", port)

	go http.Serve(listener, srv.Handler()) //nolint:errcheck

	time.Sleep(100 * time.Millisecond)
	logger.Infof("Scrape UI at %s", url)
	openBrowser(url)
	return srv
}

func openBrowser(url string) {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "windows":
		cmd = "cmd"
		args = []string{"/c", "start"}
	case "darwin":
		cmd = "open"
	default:
		cmd = "xdg-open"
	}
	args = append(args, url)
	_ = exec.Command(cmd, args...).Start()
}

func init() {
	Run.Flags().BoolVar(&save, "save", false, "Save scraped configurations to the database")
	Run.Flags().BoolVar(&export, "export", true, "Export scraped configurations to files in the output directory and/or pretty print them")
	Run.Flags().StringVarP(&outputDir, "output-dir", "o", "", "The output folder for configurations")
	Run.Flags().IntVar(&debugPort, "debug-port", -1, "Start an HTTP server to use the /debug routes, Use -1 to disable and 0 to pick a free port")
	Run.Flags().BoolVar(&uiEnabled, "ui", false, "Open a browser dashboard showing real-time scrape progress")
	Run.Flags().IntVar(&uiPort, "ui-port", 9001, "Port for the UI server (0 to pick a free port)")
	clicky.BindAllFlags(Run.Flags())
}
