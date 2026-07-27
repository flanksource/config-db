package scrapers

import (
	"errors"
	"fmt"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
)

func RunDoctors(ctx api.ScrapeContext) (v1.DoctorResults, error) {
	return runDoctors(ctx, All)
}

func runDoctors(ctx api.ScrapeContext, registry []api.Scraper) (v1.DoctorResults, error) {
	if ctx.ScrapeConfig() == nil {
		return nil, fmt.Errorf("doctor requires a scrape config")
	}

	var (
		results   v1.DoctorResults
		doctorErr error
		supported bool
	)

	for _, scraper := range registry {
		if !scraper.CanScrape(ctx.ScrapeConfig().Spec) {
			continue
		}

		doctor, ok := scraper.(api.Doctor)
		if !ok {
			continue
		}

		supported = true
		checks, err := doctor.Doctor(ctx)
		results = append(results, checks...)
		doctorErr = errors.Join(doctorErr, err)
	}

	if !supported {
		return nil, fmt.Errorf("matching scraper does not support doctor checks")
	}

	return results, doctorErr
}
