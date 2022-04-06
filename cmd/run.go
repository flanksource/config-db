package cmd

import (
	"github.com/flanksource/commons/logger"
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

		if err := scrapers.RunScrapers(scraperConfigs, filename, outputDir); err != nil {
			logger.Fatalf(err.Error())
		}
	},
}

func init() {
	Run.Flags().StringVarP(&outputDir, "output-dir", "o", "configs", "The outpul folder for configurations")
	Run.Flags().StringVarP(&filename, "filename", "f", "id", "The filename to save seach resource under")
}
