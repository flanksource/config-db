package exec

import (
	"testing"

	"github.com/flanksource/config-db/api"
	dutycontext "github.com/flanksource/duty/context"
	"github.com/flanksource/duty/tests/setup"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestExecScraper(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Exec Scraper Suite")
}

var (
	DefaultContext dutycontext.Context
	ctx            api.ScrapeContext
)

var _ = BeforeSuite(func() {
	DefaultContext = setup.BeforeSuiteFn(setup.WithoutDummyData)
	ctx = api.NewScrapeContext(DefaultContext)
})

var _ = AfterSuite(func() {
	setup.AfterSuiteFn()
})
