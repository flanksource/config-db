package v1

import (
	"github.com/flanksource/duty/connection"
	"github.com/flanksource/duty/types"
)

type HTTP struct {
	BaseScraper               `json:",inline"`
	connection.HTTPConnection `json:",inline"`
	// Environment variables to be used in the templating.
	Env    []types.EnvVar `json:"env,omitempty"`
	Method *string        `json:"method,omitempty"`
	Body   *string        `json:"body,omitempty"`
}
