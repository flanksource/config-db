//go:build !slim

package scrapers

import "github.com/flanksource/config-db/scrapers/aws"

func init() {
	All = append(All, aws.Scraper{}, aws.CostScraper{})
}
