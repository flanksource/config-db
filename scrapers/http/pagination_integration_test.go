package http

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	commonsHTTP "github.com/flanksource/commons/http"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPaginateOData(t *testing.T) {
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
		json.NewEncoder(w).Encode(page)
	}))
	defer server.Close()

	client := commonsHTTP.NewClient()
	firstResp, err := client.R(context.Background()).Get(server.URL + "/items")
	require.NoError(t, err)

	results, err := paginate(context.Background(), client, v1.Pagination{
		NextPageExpr: `"@odata.nextLink" in response.body && response.body["@odata.nextLink"] != "" ? string(response.body["@odata.nextLink"]) : ""`,
		ReduceExpr:   `acc + page.value`,
	}, firstResp, server.URL+"/items", v1.BaseScraper{})

	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, []any{"item1", "item2", "item3", "item4", "item5"}, results[0].Config)
	assert.Equal(t, int32(3), atomic.LoadInt32(&requestCount))
}

func TestPaginateODataDefaultReduce(t *testing.T) {
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
		json.NewEncoder(w).Encode(page)
	}))
	defer server.Close()

	client := commonsHTTP.NewClient()
	firstResp, err := client.R(context.Background()).Get(server.URL + "/data")
	require.NoError(t, err)

	results, err := paginate(context.Background(), client, v1.Pagination{
		NextPageExpr: `"@odata.nextLink" in response.body && response.body["@odata.nextLink"] != "" ? string(response.body["@odata.nextLink"]) : ""`,
	}, firstResp, server.URL+"/data", v1.BaseScraper{})

	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, []any{"a", "b", "c"}, results[0].Config)
}

func TestPaginatePerPage(t *testing.T) {
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
		json.NewEncoder(w).Encode(page)
	}))
	defer server.Close()

	client := commonsHTTP.NewClient()
	firstResp, err := client.R(context.Background()).Get(server.URL + "/api")
	require.NoError(t, err)

	results, err := paginate(context.Background(), client, v1.Pagination{
		NextPageExpr: `has(response.body.next) && response.body.next != "" ? response.body.next : ""`,
		PerPage:      true,
	}, firstResp, server.URL+"/api", v1.BaseScraper{})

	require.NoError(t, err)
	require.Len(t, results, 3, "should yield one result per page")

	page1 := results[0].Config.(map[string]any)
	assert.Equal(t, []any{"p1a", "p1b"}, page1["items"])

	page2 := results[1].Config.(map[string]any)
	assert.Equal(t, []any{"p2a"}, page2["items"])

	page3 := results[2].Config.(map[string]any)
	assert.Equal(t, []any{"p3a"}, page3["items"])
}

func TestPaginateMaxPages(t *testing.T) {
	var requestCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idx := int(atomic.AddInt32(&requestCount, 1))
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"value": []any{fmt.Sprintf("item-%d", idx)},
			"next":  fmt.Sprintf("http://%s/items?page=%d", r.Host, idx+1),
		})
	}))
	defer server.Close()

	client := commonsHTTP.NewClient()
	firstResp, err := client.R(context.Background()).Get(server.URL + "/items")
	require.NoError(t, err)

	results, err := paginate(context.Background(), client, v1.Pagination{
		NextPageExpr: `response.body.next`,
		ReduceExpr:   `acc + page.value`,
		MaxPages:     3,
	}, firstResp, server.URL+"/items", v1.BaseScraper{})

	require.NoError(t, err)
	require.Len(t, results, 1)
	items := results[0].Config.([]any)
	assert.Len(t, items, 3, "should stop after maxPages=3")
}

func TestPaginate429Retry(t *testing.T) {
	var requestCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idx := int(atomic.AddInt32(&requestCount, 1))
		if idx == 2 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(429)
			w.Write([]byte(`{"error":"rate limited"}`))
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if idx <= 2 {
			json.NewEncoder(w).Encode(map[string]any{
				"value": []any{fmt.Sprintf("item-%d", idx)},
				"next":  fmt.Sprintf("http://%s/items?page=%d", r.Host, idx+1),
			})
		} else {
			json.NewEncoder(w).Encode(map[string]any{
				"value": []any{fmt.Sprintf("item-%d", idx)},
			})
		}
	}))
	defer server.Close()

	client := commonsHTTP.NewClient()
	firstResp, err := client.R(context.Background()).Get(server.URL + "/items")
	require.NoError(t, err)

	results, err := paginate(context.Background(), client, v1.Pagination{
		NextPageExpr: `has(response.body.next) ? response.body.next : ""`,
		ReduceExpr:   `acc + page.value`,
	}, firstResp, server.URL+"/items", v1.BaseScraper{})

	require.NoError(t, err)
	require.Len(t, results, 1)
	items := results[0].Config.([]any)
	assert.Len(t, items, 2, "should have items from page 1 and retried page 2")
	assert.True(t, atomic.LoadInt32(&requestCount) >= 3, "should have retried the 429")
}

func TestPaginateErrorOnNonOKPage(t *testing.T) {
	var requestCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idx := int(atomic.AddInt32(&requestCount, 1))
		w.Header().Set("Content-Type", "application/json")
		if idx == 1 {
			json.NewEncoder(w).Encode(map[string]any{
				"value": []any{"item-1"},
				"next":  fmt.Sprintf("http://%s/items?page=2", r.Host),
			})
			return
		}
		w.WriteHeader(403)
		w.Write([]byte(`{"error":"forbidden"}`))
	}))
	defer server.Close()

	client := commonsHTTP.NewClient()
	firstResp, err := client.R(context.Background()).Get(server.URL + "/items")
	require.NoError(t, err)

	_, err = paginate(context.Background(), client, v1.Pagination{
		NextPageExpr: `has(response.body.next) ? response.body.next : ""`,
		ReduceExpr:   `acc + page.value`,
	}, firstResp, server.URL+"/items", v1.BaseScraper{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 403")
}
