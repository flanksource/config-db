package changes

import (
	_ "embed"

	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/config-db/api/v1"
	"gopkg.in/yaml.v3"
)

//go:embed rules.yaml
var changeRules []byte

type Change struct {
	Action v1.ChangeAction `yaml:"action"`
}

var Rules map[string]Change

func init() {
	if err := yaml.Unmarshal(changeRules, &Rules); err != nil {
		logger.Errorf("Failed to unmarshal config rules: %s", err)
	}
	logger.Infof("Loaded %d change rules", len(Rules))
}

func ProcessRules(result v1.ScrapeResult) []v1.ChangeResult {
	changes := []v1.ChangeResult{}
outer:
	for _, change := range result.Changes {

		if rule, ok := Rules[change.ChangeType]; ok {
			change.Action = rule.Action
			changes = append(changes, change)
			continue outer
		}

		changes = append(changes, change)
	}
	return changes
}
