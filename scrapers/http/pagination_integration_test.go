package http

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"time"

	commonsHTTP "github.com/flanksource/commons/http"
	v1 "github.com/flanksource/config-db/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("paginate", func() {
	Context("OData with custom reduce", func() {
		It("should reduce all pages into a single result", func() {
			pages := []map[string]any{
				{"value": []any{"item1", "item2"}, "@odata.nextLink": nil},
				{"value": []any{"item3", "item4"}, "@odata.nextLink": nil},
				{"value": []any{"item5"}},
			}

			var requestCount int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				idx := int(atomic.AddInt32(&requestCount, 1)) - 1
				page := pages[idx]
				if idx < len(pages)-1 {
					page["@odata.nextLink"] = fmt.Sprintf("http://%s/items?$skip=%d", r.Host, (idx+1)*2)
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(page)
			}))
			defer server.Close()

			client := commonsHTTP.NewClient()
			firstResp, err := client.R(context.Background()).Get(server.URL + "/items")
			Expect(err).ToNot(HaveOccurred())

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			results, err := paginate(ctx, client, v1.Pagination{
				NextPageExpr: `"@odata.nextLink" in response.body && response.body["@odata.nextLink"] != "" ? string(response.body["@odata.nextLink"]) : ""`,
				ReduceExpr:   `acc + page.value`,
			}, firstResp, server.URL+"/items", v1.BaseScraper{})

			Expect(err).ToNot(HaveOccurred())
			Expect(results).To(HaveLen(1))
			Expect(results[0].Config).To(Equal([]any{"item1", "item2", "item3", "item4", "item5"}))
			Expect(atomic.LoadInt32(&requestCount)).To(Equal(int32(3)))
		})
	})

	Context("OData with default reduce", func() {
		It("should use default reduce to concatenate value arrays", func() {
			pages := []map[string]any{
				{"value": []any{"a", "b"}, "@odata.nextLink": nil},
				{"value": []any{"c"}},
			}

			var requestCount int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				idx := int(atomic.AddInt32(&requestCount, 1)) - 1
				page := pages[idx]
				if idx < len(pages)-1 {
					page["@odata.nextLink"] = fmt.Sprintf("http://%s/data?page=%d", r.Host, idx+2)
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(page)
			}))
			defer server.Close()

			client := commonsHTTP.NewClient()
			firstResp, err := client.R(context.Background()).Get(server.URL + "/data")
			Expect(err).ToNot(HaveOccurred())

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			results, err := paginate(ctx, client, v1.Pagination{
				NextPageExpr: `"@odata.nextLink" in response.body && response.body["@odata.nextLink"] != "" ? string(response.body["@odata.nextLink"]) : ""`,
			}, firstResp, server.URL+"/data", v1.BaseScraper{})

			Expect(err).ToNot(HaveOccurred())
			Expect(results).To(HaveLen(1))
			Expect(results[0].Config).To(Equal([]any{"a", "b", "c"}))
		})
	})

	Context("PerPage mode", func() {
		It("should yield one result per page", func() {
			pages := []map[string]any{
				{"items": []any{"p1a", "p1b"}, "next": nil},
				{"items": []any{"p2a"}, "next": nil},
				{"items": []any{"p3a"}},
			}

			var requestCount int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				idx := int(atomic.AddInt32(&requestCount, 1)) - 1
				page := pages[idx]
				if idx < len(pages)-1 {
					page["next"] = fmt.Sprintf("http://%s/api?page=%d", r.Host, idx+2)
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(page)
			}))
			defer server.Close()

			client := commonsHTTP.NewClient()
			firstResp, err := client.R(context.Background()).Get(server.URL + "/api")
			Expect(err).ToNot(HaveOccurred())

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			results, err := paginate(ctx, client, v1.Pagination{
				NextPageExpr: `has(response.body.next) && response.body.next != "" ? response.body.next : ""`,
				PerPage:      true,
			}, firstResp, server.URL+"/api", v1.BaseScraper{})

			Expect(err).ToNot(HaveOccurred())
			Expect(results).To(HaveLen(3))

			page1 := results[0].Config.(map[string]any)
			Expect(page1["items"]).To(Equal([]any{"p1a", "p1b"}))

			page2 := results[1].Config.(map[string]any)
			Expect(page2["items"]).To(Equal([]any{"p2a"}))

			page3 := results[2].Config.(map[string]any)
			Expect(page3["items"]).To(Equal([]any{"p3a"}))
		})
	})

	Context("MaxPages", func() {
		It("should stop after maxPages is reached", func() {
			var requestCount int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				idx := int(atomic.AddInt32(&requestCount, 1))
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"value": []any{fmt.Sprintf("item-%d", idx)},
					"next":  fmt.Sprintf("http://%s/items?page=%d", r.Host, idx+1),
				})
			}))
			defer server.Close()

			client := commonsHTTP.NewClient()
			firstResp, err := client.R(context.Background()).Get(server.URL + "/items")
			Expect(err).ToNot(HaveOccurred())

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			results, err := paginate(ctx, client, v1.Pagination{
				NextPageExpr: `response.body.next`,
				ReduceExpr:   `acc + page.value`,
				MaxPages:     3,
			}, firstResp, server.URL+"/items", v1.BaseScraper{})

			Expect(err).ToNot(HaveOccurred())
			Expect(results).To(HaveLen(1))
			items := results[0].Config.([]any)
			Expect(items).To(HaveLen(3))
		})
	})

	Context("429 retry", func() {
		It("should retry on HTTP 429 and continue pagination", func() {
			var requestCount int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				idx := int(atomic.AddInt32(&requestCount, 1))
				if idx == 2 {
					w.Header().Set("Retry-After", "0")
					w.WriteHeader(429)
					_, _ = w.Write([]byte(`{"error":"rate limited"}`))
					return
				}

				w.Header().Set("Content-Type", "application/json")
				if idx <= 2 {
					_ = json.NewEncoder(w).Encode(map[string]any{
						"value": []any{fmt.Sprintf("item-%d", idx)},
						"next":  fmt.Sprintf("http://%s/items?page=%d", r.Host, idx+1),
					})
				} else {
					_ = json.NewEncoder(w).Encode(map[string]any{
						"value": []any{fmt.Sprintf("item-%d", idx)},
					})
				}
			}))
			defer server.Close()

			client := commonsHTTP.NewClient()
			firstResp, err := client.R(context.Background()).Get(server.URL + "/items")
			Expect(err).ToNot(HaveOccurred())

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			results, err := paginate(ctx, client, v1.Pagination{
				NextPageExpr: `has(response.body.next) ? response.body.next : ""`,
				ReduceExpr:   `acc + page.value`,
			}, firstResp, server.URL+"/items", v1.BaseScraper{})

			Expect(err).ToNot(HaveOccurred())
			Expect(results).To(HaveLen(1))
			items := results[0].Config.([]any)
			Expect(items).To(HaveLen(2))
			Expect(atomic.LoadInt32(&requestCount) >= 3).To(BeTrue())
		})
	})

	Context("error on non-OK page", func() {
		It("should return an error containing HTTP 403", func() {
			var requestCount int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				idx := int(atomic.AddInt32(&requestCount, 1))
				w.Header().Set("Content-Type", "application/json")
				if idx == 1 {
					_ = json.NewEncoder(w).Encode(map[string]any{
						"value": []any{"item-1"},
						"next":  fmt.Sprintf("http://%s/items?page=2", r.Host),
					})
					return
				}
				w.WriteHeader(403)
				_, _ = w.Write([]byte(`{"error":"forbidden"}`))
			}))
			defer server.Close()

			client := commonsHTTP.NewClient()
			firstResp, err := client.R(context.Background()).Get(server.URL + "/items")
			Expect(err).ToNot(HaveOccurred())

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_, err = paginate(ctx, client, v1.Pagination{
				NextPageExpr: `has(response.body.next) ? response.body.next : ""`,
				ReduceExpr:   `acc + page.value`,
			}, firstResp, server.URL+"/items", v1.BaseScraper{})

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("HTTP 403"))
		})
	})
})
