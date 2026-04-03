package playwright

import (
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestPlaywright(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Playwright Suite")
}

var _ = Describe("Browser", Ordered, func() {
	var b *Browser

	BeforeAll(func() {
		var err error
		b, err = NewBrowser(BrowserOptions{Headless: true})
		Expect(err).ToNot(HaveOccurred())
	})

	AfterAll(func() {
		b.Close()
	})

	It("should navigate and take a screenshot via chromedp", func() {
		err := b.Navigate("data:text/html,<h1>hello</h1>", 30*time.Second)
		Expect(err).ToNot(HaveOccurred())

		screenshot, err := b.TakeScreenshot(30 * time.Second)
		Expect(err).ToNot(HaveOccurred())
		Expect(len(screenshot)).To(BeNumerically(">", 100))
	})

	It("should run a script using playwright-boot", func() {
		script := `
const { boot } = require('./playwright-boot');

async function main() {
  const { page, log, captureScreenshot, writeOutput, close } = await boot();

  await page.goto('https://www.google.com', { waitUntil: 'domcontentloaded', timeout: 15000 });
  log('navigated to: ' + page.url());

  await captureScreenshot('google');

  writeOutput({
    url: page.url(),
    title: await page.title(),
  });

  await close();
}

main().catch(e => { process.stderr.write(e.stack + '\n'); process.exit(1); });
`
		result, err := b.Run(script, nil, 60*time.Second)
		Expect(err).ToNot(HaveOccurred())
		Expect(result.ScriptOutput).To(ContainSubstring("google"))
		Expect(len(result.Screenshots)).To(BeNumerically(">", 0))
	})
})
