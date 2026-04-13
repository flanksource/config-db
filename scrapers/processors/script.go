package processors

import (
	"encoding/json"
	"fmt"

	"github.com/flanksource/commons/logger"
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/samber/lo"
)

func RunScript(ctx api.ScrapeContext, result v1.ScrapeResult, script v1.Script) ([]v1.ScrapeResult, error) {
	env := map[string]interface{}{
		"config":              result.Config,
		"result":              result,
		"last_scrape_summary": ctx.LastScrapeSummary(),
	}

	out, err := ctx.RunTemplate(script.ToGomplate(), env)
	if err != nil {
		return nil, err
	}

	configs, err := unmarshalConfigsFromString(out, result)
	if err != nil {
		return nil, err
	}

	ctx.Logger.V(3).Infof("script produced %d config(s)", len(configs))
	return configs, nil
}

func unmarshalConfigsFromString(s string, parent v1.ScrapeResult) ([]v1.ScrapeResult, error) {
	// Decode the CEL/script output twice:
	//   1. into []v1.ScrapeResult so the JSON tags on the struct (id, name,
	//      config_type, config_class, aliases, config, …) lift top-level
	//      scalar fields directly onto the struct — without this, keys like
	//      `name` and `config_type` sit unreachable inside the result's map
	//      and the resulting config item renders with no display or id.
	//   2. into []map[string]interface{} so the FULL outer map (including
	//      `external_users`, `external_groups`, `external_user_groups`, and
	//      any other keys not represented by struct fields) is retained and
	//      re-assigned to `.Config`. The downstream ExtractFullMode path
	//      iterates this map to extract external entities and then replaces
	//      `.Config` with the nested `config` sub-dict, matching the
	//      existing pipeline contract.
	var structs []v1.ScrapeResult
	if err := json.Unmarshal([]byte(s), &structs); err != nil {
		if logger.V(5).Enabled() {
			logger.Infof("Failed to unmarshal script output into ScrapeResult: %v\n%s\n", err, lo.Ellipsis(s, 2000))
		}
		return nil, err
	}

	var maps = []map[string]interface{}{}
	if err := json.Unmarshal([]byte(s), &maps); err != nil {
		if logger.V(5).Enabled() {
			logger.Infof("Failed to unmarshal script output into map: %v\n%s\n", err, lo.Ellipsis(s, 2000))
		}
		return nil, err
	}

	if len(structs) != len(maps) {
		return nil, fmt.Errorf("script output struct/map length mismatch: %d vs %d", len(structs), len(maps))
	}

	configs := make([]v1.ScrapeResult, 0, len(structs))
	for i := range structs {
		r := structs[i]
		r.BaseScraper = parent.BaseScraper.WithoutTransform()

		// `uuid` is the canonical key for defining an external entity's id
		// in this codebase's CEL/script convention — if the transform emits
		// a `uuid` field and there is no explicit `id`, use it as the
		// ScrapeResult.ID. This keeps transform expressions explicit about
		// "this string is a UUID" while still feeding the normal id pipeline.
		if r.ID == "" {
			if v, ok := maps[i]["uuid"].(string); ok && v != "" {
				r.ID = v
			}
		}

		// Retain the full outer map as .Config so ExtractFullMode can still
		// pull external entities from it. ExtractFullMode will then replace
		// .Config with the nested `config` value per its existing contract.
		r.Config = maps[i]
		configs = append(configs, r)
	}

	return configs, nil
}
