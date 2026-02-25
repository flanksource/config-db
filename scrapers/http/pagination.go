package http

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	commonsHTTP "github.com/flanksource/commons/http"
	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/gomplate/v3"
	"github.com/google/cel-go/common/types/ref"
	"google.golang.org/protobuf/types/known/structpb"
)

const maxRetryOn429 = 3

type nextPageRequest struct {
	URL     string
	Method  string
	Body    string
	Headers map[string]string
}

// celToNative converts CEL ref.Val maps to native Go maps recursively.
func celToNative(v any) any {
	switch m := v.(type) {
	case map[ref.Val]ref.Val:
		result := make(map[string]any, len(m))
		for k, val := range m {
			result[fmt.Sprintf("%v", k)] = celToNative(val.Value())
		}
		return result
	default:
		return v
	}
}

func evalNextPage(responseMap map[string]any, expr string) (*nextPageRequest, error) {
	out, err := gomplate.RunExpression(map[string]any{"response": responseMap}, gomplate.Template{Expression: expr})
	if err != nil {
		return nil, fmt.Errorf("nextPageExpr evaluation failed: %w", err)
	}

	if out == nil {
		return nil, nil
	}
	if _, ok := out.(structpb.NullValue); ok {
		return nil, nil
	}

	out = celToNative(out)

	switch v := out.(type) {
	case string:
		if v == "" {
			return nil, nil
		}
		return &nextPageRequest{URL: v, Method: "GET"}, nil
	case map[string]any:
		return parseNextPageMap(v)
	default:
		s := fmt.Sprintf("%v", v)
		if s == "" {
			return nil, nil
		}
		return &nextPageRequest{URL: s, Method: "GET"}, nil
	}
}

func parseNextPageMap(v map[string]any) (*nextPageRequest, error) {
	req := &nextPageRequest{Method: "GET"}
	if u, ok := v["url"].(string); ok {
		req.URL = u
	}
	if req.URL == "" {
		return nil, nil
	}
	if m, ok := v["method"].(string); ok && m != "" {
		req.Method = m
	}
	if b, ok := v["body"].(string); ok {
		req.Body = b
	}
	if h, ok := v["headers"].(map[string]any); ok {
		req.Headers = make(map[string]string, len(h))
		for k, val := range h {
			req.Headers[k] = fmt.Sprintf("%v", val)
		}
	}
	return req, nil
}

func evalReduce(acc []any, pageBody any, expr string) ([]any, error) {
	out, err := gomplate.RunExpression(map[string]any{"acc": acc, "page": pageBody}, gomplate.Template{Expression: expr})
	if err != nil {
		return nil, fmt.Errorf("reduceExpr evaluation failed: %w", err)
	}
	if arr, ok := out.([]any); ok {
		return arr, nil
	}
	return nil, fmt.Errorf("reduceExpr must return a list, got %T", out)
}

func defaultReduce(acc []any, pageBody any) []any {
	switch v := pageBody.(type) {
	case []any:
		return append(acc, v...)
	case map[string]any:
		for _, key := range []string{"value", "items", "Items"} {
			if items, ok := v[key].([]any); ok {
				return append(acc, items...)
			}
		}
		return append(acc, v)
	default:
		return append(acc, v)
	}
}

func fetchPage(ctx context.Context, client *commonsHTTP.Client, req nextPageRequest) (*commonsHTTP.Response, error) {
	r := client.R(ctx)
	for k, v := range req.Headers {
		r = r.Header(k, v)
	}
	if req.Body != "" {
		if err := r.Body(req.Body); err != nil {
			return nil, fmt.Errorf("failed to set page request body: %w", err)
		}
	}
	return r.Do(req.Method, req.URL)
}

func fetchWithRetry(ctx context.Context, client *commonsHTTP.Client, req nextPageRequest) (*commonsHTTP.Response, error) {
	for attempt := 0; ; attempt++ {
		response, err := fetchPage(ctx, client, req)
		if err != nil {
			return nil, err
		}
		if response.StatusCode != 429 || attempt >= maxRetryOn429 {
			return response, nil
		}
		logger.Warnf("HTTP 429 on page request, retrying (%d/%d)", attempt+1, maxRetryOn429)
		sleepForRetryAfter(response, attempt)
	}
}

func sleepForRetryAfter(response *commonsHTTP.Response, attempt int) {
	if ra := response.Header.Get("Retry-After"); ra != "" {
		if seconds, err := strconv.Atoi(ra); err == nil {
			time.Sleep(time.Duration(seconds) * time.Second)
			return
		}
		if t, err := time.Parse(time.RFC1123, ra); err == nil {
			if d := time.Until(t); d > 0 {
				time.Sleep(d)
				return
			}
		}
	}
	time.Sleep(time.Duration(math.Pow(2, float64(attempt))) * time.Second)
}

func buildResponseMap(response *commonsHTTP.Response, requestURL string) (map[string]any, any, error) {
	responseBody, err := response.AsString()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read page response: %w", err)
	}

	var body any
	if response.IsJSON() {
		body = parseJSON(responseBody)
	} else {
		body = responseBody
	}

	return map[string]any{
		"body":    body,
		"headers": response.GetHeaders(),
		"status":  response.StatusCode,
		"url":     requestURL,
	}, body, nil
}

func parseJSON(s string) any {
	trimmed := strings.TrimSpace(s)
	if strings.HasPrefix(trimmed, "[") {
		var arr []any
		if err := json.Unmarshal([]byte(s), &arr); err == nil {
			return arr
		}
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(s), &obj); err == nil {
		return obj
	}
	return s
}

func paginate(ctx context.Context, client *commonsHTTP.Client, pagination v1.Pagination, firstResponse *commonsHTTP.Response, firstURL string, baseScraper v1.BaseScraper) (v1.ScrapeResults, error) {
	delay, err := parseDelay(pagination.Delay)
	if err != nil {
		return nil, fmt.Errorf("invalid pagination delay %q: %w", pagination.Delay, err)
	}

	responseMap, firstBody, err := buildResponseMap(firstResponse, firstURL)
	if err != nil {
		return nil, err
	}

	if pagination.PerPage {
		return paginatePerPage(ctx, client, pagination, responseMap, firstBody, delay, baseScraper)
	}
	return paginateMerge(ctx, client, pagination, responseMap, firstBody, delay, baseScraper)
}

func paginatePerPage(ctx context.Context, client *commonsHTTP.Client, pagination v1.Pagination, responseMap map[string]any, firstBody any, delay time.Duration, baseScraper v1.BaseScraper) (v1.ScrapeResults, error) {
	var results v1.ScrapeResults
	first := v1.NewScrapeResult(baseScraper)
	first.Config = firstBody
	results = append(results, *first)

	pageCount := 1
	for {
		if pagination.MaxPages > 0 && pageCount >= pagination.MaxPages {
			logger.Warnf("pagination capped at maxPages=%d", pagination.MaxPages)
			break
		}

		nextReq, err := evalNextPage(responseMap, pagination.NextPageExpr)
		if err != nil {
			return nil, err
		}
		if nextReq == nil {
			break
		}

		if delay > 0 {
			time.Sleep(delay)
		}

		response, err := fetchWithRetry(ctx, client, *nextReq)
		if err != nil {
			return nil, fmt.Errorf("page %d request failed: %w", pageCount+1, err)
		}

		if !response.IsOK() {
			return nil, fmt.Errorf("page %d request returned HTTP %d: %s", pageCount+1, response.StatusCode, errorBody(response))
		}

		var body any
		responseMap, body, err = buildResponseMap(response, nextReq.URL)
		if err != nil {
			return nil, err
		}

		r := v1.NewScrapeResult(baseScraper)
		r.Config = body
		results = append(results, *r)
		pageCount++
	}

	return results, nil
}

func paginateMerge(ctx context.Context, client *commonsHTTP.Client, pagination v1.Pagination, responseMap map[string]any, firstBody any, delay time.Duration, baseScraper v1.BaseScraper) (v1.ScrapeResults, error) {
	var acc []any
	if pagination.ReduceExpr != "" {
		var err error
		if acc, err = evalReduce(acc, firstBody, pagination.ReduceExpr); err != nil {
			return nil, err
		}
	} else {
		acc = defaultReduce(acc, firstBody)
	}

	pageCount := 1
	for {
		if pagination.MaxPages > 0 && pageCount >= pagination.MaxPages {
			logger.Warnf("pagination capped at maxPages=%d", pagination.MaxPages)
			break
		}

		nextReq, err := evalNextPage(responseMap, pagination.NextPageExpr)
		if err != nil {
			return nil, err
		}
		if nextReq == nil {
			break
		}

		if delay > 0 {
			time.Sleep(delay)
		}

		response, err := fetchWithRetry(ctx, client, *nextReq)
		if err != nil {
			return nil, fmt.Errorf("page %d request failed: %w", pageCount+1, err)
		}

		if !response.IsOK() {
			return nil, fmt.Errorf("page %d request returned HTTP %d: %s", pageCount+1, response.StatusCode, errorBody(response))
		}

		var body any
		responseMap, body, err = buildResponseMap(response, nextReq.URL)
		if err != nil {
			return nil, err
		}

		if pagination.ReduceExpr != "" {
			if acc, err = evalReduce(acc, body, pagination.ReduceExpr); err != nil {
				return nil, err
			}
		} else {
			acc = defaultReduce(acc, body)
		}
		pageCount++
	}

	result := v1.NewScrapeResult(baseScraper)
	result.Config = acc
	return v1.ScrapeResults{*result}, nil
}

func parseDelay(delay string) (time.Duration, error) {
	if delay == "" {
		return 0, nil
	}
	return time.ParseDuration(delay)
}
