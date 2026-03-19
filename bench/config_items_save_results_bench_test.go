package bench

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
	dutyModels "github.com/flanksource/duty/models"
	"github.com/google/uuid"
)

var defaultBenchSizes = []int{250, 1000}

func BenchmarkBenchSaveResultsSeed(b *testing.B) {
	scraperID := getBenchScraperID(b)

	for _, size := range benchSizes() {
		b.Run(fmt.Sprintf("N=%d", size), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(size))

			for i := 0; i < b.N; i++ {
				b.StopTimer()
				cleanupBenchRows(b, scraperID)
				ctx := newBenchScrapeContext(b)
				results := buildScrapeResults(size, fmt.Sprintf("seed-%d", i), 1)
				b.StartTimer()

				if _, err := db.SaveResults(ctx, results); err != nil {
					b.Fatalf("seed save results failed (size=%d, iter=%d): %v", size, i, err)
				}
			}

			b.StopTimer()
			cleanupBenchRows(b, scraperID)
		})
	}
}

func BenchmarkBenchSaveResultsUpdateUnchanged(b *testing.B) {
	scraperID := getBenchScraperID(b)

	for _, size := range benchSizes() {
		b.Run(fmt.Sprintf("N=%d", size), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(size))

			b.StopTimer()
			cleanupBenchRows(b, scraperID)

			seedCtx := newBenchScrapeContext(b)
			baseline := buildScrapeResults(size, "unchanged", 1)
			if _, err := db.SaveResults(seedCtx, baseline); err != nil {
				b.Fatalf("failed to seed baseline (size=%d): %v", size, err)
			}

			updateCtx := newBenchScrapeContext(b)
			b.ResetTimer()
			b.StartTimer()

			for i := 0; i < b.N; i++ {
				if _, err := db.SaveResults(updateCtx, baseline); err != nil {
					b.Fatalf("unchanged update failed (size=%d, iter=%d): %v", size, i, err)
				}
			}

			b.StopTimer()
			cleanupBenchRows(b, scraperID)
		})
	}
}

func BenchmarkBenchSaveResultsUpdateChanged(b *testing.B) {
	scraperID := getBenchScraperID(b)

	for _, size := range benchSizes() {
		b.Run(fmt.Sprintf("N=%d", size), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(size))

			b.StopTimer()
			cleanupBenchRows(b, scraperID)

			seedCtx := newBenchScrapeContext(b)
			baseline := buildScrapeResults(size, "changed", 1)
			if _, err := db.SaveResults(seedCtx, baseline); err != nil {
				b.Fatalf("failed to seed baseline (size=%d): %v", size, err)
			}

			changed := buildScrapeResults(size, "changed", 2)
			updateCtx := newBenchScrapeContext(b)
			cleanupConfigChangesForBenchRows(b, scraperID)

			b.ResetTimer()
			b.StartTimer()
			for i := 0; i < b.N; i++ {
				results := changed
				if i%2 == 1 {
					results = baseline
				}

				if _, err := db.SaveResults(updateCtx, results); err != nil {
					b.Fatalf("changed update failed (size=%d, iter=%d): %v", size, i, err)
				}

				b.StopTimer()
				cleanupConfigChangesForBenchRows(b, scraperID)
				b.StartTimer()
			}

			b.StopTimer()
			cleanupBenchRows(b, scraperID)
		})
	}
}

func getBenchScraperID(tb testing.TB) uuid.UUID {
	tb.Helper()
	sc := ensureBenchScrapeConfig(tb)
	id := sc.GetPersistedID()
	if id == nil {
		tb.Fatalf("benchmark scrape config has no persisted ID")
	}
	return *id
}

func newBenchScrapeContext(tb testing.TB) api.ScrapeContext {
	tb.Helper()

	sc := ensureBenchScrapeConfig(tb)
	ctx := api.NewScrapeContext(testCtx).WithScrapeConfig(&sc)
	ctx, err := ctx.InitTempCache()
	if err != nil {
		tb.Fatalf("failed to initialize temp cache: %v", err)
	}
	return ctx
}

func benchSizes() []int {
	raw := strings.TrimSpace(os.Getenv("CONFIG_DB_BENCH_SIZES"))
	if raw == "" {
		raw = strings.TrimSpace(os.Getenv("DUTY_BENCH_SIZES"))
	}
	if raw == "" {
		return defaultBenchSizes
	}

	parts := strings.Split(raw, ",")
	sizes := make([]int, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		n, err := strconv.Atoi(part)
		if err != nil || n <= 0 {
			continue
		}
		sizes = append(sizes, n)
	}

	if len(sizes) == 0 {
		return defaultBenchSizes
	}
	return sizes
}

func buildScrapeResults(size int, dataset string, revision int) []v1.ScrapeResult {
	results := make([]v1.ScrapeResult, 0, size)
	for i := range size {
		externalID := fmt.Sprintf("bench-%s-%06d", dataset, i)
		name := fmt.Sprintf("bench-item-%06d", i)

		results = append(results, v1.ScrapeResult{
			ID:          externalID,
			Type:        "Benchmark::ConfigItem",
			ConfigClass: "Node",
			Name:        name,
			Source:      dutyModels.SourceUI,
			Ready:       true,
			Tags: v1.JSONStringMap{
				"suite":   "config-items-save-results",
				"dataset": dataset,
				"size":    strconv.Itoa(size),
			},
			Config: map[string]any{
				"apiVersion": "bench.flanksource.com/v1",
				"kind":       "ConfigItem",
				"metadata": map[string]any{
					"name": name,
					"labels": map[string]any{
						"dataset": dataset,
						"index":   fmt.Sprintf("%d", i),
					},
				},
				"spec": map[string]any{
					"enabled":  true,
					"revision": revision,
					"port":     8080,
				},
			},
		})
	}

	return results
}

func cleanupBenchRows(tb testing.TB, scraperID uuid.UUID) {
	tb.Helper()

	queries := []string{
		"DELETE FROM config_relationships WHERE config_id IN (SELECT id FROM config_items WHERE scraper_id = ?) OR related_id IN (SELECT id FROM config_items WHERE scraper_id = ?)",
		"DELETE FROM config_access_logs WHERE scraper_id = ?",
		"DELETE FROM config_access WHERE scraper_id = ?",
		"DELETE FROM config_changes WHERE config_id IN (SELECT id FROM config_items WHERE scraper_id = ?)",
		"DELETE FROM config_items_last_scraped_time WHERE config_id IN (SELECT id FROM config_items WHERE scraper_id = ?)",
		"DELETE FROM config_items WHERE scraper_id = ?",
	}

	for _, query := range queries {
		var err error
		switch strings.Count(query, "?") {
		case 2:
			err = testCtx.DB().Exec(query, scraperID, scraperID).Error
		default:
			err = testCtx.DB().Exec(query, scraperID).Error
		}
		if err != nil {
			tb.Fatalf("cleanup failed for query %q: %v", query, err)
		}
	}
}

func cleanupConfigChangesForBenchRows(tb testing.TB, scraperID uuid.UUID) {
	tb.Helper()

	if err := testCtx.DB().Exec(
		"DELETE FROM config_changes WHERE config_id IN (SELECT id FROM config_items WHERE scraper_id = ?)",
		scraperID,
	).Error; err != nil {
		tb.Fatalf("failed to cleanup config_changes: %v", err)
	}
}
