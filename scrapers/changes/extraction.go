package changes

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sync"
	"time"

	"github.com/flanksource/commons/hash"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty/context"
	"github.com/flanksource/duty/query"
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

func MapChanges(ctx context.Context, rule v1.ChangeExtractionRule, text string) ([]v1.ChangeResult, error) {
	env := map[string]any{
		"text": text,
	}

	regexpEnv := map[string]string{}
	if rule.Regexp != "" {
		compiled, err := compileRegexp(rule.Regexp)
		if err != nil {
			return nil, err
		}

		match := compiled.FindStringSubmatch(text)
		for i, name := range compiled.SubexpNames() {
			if i != 0 && name != "" && len(match) >= len(compiled.SubexpNames()) {
				regexpEnv[name] = match[i]
			}
		}
		env["env"] = regexpEnv

		if len(match) != len(compiled.SubexpNames()) {
			// the regexp did not match all the capture groups.
			return nil, nil
		}
	}

	var changeType, severity, summary, changeCreatedAtRaw string
	var changeDetails map[string]any
	var changeCreatedAt *time.Time
	var err error

	if rule.Mapping != nil && !rule.Mapping.Type.Empty() {
		changeType, err = rule.Mapping.Type.Eval(env)
		if err != nil {
			return nil, fmt.Errorf("failed to evaluate type: %v", err)
		}
	} else if t, ok := regexpEnv["type"]; ok {
		changeType = t
	}

	if rule.Mapping != nil && !rule.Mapping.Severity.Empty() {
		severity, err = rule.Mapping.Severity.Eval(env)
		if err != nil {
			return nil, fmt.Errorf("failed to evaluate severity: %v", err)
		}
	} else if s, ok := regexpEnv["severity"]; ok {
		severity = s
	}

	if rule.Mapping != nil && !rule.Mapping.Summary.Empty() {
		summary, err = rule.Mapping.Summary.Eval(env)
		if err != nil {
			return nil, fmt.Errorf("failed to evaluate summary: %v", err)
		}
	} else if s, ok := regexpEnv["summary"]; ok {
		summary = s
	}

	if rule.Mapping != nil && !rule.Mapping.CreatedAt.Empty() {
		changeCreatedAtRaw, err = rule.Mapping.CreatedAt.Eval(env)
		if err != nil {
			return nil, fmt.Errorf("failed to evaluate createdAt: %v", err)
		}
	} else if s, ok := regexpEnv["created_at"]; ok {
		changeCreatedAtRaw = s
	}

	if rule.Mapping != nil && !rule.Mapping.Details.Empty() {
		d, err := rule.Mapping.Details.Eval(env)
		if err != nil {
			return nil, fmt.Errorf("failed to evaluate details: %v", err)
		}

		var jsonData map[string]any
		if err := json.Unmarshal([]byte(d), &jsonData); err == nil {
			changeDetails = jsonData
		} else {
			changeDetails = map[string]any{
				"text": d,
			}
		}
	} else {
		changeDetails = map[string]any{
			"text": text,
		}
	}

	if rule.Mapping != nil && changeCreatedAtRaw != "" {
		val, err := time.Parse(lo.CoalesceOrEmpty(rule.Mapping.TimeFormat, time.RFC3339), changeCreatedAtRaw)
		if err != nil {
			return nil, fmt.Errorf("failed to parse createdAt: %v", err)
		}
		changeCreatedAt = &val
	}

	var output []v1.ChangeResult
	for _, configSelector := range rule.Config {
		resourceSelector, err := configSelector.Hydrate(env)
		if err != nil {
			return nil, fmt.Errorf("failed to hydrate config selector: %w", err)
		}

		configIDs, err := query.FindConfigIDsByResourceSelector(ctx, *resourceSelector)
		if err != nil {
			return nil, fmt.Errorf("failed to select configs: %w", err)
		}
		ctx.Logger.V(3).Infof("found %d configs for selector %v", len(configIDs), resourceSelector)

		for _, configID := range configIDs {
			output = append(output, v1.ChangeResult{
				Source:           "slack",
				CreatedAt:        changeCreatedAt,
				Details:          changeDetails,
				Severity:         severity,
				ChangeType:       changeType,
				Summary:          summary,
				ExternalChangeID: hash.Sha256Hex(text),
				ConfigID:         configID.String(),
			})
		}

		if len(configIDs) > 0 {
			break // we've found at least one config, no need to continue
		}
	}

	return output, nil
}
