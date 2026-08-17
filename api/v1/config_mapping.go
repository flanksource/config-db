package v1

import "github.com/flanksource/duty/types"

// TransformConfigs mirrors TransformChange for scraped config items.
type TransformConfigs struct {
	// Mapping overrides fields on scraped config items. Rules are evaluated in order
	// against pre-mapping values, and the first matching rule wins.
	Mapping []ConfigMapping `json:"mapping,omitempty"`
}

func (t TransformConfigs) IsEmpty() bool {
	return len(t.Mapping) == 0
}

// ConfigMapping overrides fields on a config item when Match evaluates true.
// Output fields are optional; an empty output leaves the scraped value unchanged.
type ConfigMapping struct {
	// Match is a CEL expression selecting which config items this mapping applies to.
	// It receives the full config-item environment, including config, config_type,
	// labels, and tags. A non-match leaves the item unchanged.
	Match string `json:"match,omitempty"`

	// Type replaces the config type. Use value for a literal or expr for a CEL expression.
	Type types.ValueExpression `json:"type,omitempty"`
}
