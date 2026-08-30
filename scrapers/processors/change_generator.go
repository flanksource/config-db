package processors

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/utils"
	"github.com/flanksource/gomplate/v3"
)

// applyConfigChangeGenerators converts matching config items into config changes
// while leaving the config items themselves intact. Generator failures are
// isolated to the failing rule and recorded as warnings so a global plugin
// cannot prevent an otherwise valid config item from being scraped.
func applyConfigChangeGenerators(ctx api.ScrapeContext, input *v1.ScrapeResult, generators []v1.ConfigChangeGenerator) {
	if len(generators) == 0 {
		return
	}

	input.FlushMap()
	env := input.AsMap()

	for i, generator := range generators {
		matched := generator.Filter == ""
		if !matched {
			var err error
			matched, err = ctx.RunTemplateBool(gomplate.Template{
				Expression: generator.Filter,
				CacheKey:   "processors.change.generator.filter:" + generator.Filter,
				CacheTime:  utils.RandomDurationBetween(24*time.Hour, 36*time.Hour),
			}, env)
			if err != nil {
				addConfigChangeGeneratorWarning(input, i, generator.Filter, nil, "evaluate filter", err)
				continue
			}
		}

		if !matched {
			continue
		}
		if generator.Expr == "" {
			addConfigChangeGeneratorWarning(input, i, generator.Expr, nil, "evaluate expression", fmt.Errorf("expression is empty"))
			continue
		}

		output, err := ctx.RunTemplate(gomplate.Template{
			Expression: generator.Expr,
			CacheKey:   "processors.change.generator.expr:" + generator.Expr,
			CacheTime:  utils.RandomDurationBetween(24*time.Hour, 36*time.Hour),
		}, env)
		if err != nil {
			addConfigChangeGeneratorWarning(input, i, generator.Expr, nil, "evaluate expression", err)
			continue
		}

		var generated []v1.ChangeResult
		if err := json.Unmarshal([]byte(output), &generated); err != nil {
			addConfigChangeGeneratorWarning(input, i, generator.Expr, output, "decode output", err)
			continue
		}

		for j := range generated {
			if generated[j].ExternalID == "" {
				generated[j].ExternalID = input.ID
			}
			if generated[j].ConfigType == "" {
				generated[j].ConfigType = input.Type
			}
		}
		input.Changes = append(input.Changes, generated...)
	}
}

func addConfigChangeGeneratorWarning(input *v1.ScrapeResult, index int, expr string, output any, operation string, err error) {
	input.Warnings = append(input.Warnings, v1.Warning{
		Input:  input.Config,
		Output: output,
		Expr:   expr,
		Error:  fmt.Sprintf("config change generator %d failed to %s: %v", index, operation, err),
	})
}
