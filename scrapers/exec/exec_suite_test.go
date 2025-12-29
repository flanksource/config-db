package exec

import (
	"fmt"
	"os"
	"testing"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
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

	// Change to repository root so fixtures/ is accessible
	if err := os.Chdir("../.."); err != nil {
		Fail(err.Error())
	}
})

var _ = AfterSuite(func() {
	setup.AfterSuiteFn()
})

// getConfigSpec loads a scraper config from the fixtures directory
func getConfigSpec(name string) v1.ScrapeConfig {
	configs, err := v1.ParseConfigs("fixtures/" + name + ".yaml")
	if err != nil {
		Fail(fmt.Sprintf("Failed to parse config: %v", err))
	}
	return configs[0]
}
