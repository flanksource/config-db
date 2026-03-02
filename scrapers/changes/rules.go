package changes

import (
	_ "embed"
	"fmt"

	"github.com/flanksource/commons/logger"
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty"
	"github.com/flanksource/gomplate/v3"
	"github.com/samber/lo"
	"github.com/samber/oops"
	"gopkg.in/yaml.v3"
)

//go:embed rules.yaml
var changeRulesConfig []byte

type changeRule struct {
	Action       v1.ChangeAction                    `json:"action"`        // map the change action to this action.
	Filter       string                             `json:"filter"`        // cel-go filter for a config item.
	Rule         string                             `json:"rule"`          // cel-go filter for a config change.
	Severity     string                             `json:"severity"`      // replace with this severity.
	Type         string                             `json:"type"`          // replace with this change type.
	Summary      string                             `json:"summary"`       // Go templatable summary to replace the existing change summary.
	ConfigID     string                             `json:"config_id"`     // CEL expression for target config external ID.
	ConfigType   string                             `json:"config_type"`   // Target config type for redirecting changes.
	ScraperID    string                             `json:"scraper_id"`    // Scraper ID for target config ("all" for cross-scraper).
	AncestorType string                             `json:"ancestor_type"` // Config type of ancestor to target for move-up/copy-up.
	Target       *duty.RelationshipSelectorTemplate `json:"target"`        // Config selector for copy/move actions.
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
	env["last_scrape_summary"] = ctx.LastScrapeSummary()

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

	if t.ConfigID != "" {
		configID, err := gomplate.RunTemplate(env, gomplate.Template{Expression: t.ConfigID})
		if err != nil {
			return fmt.Errorf("failed to evaluate config_id expression %s: %w", t.ConfigID, err)
		}
		if configID != "" {
			change.ExternalID = configID
		}
	}

	if t.ConfigType != "" {
		change.ConfigType = t.ConfigType
	}

	if t.ScraperID != "" {
		change.ScraperID = t.ScraperID
	}

	if t.AncestorType != "" {
		change.AncestorType = t.AncestorType
	}

	if t.Target != nil {
		change.Target = t.Target
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
	var errors []error
	for _, r := range rules {
		if r.Target != nil && !r.Target.IsEmpty() && (r.Action == v1.MoveUp || r.Action == v1.CopyUp || r.AncestorType != "") {
			errors = append(errors, fmt.Errorf("change mapping: target is mutually exclusive with move-up/copy-up/ancestor_type"))
			continue
		}
		allRules = append(allRules, changeRule{
			Action:       r.Action,
			Rule:         r.Filter,
			Type:         r.Type,
			Severity:     r.Severity,
			Summary:      r.Summary,
			ConfigID:     r.ConfigID,
			ConfigType:   r.ConfigType,
			ScraperID:    r.ScraperID,
			AncestorType: r.AncestorType,
			Target:       r.Target,
		})
	}
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
