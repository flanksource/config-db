package cmd

import (
	"context"
	"encoding/json"
	"io/ioutil"
	"os"
	"path"

	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/confighub/api/v1"
	"github.com/flanksource/confighub/db"
	fs "github.com/flanksource/confighub/filesystem"
	"github.com/flanksource/confighub/scrapers"
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
		scraperConfigs, err := getConfigs(configFiles)
		if err != nil {
			logger.Fatalf(err.Error())
		}

		ctx := v1.ScrapeContext{Context: context.Background(), Kommons: kommonsClient}

		manager := v1.Manager{
			Finder: fs.NewFileFinder(),
		}

		results, err := scrapers.Run(ctx, manager, scraperConfigs...)
		if err != nil {
			logger.Fatalf(err.Error())
		}
		logger.Infof("Found %d resources", len(results))

		if db.ConnectionString != "" {
			db.MustInit()
			if err = db.Update(ctx, results); err != nil {
				logger.Errorf("Failed to update db: %+v", err)
			}
		} else if outputDir != "" {
			for _, result := range results {
				if err := exportResource(result, filename, outputDir); err != nil {
					logger.Fatalf("failed to export results %v", err)
				}
			}

		} else {
			logger.Infof("skipping export: neither --output-dir or --db is specified")
		}

	},
}

func exportResource(resource v1.ScrapeResult, filename, outputDir string) error {
	outputPath := path.Join(outputDir, resource.Type, resource.Name+".json")
	_ = os.MkdirAll(path.Dir(outputPath), 0755)
	data, err := json.MarshalIndent(resource, "", "  ")
	if err != nil {
		return err
	}

	logger.Debugf("Exporting %s", outputPath)
	return ioutil.WriteFile(outputPath, data, 0644)
}

func init() {
	Run.Flags().StringVarP(&outputDir, "output-dir", "o", "configs", "The output folder for configurations")
	Run.Flags().StringVarP(&filename, "filename", "f", ".id", "The filename to save seach resource under")
}
