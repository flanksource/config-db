package changes

import (
	_ "embed"
	"fmt"

	"github.com/flanksource/commons/logger"
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/gomplate/v3"
	"github.com/samber/lo"
	"github.com/samber/oops"
	"gopkg.in/yaml.v3"
)

//go:embed rules.yaml
var changeRulesConfig []byte

type changeRule struct {
	Action   v1.ChangeAction `json:"action"`   // map the change action to this action.
	Filter   string          `json:"filter"`   // cel-go filter for a config item.
	Rule     string          `json:"rule"`     // cel-go filter for a config change.
	Severity string          `json:"severity"` // replace with this severity.
	Type     string          `json:"type"`     // replace with this change type.
	Summary  string          `json:"summary"`  // Go templatable summary to replace the existing change summary.
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

	return gomplate.RunTemplateBool(env, gomplate.Template{Expression: t.Filter})
}

func (t *changeRule) process(ctx api.ScrapeContext, change *v1.ChangeResult) error {
	env := change.AsMap()

	// The "change" and "patch" variables are deprecated.
	// For backwards compatibility, we set them to the current change and patch.
	//
	change.FlushMap() // To prevent recursion

	env["change"] = change.AsMap()
	env["patch"] = change.PatchesMap()

	ok, err := gomplate.RunTemplateBool(env, gomplate.Template{Expression: t.Rule})
	if err != nil {
		return fmt.Errorf("failed to evaluate change mapping rule (%s): %w", lo.Ellipsis(t.Rule, 30), err)
	} else if !ok {
		return nil
	}

	if ctx.PropertyOn(false, "log.rule.expr") {
		logger.Infof("result for expr=%s with env=%v is %v", t.Rule, env, ok)
	}

	if t.Type != "" {
		change.ChangeType = t.Type
	}

	if t.Severity != "" {
		change.Severity = t.Severity
	}

	if t.Action != "" {
		change.Action = t.Action
	}

	if t.Summary != "" {
		summary, err := gomplate.RunTemplate(env, gomplate.Template{Template: t.Summary})
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
func ProcessRules(ctx api.ScrapeContext, result *v1.ScrapeResult, rules ...v1.ChangeMapping) error {
	if len(result.Changes) == 0 {
		return nil
	}

	allRules := Rules
	for _, r := range rules {
		allRules = append(allRules, changeRule{
			Action:   r.Action,
			Rule:     r.Filter,
			Type:     r.Type,
			Severity: r.Severity,
			Summary:  r.Summary,
		})
	}

	var errors []error
	for _, rule := range allRules {
		if match, err := rule.match(result); err != nil {
			errors = append(errors, oops.Wrapf(err, "failed to match filter"))
			continue
		} else if !match {
			continue
		}

		for i := range result.Changes {
			if err := rule.process(ctx, &result.Changes[i]); err != nil {
				errors = append(errors, err)
			}
		}
	}

	return oops.Join(errors...)
}
