package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path"

	"github.com/flanksource/commons/logger"
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
	"github.com/flanksource/config-db/scrapers"
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
		if db.ConnectionString != "" {
			dutyCtx = db.MustInit()
		}
		api.DefaultContext = api.NewScrapeContext(dutyCtx)

		if db.ConnectionString == "" && outputDir == "" {
			logger.Fatalf("skipping export: neither --output-dir nor --db is specified")
		}

		for i := range scraperConfigs {
			ctx := api.DefaultContext.WithScrapeConfig(&scraperConfigs[i])
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

	results, err := scrapers.Run(ctx)
	if err != nil {
		return err
	}
	if db.ConnectionString != "" {
		logger.Infof("Exporting %d resources to DB", len(results))
		return db.SaveResults(ctx, results)
	}

	if outputDir != "" {
		logger.Infof("Exporting %d resources to %s", len(results), outputDir)

		for _, result := range results {
			if err := exportResource(result, filename, outputDir); err != nil {
				return fmt.Errorf("failed to export results %v", err)
			}
		}
	}

	return nil
}

func exportResource(resource v1.ScrapeResult, filename, outputDir string) error {
	if resource.Config == nil && resource.AnalysisResult != nil {
		logger.Debugf("%s/%s => %s", resource.Type, resource.ID, *resource.AnalysisResult)
		return nil
	}
	outputPath := path.Join(outputDir, resource.Type, resource.Name+".json")
	_ = os.MkdirAll(path.Dir(outputPath), 0755)
	data, err := json.MarshalIndent(resource, "", "  ")
	if err != nil {
		return err
	}

	logger.Debugf("Exporting %s", outputPath)
	return os.WriteFile(outputPath, data, 0644)
}

func init() {
	Run.Flags().StringVarP(&outputDir, "output-dir", "o", "configs", "The output folder for configurations")
	Run.Flags().StringVarP(&filename, "filename", "f", ".id", "The filename to save seach resource under")
}
