package changes

import (
	_ "embed"
	"strings"

	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/config-db/api/v1"
	"gopkg.in/yaml.v3"
)

//go:embed rules.yaml
var changeRules []byte

type Change struct {
	Action v1.ChangeAction `yaml:"action"`
}

type changeRule struct {
	allRules         map[string]Change
	exactMatchRules  map[string]Change
	prefixMatchRules map[string]Change
	suffixMatchRules map[string]Change
}

func (t *changeRule) init() {
	t.exactMatchRules = make(map[string]Change)
	t.prefixMatchRules = make(map[string]Change)
	t.suffixMatchRules = make(map[string]Change)

	for k, v := range t.allRules {
		if strings.HasSuffix(k, "*") {
			t.suffixMatchRules[strings.TrimSuffix(k, "*")] = v
		} else if strings.HasPrefix(k, "*") {
			t.prefixMatchRules[strings.TrimPrefix(k, "*")] = v
		} else {
			t.exactMatchRules[k] = v
		}
	}
}

func (t changeRule) determineAction(action string) (v1.ChangeAction, bool) {
	if rule, ok := t.exactMatchRules[action]; ok {
		return rule.Action, true
	}

	for k, rule := range t.prefixMatchRules {
		if strings.HasSuffix(action, k) {
			return rule.Action, true
		}
	}

	for k, rule := range t.suffixMatchRules {
		if strings.HasPrefix(action, k) {
			return rule.Action, true
		}
	}

	return "", false
}

var Rules changeRule

func init() {
	if err := yaml.Unmarshal(changeRules, &Rules.allRules); err != nil {
		logger.Errorf("Failed to unmarshal config rules: %s", err)
	}

	Rules.init()
	logger.Infof("Loaded %d change rules", len(Rules.allRules))
}

func ProcessRules(result v1.ScrapeResult) []v1.ChangeResult {
	changes := make([]v1.ChangeResult, 0, len(result.Changes))

	for _, change := range result.Changes {
		if action, ok := Rules.determineAction(change.ChangeType); ok {
			change.Action = action
		}

		changes = append(changes, change)
	}

	return changes
}
