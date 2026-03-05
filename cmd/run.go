package cmd

import (
	"bytes"
	gocontext "context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"time"

	"github.com/flanksource/clicky"
	clickyapi "github.com/flanksource/clicky/api"
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
	"github.com/labstack/echo/v4"
	"github.com/spf13/cobra"
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

		clicky.Flags.UseFlags()

		logger.Use(os.Stderr)
		logger.Infof("Scraping %v", configFiles)
		scraperConfigs, err := v1.ParseConfigs(configFiles...)
		if err != nil {
			logger.Fatalf(err.Error())
		}

		dutyCtx := context.New()
		if dutyapi.DefaultConfig.ConnectionString != "" {
			c, _, err := duty.Start(app, duty.ClientOnly)
			if err != nil {
				logger.Fatalf("Failed to initialize db: %v", err.Error())
			}

			dutyCtx = c
			db.WarmExternalEntityCaches(dutyCtx)
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

		var hasErrors bool
		for i := range scraperConfigs {
			ctx, cancel, cancelTimeout := api.NewScrapeContext(dutyCtx).WithScrapeConfig(&scraperConfigs[i]).
				WithTimeout(dutyCtx.Properties().Duration("scraper.timeout", 4*time.Hour))
			defer cancelTimeout()
			shutdown.AddHook(func() { defer cancel() })
			if err := scrapeAndStore(ctx); err != nil {
				hasErrors = true
				logger.Errorf("error scraping config: (name=%s) %+v", scraperConfigs[i].Name, err)
			}
		}
		if hasErrors {
			os.Exit(1)
		}
	},
}

// runHTMLOutput wraps scrape results for HTML rendering.
// Uses pretty:"table" tags to prevent empty slices from appearing as broken summary entries.
type runHTMLOutput struct {
	Counts             clickyapi.Textable             `json:"-"`
	Configs            []v1.ScrapeResult              `pretty:"table"`
	Analysis           []models.ConfigAnalysis        `pretty:"table"`
	Changes            []models.ConfigChange          `pretty:"table"`
	Relationships      []models.ConfigRelationship    `pretty:"table"`
	ExternalRoles      []models.ExternalRole          `pretty:"table"`
	ExternalUsers      []models.ExternalUser          `pretty:"table"`
	ExternalGroups     []models.ExternalGroup         `pretty:"table"`
	ExternalUserGroups []models.ExternalUserGroup     `pretty:"table"`
	ConfigAccess       []v1.ExternalConfigAccess      `pretty:"table"`
	ConfigAccessLogs   []v1.ExternalConfigAccessLog   `pretty:"table"`
	Logs               clickyapi.Textable             `json:"-"`
}

func scrapeAndStore(ctx api.ScrapeContext) error {
	ctx, err := ctx.InitTempCache()
	if err != nil {
		return err
	}

	// Capture logs for HTML output by teeing to both stderr and buffer.
	// Must also update ctx.Logger since the context holds its own logger reference.
	var logBuf bytes.Buffer
	logger.Use(io.MultiWriter(os.Stderr, &logBuf))
	ctx.Logger = logger.StandardLogger()

	timer := timer.NewMemoryTimer()
	results, err := scrapers.Run(ctx)
	if err != nil {
		logger.Use(os.Stderr)
		return err
	}

	scrapeResults := v1.ScrapeResults(results)
	if scrapeResults.HasErr() {
		for _, e := range scrapeResults.Errors() {
			logger.Errorf("scrape error: %s", e)
		}
		logger.Use(os.Stderr)
		return fmt.Errorf("scrape completed with %d error(s)", len(scrapeResults.Errors()))
	}

	var all = v1.MergeScrapeResults(results)
	logger.Infof("Scraped %d resources (%s)", len(results), timer.End())

	// Restore stderr-only logging before rendering
	logger.Use(os.Stderr)

	if outputDir != "" {
		for _, result := range results {
			if err := exportResource(result, outputDir); err != nil {
				return fmt.Errorf("failed to export results: %w", err)
			}
		}
		logger.Infof("Exported %d resources to %s (%s)", len(results), outputDir, timer.End())

	} else {
		output := runHTMLOutput{
			Counts: v1.BuildCountsSummary(all),
			Configs:            all.Configs,
			Analysis:           all.Analysis,
			Changes:            all.Changes,
			Relationships:      all.Relationships,
			ExternalRoles:      all.ExternalRoles,
			ExternalUsers:      all.ExternalUsers,
			ExternalGroups:     all.ExternalGroups,
			ExternalUserGroups: all.ExternalUserGroups,
			ConfigAccess:       all.ConfigAccess,
			ConfigAccessLogs:   all.ConfigAccessLogs,
			Logs:               v1.BuildLogOutput(logBuf.String()),
		}
		clicky.MustPrint(output)
	}

	if save && dutyapi.DefaultConfig.ConnectionString != "" {

		summary, err := db.SaveResults(ctx, results)
		if err != nil {
			return fmt.Errorf("failed to save results to db: %w", err)
		}
		logger.Infof("Exported %d resources to DB: %s (%s)", len(results), summary.PrettyShort(), timer.End())

	}

	return nil
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

func init() {
	Run.Flags().BoolVar(&save, "save", false, "Save scraped configurations to the database")
	Run.Flags().BoolVar(&export, "export", true, "Export scraped configurations to files in the output directory and/or pretty print them")
	Run.Flags().StringVarP(&outputDir, "output-dir", "o", "", "The output folder for configurations")
	Run.Flags().IntVar(&debugPort, "debug-port", -1, "Start an HTTP server to use the /debug routes, Use -1 to disable and 0 to pick a free port")
	clicky.BindAllFlags(Run.Flags())

}
