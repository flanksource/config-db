package cmd

import (
	"context"
	"encoding/json"
	"os"
	"path"

	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
	"github.com/flanksource/config-db/scrapers"
	"github.com/spf13/cobra"
)

var outputDir string
var filename string

// Run ...
var Run = &cobra.Command{
	Use:   "run <scraper.yaml>",
	Short: "Run scrapers and return",
	Run: func(cmd *cobra.Command, configFiles []string) {

		logger.Infof("Scrapping %v", configFiles)
		scraperConfigs, err := v1.ParseConfigs(configFiles...)
		if err != nil {
			logger.Fatalf(err.Error())
		}

		ctx := &v1.ScrapeContext{Context: context.Background(), Kommons: kommonsClient}

		if db.ConnectionString != "" {
			db.MustInit()
		}
		results, err := scrapers.Run(ctx, scraperConfigs...)
		if err != nil {
			logger.Fatalf(err.Error())
		}
		if db.ConnectionString != "" {
			logger.Infof("Exporting %d resources to DB", len(results))
			if err = db.Update(ctx, results); err != nil {
				logger.Errorf("Failed to update db: %+v", err)
			}
		} else if outputDir != "" {
			logger.Infof("Exporting %d resources to %s", len(results), outputDir)

			for _, result := range results {
				if err := exportResource(result, filename, outputDir); err != nil {
					logger.Fatalf("failed to export results %v", err)
				}
			}

		} else {
			logger.Fatalf("skipping export: neither --output-dir or --db is specified")
		}

	},
}

func exportResource(resource v1.ScrapeResult, filename, outputDir string) error {
	if resource.Config == nil && resource.AnalysisResult != nil {
		logger.Debugf("%s/%s => %s", resource.ExternalType, resource.ID, *resource.AnalysisResult)
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
