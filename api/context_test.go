package api

import (
	"testing"

	v1 "github.com/flanksource/config-db/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestAPI(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "API Suite")
}

var _ = Describe("ScrapeContext LastScrapeSummary", func() {
	It("returns empty map when unset", func() {
		ctx := ScrapeContext{}
		summary := ctx.LastScrapeSummary()
		Expect(summary).ToNot(BeNil())
		Expect(summary).To(BeEmpty())
	})

	It("returns set summary", func() {
		ctx := ScrapeContext{}
		ctx = ctx.WithLastScrapeSummary(v1.ScrapeSummary{
			ConfigTypes: map[string]v1.ConfigTypeScrapeSummary{
				"AWS::EC2::Instance": {Added: 3, Updated: 5, Unchanged: 10},
			},
		})

		got := ctx.LastScrapeSummary()
		Expect(got["AWS::EC2::Instance"].Added).To(Equal(3))
		Expect(got["AWS::EC2::Instance"].Updated).To(Equal(5))
		Expect(got["AWS::EC2::Instance"].Unchanged).To(Equal(10))
	})

	It("preserves summary through WithJobHistory", func() {
		ctx := ScrapeContext{}
		ctx = ctx.WithLastScrapeSummary(v1.ScrapeSummary{
			ConfigTypes: map[string]v1.ConfigTypeScrapeSummary{
				"Kubernetes::Pod": {Added: 1},
			},
		})
		ctx = ctx.WithJobHistory(nil)

		Expect(ctx.LastScrapeSummary()["Kubernetes::Pod"].Added).To(Equal(1))
	})

	It("preserves summary through AsIncrementalScrape", func() {
		ctx := ScrapeContext{}
		ctx = ctx.WithLastScrapeSummary(v1.ScrapeSummary{
			ConfigTypes: map[string]v1.ConfigTypeScrapeSummary{
				"Kubernetes::Pod": {Updated: 7},
			},
		})
		ctx = ctx.AsIncrementalScrape()

		Expect(ctx.LastScrapeSummary()["Kubernetes::Pod"].Updated).To(Equal(7))
	})
})
