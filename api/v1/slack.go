package v1

import "github.com/flanksource/duty/types"

type Slack struct {
	BaseScraper `json:",inline"`

	// Slack token
	Token types.EnvVar `yaml:"token" json:"token"`

	// Duration string
	Since string `yaml:"since,omitempty" json:"since,omitempty"`

	// Process messages from these channels and discard others.
	// If empty, all channels are matched.
	Channels MatchExpressions `yaml:"channels,omitempty" json:"channels,omitempty"`

	// Regexp to capture the fields from messages.
	Regexp string `yaml:"regexp,omitempty" json:"regexp,omitempty"`

	// Rules define the change extraction rules.
	// +kubebuilder:validation:MinItems=1
	Rules []SlackChangeExtractionRule `yaml:"rules" json:"rules"`
}

type SlackChangeExtractionRule struct {
	// Only those messages matching this filter will be processed.
	Filter *SlackChangeAcceptanceFilter `yaml:"filter,omitempty" json:"filter,omitempty"`

	// Mapping defines the Change to be extracted from the message.
	Mapping ChangeExtractionMapping `yaml:"mapping" json:"mapping"`

	// Config is a list of selectors to attach the change to.
	// +kubebuilder:validation:MinItems=1
	Config []EnvVarResourceSelector `yaml:"config" json:"config"`
}

type SlackChangeAcceptanceFilter struct {
	// Bot name to match
	Bot MatchExpression `yaml:"bot,omitempty" json:"bot,omitempty"`

	// Slack User to match
	User SlackUserFilter `yaml:"user,omitempty" json:"user,omitempty"`

	// Filter the message based on the text
	Expr CelExpression `yaml:"expr,omitempty" json:"expr,omitempty"`
}

type SlackUserFilter struct {
	Name        MatchExpression `yaml:"name,omitempty" json:"name,omitempty"`
	DisplayName MatchExpression `yaml:"displayName,omitempty" json:"displayName,omitempty"`
}
