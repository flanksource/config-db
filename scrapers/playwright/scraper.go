package playwright

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/flanksource/commons/collections"
	"github.com/flanksource/commons/har"
	"github.com/flanksource/duty/artifact"
	dutyModels "github.com/flanksource/duty/models"
	"github.com/google/uuid"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty/query"
)

const defaultDestination = "https://console.aws.amazon.com/"

type Scraper struct{}

func (s Scraper) CanScrape(configs v1.ScraperSpec) bool {
	return len(configs.Playwright) > 0
}

func (s Scraper) Scrape(ctx api.ScrapeContext) v1.ScrapeResults {
	ctx.Context = ctx.Context.WithName("playwright")
	var results v1.ScrapeResults
	for _, config := range ctx.ScrapeConfig().Spec.Playwright {
		scraped, err := scrape(ctx, config)
		if err != nil {
			results = append(results, v1.NewScrapeResult(config.BaseScraper).Errorf("%v", err))
		} else {
			results = append(results, scraped...)
		}
	}
	return results
}

func scrape(ctx api.ScrapeContext, config v1.Playwright) (v1.ScrapeResults, error) {
	if err := ensureBrowsers(ctx); err != nil {
		return nil, fmt.Errorf("installing browsers: %w", err)
	}

	headless := true
	if config.Headless != nil {
		headless = *config.Headless
	}

	b, err := NewBrowser(BrowserOptions{
		Headless:    headless,
		UserDataDir: ctx.ScrapeConfig().Annotations["playwright.userDataDir"],
		Keep:        strings.EqualFold(ctx.ScrapeConfig().Annotations["playwright.keep"], "true"),
		Trace:       ctx.IsTrace(),
		TraceConfig: config.Trace,
		ScraperName: ctx.ScrapeConfig().Namespace + "_" + ctx.ScrapeConfig().Name,
		Logger:      ctx.Logger,
	})
	if err != nil {
		return nil, err
	}
	defer b.Close()

	env := buildEnv(config)

	if config.Login != nil {
		if err := login(ctx, b, *config.Login, &env); err != nil {
			return nil, fmt.Errorf("logging in: %w", err)
		}
	}

	timeout := 300 * time.Second
	if config.Timeout > 0 {
		timeout = time.Duration(config.Timeout) * time.Second
	}
	for _, e := range config.Env {
		val, err := ctx.GetEnvValueFromCache(e, ctx.GetNamespace())
		if err != nil {
			return nil, fmt.Errorf("resolving env var %s: %w", e.Name, err)
		}
		env = append(env, e.Name+"="+val)
	}

	if len(config.Query) > 0 {
		if err := runQueries(ctx, config.Query, b.QueryDir); err != nil {
			return nil, fmt.Errorf("running queries: %w", err)
		}
	}

	result, runErr := b.Run(config.Script, env, timeout)

	// Merge HAR even on script failure — network traffic is valuable for debugging
	if result != nil && result.HARFile != "" {
		var domains []string
		if config.Trace != nil {
			domains = config.Trace.Domains
		}
		if err := mergeHAR(ctx, result.HARFile, domains); err != nil {
			ctx.Logger.Errorf("failed to merge HAR: %v", err)
		}
	}

	if runErr != nil {
		return nil, runErr
	}

	results, err := parseOutput(ctx, config, result.ScriptOutput)
	if err != nil {
		return nil, err
	}

	uploadArtifacts(ctx, results)
	return results, nil
}

func login(ctx api.ScrapeContext, b *Browser, provider v1.PlaywrightLoginProvider, env *[]string) error {
	if provider.AWS != nil {
		loginURL, err := getAWSConsoleLoginURL(ctx, *provider.AWS, defaultDestination)
		if err != nil {
			return fmt.Errorf("generating AWS login URL: %w", err)
		}

		if strings.EqualFold(provider.AWS.Login, "playwright") {
			ctx.Logger.Infof("passing login URL to playwright script")
			*env = append(*env, "BROWSER_LOGIN_URL="+loginURL)
			return nil
		}

		ctx.Logger.V(2).Infof("navigating to AWS federation login URL (length=%d)", len(loginURL))
		if err := b.Login(loginURL, 60*time.Second); err != nil {
			return fmt.Errorf("AWS console login: %w", err)
		}
		return nil
	}

	if provider.Browser != nil {
		path, err := loginWithBrowser(ctx, *provider.Browser)
		if err != nil {
			return fmt.Errorf("browser login: %w", err)
		}
		b.StorageState = path
		return nil
	}

	return fmt.Errorf("no login provider configured")
}

func buildEnv(config v1.Playwright) []string {
	var env []string
	if config.Login != nil && config.Login.AWS != nil {
		region := "us-east-1"
		if len(config.Login.AWS.Regions) > 0 {
			region = config.Login.AWS.Regions[0]
		}
		env = append(env, "AWS_REGION="+region)
	}
	return env
}

var ignoredExtensions = []string{
	"woff", "woff2", "ttf", "eot",
	"png", "jpeg", "jpg", "gif", "webp", "svg", "ico",
	"js", "css",
	"mp3", "mp4", "wav",
	"wasm",
}

var ignoredMimeTypes = []string{
	"image/", "font/", "audio/", "video/",
	"text/css", "text/javascript",
	"application/javascript", "application/x-javascript",
	"application/wasm", "application/octet-stream", "application/font",
}

func mergeHAR(ctx api.ScrapeContext, harPath string, domains []string) error {
	collector := ctx.HARCollector()
	if collector == nil {
		ctx.Logger.Infof("no HAR collector on context, HAR file preserved at %s", harPath)
		return nil
	}
	defer os.Remove(harPath) //nolint:errcheck

	data, err := os.ReadFile(harPath)
	if err != nil {
		return fmt.Errorf("reading HAR file: %w", err)
	}

	var harFile har.File
	if err := json.Unmarshal(data, &harFile); err != nil {
		return fmt.Errorf("parsing HAR file: %w", err)
	}

	merged, skipped, domainFiltered := 0, 0, 0
	for i := range harFile.Log.Entries {
		entry := &harFile.Log.Entries[i]
		if shouldSkipHAREntry(entry) {
			skipped++
			continue
		}
		if len(domains) > 0 && !collections.MatchItems(entry.Request.URL, domains...) {
			domainFiltered++
			continue
		}
		collector.Add(entry)
		merged++
	}

	ctx.Logger.Infof("merged %d HAR entries from %s (skipped %d static, %d domain-filtered)", merged, harPath, skipped, domainFiltered)
	return nil
}

func shouldSkipHAREntry(entry *har.Entry) bool {
	if u := entry.Request.URL; u != "" {
		ext := strings.ToLower(filepath.Ext(strings.SplitN(u, "?", 2)[0]))
		ext = strings.TrimPrefix(ext, ".")
		for _, ignored := range ignoredExtensions {
			if ext == ignored {
				return true
			}
		}
	}

	mime := strings.ToLower(entry.Response.Content.MimeType)
	if i := strings.IndexByte(mime, ';'); i >= 0 {
		mime = strings.TrimSpace(mime[:i])
	}
	for _, prefix := range ignoredMimeTypes {
		if strings.HasPrefix(mime, prefix) {
			return true
		}
	}
	return false
}

func uploadArtifacts(ctx api.ScrapeContext, results v1.ScrapeResults) {
	blobs, err := ctx.DutyContext().Blobs()
	if err != nil {
		ctx.Logger.V(2).Infof("no blob store available, skipping artifact upload: %v", err)
		return
	}
	defer blobs.Close() //nolint:errcheck

	for _, result := range results {
		for i, change := range result.Changes {
			if change.Details == nil {
				continue
			}
			artifacts, ok := change.Details["artifacts"].([]any)
			if !ok {
				continue
			}
			for _, item := range artifacts {
				art, ok := item.(map[string]any)
				if !ok {
					continue
				}
				localPath, ok := art["path"].(string)
				if !ok {
					continue
				}

				data, err := os.ReadFile(localPath)
				if err != nil {
					ctx.Logger.Errorf("failed to read artifact %s: %v", localPath, err)
					continue
				}

				filename := filepath.Base(localPath)
				a := &dutyModels.Artifact{
					Path:     fmt.Sprintf("playwright/%s/%s", ctx.ScrapeConfig().Name, filename),
					Filename: filename,
				}
				artifactData := artifact.Data{
					Content:       io.NopCloser(bytes.NewReader(data)),
					ContentLength: int64(len(data)),
					Filename:      filename,
					ContentType:   "image/png",
				}

				saved, err := blobs.Write(artifactData, a)
				if err != nil {
					ctx.Logger.Errorf("failed to upload artifact %s: %v", a.Path, err)
					continue
				}

				delete(art, "path")
				art["blobPath"] = saved.Path
				art["artifactId"] = saved.ID.String()
				ctx.Logger.Infof("uploaded artifact %s (%d bytes)", saved.Path, len(data))
			}
			result.Changes[i].Details = change.Details
		}
	}
}

func runQueries(ctx api.ScrapeContext, queries []v1.ConfigQuery, workDir string) error {
	for _, q := range queries {
		items, err := query.FindConfigsByResourceSelector(ctx.Context, 0, q.ResourceSelector)
		if err != nil {
			return fmt.Errorf("query for %s: %w", q.Path, err)
		}

		data, err := json.Marshal(items)
		if err != nil {
			return fmt.Errorf("marshaling results for %s: %w", q.Path, err)
		}

		outPath := filepath.Join(workDir, q.Path)
		os.MkdirAll(filepath.Dir(outPath), 0755) //nolint:errcheck
		if err := os.WriteFile(outPath, data, 0644); err != nil {
			return fmt.Errorf("writing results to %s: %w", q.Path, err)
		}

		ctx.Logger.V(2).Infof("query exported %d items to %s", len(items), outPath)
	}
	return nil
}

var browsersInstalled bool

func ensureBrowsers(ctx api.ScrapeContext) error {
	if browsersInstalled {
		return nil
	}

	ctx.Logger.V(2).Infof("installing chromium browser")
	cmd := exec.CommandContext(ctx.Context, "bunx", "playwright", "install", "chromium")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w\noutput: %s", err, string(out))
	}

	ctx.Logger.V(2).Infof("chromium browser installed")
	browsersInstalled = true
	return nil
}

type scriptOutput struct {
	Data    any              `json:"data"`
	Changes []map[string]any `json:"changes"`
}

func parseOutput(ctx api.ScrapeContext, config v1.Playwright, output string) (v1.ScrapeResults, error) {
	output = strings.TrimSpace(output)
	if output == "" {
		return nil, nil
	}

	var wrapped scriptOutput
	if err := json.Unmarshal([]byte(output), &wrapped); err != nil {
		return parseRawOutput(config, output)
	}

	if wrapped.Data == nil && len(wrapped.Changes) == 0 {
		return parseRawOutput(config, output)
	}

	var results v1.ScrapeResults
	if wrapped.Data != nil {
		var err error
		results, err = parseData(config, wrapped.Data)
		if err != nil {
			return nil, err
		}
	}

	changes := parseChanges(wrapped.Changes)
	if len(changes) > 0 {
		ctx.Logger.Infof("parsed %d changes from script output", len(changes))
		for _, c := range changes {
			ctx.Logger.V(2).Infof("  change: type=%s config_id=%s external_id=%s summary=%s", c.ChangeType, c.ConfigID, c.ExternalID, c.Summary)
		}
	}

	if len(results) == 0 && len(changes) > 0 {
		result := v1.NewScrapeResult(config.BaseScraper)
		result.Changes = changes
		results = append(results, *result)
	} else {
		for i := range results {
			results[i].Changes = append(results[i].Changes, changes...)
		}
	}

	return results, nil
}

func parseRawOutput(config v1.Playwright, output string) (v1.ScrapeResults, error) {
	var jsonData any
	if err := json.Unmarshal([]byte(output), &jsonData); err != nil {
		result := v1.NewScrapeResult(config.BaseScraper)
		return v1.ScrapeResults{result.Success(output)}, nil
	}
	return parseData(config, jsonData)
}

func parseData(config v1.Playwright, data any) (v1.ScrapeResults, error) {
	var results v1.ScrapeResults
	switch typed := data.(type) {
	case []any:
		for _, item := range typed {
			result := v1.NewScrapeResult(config.BaseScraper)
			jsonStr, err := json.Marshal(item)
			if err != nil {
				return nil, fmt.Errorf("marshaling item: %w", err)
			}
			results = append(results, result.Success(string(jsonStr)))
		}
	default:
		result := v1.NewScrapeResult(config.BaseScraper)
		jsonStr, err := json.Marshal(typed)
		if err != nil {
			return nil, fmt.Errorf("marshaling result: %w", err)
		}
		results = append(results, result.Success(string(jsonStr)))
	}
	return results, nil
}

func parseChanges(raw []map[string]any) []v1.ChangeResult {
	var changes []v1.ChangeResult
	for _, c := range raw {
		change := v1.ChangeResult{
			Details: make(map[string]any),
		}
		if v, ok := c["change_type"].(string); ok {
			change.ChangeType = v
		}
		if v, ok := c["config_id"].(string); ok {
			if _, err := uuid.Parse(v); err == nil {
				change.ConfigID = v
			} else {
				change.ExternalID = v
			}
		}
		if v, ok := c["external_id"].(string); ok {
			change.ExternalID = v
		}
		if v, ok := c["config_type"].(string); ok {
			change.ConfigType = v
		}
		if v, ok := c["summary"].(string); ok {
			change.Summary = v
		}
		if v, ok := c["severity"].(string); ok {
			change.Severity = v
		}
		if v, ok := c["source"].(string); ok {
			change.Source = v
		}
		if v, ok := c["external_change_id"].(string); ok {
			change.ExternalChangeID = v
		}
		if v, ok := c["scraper_id"].(string); ok {
			change.ScraperID = v
		} else {
			change.ScraperID = "all"
		}
		if v, ok := c["details"].(map[string]any); ok {
			change.Details = v
		}
		changes = append(changes, change)
	}
	return changes
}
