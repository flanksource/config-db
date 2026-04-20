package cmd

import (
	"bytes"
	gocontext "context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"time"

	"github.com/flanksource/clicky"
	clickyapi "github.com/flanksource/clicky/api"
	"github.com/flanksource/commons/har"
	"github.com/flanksource/commons/hash"
	"github.com/flanksource/commons/logger"
	"github.com/flanksource/commons/timer"
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
	"github.com/flanksource/config-db/scrapers"
	"github.com/flanksource/duty"
	dutyapi "github.com/flanksource/duty/api"
	"github.com/flanksource/duty/context"
	dutyEcho "github.com/flanksource/duty/echo"
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

// Run ...
var Run = &cobra.Command{
	Use:   "run <scraper.yaml>",
	Short: "Run scrapers and return",
	Run: func(cmd *cobra.Command, configFiles []string) {
		var logBuf bytes.Buffer
		var harCollector *har.Collector

		if logger.IsTraceEnabled() {
			logger.Tracef("Enabling HAR collection")
			harCollector = har.NewCollector(har.DefaultConfig())
		}

		clicky.Flags.UseFlags()

		// Capture all logs by teeing stderr to a buffer.
		// Must happen BEFORE context.New() so contexts inherit the multiwriter logger.
		logger.Use(io.MultiWriter(os.Stderr, &logBuf))
		// logger.Use() creates a fresh logger that doesn't inherit the level
		// set by UseFlags(), so re-apply trace to capture everything.
		logger.StandardLogger().SetLogLevel("trace")

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

		var hasErrors bool
		var allResults v1.ScrapeResults
		var lastSummary *v1.ScrapeSummary
		for i := range scraperConfigs {
			scrapeCtx, cancel, cancelTimeout := api.NewScrapeContext(dutyCtx).WithScrapeConfig(&scraperConfigs[i]).
				WithTimeout(dutyCtx.Properties().Duration("scraper.timeout", 4*time.Hour))
			defer cancelTimeout()
			shutdown.AddHook(func() { defer cancel() })

			scrapeCtx = scrapeCtx.WithHARCollector(harCollector)

			results, summary, err := scrapeAndStore(scrapeCtx)
			if err != nil {
				hasErrors = true
				logger.Errorf("error scraping config: (name=%s) %+v", scraperConfigs[i].Name, err)
			}
			allResults = append(allResults, results...)
			if summary != nil {
				lastSummary = summary
			}
		}

		// Restore stderr-only logging before rendering
		logger.Use(os.Stderr)

		printOutput(allResults, lastSummary, harCollector, logBuf.String())

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
	Configs            []v1.ScrapeResult            `pretty:"table"`
	Changes            []changeWithScreenshot       `pretty:"table"`
	Artifacts          []models.Artifact            `pretty:"table"`
	Analysis           []models.ConfigAnalysis      `pretty:"table"`
	Relationships      []models.ConfigRelationship  `pretty:"table"`
	ExternalRoles      []models.ExternalRole        `pretty:"table"`
	ExternalUsers      []models.ExternalUser        `pretty:"table"`
	ExternalGroups     []models.ExternalGroup       `pretty:"table"`
	ExternalUserGroups []v1.ExternalUserGroup       `pretty:"table"`
	ConfigAccess       []v1.ExternalConfigAccess    `pretty:"table"`
	ConfigAccessLogs   []v1.ExternalConfigAccessLog `pretty:"table"`
	Logs               v1.LogOutput                 `json:"-"`
	HTTPTraffic        []har.Entry                  `json:"har,omitempty"`
}

func scrapeAndStore(ctx api.ScrapeContext) ([]v1.ScrapeResult, *v1.ScrapeSummary, error) {
	ctx, err := ctx.InitTempCache()
	if err != nil {
		return nil, nil, err
	}

	timer := timer.NewMemoryTimer()
	results, err := scrapers.Run(ctx)
	if err != nil {
		return nil, nil, err
	}

	scrapeResults := v1.ScrapeResults(results)
	if scrapeResults.HasErr() {
		for _, e := range scrapeResults.Errors() {
			logger.Errorf("scrape error: %s", e)
		}
		return results, nil, fmt.Errorf("scrape completed with %d error(s)", len(scrapeResults.Errors()))
	}

	logger.Infof("Scraped %d resources (%s)", len(results), timer.End())

	if outputDir != "" {
		for _, result := range results {
			if err := exportResource(result, outputDir); err != nil {
				return results, nil, fmt.Errorf("failed to export results: %w", err)
			}
		}
		logger.Infof("Exported %d resources to %s (%s)", len(results), outputDir, timer.End())
	}

	if save && dutyapi.DefaultConfig.ConnectionString != "" {
		summary, err := db.SaveResults(ctx, results)
		if err != nil {
			return results, nil, fmt.Errorf("failed to save results to db: %w", err)
		}
		logger.Infof("Exported %d resources to DB: %s (%s)", len(results), summary.PrettyShort(), timer.End())
		return results, &summary, nil
	}

	return results, nil, nil
}

type changeWithScreenshot struct {
	models.ConfigChange
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
	row := c.ConfigChange.Row()
	row["ConfigType"] = clicky.Text(c.ConfigType)
	row["ExternalID"] = clicky.Text(c.ExternalID)
	return row
}

func (c changeWithScreenshot) RowDetail() clickyapi.Textable {
	base := c.ConfigChange.RowDetail()

	if c.Details == nil {
		return base
	}
	var details map[string]any
	if err := json.Unmarshal(c.Details, &details); err != nil {
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
	if base != nil {
		return screenshotDetail{html: base.HTML() + imgs}
	}
	return screenshotDetail{html: imgs}
}

type screenshotDetail struct{ html string }

func (s screenshotDetail) String() string     { return "[screenshot]" }
func (s screenshotDetail) ANSI() string       { return "[screenshot]" }
func (s screenshotDetail) HTML() string       { return s.html }
func (s screenshotDetail) Markdown() string   { return "[screenshot]" }
func (s screenshotDetail) StaticHTML() string { return s.html }

func printOutput(results v1.ScrapeResults, summary *v1.ScrapeSummary, harCollector *har.Collector, logs string) {
	if outputDir != "" {
		return
	}

	all := v1.MergeScrapeResults(results)
	var changes []changeWithScreenshot
	var artifacts []models.Artifact
	for _, c := range all.Changes {
		changes = append(changes, changeWithScreenshot{c})
		if c.Details == nil {
			continue
		}
		var details map[string]any
		if err := json.Unmarshal(c.Details, &details); err != nil {
			continue
		}

		var artMaps []map[string]any
		if arr, ok := details["artifacts"].([]any); ok {
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

	output := runHTMLOutput{
		Counts:             v1.BuildCounts(all),
		Configs:            all.Configs,
		Changes:            changes,
		Artifacts:          artifacts,
		Analysis:           all.Analysis,
		Relationships:      all.Relationships,
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
	clicky.MustPrint(output)
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

func init() {
	Run.Flags().BoolVar(&save, "save", false, "Save scraped configurations to the database")
	Run.Flags().BoolVar(&export, "export", true, "Export scraped configurations to files in the output directory and/or pretty print them")
	Run.Flags().StringVarP(&outputDir, "output-dir", "o", "", "The output folder for configurations")
	Run.Flags().IntVar(&debugPort, "debug-port", -1, "Start an HTTP server to use the /debug routes, Use -1 to disable and 0 to pick a free port")
	clicky.BindAllFlags(Run.Flags())

}
