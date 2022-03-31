package file

import (
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

	instanceID := gjson.Get(config.File, "Config.InstanceId")
	instanceType := gjson.Get(config.File, "Config.InstanceType")

	if !(instanceID.Exists() && instanceType.Exists()) {
		return results
	}

	logger.Infof("Scraping JSON file id=%s =%s", instanceID.String(), instanceType.String())

	results = append(results, v1.ScrapeResult{
		Config: config.File,
		Type:   instanceID.String(),
		Id:     instanceType.String()})
	return results

}
