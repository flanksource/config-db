package changes

import (
	"fmt"
	"regexp"
	"sync"
	"time"

	"github.com/flanksource/commons/collections"
	"github.com/flanksource/commons/hash"
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty/query"
	"github.com/flanksource/duty/types"
	"github.com/samber/lo"
)

var regexpCache sync.Map

func compileRegexp(r string) (*regexp.Regexp, error) {
	if cached, ok := regexpCache.Load(r); ok {
		return cached.(*regexp.Regexp), nil
	}

	parsed, err := regexp.Compile(r)
	if err != nil {
		return nil, err
	}
	regexpCache.Store(r, parsed)

	return parsed, nil
}

func MapChanges(ctx api.ScrapeContext, rule v1.ChangeExtractionRule, text string) ([]v1.ChangeResult, error) {
	env := map[string]any{
		"text": text,
	}

	if rule.Regexp != "" {
		compiled, err := compileRegexp(rule.Regexp)
		if err != nil {
			return nil, err
		}

		regexpEnv := map[string]string{}
		match := compiled.FindStringSubmatch(text)
		for i, name := range compiled.SubexpNames() {
			if i != 0 && name != "" && len(match) >= len(compiled.SubexpNames()) {
				regexpEnv[name] = match[i]
			}
		}

		env["env"] = regexpEnv
	}

	var changeType, severity, summary string
	var changeCreatedAt *time.Time
	var err error

	changeType, err = rule.Mapping.Type.Eval(env)
	if err != nil {
		return nil, fmt.Errorf("failed to evaluate type: %v", err)
	}

	if !rule.Mapping.Severity.Empty() {
		severity, err = rule.Mapping.Severity.Eval(env)
		if err != nil {
			return nil, fmt.Errorf("failed to evaluate severity: %v", err)
		}
	}

	if !rule.Mapping.Summary.Empty() {
		summary, err = rule.Mapping.Summary.Eval(env)
		if err != nil {
			return nil, fmt.Errorf("failed to evaluate summary: %v", err)
		}
	}

	if !rule.Mapping.CreatedAt.Empty() {
		_createdAt, err := rule.Mapping.CreatedAt.Eval(env)
		if err != nil {
			return nil, fmt.Errorf("failed to evaluate summary: %v", err)
		}

		val, err := time.Parse(lo.CoalesceOrEmpty(rule.Mapping.TimeFormat, time.RFC3339), _createdAt)
		if err != nil {
			return nil, fmt.Errorf("failed to parse createdAt: %v", err)
		}
		changeCreatedAt = &val
	}

	var output []v1.ChangeResult
	for _, configSelector := range rule.Config {
		var resourceSelector types.ResourceSelector
		if !configSelector.Name.Empty() {
			name, err := configSelector.Name.Eval(env)
			if err != nil {
				return nil, fmt.Errorf("failed to evaluate config name: %v", err)
			}
			resourceSelector.Name = name
		}

		if !configSelector.Type.Empty() {
			configType, err := configSelector.Type.Eval(env)
			if err != nil {
				return nil, fmt.Errorf("failed to evaluate config type: %v", err)
			}
			resourceSelector.Types = []string{configType}
		}

		if len(configSelector.Tags) != 0 {
			resourceSelector.TagSelector = collections.SortedMap(configSelector.Tags)
		}

		configIDs, err := query.FindConfigIDsByResourceSelector(ctx.DutyContext(), resourceSelector)
		if err != nil {
			return nil, fmt.Errorf("failed to select configs: %w", err)
		}
		ctx.Logger.V(3).Infof("found %d configs for selector %v", len(configIDs), resourceSelector)

		for _, configID := range configIDs {
			output = append(output, v1.ChangeResult{
				Source:           "slack",
				CreatedAt:        changeCreatedAt,
				Severity:         severity,
				ChangeType:       changeType,
				Summary:          summary,
				ExternalChangeID: hash.Sha256Hex(text),
				ConfigID:         configID.String(),
			})
		}
	}

	return output, nil
}
