package v1

import (
	"github.com/flanksource/duty/connection"
	"github.com/flanksource/duty/shell"
	"github.com/flanksource/duty/types"
)

type Playwright struct {
	BaseScraper `json:",inline"`

	// Script is the inline TypeScript/JavaScript script to run with Playwright.
	// The script should import from 'playwright' and output JSON to stdout.
	Script string `json:"script" yaml:"script" template:"true"`

	// Connections for AWS/GCP/Azure/K8s credential injection
	Connections *connection.ExecConnections `json:"connections,omitempty" yaml:"connections,omitempty" template:"true"`

	// Checkout is a git repository to check out the script from
	Checkout *connection.GitConnection `json:"checkout,omitempty" yaml:"checkout,omitempty"`

	// Env are environment variables passed to the script
	Env []types.EnvVar `json:"env,omitempty" yaml:"env,omitempty"`

	// Artifacts are additional artifact paths to collect after execution
	Artifacts []shell.Artifact `json:"artifacts,omitempty" yaml:"artifacts,omitempty" template:"true"`

	// Trace enables Playwright tracing. The trace zip file is collected as an artifact.
	// Valid values: "on", "off", "retain-on-failure". Defaults to "off".
	Trace string `json:"trace,omitempty" yaml:"trace,omitempty"`

	// HAR enables Playwright HAR recording. The HAR file is collected as an artifact.
	HAR bool `json:"har,omitempty" yaml:"har,omitempty"`

	// OutputMode controls how stdout is parsed.
	// "json" (default): parse stdout as JSON/YAML into config items.
	// "raw": return stdout as a single plain-text config item.
	OutputMode string `json:"outputMode,omitempty" yaml:"outputMode,omitempty"`
}
