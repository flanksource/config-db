package file

import (
	"os"
	"path/filepath"

	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/confighub/api/v1"
	"github.com/tidwall/gjson"
)

// FScrapper ...
type FScrapper struct {
}

// Scrape ...
func (file FScrapper) Scrape(ctx v1.ScrapeContext, config v1.ConfigScraper) []v1.ScrapeResult {

	results := []v1.ScrapeResult{}

	for _, fileConfig := range config.File {

		logger.Infof("Scraping JSON file id=%s type=%s", fileConfig.ID, fileConfig.Type)

		globMatches := []string{}

		for _, path := range fileConfig.Glob {
			matches, err := filepath.Glob(path)
			if err != nil {
				logger.Errorf("failed to match glob patter: %v", err)
				continue
			}

			globMatches = append(globMatches, matches...) // using a seperate slice to avoid nested loops and complexity
		}

		for _, match := range globMatches {
			contentByte, err := os.ReadFile(match)
			if err != nil {
				logger.Errorf("failed to reading matched file: %v", err)
				continue
			}

			jsonContent := string(contentByte)

			instanceID := gjson.Get(jsonContent, "Config.InstanceId")
			instanceType := gjson.Get(jsonContent, "Config.InstanceType")

			if !(instanceID.Exists() && instanceType.Exists()) {
				return results
			}
			results = append(results, v1.ScrapeResult{
				Config: jsonContent,
				Type:   instanceID.String(),
				Id:     instanceType.String(),
			})

		}

	}

	return results

}
