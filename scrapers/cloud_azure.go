//go:build !slim

package scrapers

import "github.com/flanksource/config-db/scrapers/azure"

func init() {
	All = append(All, azure.Scraper{})
}
