//go:build !slim

package scrapers

import "github.com/flanksource/config-db/scrapers/gcp"

func init() {
	All = append(All, gcp.Scraper{})
}
