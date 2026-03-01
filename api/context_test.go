// ABOUTME: Unit tests for ScrapeContext, specifically the last scrape summary
// ABOUTME: getter, setter, and propagation through context methods.
package api

import (
	"testing"

	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/stretchr/testify/assert"
)

func TestScrapeContext_LastScrapeSummary(t *testing.T) {
	t.Run("returns empty map when unset", func(t *testing.T) {
		ctx := ScrapeContext{}
		summary := ctx.LastScrapeSummary()
		assert.NotNil(t, summary)
		assert.Empty(t, summary)
	})

	t.Run("returns set summary", func(t *testing.T) {
		summary := v1.ScrapeSummary{
			"AWS::EC2::Instance": v1.ConfigTypeScrapeSummary{
				Added:     3,
				Updated:   5,
				Unchanged: 10,
			},
		}

		ctx := ScrapeContext{}
		ctx = ctx.WithLastScrapeSummary(summary)

		got := ctx.LastScrapeSummary()
		assert.Equal(t, 3, got["AWS::EC2::Instance"].Added)
		assert.Equal(t, 5, got["AWS::EC2::Instance"].Updated)
		assert.Equal(t, 10, got["AWS::EC2::Instance"].Unchanged)
	})

	t.Run("preserves summary through WithJobHistory", func(t *testing.T) {
		summary := v1.ScrapeSummary{
			"Kubernetes::Pod": v1.ConfigTypeScrapeSummary{Added: 1},
		}

		ctx := ScrapeContext{}
		ctx = ctx.WithLastScrapeSummary(summary)
		ctx = ctx.WithJobHistory(nil)

		got := ctx.LastScrapeSummary()
		assert.Equal(t, 1, got["Kubernetes::Pod"].Added)
	})

	t.Run("preserves summary through AsIncrementalScrape", func(t *testing.T) {
		summary := v1.ScrapeSummary{
			"Kubernetes::Pod": v1.ConfigTypeScrapeSummary{Updated: 7},
		}

		ctx := ScrapeContext{}
		ctx = ctx.WithLastScrapeSummary(summary)
		ctx = ctx.AsIncrementalScrape()

		got := ctx.LastScrapeSummary()
		assert.Equal(t, 7, got["Kubernetes::Pod"].Updated)
	})
}
