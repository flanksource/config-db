package cmd

import (
	"fmt"
	"os"
	"os/signal"
	"path"
	"time"

	"github.com/flanksource/commons/logger"
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
	"github.com/flanksource/config-db/scrapers"
	"github.com/flanksource/config-db/utils"
	"github.com/flanksource/duty"
	dutyapi "github.com/flanksource/duty/api"
	"github.com/flanksource/duty/context"
	"github.com/spf13/cobra"
)

var outputDir string
var filename string

// Run ...
var Run = &cobra.Command{
	Use:   "run <scraper.yaml>",
	Short: "Run scrapers and return",
	Run: func(cmd *cobra.Command, configFiles []string) {
		logger.Infof("Scraping %v", configFiles)
		scraperConfigs, err := v1.ParseConfigs(configFiles...)
		if err != nil {
			logger.Fatalf(err.Error())
		}

		dutyCtx := context.New()
		if dutyapi.DefaultConfig.ConnectionString != "" {
			c, _, err := duty.Start("config-db", duty.ClientOnly)
			if err != nil {
				logger.Fatalf("Failed to initialize db: %v", err.Error())
			}

			dutyCtx = c
		}

		api.DefaultContext = api.NewScrapeContext(dutyCtx)

		if dutyapi.DefaultConfig.ConnectionString == "" && outputDir == "" {
			logger.Fatalf("skipping export: neither --output-dir nor --db is specified")
		}

		go func() {
			quit := make(chan os.Signal, 1)
			signal.Notify(quit, os.Interrupt)
			<-quit
			logger.Infof("Caught Ctrl+C")
			// call shutdown hooks explicitly, post-run cleanup hooks will be a no-op
			Shutdown()
		}()

		for i := range scraperConfigs {
			ctx, cancel, cancelTimeout := api.DefaultContext.WithScrapeConfig(&scraperConfigs[i]).
				WithTimeout(api.DefaultContext.Properties().Duration("scraper.timeout", 4*time.Hour))
			defer cancelTimeout()
			shutdownHooks = append(shutdownHooks, cancel)
			if err := scrapeAndStore(ctx); err != nil {
				logger.Errorf("error scraping config: (name=%s) %v", scraperConfigs[i].Name, err)
			}
		}
	},
}

func scrapeAndStore(ctx api.ScrapeContext) error {
	ctx, err := ctx.InitTempCache()
	if err != nil {
		return err
	}

	timer := utils.NewMemoryTimer()
	results, err := scrapers.Run(ctx)
	if err != nil {
		return err
	}
	logger.Infof("Scraped %d resources (%s)", len(results), timer.End())
	if dutyapi.DefaultConfig.ConnectionString != "" && outputDir == "" {

		summary, err := db.SaveResults(ctx, results)
		logger.Infof("Exported %d resources to DB (%s)", len(results), timer.End())

		fmt.Println(logger.Pretty(summary))

		return err
	}

	if outputDir != "" {

		for _, result := range results {
			if err := exportResource(result, filename, outputDir); err != nil {
				return fmt.Errorf("failed to export results %v", err)
			}
		}
		logger.Infof("Exported %d resources to %s (%s)", len(results), outputDir, timer.End())

	}

	return nil
}

func exportResource(resource v1.ScrapeResult, filename, outputDir string) error {
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
	Run.Flags().StringVarP(&outputDir, "output-dir", "o", "", "The output folder for configurations")
	Run.Flags().StringVarP(&filename, "filename", "f", ".id", "The filename to save seach resource under")
}
