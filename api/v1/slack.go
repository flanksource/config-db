package v1

import "github.com/flanksource/duty/types"

type Slack struct {
	// Slack token
	Token types.EnvVar `yaml:"token" json:"token"`

	// Process messages from these channels and discard others.
	// If empty, all channels are matched.
	Channels []MatchExpression `yaml:"channels" json:"channels"`

	// Rule defines the change extraction rules
	Rule []SlackChangeExtractionRule `yaml:"rules" json:"rules"`
}

type SlackChangeExtractionRule struct {
	// Only those messages matching this filter will be processed.
	Filter SlackChangeAcceptanceFilter `yaml:"filter" json:"filter"`

	// Mapping defines the Change to be extracted from the message.
	Mapping ChangeExtractionMapping `yaml:"mapping" json:"mapping"`

	// Config is a list of selectors to attach the change to.
	// +kubebuilder:validation:MinItems=1
	Config []ChangeExtractionConfigSelector `yaml:"config" json:"config"`
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
