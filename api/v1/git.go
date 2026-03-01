package v1

import "github.com/flanksource/duty/connection"

type GitFileStrategy string

const (
	GitFileStrategyIgnore GitFileStrategy = "ignore"
	GitFileStrategyTrack  GitFileStrategy = "track"
	GitFileStrategyDiff   GitFileStrategy = "diff"
)

type GitFileRule struct {
	Pattern  string          `json:"pattern" yaml:"pattern"`
	Strategy GitFileStrategy `json:"strategy" yaml:"strategy"`
}

// +kubebuilder:object:generate=true
type Git struct {
	BaseScraper              `json:",inline" yaml:",inline"`
	connection.GitConnection `json:",inline" yaml:",inline"`

	// Branches to track. Supports glob patterns (e.g. "release/*").
	// Defaults to the default branch only.
	Branches []string `json:"branches,omitempty" yaml:"branches,omitempty"`

	// Files configures per-glob file handling strategy.
	// Default strategy for unmatched files is "track".
	Files []GitFileRule `json:"files,omitempty" yaml:"files,omitempty"`
}
