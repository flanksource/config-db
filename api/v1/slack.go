package v1

import "github.com/flanksource/duty/types"

// Slack scraper creates Slack::Workspace & Slack::Channel config items,
// maps channel members to config access and optionally extracts changes from
// the message history.
type Slack struct {
	BaseScraper `json:",inline"`

	// Slack token
	Token types.EnvVar `yaml:"token" json:"token"`

	// Members maps channel members to external users and config access records.
	// Default: true
	Members *bool `yaml:"members,omitempty" json:"members,omitempty"`

	// Messages reads the message history of every channel and extracts changes
	// from it using the rules below.
	// Default: false
	Messages bool `yaml:"messages,omitempty" json:"messages,omitempty"`

	// Fetch the messages since this period. Requires messages to be enabled.
	// Default: 7d
	//
	// Specify the duration string.
	//   eg: 1h, 7d, ...
	Since string `yaml:"since,omitempty" json:"since,omitempty"`

	// Rules define the change extraction rules applied to the message history.
	Rules []SlackChangeExtractionRule `yaml:"rules,omitempty" json:"rules,omitempty"`
}

func (s Slack) ScrapeMembers() bool {
	return s.Members == nil || *s.Members
}

type SlackChangeExtractionRule struct {
	ChangeExtractionRule `json:",inline" yaml:",inline"`

	// Only those messages matching this filter will be processed.
	Filter *SlackChangeAcceptanceFilter `yaml:"filter,omitempty" json:"filter,omitempty"`
}

type SlackChangeAcceptanceFilter struct {
	// Bot name to match
	Bot types.MatchExpression `yaml:"bot,omitempty" json:"bot,omitempty"`

	// Slack User to match
	User SlackUserFilter `yaml:"user,omitempty" json:"user,omitempty"`

	// Must match the given expression
	Expr types.CelExpression `yaml:"expr,omitempty" json:"expr,omitempty"`
}

type SlackUserFilter struct {
	Name        types.MatchExpression `yaml:"name,omitempty" json:"name,omitempty"`
	DisplayName types.MatchExpression `yaml:"displayName,omitempty" json:"displayName,omitempty"`
}
