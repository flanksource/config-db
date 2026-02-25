package http

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEvalNextPage_StringReturn(t *testing.T) {
	responseMap := map[string]any{
		"body":    map[string]any{"@odata.nextLink": "https://api.example.com/items?$skip=100"},
		"headers": map[string]string{"Content-Type": "application/json"},
		"status":  200,
		"url":     "https://api.example.com/items",
	}

	req, err := evalNextPage(responseMap, `response.body["@odata.nextLink"]`)
	require.NoError(t, err)
	require.NotNil(t, req)
	assert.Equal(t, "https://api.example.com/items?$skip=100", req.URL)
	assert.Equal(t, "GET", req.Method)
}

func TestEvalNextPage_MapReturn(t *testing.T) {
	responseMap := map[string]any{
		"body":    map[string]any{"NextToken": "abc123"},
		"headers": map[string]string{},
		"status":  200,
		"url":     "https://api.example.com/list",
	}

	expr := `has(response.body.NextToken) ? {"url": response.url, "method": "POST", "body": response.body.NextToken} : {"url": ""}`
	req, err := evalNextPage(responseMap, expr)
	require.NoError(t, err)
	require.NotNil(t, req)
	assert.Equal(t, "https://api.example.com/list", req.URL)
	assert.Equal(t, "POST", req.Method)
	assert.Equal(t, "abc123", req.Body)
}

func TestEvalNextPage_MapReturnStops(t *testing.T) {
	responseMap := map[string]any{
		"body":    map[string]any{"items": []any{1}},
		"headers": map[string]string{},
		"status":  200,
		"url":     "https://api.example.com/list",
	}

	expr := `has(response.body.NextToken) ? {"url": response.url, "method": "POST", "body": response.body.NextToken} : {"url": ""}`
	req, err := evalNextPage(responseMap, expr)
	require.NoError(t, err)
	assert.Nil(t, req, "should return nil when url is empty")
}

func TestEvalNextPage_NullReturn(t *testing.T) {
	responseMap := map[string]any{
		"body":    map[string]any{"items": []any{1, 2, 3}},
		"headers": map[string]string{},
		"status":  200,
		"url":     "https://api.example.com/items",
	}

	req, err := evalNextPage(responseMap, `has(response.body.nextLink) ? response.body.nextLink : null`)
	require.NoError(t, err)
	assert.Nil(t, req, "should return nil when CEL evaluates to null")
}

func TestEvalNextPage_EmptyStringReturn(t *testing.T) {
	responseMap := map[string]any{
		"body":    map[string]any{"next": ""},
		"headers": map[string]string{},
		"status":  200,
		"url":     "https://api.example.com/items",
	}

	req, err := evalNextPage(responseMap, `response.body.next`)
	require.NoError(t, err)
	assert.Nil(t, req, "should return nil for empty string")
}

func TestEvalNextPage_WithHeaders(t *testing.T) {
	responseMap := map[string]any{
		"body":    map[string]any{"cursor": "xyz"},
		"headers": map[string]string{"X-Request-Id": "req-1"},
		"status":  200,
		"url":     "https://api.example.com/data",
	}

	expr := `{"url": response.url + "?cursor=" + response.body.cursor, "headers": {"X-Request-Id": response.headers["X-Request-Id"]}}`
	req, err := evalNextPage(responseMap, expr)
	require.NoError(t, err)
	require.NotNil(t, req)
	assert.Equal(t, "https://api.example.com/data?cursor=xyz", req.URL)
	assert.Equal(t, "GET", req.Method)
	assert.Equal(t, map[string]string{"X-Request-Id": "req-1"}, req.Headers)
}

func TestEvalNextPage_StatusCodeAccess(t *testing.T) {
	responseMap := map[string]any{
		"body":    map[string]any{"next": "https://next.page"},
		"headers": map[string]string{},
		"status":  200,
		"url":     "https://api.example.com/items",
	}

	req, err := evalNextPage(responseMap, `response.status == 200 ? response.body.next : null`)
	require.NoError(t, err)
	require.NotNil(t, req)
	assert.Equal(t, "https://next.page", req.URL)
}

func TestEvalReduce_SimpleConcat(t *testing.T) {
	acc := []any{"a", "b"}
	pageBody := map[string]any{"value": []any{"c", "d"}}

	result, err := evalReduce(acc, pageBody, `acc + page.value`)
	require.NoError(t, err)
	assert.Equal(t, []any{"a", "b", "c", "d"}, result)
}

func TestEvalReduce_EmptyAcc(t *testing.T) {
	var acc []any
	pageBody := map[string]any{"items": []any{1, 2, 3}}

	result, err := evalReduce(acc, pageBody, `acc + page.items`)
	require.NoError(t, err)
	assert.Equal(t, []any{int64(1), int64(2), int64(3)}, result)
}

func TestDefaultReduce_Array(t *testing.T) {
	acc := []any{"a"}
	result := defaultReduce(acc, []any{"b", "c"})
	assert.Equal(t, []any{"a", "b", "c"}, result)
}

func TestDefaultReduce_MapWithValue(t *testing.T) {
	acc := []any{"a"}
	result := defaultReduce(acc, map[string]any{"value": []any{"b", "c"}})
	assert.Equal(t, []any{"a", "b", "c"}, result)
}

func TestDefaultReduce_MapWithItems(t *testing.T) {
	acc := []any{"a"}
	result := defaultReduce(acc, map[string]any{"items": []any{"b"}})
	assert.Equal(t, []any{"a", "b"}, result)
}

func TestDefaultReduce_MapWithCapitalItems(t *testing.T) {
	acc := []any{"a"}
	result := defaultReduce(acc, map[string]any{"Items": []any{"b"}})
	assert.Equal(t, []any{"a", "b"}, result)
}

func TestDefaultReduce_MapNoKnownKey(t *testing.T) {
	acc := []any{"a"}
	page := map[string]any{"data": "something"}
	result := defaultReduce(acc, page)
	assert.Equal(t, []any{"a", page}, result)
}

func TestParseDelay(t *testing.T) {
	tests := []struct {
		input    string
		expected time.Duration
		wantErr  bool
	}{
		{"", 0, false},
		{"500ms", 500 * time.Millisecond, false},
		{"2s", 2 * time.Second, false},
		{"1m", time.Minute, false},
		{"invalid", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			d, err := parseDelay(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expected, d)
			}
		})
	}
}

func TestParseJSON(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected any
	}{
		{"array", `[1,2,3]`, []any{float64(1), float64(2), float64(3)}},
		{"object", `{"key":"val"}`, map[string]any{"key": "val"}},
		{"plain text", `not json`, "not json"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, parseJSON(tt.input))
		})
	}
}
