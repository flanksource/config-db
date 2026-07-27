package scrapers

import (
	"errors"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	dutycontext "github.com/flanksource/duty/context"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type testScraper struct {
	doctorResults v1.DoctorResults
	doctorErr     error
	supported     bool
}

func (t testScraper) CanScrape(v1.ScraperSpec) bool {
	return t.supported
}

func (t testScraper) Scrape(api.ScrapeContext) v1.ScrapeResults {
	return nil
}

func (t testScraper) Doctor(api.ScrapeContext) (v1.DoctorResults, error) {
	return t.doctorResults, t.doctorErr
}

type testScrapeOnly struct{}

func (testScrapeOnly) CanScrape(v1.ScraperSpec) bool {
	return true
}

func (testScrapeOnly) Scrape(api.ScrapeContext) v1.ScrapeResults {
	return nil
}

var _ = Describe("runDoctors", func() {
	It("aggregates matching doctor implementations and their errors", func() {
		expectedErr := errors.New("probe failed")
		ctx := api.NewScrapeContext(dutycontext.New()).WithScrapeConfig(&v1.ScrapeConfig{})
		results, err := runDoctors(ctx, []api.Scraper{
			testScraper{
				supported: true,
				doctorResults: v1.DoctorResults{{
					Scraper: "github",
					Status:  v1.DoctorStatusFail,
				}},
				doctorErr: expectedErr,
			},
			testScraper{
				supported: false,
				doctorResults: v1.DoctorResults{{
					Scraper: "ignored",
					Status:  v1.DoctorStatusPass,
				}},
			},
		})

		Expect(results).To(HaveLen(1))
		Expect(err).To(MatchError(ContainSubstring(expectedErr.Error())))
	})

	It("fails when the matching scraper has no doctor implementation", func() {
		ctx := api.NewScrapeContext(dutycontext.New()).WithScrapeConfig(&v1.ScrapeConfig{})
		_, err := runDoctors(ctx, []api.Scraper{testScrapeOnly{}})

		Expect(err).To(MatchError(ContainSubstring("does not support doctor checks")))
	})
})
