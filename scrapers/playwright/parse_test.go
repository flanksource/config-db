//go:build e2e

package playwright

import (
	"context"
	"os/exec"
	"testing"
	"time"

	v1 "github.com/flanksource/config-db/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/flanksource/config-db/api"
	dutyContext "github.com/flanksource/duty/context"
)

func TestPlaywright(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Playwright Suite")
}

var _ = Describe("parseOutput", func() {
	var ctx api.ScrapeContext
	var config v1.Playwright

	BeforeEach(func() {
		ctx = api.NewScrapeContext(dutyContext.New())
		config = v1.Playwright{}
		config.BaseScraper.Type = "Test::Type"
	})

	It("should parse raw JSON array", func() {
		results, err := parseOutput(ctx, config, `[{"id":"1"},{"id":"2"}]`)
		Expect(err).ToNot(HaveOccurred())
		Expect(results).To(HaveLen(2))
	})

	It("should parse raw JSON object", func() {
		results, err := parseOutput(ctx, config, `{"id":"1","name":"foo"}`)
		Expect(err).ToNot(HaveOccurred())
		Expect(results).To(HaveLen(1))
	})

	It("should parse wrapped {data, changes}", func() {
		results, err := parseOutput(ctx, config, `{"data":[{"id":"1"}],"changes":[{"change_type":"Screenshot","config_id":"abc-123","summary":"test"}]}`)
		Expect(err).ToNot(HaveOccurred())
		Expect(results).To(HaveLen(1))
		Expect(results[0].Changes).To(HaveLen(1))
		Expect(results[0].Changes[0].ChangeType).To(Equal("Screenshot"))
		Expect(results[0].Changes[0].ExternalID).To(Equal("abc-123"))
	})

	It("should parse null data with changes only", func() {
		results, err := parseOutput(ctx, config, `{"data":null,"changes":[{"change_type":"Backup","external_id":"db-001","severity":"info"}]}`)
		Expect(err).ToNot(HaveOccurred())
		Expect(results).To(HaveLen(1))
		Expect(results[0].Changes[0].ExternalID).To(Equal("db-001"))
		Expect(results[0].Changes[0].ScraperID).To(Equal("all"))
	})

	It("should route non-UUID config_id to external_id", func() {
		results, err := parseOutput(ctx, config, `{"data":null,"changes":[{"change_type":"Test","config_id":"my-instance"}]}`)
		Expect(err).ToNot(HaveOccurred())
		Expect(results[0].Changes[0].ConfigID).To(BeEmpty())
		Expect(results[0].Changes[0].ExternalID).To(Equal("my-instance"))
	})

	It("should keep UUID config_id as config_id", func() {
		results, err := parseOutput(ctx, config, `{"data":null,"changes":[{"change_type":"Test","config_id":"550e8400-e29b-41d4-a716-446655440000"}]}`)
		Expect(err).ToNot(HaveOccurred())
		Expect(results[0].Changes[0].ConfigID).To(Equal("550e8400-e29b-41d4-a716-446655440000"))
	})

	It("should handle empty output", func() {
		results, err := parseOutput(ctx, config, "")
		Expect(err).ToNot(HaveOccurred())
		Expect(results).To(BeNil())
	})

	It("should handle plain text", func() {
		results, err := parseOutput(ctx, config, "not json")
		Expect(err).ToNot(HaveOccurred())
		Expect(results).To(HaveLen(1))
	})

	It("should parse artifacts in changes", func() {
		results, err := parseOutput(ctx, config, `{"data":null,"changes":[{"change_type":"S","details":{"artifacts":[{"name":"t.png","sha":"abc","size":1024}]}}]}`)
		Expect(err).ToNot(HaveOccurred())
		artifacts := results[0].Changes[0].Details["artifacts"].([]any)
		Expect(artifacts).To(HaveLen(1))
		Expect(artifacts[0].(map[string]any)["name"]).To(Equal("t.png"))
	})
})

var _ = Describe("Browser E2E", Ordered, func() {
	var b *Browser

	BeforeAll(func() {
		_, err := exec.LookPath("bun")
		Expect(err).ToNot(HaveOccurred(), "bun must be installed")

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, "bunx", "playwright", "install", "chromium")
		out, err := cmd.CombinedOutput()
		Expect(err).ToNot(HaveOccurred(), "chromium install failed: "+string(out))

		b, err = NewBrowser(BrowserOptions{Headless: true})
		Expect(err).ToNot(HaveOccurred())
	})

	AfterAll(func() {
		if b != nil {
			b.Close()
		}
	})

	It("should navigate and screenshot via chromedp", func() {
		Expect(b.Navigate("data:text/html,<h1>hello</h1>", 30*time.Second)).To(Succeed())

		screenshot, err := b.TakeScreenshot(30 * time.Second)
		Expect(err).ToNot(HaveOccurred())
		Expect(len(screenshot)).To(BeNumerically(">", 100))
	})

	It("should run a boot script and navigate", func() {
		script := `
const { boot } = require('./playwright-boot');
async function main() {
  const { page, log, screenshot, writeOutput, close } = await boot();
  await page.goto('data:text/html,<h1>Hello Playwright</h1>', { waitUntil: 'domcontentloaded', timeout: 15000 });
  await screenshot('test-page');
  writeOutput({ url: page.url(), title: await page.title() });
  await close();
}
main().catch(e => { process.stderr.write(e.stack + '\n'); process.exit(1); });
`
		result, err := b.Run(script, nil, 60*time.Second)
		Expect(err).ToNot(HaveOccurred())
		Expect(result.ScriptOutput).To(ContainSubstring("Hello Playwright"))
		Expect(result.Screenshots).ToNot(BeEmpty())
	})

	It("should run appendChange with screenshot artifact", func() {
		script := `
const { boot } = require('./playwright-boot');
async function main() {
  const { page, screenshot, appendChange, writeOutput, close } = await boot();
  await page.goto('data:text/html,<h1>Test</h1>', { waitUntil: 'domcontentloaded' });
  const path = await screenshot('test-page');
  appendChange({
    change_type: 'TestScreenshot',
    config_id: 'test-instance-001',
    config_type: 'Test::Instance',
    summary: 'E2E test screenshot',
    screenshot: path,
  });
  writeOutput(null);
  await close();
}
main().catch(e => { process.stderr.write(e.stack + '\n'); process.exit(1); });
`
		ctx := api.NewScrapeContext(dutyContext.New())
		config := v1.Playwright{}
		config.BaseScraper.Type = "Test::Instance"

		result, err := b.Run(script, nil, 60*time.Second)
		Expect(err).ToNot(HaveOccurred())

		results, err := parseOutput(ctx, config, result.ScriptOutput)
		Expect(err).ToNot(HaveOccurred())
		Expect(results).To(HaveLen(1))
		Expect(results[0].Changes).To(HaveLen(1))

		change := results[0].Changes[0]
		Expect(change.ChangeType).To(Equal("TestScreenshot"))
		Expect(change.ExternalID).To(Equal("test-instance-001"))
		Expect(change.ConfigType).To(Equal("Test::Instance"))
		Expect(change.Summary).To(Equal("E2E test screenshot"))
		Expect(change.Details).To(HaveKey("artifacts"))

		artifacts := change.Details["artifacts"].([]any)
		Expect(artifacts).To(HaveLen(1))
		art := artifacts[0].(map[string]any)
		Expect(art["name"]).To(Equal("test-page.png"))
		Expect(art["sha"]).ToNot(BeEmpty())
		Expect(art["size"]).To(BeNumerically(">", 0))
	})

	It("should propagate script errors with result", func() {
		script := `
const { boot } = require('./playwright-boot');
async function main() {
  await boot();
  throw new Error('intentional test error');
}
main().catch(e => { process.stderr.write(e.stack + '\n'); process.exit(1); });
`
		result, err := b.Run(script, nil, 30*time.Second)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("intentional test error"))
		Expect(result).ToNot(BeNil())
	})
})
