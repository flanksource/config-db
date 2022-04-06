package scrapers

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
)

// Run ...
func Run(ctx v1.ScrapeContext, config v1.ConfigScraper) ([]v1.ScrapeResult, error) {
	results := []v1.ScrapeResult{}
	for _, scraper := range All {
		results = append(results, scraper.Scrape(ctx, config)...)
	}
	return results, nil
}

// RunScrapers ...
func RunScrapers(scraperConfigs []v1.ConfigScraper, filename, outputDir string) error {
	ctx := v1.ScrapeContext{Context: context.Background()}
	results := []v1.ScrapeResult{}
	for _, scraperConfig := range scraperConfigs {
		logger.Debugf("Scrapping %+v", scraperConfig)
		_results, err := Run(ctx, scraperConfig)
		if err != nil {
			return err
		}
		results = append(results, _results...)
	}

	for _, result := range results {
		if err := exportResource(result, filename, outputDir); err != nil {
			return err
		}
	}
	return nil
}

func exportResource(resource v1.ScrapeResult, filename, outputDir string) error {
	os.MkdirAll(path.Join(outputDir, resource.Type), 0755)
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
