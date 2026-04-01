package playwright

import (
	"encoding/json"
	"fmt"

	"github.com/flanksource/commons/har"
	"github.com/flanksource/config-db/api"
	"github.com/flanksource/duty/shell"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	v1 "github.com/flanksource/config-db/api/v1"
)

var _ = Describe("PlaywrightScraper", func() {

	Describe("CanScrape", func() {
		scraper := PlaywrightScraper{}

		It("should return true when Playwright configs exist", func() {
			spec := v1.ScraperSpec{
				Playwright: []v1.Playwright{{Script: "console.log('hi')"}},
			}
			Expect(scraper.CanScrape(spec)).To(BeTrue())
		})

		It("should return false when no Playwright configs", func() {
			spec := v1.ScraperSpec{}
			Expect(scraper.CanScrape(spec)).To(BeFalse())
		})

		It("should return false for other scraper types", func() {
			spec := v1.ScraperSpec{
				Exec: []v1.Exec{{Script: "echo hi"}},
			}
			Expect(scraper.CanScrape(spec)).To(BeFalse())
		})
	})

	Describe("defaultArtifacts", func() {
		It("should include stdout and stderr capture", func() {
			paths := make([]string, len(defaultArtifacts))
			for i, a := range defaultArtifacts {
				paths[i] = a.Path
			}
			Expect(paths).To(ContainElements(
				"/dev/stdout",
				"/dev/stderr",
			))
		})
	})

	Describe("artifact merging", func() {
		It("should merge default and user artifacts", func() {
			userArtifacts := []shell.Artifact{
				{Path: "custom-output/**/*"},
			}

			merged := make([]shell.Artifact, 0, len(defaultArtifacts)+len(userArtifacts))
			merged = append(merged, defaultArtifacts...)
			merged = append(merged, userArtifacts...)

			Expect(merged).To(HaveLen(len(defaultArtifacts) + 1))
			Expect(merged[len(merged)-1].Path).To(Equal("custom-output/**/*"))
		})
	})
})

var _ = Describe("shouldIgnoreHAREntry", func() {
	It("should ignore font files", func() {
		entry := &har.Entry{Request: har.Request{URL: "https://example.com/font.woff2"}}
		Expect(shouldIgnoreHAREntry(entry)).To(BeTrue())
	})

	It("should ignore images", func() {
		entry := &har.Entry{Request: har.Request{URL: "https://example.com/logo.png"}}
		Expect(shouldIgnoreHAREntry(entry)).To(BeTrue())
	})

	It("should ignore JS files by extension", func() {
		entry := &har.Entry{Request: har.Request{URL: "https://example.com/app.js?v=123"}}
		Expect(shouldIgnoreHAREntry(entry)).To(BeTrue())
	})

	It("should ignore by mimeType", func() {
		entry := &har.Entry{
			Request:  har.Request{URL: "https://example.com/chunk"},
			Response: har.Response{Content: har.Content{MimeType: "text/javascript"}},
		}
		Expect(shouldIgnoreHAREntry(entry)).To(BeTrue())
	})

	It("should ignore mimeType with charset parameter", func() {
		entry := &har.Entry{
			Request:  har.Request{URL: "https://example.com/bundle"},
			Response: har.Response{Content: har.Content{MimeType: "text/javascript; charset=UTF-8"}},
		}
		Expect(shouldIgnoreHAREntry(entry)).To(BeTrue())
	})

	It("should ignore image/* mimeType", func() {
		entry := &har.Entry{
			Request:  har.Request{URL: "https://example.com/data"},
			Response: har.Response{Content: har.Content{MimeType: "image/webp"}},
		}
		Expect(shouldIgnoreHAREntry(entry)).To(BeTrue())
	})

	It("should not ignore API calls", func() {
		entry := &har.Entry{
			Request:  har.Request{URL: "https://api.example.com/v1/data"},
			Response: har.Response{Content: har.Content{MimeType: "application/json"}},
		}
		Expect(shouldIgnoreHAREntry(entry)).To(BeFalse())
	})

	It("should not ignore HTML pages", func() {
		entry := &har.Entry{
			Request:  har.Request{URL: "https://example.com/page"},
			Response: har.Response{Content: har.Content{MimeType: "text/html"}},
		}
		Expect(shouldIgnoreHAREntry(entry)).To(BeFalse())
	})
})

var _ = Describe("sanitizeHAREntry", func() {
	It("should redact authorization headers", func() {
		entry := &har.Entry{
			Request: har.Request{
				Headers: []har.Header{
					{Name: "Authorization", Value: "Bearer secret-token-12345"},
					{Name: "Content-Type", Value: "application/json"},
				},
			},
			Response: har.Response{
				Headers: []har.Header{
					{Name: "Set-Cookie", Value: "session=abc123"},
				},
			},
		}
		sanitizeHAREntry(entry)

		for _, h := range entry.Request.Headers {
			if h.Name == "Authorization" {
				Expect(h.Value).NotTo(Equal("Bearer secret-token-12345"))
				Expect(h.Value).To(ContainSubstring("****"))
			}
		}
	})

	It("should clear cookies", func() {
		entry := &har.Entry{
			Request: har.Request{
				Cookies: []har.Cookie{{Name: "session", Value: "abc"}},
			},
			Response: har.Response{
				Cookies: []har.Cookie{{Name: "token", Value: "xyz"}},
			},
		}
		sanitizeHAREntry(entry)
		Expect(entry.Request.Cookies).To(BeNil())
		Expect(entry.Response.Cookies).To(BeNil())
	})

	It("should strip non-JSON response bodies", func() {
		entry := &har.Entry{
			Response: har.Response{
				Content: har.Content{
					MimeType: "text/html",
					Text:     "<html><body>big page</body></html>",
					Size:     33,
				},
			},
		}
		sanitizeHAREntry(entry)
		Expect(entry.Response.Content.Text).To(BeEmpty())
		Expect(entry.Response.Content.Size).To(Equal(int64(33)))
	})

	It("should keep JSON response bodies", func() {
		entry := &har.Entry{
			Response: har.Response{
				Content: har.Content{
					MimeType: "application/json",
					Text:     `{"key":"value"}`,
				},
			},
		}
		sanitizeHAREntry(entry)
		Expect(entry.Response.Content.Text).To(Equal(`{"key":"value"}`))
	})
})

var _ = Describe("traceEnabled", func() {
	It("should return true for 'on'", func() {
		Expect(traceEnabled("on")).To(BeTrue())
	})

	It("should return true for 'retain-on-failure'", func() {
		Expect(traceEnabled("retain-on-failure")).To(BeTrue())
	})

	It("should return false for empty string", func() {
		Expect(traceEnabled("")).To(BeFalse())
	})

	It("should return false for 'off'", func() {
		Expect(traceEnabled("off")).To(BeFalse())
	})
})

var _ = Describe("buildEnv", func() {
	It("should inject PW_TRACE when trace is enabled", func() {
		config := v1.Playwright{Trace: "on"}
		ctx := api.NewScrapeContext(DefaultContext)
		env := buildEnv(ctx, config)
		var names []string
		for _, e := range env {
			names = append(names, e.Name)
		}
		Expect(names).To(ContainElement("PW_TRACE"))
	})

	It("should inject PW_HAR when HAR is enabled", func() {
		config := v1.Playwright{HAR: true}
		ctx := api.NewScrapeContext(DefaultContext)
		env := buildEnv(ctx, config)
		var names []string
		for _, e := range env {
			names = append(names, e.Name)
		}
		Expect(names).To(ContainElement("PW_HAR"))
	})

	It("should not inject trace/har env when disabled", func() {
		config := v1.Playwright{}
		ctx := api.NewScrapeContext(DefaultContext)
		env := buildEnv(ctx, config)
		var names []string
		for _, e := range env {
			names = append(names, e.Name)
		}
		Expect(names).NotTo(ContainElement("PW_TRACE"))
		Expect(names).NotTo(ContainElement("PW_HAR"))
		Expect(names).NotTo(ContainElement("PWDEBUG"))
	})
})

var _ = Describe("Playwright Integration", Label("slow"), Ordered, func() {
	It("should scrape using playwright fixture", func() {
		scrapeConfig := getConfigSpec("playwright-simple")

		scraperCtx := api.NewScrapeContext(DefaultContext).WithScrapeConfig(&scrapeConfig)

		scraper := PlaywrightScraper{}
		results := scraper.Scrape(scraperCtx)

		Expect(results).To(HaveLen(1), "Expected 1 config item from playwright script")

		result := results[0]
		Expect(result.Error).To(BeNil(), "Result should not have error")
		Expect(result.Config).NotTo(BeNil(), "Result should have config")

		configStr, ok := result.Config.(string)
		Expect(ok).To(BeTrue(), "Config should be a string")

		var config map[string]any
		err := json.Unmarshal([]byte(configStr), &config)
		Expect(err).NotTo(HaveOccurred())
		Expect(config["url"]).To(ContainSubstring("google.com/finance"))
		Expect(config["title"]).NotTo(BeEmpty())

		fmt.Printf("Playwright scrape result: %s\n", configStr)
	})
})
