package playwright

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	_ "embed"

	"github.com/chromedp/chromedp"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/commons/logger"
)

//go:embed playwright-boot.ts
var playwrightBootTS []byte

const playwrightBaseDir = ".config-db-playwright"

type BrowserOptions struct {
	Headless    bool
	UserDataDir string // if set, reuse this dir instead of creating a temp one
	Keep        bool   // when true, preserve working dir on Close
	Trace       bool   // when true, persist all artifacts to CWD for debugging
	ScraperName string // used for trace output directory naming
	TraceConfig *v1.PlaywrightTrace
	Logger      logger.Logger
}

type ConsoleMessage struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type Result struct {
	Console      []ConsoleMessage
	Screenshots  [][]byte
	Video        []byte // placeholder for future use
	ScriptOutput string
	HARFile      string // path to HAR file produced by the script
}

type Browser struct {
	DataDir            string
	WorkDir            string // root working dir for all artifacts
	ScreenshotsDir     string
	QueryDir           string // directory for config query JSON exports
	StorageState       string // path to Playwright storageState JSON file
	SessionStorageFile string // path to JSON {origin, items} for sessionStorage rehydration
	Headless           bool
	trace          bool // when true, enable HAR/video recording and persist artifacts
	keep           bool // when true, skip cleanup on Close (but don't enable tracing)
	traceConfig    *v1.PlaywrightTrace
	logger         logger.Logger
	ownsDir        bool // true if we created the dir and should clean it up
}

func findChromiumBinary() (string, error) {
	cacheDir := filepath.Join(os.Getenv("HOME"), "Library", "Caches", "ms-playwright")
	if runtime.GOOS == "linux" {
		cacheDir = filepath.Join(os.Getenv("HOME"), ".cache", "ms-playwright")
	}

	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		return "", fmt.Errorf("reading playwright cache dir: %w", err)
	}

	var latestChromium string
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "chromium-") && !strings.Contains(e.Name(), "headless") {
			latestChromium = e.Name()
		}
	}
	if latestChromium == "" {
		return "", fmt.Errorf("no chromium installation found in %s", cacheDir)
	}

	base := filepath.Join(cacheDir, latestChromium)
	switch runtime.GOOS {
	case "darwin":
		for _, dir := range []string{"chrome-mac-arm64", "chrome-mac"} {
			p := filepath.Join(base, dir, "Google Chrome for Testing.app", "Contents", "MacOS", "Google Chrome for Testing")
			if _, err := os.Stat(p); err == nil {
				return p, nil
			}
		}
		return "", fmt.Errorf("chromium binary not found in %s", base)
	case "linux":
		// Playwright installs to chrome-linux64/ on 64-bit Linux
		for _, dir := range []string{"chrome-linux64", "chrome-linux"} {
			p := filepath.Join(base, dir, "chrome")
			if _, err := os.Stat(p); err == nil {
				return p, nil
			}
		}
		return "", fmt.Errorf("chromium binary not found in %s", base)
	default:
		return "", fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
}

func ensureBaseDir() string {
	base := filepath.Join(playwrightBaseDir)
	os.MkdirAll(base, 0755) //nolint:errcheck
	return base
}

func NewBrowser(opts BrowserOptions) (*Browser, error) {
	log := opts.Logger
	if log == nil {
		log = logger.StandardLogger()
	}

	base := ensureBaseDir()
	name := opts.ScraperName
	if name == "" {
		name = "playwright"
	}

	// Single working dir for all artifacts — use MkdirTemp to avoid collisions
	runBase := filepath.Join(base, name)
	if err := os.MkdirAll(runBase, 0755); err != nil {
		return nil, fmt.Errorf("creating playwright run base: %w", err)
	}
	workDir, err := os.MkdirTemp(runBase, time.Now().Format("20060102-150405")+"-")
	if err != nil {
		return nil, fmt.Errorf("creating playwright work dir: %w", err)
	}

	dataDir := opts.UserDataDir
	ownsDir := false
	if dataDir == "" {
		dataDir = filepath.Join(workDir, "userdata")
		os.MkdirAll(dataDir, 0755) //nolint:errcheck
		ownsDir = true
	} else {
		os.MkdirAll(dataDir, 0755) //nolint:errcheck
	}

	b := &Browser{
		DataDir:        dataDir,
		WorkDir:        workDir,
		ScreenshotsDir: filepath.Join(workDir, "screenshots"),
		QueryDir:       filepath.Join(workDir, "query"),
		Headless:       opts.Headless,
		logger:         log,
		ownsDir:        ownsDir,
		trace:          opts.Trace,
		keep:           opts.Keep,
		traceConfig:    opts.TraceConfig,
	}
	os.MkdirAll(b.ScreenshotsDir, 0755) //nolint:errcheck
	os.MkdirAll(b.QueryDir, 0755)       //nolint:errcheck

	if opts.Trace {
		log.Infof("trace mode: artifacts will be saved to %s", workDir)
	}

	log.V(2).Infof("working dir: %s (keep=%v)", workDir, opts.Keep || opts.Trace)
	return b, nil
}

func (b *Browser) chromedpOpts() ([]chromedp.ExecAllocatorOption, error) {
	chromiumPath, err := findChromiumBinary()
	if err != nil {
		return nil, err
	}

	opts := append(
		chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(chromiumPath),
		chromedp.UserDataDir(b.DataDir),
		chromedp.Flag("no-sandbox", true),
	)
	if !b.Headless {
		opts = append(opts, chromedp.Flag("headless", false))
	}
	return opts, nil
}

// withChromedp launches Chrome via chromedp, runs actions, then shuts it down.
// Cookies and session data persist in the shared DataDir.
func (b *Browser) withChromedp(timeout time.Duration, actions ...chromedp.Action) error {
	opts, err := b.chromedpOpts()
	if err != nil {
		return err
	}

	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), opts...)
	defer allocCancel()

	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	ctx, timeoutCancel := context.WithTimeout(ctx, timeout)
	defer timeoutCancel()

	return chromedp.Run(ctx, actions...)
}

// Login navigates to the given URL using chromedp, waits for the page to load,
// then shuts down Chrome. Session cookies persist in DataDir.
func (b *Browser) Login(url string, timeout time.Duration) error {
	b.logger.V(2).Infof("logging in via chromedp (timeout=%s)", timeout)

	opts, err := b.chromedpOpts()
	if err != nil {
		return err
	}

	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), opts...)
	defer allocCancel()

	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	ctx, timeoutCancel := context.WithTimeout(ctx, timeout)
	defer timeoutCancel()

	if err := chromedp.Run(ctx, chromedp.Navigate(url)); err != nil {
		return fmt.Errorf("navigating: %w", err)
	}

	// Wait for the AWS console to fully load by waiting for the account menu button
	if err := chromedp.Run(ctx,
		chromedp.WaitVisible(`#nav-usernameMenu`, chromedp.ByID),
	); err != nil {
		b.logger.V(2).Infof("account menu not found, falling back to body wait")
		chromedp.Run(ctx, chromedp.WaitReady("body")) //nolint:errcheck
		chromedp.Run(ctx, chromedp.Sleep(3*time.Second)) //nolint:errcheck
	}

	var accountLabel, finalURL string
	chromedp.Run(ctx, chromedp.Location(&finalURL)) //nolint:errcheck
	chromedp.Run(ctx, chromedp.Text(`[data-testid="account-label"]`, &accountLabel, chromedp.ByQuery, chromedp.NodeVisible)) //nolint:errcheck
	if accountLabel != "" {
		b.logger.Infof("logged in as: %s", strings.TrimSpace(accountLabel))
	}

	if err := checkLoginError(ctx, finalURL); err != nil {
		b.logger.Errorf("login failed at url=%s: %v", finalURL, err)
		return err
	}

	b.logger.Infof("login complete, url=%s, session saved to %s", finalURL, b.DataDir)

	var screenshot []byte
	if err := chromedp.Run(ctx, chromedp.FullScreenshot(&screenshot, 90)); err == nil && len(screenshot) > 0 {
		path := filepath.Join(b.ScreenshotsDir, "login.png")
		os.WriteFile(path, screenshot, 0644) //nolint:errcheck
		b.logger.V(2).Infof("login screenshot saved to %s", path)
	}

	return nil
}

// TakeScreenshot launches Chrome via chromedp, captures a full-page screenshot, then shuts down.
func (b *Browser) TakeScreenshot(timeout time.Duration) ([]byte, error) {
	var buf []byte
	if err := b.withChromedp(timeout, chromedp.FullScreenshot(&buf, 90)); err != nil {
		return nil, err
	}
	return buf, nil
}

// Navigate launches Chrome via chromedp, navigates to the URL, then shuts down.
func (b *Browser) Navigate(url string, timeout time.Duration) error {
	return b.withChromedp(timeout, chromedp.Navigate(url))
}

// Run executes a bun/playwright script. The script should use
// chromium.launchPersistentContext(process.env.PLAYWRIGHT_USER_DATA_DIR, ...)
// to reuse the session from Login.
func (b *Browser) Run(script string, env []string, timeout time.Duration) (*Result, error) {
	scriptPath, cleanup, err := writeScript(script)
	if err != nil {
		return nil, fmt.Errorf("writing script: %w", err)
	}
	defer cleanup()

	resultsPath := filepath.Join(b.WorkDir, "results.json")
	if !b.trace {
		defer os.Remove(resultsPath) //nolint:errcheck
	}

	harEnabled := b.trace || (b.traceConfig != nil && b.traceConfig.HAR)
	videoMode := ""
	if b.traceConfig != nil {
		videoMode = b.traceConfig.Video
	}
	videoEnabled := b.trace || videoMode == "always" || videoMode == "on-error"

	var harFile, videoDir string
	if harEnabled {
		harFile = filepath.Join(b.WorkDir, "trace.har")
	}
	if videoEnabled {
		videoDir = filepath.Join(b.WorkDir, "video")
		os.MkdirAll(videoDir, 0755) //nolint:errcheck
	}

	headlessStr := "true"
	if !b.Headless {
		headlessStr = "false"
	}

	env = append(env,
		"PLAYWRIGHT_USER_DATA_DIR="+b.DataDir,
		"PLAYWRIGHT_OUTPUT_FILE="+resultsPath,
		"PLAYWRIGHT_SCREENSHOTS_DIR="+b.ScreenshotsDir,
		"PLAYWRIGHT_QUERY_DIR="+b.QueryDir,
		"HEADLESS="+headlessStr,
		fmt.Sprintf("TIMEOUT=%d", int(timeout.Seconds())),
	)
	if harFile != "" {
		env = append(env, "PLAYWRIGHT_HAR_FILE="+harFile)
	}
	if videoDir != "" {
		env = append(env, "PLAYWRIGHT_VIDEO_DIR="+videoDir)
	}
	if b.StorageState != "" {
		env = append(env, "PLAYWRIGHT_STORAGE_STATE="+b.StorageState)
	}
	if b.SessionStorageFile != "" {
		env = append(env, "PLAYWRIGHT_SESSION_STORAGE_FILE="+b.SessionStorageFile)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	b.logger.V(2).Infof("running script: %s", scriptPath)
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "bun", "run", scriptPath)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	scriptErr := cmd.Run()
	if stderr.Len() > 0 {
		b.logger.V(3).Infof("script stderr:\n%s", stderr.String())
	}

	// Always collect artifacts, even on script failure
	result := &Result{}
	if harFile != "" {
		if _, err := os.Stat(harFile); err == nil {
			result.HARFile = harFile
		}
	}
	result.Screenshots = collectFiles(b.ScreenshotsDir)
	if videoDir != "" {
		keepVideo := videoMode != "on-error" || scriptErr != nil
		if keepVideo {
			result.Video = firstFile(videoDir)
		}
		if !b.trace {
			os.RemoveAll(videoDir) //nolint:errcheck
		}
	}

	output, _ := os.ReadFile(resultsPath)
	result.ScriptOutput = string(output)

	if b.trace {
		b.logger.Infof("trace artifacts saved to %s", b.WorkDir)
	}

	if scriptErr != nil {
		return result, fmt.Errorf("running script: %w\nstdout: %s\nstderr: %s", scriptErr, stdout.String(), stderr.String())
	}
	return result, nil
}

func collectFiles(dir string) [][]byte {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var files [][]byte
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err == nil && len(data) > 0 {
			files = append(files, data)
		}
	}
	return files
}

func firstFile(dir string) []byte {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err == nil && len(data) > 0 {
			return data
		}
	}
	return nil
}

var loginErrorURLPatterns = []string{
	"/signin", "/login", "/auth", "/oauth", "/sso",
	"login.microsoftonline.com", "signin.aws.amazon.com",
	"accounts.google.com/signin",
}

func checkLoginError(ctx context.Context, finalURL string) error {
	lowerURL := strings.ToLower(finalURL)
	for _, pattern := range loginErrorURLPatterns {
		if strings.Contains(lowerURL, pattern) {
			return fmt.Errorf("login redirect detected (url contains %q): %s", pattern, finalURL)
		}
	}

	var result struct {
		Error string `json:"error"`
		Title string `json:"title"`
		HTTP  int    `json:"http"`
	}
	_ = chromedp.Run(ctx, chromedp.Evaluate(`(() => {
		const title = (document.title || '').toLowerCase();
		const errorKeywords = ['error', 'invalid', 'denied', 'unauthorized', 'forbidden', 'expired', 'sign in', 'log in'];
		const isError = errorKeywords.some(k => title.includes(k));

		let errorMsg = '';
		if (isError) {
			const selectors = [
				'#error_message', '#message_error', '.alert-danger', '.alert-error',
				'[data-testid="error-message"]', '#errorMessage', '.error-message',
				'#content .error', '#content p', '.login-error', '#error',
			];
			for (const sel of selectors) {
				const el = document.querySelector(sel);
				if (el && el.textContent.trim()) { errorMsg = el.textContent.trim(); break; }
			}
			if (!errorMsg) errorMsg = document.title;
		}

		const status = window.performance?.getEntriesByType?.('navigation')?.[0]?.responseStatus || 0;
		return { error: errorMsg, title: document.title, http: status };
	})()`, &result)) //nolint:errcheck

	if result.HTTP >= 400 {
		msg := result.Error
		if msg == "" {
			msg = result.Title
		}
		return fmt.Errorf("HTTP %d: %s", result.HTTP, msg)
	}

	if result.Error != "" {
		return fmt.Errorf("login failed: %s", strings.TrimSpace(result.Error))
	}

	return nil
}

func (b *Browser) Close() {
	if b.trace || b.keep {
		return // preserve all artifacts in trace/keep mode
	}
	os.RemoveAll(b.WorkDir) //nolint:errcheck
}

func writeScript(script string) (string, func(), error) {
	dir, err := os.MkdirTemp("", "playwright-scraper-*")
	if err != nil {
		return "", nil, err
	}

	if err := os.WriteFile(filepath.Join(dir, "playwright-boot.ts"), playwrightBootTS, 0600); err != nil {
		os.RemoveAll(dir) //nolint:errcheck
		return "", nil, err
	}

	ext := ".ts"
	if strings.HasPrefix(strings.TrimSpace(script), "#!/") {
		ext = ".mjs"
	}

	path := filepath.Join(dir, "script"+ext)
	if err := os.WriteFile(path, []byte(script), 0600); err != nil {
		os.RemoveAll(dir) //nolint:errcheck
		return "", nil, err
	}

	return path, func() { os.RemoveAll(dir) }, nil //nolint:errcheck
}
