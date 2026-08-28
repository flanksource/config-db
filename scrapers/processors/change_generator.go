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
// while leaving the config items themselves intact.
func applyConfigChangeGenerators(ctx api.ScrapeContext, input *v1.ScrapeResult, generators []v1.ConfigChangeGenerator) error {
	if len(generators) == 0 {
		return nil
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
				return fmt.Errorf("failed to evaluate config change generator %d filter: %w", i, err)
			}
		}

		if !matched {
			continue
		}
		if generator.Expr == "" {
			return fmt.Errorf("config change generator %d has no expression", i)
		}

		output, err := ctx.RunTemplate(gomplate.Template{
			Expression: generator.Expr,
			CacheKey:   "processors.change.generator.expr:" + generator.Expr,
			CacheTime:  utils.RandomDurationBetween(24*time.Hour, 36*time.Hour),
		}, env)
		if err != nil {
			return fmt.Errorf("failed to evaluate config change generator %d expression: %w", i, err)
		}

		var generated []v1.ChangeResult
		if err := json.Unmarshal([]byte(output), &generated); err != nil {
			return fmt.Errorf("failed to decode config change generator %d output: %w", i, err)
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

	return nil
}
