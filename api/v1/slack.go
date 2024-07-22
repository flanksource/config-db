package v1

import "github.com/flanksource/duty/types"

type Slack struct {
	BaseScraper `json:",inline"`

	// Slack token
	Token types.EnvVar `yaml:"token" json:"token"`

	// Fetch the messages since this period.
	// Specify the duration string.
	//   eg: 1h, 7d, ...
	Since string `yaml:"since,omitempty" json:"since,omitempty"`

	// Process messages from these channels and discard others.
	// If empty, all channels are matched.
	Channels MatchExpressions `yaml:"channels,omitempty" json:"channels,omitempty"`

	// Rules define the change extraction rules.
	// +kubebuilder:validation:MinItems=1
	Rules []SlackChangeExtractionRule `yaml:"rules" json:"rules"`
}

type SlackChangeExtractionRule struct {
	ChangeExtractionRule `json:",inline" yaml:",inline"`

	// Only those messages matching this filter will be processed.
	Filter *SlackChangeAcceptanceFilter `yaml:"filter,omitempty" json:"filter,omitempty"`
}

type SlackChangeAcceptanceFilter struct {
	// Bot name to match
	Bot MatchExpression `yaml:"bot,omitempty" json:"bot,omitempty"`

	// Slack User to match
	User SlackUserFilter `yaml:"user,omitempty" json:"user,omitempty"`

	// Must match the given expression
	Expr CelExpression `yaml:"expr,omitempty" json:"expr,omitempty"`
}

type SlackUserFilter struct {
	Name        MatchExpression `yaml:"name,omitempty" json:"name,omitempty"`
	DisplayName MatchExpression `yaml:"displayName,omitempty" json:"displayName,omitempty"`
}
