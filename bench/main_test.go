package bench

import (
	"fmt"
	"os"
	"sync"
	"testing"

	v1 "github.com/flanksource/config-db/api/v1"
	dutyContext "github.com/flanksource/duty/context"
	dutyModels "github.com/flanksource/duty/models"
	"github.com/flanksource/duty/tests/setup"
	"github.com/google/uuid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sTypes "k8s.io/apimachinery/pkg/types"
)

var (
	testCtx dutyContext.Context

	benchScrapeConfig    v1.ScrapeConfig
	benchScrapeConfigErr error
	benchScrapeConfigMu  sync.Once
)

func TestMain(m *testing.M) {
	var err error
	testCtx, err = setup.SetupDB("config_db_bench", setup.WithoutDummyData, setup.WithoutRLS)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to setup benchmark db: %v\n", err)
		os.Exit(1)
	}

	code := m.Run()
	setup.AfterSuiteFn()
	os.Exit(code)
}

func ensureBenchScrapeConfig(tb testing.TB) v1.ScrapeConfig {
	tb.Helper()

	benchScrapeConfigMu.Do(func() {
		scraperID := uuid.New()
		scraper := dutyModels.ConfigScraper{
			ID:        scraperID,
			Name:      "bench/config-db-save-results",
			Namespace: "bench",
			Spec:      "{}",
			Source:    dutyModels.SourceUI,
		}

		if err := testCtx.DB().Create(&scraper).Error; err != nil {
			benchScrapeConfigErr = fmt.Errorf("failed to create benchmark scraper: %w", err)
			return
		}

		benchScrapeConfig = v1.ScrapeConfig{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "bench-save-results",
				Namespace: "bench",
			},
			Spec: v1.ScraperSpec{
				LogLevel: "error",
			},
		}
		benchScrapeConfig.SetUID(k8sTypes.UID(scraperID.String()))
	})

	if benchScrapeConfigErr != nil {
		tb.Fatalf("failed to init benchmark scrape config: %v", benchScrapeConfigErr)
	}

	return benchScrapeConfig
}
