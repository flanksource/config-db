package http

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("evalNextPage", func() {
	It("should return URL and GET method for string return", func() {
		responseMap := map[string]any{
			"body":    map[string]any{"@odata.nextLink": "https://api.example.com/items?$skip=100"},
			"headers": map[string]string{"Content-Type": "application/json"},
			"status":  200,
			"url":     "https://api.example.com/items",
		}

		req, err := evalNextPage(responseMap, `response.body["@odata.nextLink"]`)
		Expect(err).ToNot(HaveOccurred())
		Expect(req).ToNot(BeNil())
		Expect(req.URL).To(Equal("https://api.example.com/items?$skip=100"))
		Expect(req.Method).To(Equal("GET"))
	})

	It("should parse map return with url, method, and body", func() {
		responseMap := map[string]any{
			"body":    map[string]any{"NextToken": "abc123"},
			"headers": map[string]string{},
			"status":  200,
			"url":     "https://api.example.com/list",
		}

		expr := `has(response.body.NextToken) ? {"url": response.url, "method": "POST", "body": response.body.NextToken} : {"url": ""}`
		req, err := evalNextPage(responseMap, expr)
		Expect(err).ToNot(HaveOccurred())
		Expect(req).ToNot(BeNil())
		Expect(req.URL).To(Equal("https://api.example.com/list"))
		Expect(req.Method).To(Equal("POST"))
		Expect(req.Body).To(Equal("abc123"))
	})

	It("should return nil when map url is empty (stop condition)", func() {
		responseMap := map[string]any{
			"body":    map[string]any{"items": []any{1}},
			"headers": map[string]string{},
			"status":  200,
			"url":     "https://api.example.com/list",
		}

		expr := `has(response.body.NextToken) ? {"url": response.url, "method": "POST", "body": response.body.NextToken} : {"url": ""}`
		req, err := evalNextPage(responseMap, expr)
		Expect(err).ToNot(HaveOccurred())
		Expect(req).To(BeNil())
	})

	It("should return nil when CEL evaluates to null", func() {
		responseMap := map[string]any{
			"body":    map[string]any{"items": []any{1, 2, 3}},
			"headers": map[string]string{},
			"status":  200,
			"url":     "https://api.example.com/items",
		}

		req, err := evalNextPage(responseMap, `has(response.body.nextLink) ? response.body.nextLink : null`)
		Expect(err).ToNot(HaveOccurred())
		Expect(req).To(BeNil())
	})

	It("should return nil for empty string", func() {
		responseMap := map[string]any{
			"body":    map[string]any{"next": ""},
			"headers": map[string]string{},
			"status":  200,
			"url":     "https://api.example.com/items",
		}

		req, err := evalNextPage(responseMap, `response.body.next`)
		Expect(err).ToNot(HaveOccurred())
		Expect(req).To(BeNil())
	})

	It("should include headers from map return", func() {
		responseMap := map[string]any{
			"body":    map[string]any{"cursor": "xyz"},
			"headers": map[string]string{"X-Request-Id": "req-1"},
			"status":  200,
			"url":     "https://api.example.com/data",
		}

		expr := `{"url": response.url + "?cursor=" + response.body.cursor, "headers": {"X-Request-Id": response.headers["X-Request-Id"]}}`
		req, err := evalNextPage(responseMap, expr)
		Expect(err).ToNot(HaveOccurred())
		Expect(req).ToNot(BeNil())
		Expect(req.URL).To(Equal("https://api.example.com/data?cursor=xyz"))
		Expect(req.Method).To(Equal("GET"))
		Expect(req.Headers).To(Equal(map[string]string{"X-Request-Id": "req-1"}))
	})

	It("should allow status code access in CEL", func() {
		responseMap := map[string]any{
			"body":    map[string]any{"next": "https://next.page"},
			"headers": map[string]string{},
			"status":  200,
			"url":     "https://api.example.com/items",
		}

		req, err := evalNextPage(responseMap, `response.status == 200 ? response.body.next : null`)
		Expect(err).ToNot(HaveOccurred())
		Expect(req).ToNot(BeNil())
		Expect(req.URL).To(Equal("https://next.page"))
	})
})

var _ = Describe("evalReduce", func() {
	It("should concatenate lists", func() {
		acc := []any{"a", "b"}
		pageBody := map[string]any{"value": []any{"c", "d"}}

		result, err := evalReduce(acc, pageBody, `acc + page.value`)
		Expect(err).ToNot(HaveOccurred())
		Expect(result).To(Equal([]any{"a", "b", "c", "d"}))
	})

	It("should handle nil accumulator", func() {
		var acc []any
		pageBody := map[string]any{"items": []any{1, 2, 3}}

		result, err := evalReduce(acc, pageBody, `acc + page.items`)
		Expect(err).ToNot(HaveOccurred())
		Expect(result).To(Equal([]any{int64(1), int64(2), int64(3)}))
	})
})

var _ = Describe("defaultReduce", func() {
	It("should append array page to accumulator", func() {
		Expect(defaultReduce([]any{"a"}, []any{"b", "c"})).To(Equal([]any{"a", "b", "c"}))
	})

	It("should extract 'value' key from map page", func() {
		Expect(defaultReduce([]any{"a"}, map[string]any{"value": []any{"b", "c"}})).To(Equal([]any{"a", "b", "c"}))
	})

	It("should extract 'items' key from map page", func() {
		Expect(defaultReduce([]any{"a"}, map[string]any{"items": []any{"b"}})).To(Equal([]any{"a", "b"}))
	})

	It("should extract 'Items' key from map page", func() {
		Expect(defaultReduce([]any{"a"}, map[string]any{"Items": []any{"b"}})).To(Equal([]any{"a", "b"}))
	})

	It("should append whole map when no known key exists", func() {
		page := map[string]any{"data": "something"}
		Expect(defaultReduce([]any{"a"}, page)).To(Equal([]any{"a", page}))
	})
})

var _ = Describe("parseDelay", func() {
	DescribeTable("parsing duration strings",
		func(input string, expected time.Duration, wantErr bool) {
			d, err := parseDelay(input)
			if wantErr {
				Expect(err).To(HaveOccurred())
			} else {
				Expect(err).ToNot(HaveOccurred())
				Expect(d).To(Equal(expected))
			}
		},
		Entry("empty string", "", time.Duration(0), false),
		Entry("milliseconds", "500ms", 500*time.Millisecond, false),
		Entry("seconds", "2s", 2*time.Second, false),
		Entry("minutes", "1m", time.Minute, false),
		Entry("invalid", "invalid", time.Duration(0), true),
	)
})

var _ = Describe("parseJSON", func() {
	DescribeTable("parsing JSON strings",
		func(input string, expected any) {
			Expect(parseJSON(input)).To(Equal(expected))
		},
		Entry("array", `[1,2,3]`, []any{float64(1), float64(2), float64(3)}),
		Entry("object", `{"key":"val"}`, map[string]any{"key": "val"}),
		Entry("plain text", `not json`, "not json"),
	)
})
