package v1

import (
	"github.com/flanksource/duty/pubsub"
)

type PubSub struct {
	BaseScraper `yaml:",inline" json:",inline"`

	pubsub.QueueConfig `yaml:",inline" json:",inline"`

	MaxMessages int `json:"maxMessages,omitempty" yaml:"maxMessages,omitempty"`
}
