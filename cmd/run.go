package cmd

import (
	"context"
	"encoding/json"
	"io/ioutil"
	"os"
	"path"

	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/confighub/api/v1"
	"github.com/flanksource/confighub/scrapers"
	"github.com/spf13/cobra"
)

var outputDir string

var Run = &cobra.Command{
	Use:   "run <scraper.yaml>",
	Short: "Run scrapers and return",
	Run: func(cmd *cobra.Command, configFiles []string) {
		logger.Infof("Scrapping %v", configFiles)
		scraperConfigs, err := getConfigs(configFiles)
		if err != nil {
			logger.Fatalf(err.Error())
		}
		ctx := v1.ScrapeContext{Context: context.Background()}
		results := []v1.ScrapeResult{}
		for _, scraperConfig := range scraperConfigs {
			for _, scraper := range scrapers.All {
				results = append(results, scraper.Scrape(ctx, scraperConfig)...)
			}
		}

		for _, result := range results {
			os.MkdirAll(path.Join(outputDir, result.Type), 0755)
			data, err := json.MarshalIndent(result, "", "  ")
			if err != nil {
				logger.Fatalf(err.Error())
			}
			if err := ioutil.WriteFile(path.Join(outputDir, result.Type, result.Id+".json"), data, 0644); err != nil {
				logger.Fatalf(err.Error())
			}
		}
	},
}

func init() {
	Run.Flags().StringVarP(&outputDir, "output-dir", "o", "configs", "The outpul folder for configurations")
}
