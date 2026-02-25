package v1

import (
	"github.com/flanksource/duty/connection"
	"github.com/flanksource/duty/types"
)

type Pagination struct {
	// CEL expression to extract next page URL or request from response.
	// Receives response map with body, headers, status, url fields.
	// Returns string (URL), map (request spec with url/method/body/headers), or null (stop).
	NextPageExpr string `json:"nextPageExpr"`
	// CEL expression to merge pages using accumulator pattern.
	// Receives acc ([]any, starts empty) and page (response body). Returns new acc.
	ReduceExpr string `json:"reduceExpr,omitempty"`
	// Process each page independently instead of merging.
	PerPage bool `json:"perPage,omitempty"`
	// Maximum number of pages to fetch. 0 means unlimited.
	MaxPages int `json:"maxPages,omitempty"`
	// Delay between page requests (e.g. "500ms", "2s").
	Delay string `json:"delay,omitempty"`
}

type HTTP struct {
	BaseScraper               `json:",inline"`
	connection.HTTPConnection `json:",inline"`
	// Environment variables to be used in the templating.
	Env        []types.EnvVar `json:"env,omitempty"`
	Method     *string        `json:"method,omitempty"`
	Body       *string        `json:"body,omitempty"`
	Pagination *Pagination    `json:"pagination,omitempty"`
}
