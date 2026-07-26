//go:build e2e

package playwright

import (
	"context"
	"os/exec"
	"time"

	v1 "github.com/flanksource/config-db/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/flanksource/config-db/api"
	dutyContext "github.com/flanksource/duty/context"
)

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
