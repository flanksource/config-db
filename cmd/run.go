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
	"github.com/flanksource/commons/har"
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
	"github.com/flanksource/duty/shutdown"
	"github.com/labstack/echo/v4"
	"github.com/spf13/cobra"
)

var outputDir string
var debugPort int
var export bool
var save bool
var debug bool

// Run ...
var Run = &cobra.Command{
	Use:   "run <scraper.yaml>",
	Short: "Run scrapers and return",
	Run: func(cmd *cobra.Command, configFiles []string) {
		var logBuf bytes.Buffer
		var harCollector *har.Collector

		if debug {
			clicky.Flags.Level = "trace"
			harCollector = har.NewCollector(har.DefaultConfig())
		}

		clicky.Flags.UseFlags()

		if debug {
			logger.Use(io.MultiWriter(os.Stderr, &logBuf))
			// logger.Use() creates a new logger that doesn't inherit the
			// trace level set by UseFlags(), so re-apply it.
			logger.StandardLogger().SetLogLevel("trace")
		} else {
			logger.Use(os.Stderr)
		}

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
		var allResults v1.ScrapeResults
		for i := range scraperConfigs {
			scrapeCtx, cancel, cancelTimeout := api.NewScrapeContext(dutyCtx).WithScrapeConfig(&scraperConfigs[i]).
				WithTimeout(dutyCtx.Properties().Duration("scraper.timeout", 4*time.Hour))
			defer cancelTimeout()
			shutdown.AddHook(func() { defer cancel() })

			if debug {
				scrapeCtx = scrapeCtx.AsDebugRun("trace").WithHARCollector(harCollector)
			}

			results, err := scrapeAndStore(scrapeCtx)
			if err != nil {
				hasErrors = true
				logger.Errorf("error scraping config: (name=%s) %+v", scraperConfigs[i].Name, err)
			}
			allResults = append(allResults, results...)
		}

		if debug {
			writeDebugOutput(allResults, harCollector, logBuf.String())
		}

		if hasErrors {
			os.Exit(1)
		}
	},
}

func scrapeAndStore(ctx api.ScrapeContext) ([]v1.ScrapeResult, error) {
	ctx, err := ctx.InitTempCache()
	if err != nil {
		return nil, err
	}

	timer := timer.NewMemoryTimer()
	results, err := scrapers.Run(ctx)
	if err != nil {
		return nil, err
	}

	scrapeResults := v1.ScrapeResults(results)
	if scrapeResults.HasErr() {
		for _, e := range scrapeResults.Errors() {
			logger.Errorf("scrape error: %s", e)
		}
		return results, fmt.Errorf("scrape completed with %d error(s)", len(scrapeResults.Errors()))
	}

	var all = v1.MergeScrapeResults(results)
	logger.Infof("Scraped %d resources (%s)", len(results), timer.End())

	if outputDir != "" {
		for _, result := range results {
			if err := exportResource(result, outputDir); err != nil {
				return results, fmt.Errorf("failed to export results: %w", err)
			}
		}
		logger.Infof("Exported %d resources to %s (%s)", len(results), outputDir, timer.End())

	} else if !debug {
		clicky.MustPrint(all)

	}

	if save && dutyapi.DefaultConfig.ConnectionString != "" {

		summary, err := db.SaveResults(ctx, results)
		if err != nil {
			return results, fmt.Errorf("failed to save results to db: %w", err)
		}
		logger.Infof("Exported %d resources to DB: %s (%s)", len(results), summary.PrettyShort(), timer.End())

	}

	return results, nil
}

func writeDebugOutput(results v1.ScrapeResults, harCollector *har.Collector, logs string) {
	merged := v1.MergeScrapeResults(results)
	debugResult := v1.DebugResult{
		Results: merged,
		HAR:     harCollector.Entries(),
		Logs:    logs,
	}

	dir := outputDir
	if dir == "" {
		dir = "."
	}
	outputPath := path.Join(dir, "debug.html")
	if err := clicky.FormatToFile(debugResult, clicky.FormatOptions{HTML: true}, outputPath); err != nil {
		logger.Errorf("failed to write debug output: %v", err)
		return
	}
	logger.Infof("Debug output written to %s", outputPath)
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
	Run.Flags().BoolVar(&debug, "debug", false, "Capture all logs, HAR traffic, and output an HTML debug report")
	clicky.BindAllFlags(Run.Flags())
}
