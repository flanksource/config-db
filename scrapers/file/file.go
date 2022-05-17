package file

import (
	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/confighub/api/v1"
	"github.com/tidwall/gjson"
)

// JSONScrapper ...
type JSONScrapper struct {
}

// Scrape ...
func (file JSONScrapper) Scrape(ctx v1.ScrapeContext, config v1.ConfigScraper, manager v1.Manager) []v1.ScrapeResult {

	results := []v1.ScrapeResult{}

	finder := manager.Finder

	for _, fileConfig := range config.File {

		logger.Infof("Scraping JSON file id=%s type=%s", fileConfig.ID, fileConfig.Type)

		globMatches := []string{}

		for _, path := range fileConfig.Glob {
			matches, err := finder.Find(path)
			if err != nil {
				logger.Tracef("could not match glob pattern(%s): %v", path, err)
				continue
			}

			globMatches = append(globMatches, matches...) // using a seperate slice to avoid nested loops and complexity
		}

		for _, match := range globMatches {
			contentByte, err := finder.Read(match)
			if err != nil {
				logger.Errorf("failed to reading matched file: %v", err)
				continue
			}

			jsonContent := string(contentByte)

			resultID := gjson.Get(jsonContent, fileConfig.ID)
			resultType := gjson.Get(jsonContent, fileConfig.Type)

			if !(resultID.Exists() && resultType.Exists()) {
				continue
			}
			results = append(results, v1.ScrapeResult{
				Config: jsonContent,
				Type:   resultType.String(),
				ID:     resultID.String(),
			})

		}

	}

	return results

}
