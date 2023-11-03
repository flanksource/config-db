package scrapers

import (
	"context"
	"os"
	"testing"

	epg "github.com/fergusstrange/embedded-postgres"
	"github.com/flanksource/commons/logger"
	"github.com/flanksource/config-db/db"
	"github.com/flanksource/duty"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestRunScrapers(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Scrapers Suite")
}

var postgres *epg.EmbeddedPostgres

const (
	pgUrl  = "postgres://postgres:postgres@localhost:9876/test?sslmode=disable"
	pgPort = 9876
)

var _ = BeforeSuite(func() {
	postgres = epg.NewDatabase(epg.DefaultConfig().Database("test").Port(pgPort))
	if err := postgres.Start(); err != nil {
		Fail(err.Error())
	}

	logger.Infof("Started postgres on port %d", pgPort)
	if _, err := duty.NewDB(pgUrl); err != nil {
		Fail(err.Error())
	}
	if err := db.Init(context.Background(), pgUrl); err != nil {
		Fail(err.Error())
	}

	if err := os.Chdir(".."); err != nil {
		Fail(err.Error())
	}
})

var _ = AfterSuite(func() {
	logger.Infof("Stopping postgres")
	if err := postgres.Stop(); err != nil {
		Fail(err.Error())
	}
})
