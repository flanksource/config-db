package v1

import (
	"github.com/flanksource/duty/connection"
	"github.com/flanksource/duty/shell"
	"github.com/flanksource/duty/types"
)

type Exec struct {
	BaseScraper `json:",inline"`

	// Script can be inline script or path to script file
	Script string `json:"script" yaml:"script" template:"true"`

	// Connections for AWS/GCP/Azure/K8s credential injection
	Connections connection.ExecConnections `json:"connections,omitempty" yaml:"connections,omitempty" template:"true"`

	// Git repository to checkout before running script
	Checkout *connection.GitConnection `json:"checkout,omitempty" yaml:"checkout,omitempty"`

	// Environment variables
	Env []types.EnvVar `json:"env,omitempty" yaml:"env,omitempty"`

	// Artifacts to collect after execution
	Artifacts []shell.Artifact `json:"artifacts,omitempty" yaml:"artifacts,omitempty" template:"true"`
}
