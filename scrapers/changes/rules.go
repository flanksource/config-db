package changes

import (
	_ "embed"
	"fmt"

	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/config-db/api/v1"
	"gopkg.in/yaml.v3"
)

//go:embed rules.yaml
var changeRulesConfig []byte

type changeRule struct {
	Action  v1.ChangeAction `json:"action"`  // map the change action to this action.
	Filter  string          `json:"filter"`  // cel-go filter for a config item.
	Rule    string          `json:"rule"`    // cel-go filter for a config change.
	Type    string          `json:"type"`    // replace with this change type.
	Summary string          `json:"summary"` // Go templatable summary to replace the existing change summary.
}

// matches the rule with a config using the filter
func (t *changeRule) match(result *v1.ScrapeResult) (bool, error) {
	if t.Filter == "" {
		return true, nil
	}

	env := map[string]any{
		"config":      result.ConfigMap(),
		"config_type": result.Type,
	}
	return evaluateCelExpression(t.Filter, env, "config", "config_type")
}

func (t *changeRule) process(change *v1.ChangeResult) error {
	env := map[string]any{
		"change": change.AsMap(),
		"patch":  change.PatchesMap(),
	}

	if ok, err := evaluateCelExpression(t.Rule, env, "change", "patch"); err != nil {
		return fmt.Errorf("failed to evaluate rule %s: %w", t.Rule, err)
	} else if !ok {
		return nil
	}

	if t.Type != "" {
		change.ChangeType = t.Type
	}

	if t.Action != "" {
		change.Action = t.Action
	}

	if t.Summary != "" {
		summary, err := evaluateGoTemplate(t.Summary, env)
		if err != nil {
			return fmt.Errorf("failed to evaluate summary template %s: %w", t.Summary, err)
		}

		change.Summary = summary
	}

	return nil
}

var Rules []changeRule

func init() {
	if err := yaml.Unmarshal(changeRulesConfig, &Rules); err != nil {
		logger.Errorf("Failed to unmarshal config rules: %s", err)
	}

	logger.Infof("Loaded %d change rules", len(Rules))
}

// ProcessRules modifies the scraped changes in-place
// using the change rules.
func ProcessRules(result *v1.ScrapeResult, rules ...v1.ChangeMapping) {
	allRules := Rules
	for _, r := range rules {
		allRules = append(allRules, changeRule{
			Rule: r.Filter,
			Type: r.Type,
		})
	}

	for _, rule := range allRules {
		if len(result.Changes) == 0 {
			continue
		}

		if match, err := rule.match(result); err != nil {
			logger.Errorf("Failed to match filter %s: %s", rule.Filter, err)
			continue
		} else if !match {
			continue
		}

		for i := range result.Changes {
			if err := rule.process(&result.Changes[i]); err != nil {
				logger.Errorf("Failed to process rule %s: %v", rule.Rule, err)
			}
		}
	}
}
