package v1

import (
	"github.com/flanksource/duty/connection"
	"github.com/flanksource/duty/shell"
	"github.com/flanksource/duty/types"
)

type Playwright struct {
	BaseScraper `json:",inline"`
	// Script is an inline TypeScript/JavaScript to run with Playwright.
	Script string `json:"script" yaml:"script" template:"true"`
	// Connections for AWS/GCP/Azure/K8s credential injection
	Connections *connection.ExecConnections `json:"connections,omitempty" yaml:"connections,omitempty" template:"true"`
	// Checkout is a git repository to check out the script from
	Checkout *connection.GitConnection `json:"checkout,omitempty" yaml:"checkout,omitempty"`
	// Artifacts are additional artifact paths to collect after execution
	Artifacts []shell.Artifact `json:"artifacts,omitempty" yaml:"artifacts,omitempty" template:"true"`
	// OutputMode controls how stdout is parsed: "json" (default) or "raw"
	OutputMode string `json:"outputMode,omitempty" yaml:"outputMode,omitempty"`
	// Login provider for auto-login (AWS federation, browser cookies)
	Login *PlaywrightLoginProvider `json:"login,omitempty"`
	// Headless mode (default true)
	Headless *bool `json:"headless,omitempty"`
	// Timeout in seconds for the script execution (default 300)
	Timeout int `json:"timeout,omitempty"`
	// Trace configures HAR, video, and network recording
	Trace *PlaywrightTrace `json:"trace,omitempty" yaml:"trace,omitempty"`
	// HAR enables HAR (HTTP Archive) recording
	HAR bool `json:"har,omitempty"`
	// Env additional environment variables for the script
	Env []types.EnvVar `json:"env,omitempty" yaml:"env,omitempty"`
	// Query exports config items as JSON files for use in scripts
	Query []ConfigQuery `json:"query,omitempty" yaml:"query,omitempty"`
}

type PlaywrightLoginProvider struct {
	AWS     *PlaywrightAWSLogin     `json:"aws,omitempty"`
	Browser *PlaywrightBrowserLogin `json:"browser,omitempty"`
}

type PlaywrightBrowserLogin struct {
	ConnectionName string `json:"connection" yaml:"connection"`
}

type PlaywrightTrace struct {
	HAR     bool     `json:"har,omitempty" yaml:"har,omitempty"`
	Domains []string `json:"domains,omitempty" yaml:"domains,omitempty"`
	Video   string   `json:"video,omitempty" yaml:"video,omitempty"`
}

type PlaywrightAWSLogin struct {
	AWSConnection   `json:",inline"`
	SessionDuration int    `json:"sessionDuration,omitempty"`
	Issuer          string `json:"issuer,omitempty"`
	Login           string `json:"login,omitempty" yaml:"login,omitempty"`
}
