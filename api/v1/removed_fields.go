package v1

import (
	"fmt"
	"strings"

	yamlutil "k8s.io/apimachinery/pkg/util/yaml"
)

// removedField is a spec field that no longer exists. The typed decoder drops
// unknown keys, so a config that still sets one would scrape with the field
// silently ignored — which, for a filter, means scraping more than it asks for.
type removedField struct {
	name   string
	reason string
}

var removedSlackFields = []removedField{{
	name:   "channels",
	reason: "the slack scraper now collects every channel its token can see; drop the filter or narrow the token's channel scopes",
}}

// rejectRemovedFields fails a config chunk that still carries a field deleted
// from the API, rather than letting it load with the field discarded.
func rejectRemovedFields(chunk string) error {
	var raw struct {
		Spec struct {
			Slack []map[string]any `json:"slack"`
		} `json:"spec"`
	}

	decoder := yamlutil.NewYAMLOrJSONDecoder(strings.NewReader(chunk), 1024)
	if err := decoder.Decode(&raw); err != nil {
		return err
	}

	for i, slack := range raw.Spec.Slack {
		for _, field := range removedSlackFields {
			if _, ok := slack[field.name]; ok {
				return fmt.Errorf("spec.slack[%d].%s has been removed: %s", i, field.name, field.reason)
			}
		}
	}

	return nil
}
