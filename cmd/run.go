package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path"

	"github.com/antonmedv/expr"
	"github.com/flanksource/commons/logger"
	"github.com/flanksource/commons/text"
	v1 "github.com/flanksource/confighub/api/v1"
	"github.com/flanksource/confighub/db"
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

		results, err := scrapers.Run(ctx, scraperConfigs...)
		if err != nil {
			logger.Fatalf(err.Error())
		}

		if db.ConnectionString != "" {
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
	_ = os.MkdirAll(path.Join(outputDir, resource.Type), 0755)
	data, err := json.MarshalIndent(resource, "", "  ")
	if err != nil {
		return err
	}

	var _data map[string]interface{}
	if err := json.Unmarshal(data, &_data); err != nil {
		return err
	}

	program, err := expr.Compile(filename, text.MakeExpressionOptions(_data)...)
	if err != nil {
		return err
	}
	output, err := expr.Run(program, text.MakeExpressionEnvs(_data))
	if err != nil {
		return err
	}
	outputPath := path.Join(outputDir, resource.Type, fmt.Sprint(output)+".json")

	logger.Debugf("Exporting %s", outputPath)
	return ioutil.WriteFile(outputPath, data, 0644)
}

func init() {
	Run.Flags().StringVarP(&outputDir, "output-dir", "o", "configs", "The outpul folder for configurations")
	Run.Flags().StringVarP(&filename, "filename", "f", "id", "The filename to save seach resource under")
}
