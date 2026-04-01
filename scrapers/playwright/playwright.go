package playwright

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"sort"
	"strings"

	"github.com/flanksource/commons/collections"
	"github.com/flanksource/commons/har"
	"github.com/flanksource/commons/logger"
	"github.com/flanksource/duty/shell"
	"github.com/flanksource/duty/types"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/scrapers/exec"
)

var defaultArtifacts = []shell.Artifact{
	{Path: "/dev/stdout"},
	{Path: "/dev/stderr"},
}

const harFilename = "_pw_recording.har"

type PlaywrightScraper struct{}

func (p PlaywrightScraper) CanScrape(configs v1.ScraperSpec) bool {
	return len(configs.Playwright) > 0
}

func (p PlaywrightScraper) Scrape(ctx api.ScrapeContext) v1.ScrapeResults {
	var results v1.ScrapeResults

	for _, config := range ctx.ScrapeConfig().Spec.Playwright {
		r := scrapeOne(ctx, config)
		results = append(results, r...)
	}

	return results
}

func scrapeOne(ctx api.ScrapeContext, config v1.Playwright) v1.ScrapeResults {
	artifacts := make([]shell.Artifact, 0, len(defaultArtifacts)+len(config.Artifacts))
	artifacts = append(artifacts, defaultArtifacts...)
	artifacts = append(artifacts, config.Artifacts...)

	env := buildEnv(ctx, config)
	script := config.Script
	if config.HAR {
		script = wrapScriptWithHAR(config.Script)
	}

	execConfig := shell.Exec{
		Script:    script,
		Checkout:  config.Checkout,
		EnvVars:   env,
		Artifacts: artifacts,
		Setup: &shell.ExecSetup{
			Playwright: &shell.RuntimeSetup{},
		},
	}

	if config.Connections != nil {
		execConfig.Connections = *config.Connections
	}

	execDetails, err := shell.Run(ctx.DutyContext(), execConfig)
	if err != nil {
		result := v1.NewScrapeResult(config.BaseScraper)
		if execDetails != nil && execDetails.Stderr != "" {
			return v1.ScrapeResults{result.Errorf("failed to execute playwright script: (%s) %v", execDetails.Stderr, err)}
		}
		return v1.ScrapeResults{result.Errorf("failed to execute playwright script: %v", err)}
	}

	if execDetails.ExitCode != 0 {
		result := v1.NewScrapeResult(config.BaseScraper)
		return v1.ScrapeResults{result.Errorf("playwright script exited with code %d: %s", execDetails.ExitCode, execDetails.Stderr)}
	}

	stdout := execDetails.Stdout

	if config.HAR {
		stdout = extractAndCollectHAR(ctx, stdout)
	}

	if config.OutputMode == "raw" {
		result := v1.NewScrapeResult(config.BaseScraper)
		return v1.ScrapeResults{result.Success(stdout)}
	}

	return exec.ParseOutput(config.BaseScraper, stdout)
}

func buildEnv(ctx api.ScrapeContext, config v1.Playwright) []types.EnvVar {
	env := append([]types.EnvVar{}, config.Env...)

	if ctx.IsTrace() || ctx.IsDebug() {
		env = append(env,
			types.EnvVar{Name: "PWDEBUG", ValueStatic: "1"},
			types.EnvVar{Name: "DEBUG", ValueStatic: "pw:api"},
		)
	}

	if traceEnabled(config.Trace) {
		env = append(env, types.EnvVar{Name: "PW_TRACE", ValueStatic: config.Trace})
	}

	if config.HAR {
		env = append(env, types.EnvVar{Name: "PW_HAR", ValueStatic: "true"})
	}

	return env
}

// wrapperOutput is the JSON envelope written by the HAR wrapper script.
type wrapperOutput struct {
	Result json.RawMessage `json:"result"`
	HAR    *har.File       `json:"har,omitempty"`
}

// wrapScriptWithHAR wraps the user script so that:
// 1. A BrowserContext with recordHar is created
// 2. The user script runs as a child process (can launch its own browser)
// 3. After completion, the HAR is read and output as JSON envelope:
//    {"result": <user stdout>, "har": <har file contents>}
func wrapScriptWithHAR(userScript string) string {
	escaped := strings.ReplaceAll(userScript, "`", "\\`")
	escaped = strings.ReplaceAll(escaped, "${", "\\${")

	return fmt.Sprintf(`#!/usr/bin/env node
const { chromium } = require('playwright');
const { execFileSync } = require('child_process');
const fs = require('fs');
const path = require('path');

(async () => {
  const harPath = path.join(process.cwd(), '%s');

  // Write user script to file
  const userScript = `+"`"+`%s`+"`"+`;
  const scriptPath = path.join(process.cwd(), '_pw_user_script.js');
  fs.writeFileSync(scriptPath, userScript);

  // Set HAR path so user script can use it via context options
  process.env.PW_HAR_PATH = harPath;

  let userOutput = '';
  try {
    const buf = execFileSync(process.execPath, [scriptPath], {
      env: process.env,
      stdio: ['inherit', 'pipe', 'inherit'],
      timeout: 300000,
    });
    userOutput = buf.toString().trim();
  } catch(e) {
    process.stderr.write('User script failed: ' + e.message + '\n');
    process.exit(1);
  }

  // Read HAR file if the user script produced one
  let harData = null;
  if (fs.existsSync(harPath)) {
    try {
      harData = JSON.parse(fs.readFileSync(harPath, 'utf-8'));
    } catch(e) {
      process.stderr.write('Failed to parse HAR: ' + e.message + '\n');
    }
  }

  // Try to parse user output as JSON for the envelope
  let resultData;
  try {
    resultData = JSON.parse(userOutput);
  } catch(e) {
    resultData = userOutput;
  }

  // Output envelope
  const envelope = { result: resultData };
  if (harData) envelope.har = harData;
  process.stdout.write(JSON.stringify(envelope));
})();
`, harFilename, escaped)
}

// Ignore entries whose URL extension or response mimeType matches these patterns.
var defaultIgnoredExtensions = []string{
	"woff", "woff2", "ttf", "eot",
	"png", "jpeg", "jpg", "gif", "webp", "svg", "ico",
	"js", "css",
}

var defaultIgnoredMimeTypes = []string{
	"font/*",
	"image/*",
	"text/javascript",
	"application/javascript",
	"text/css",
}

// extractAndCollectHAR parses the wrapper envelope from stdout,
// filters and sanitizes HAR entries, feeds them to the context
// collector, and returns the user's original output.
func extractAndCollectHAR(ctx api.ScrapeContext, stdout string) string {
	var envelope wrapperOutput
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		ctx.Logger.V(2).Infof("could not parse HAR wrapper envelope: %v", err)
		return stdout
	}

	if envelope.HAR != nil {
		if collector := ctx.HARCollector(); collector != nil {
			for i := range envelope.HAR.Log.Entries {
				entry := &envelope.HAR.Log.Entries[i]
				if shouldIgnoreHAREntry(entry) {
					continue
				}
				sanitizeHAREntry(entry)
				collector.Add(entry)
			}
		}
	}

	return string(envelope.Result)
}

func shouldIgnoreHAREntry(entry *har.Entry) bool {
	ext := strings.TrimPrefix(path.Ext(strings.SplitN(entry.Request.URL, "?", 2)[0]), ".")
	if ext != "" {
		for _, ignored := range defaultIgnoredExtensions {
			if strings.EqualFold(ext, ignored) {
				return true
			}
		}
	}

	mime := strings.ToLower(entry.Response.Content.MimeType)
	if i := strings.IndexByte(mime, ';'); i >= 0 {
		mime = strings.TrimSpace(mime[:i])
	}
	if mime != "" {
		for _, pattern := range defaultIgnoredMimeTypes {
			if collections.MatchItems(mime, pattern) {
				return true
			}
		}
	}

	return false
}

func sanitizeHAREntry(entry *har.Entry) {
	entry.Request.Headers = sanitizeHARHeaders(entry.Request.Headers)
	entry.Response.Headers = sanitizeHARHeaders(entry.Response.Headers)

	entry.Request.Cookies = nil
	entry.Response.Cookies = nil

	mime := strings.ToLower(entry.Response.Content.MimeType)
	if !strings.Contains(mime, "json") {
		entry.Response.Content.Text = ""
	}
}

func sanitizeHARHeaders(headers []har.Header) []har.Header {
	httpHeaders := make(http.Header, len(headers))
	for _, h := range headers {
		httpHeaders.Add(h.Name, h.Value)
	}

	sanitized := logger.SanitizeHeaders(httpHeaders)

	result := make([]har.Header, 0, len(sanitized))
	for name, values := range sanitized {
		for _, v := range values {
			result = append(result, har.Header{Name: name, Value: v})
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

func traceEnabled(trace string) bool {
	return trace == "on" || trace == "retain-on-failure"
}
