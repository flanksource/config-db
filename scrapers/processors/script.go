package processors

import (
	"encoding/json"

	"github.com/flanksource/commons/logger"
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/samber/lo"
)

type ScriptResult struct {
	Results   []v1.ScrapeResult
	RawInput  any
	RawOutput string
	Expr      string
}

func scriptEnv(result v1.ScrapeResult) map[string]interface{} {
	return map[string]interface{}{
		"config": result.Config,
		"result": result,
	}
}

func RunScript(ctx api.ScrapeContext, result v1.ScrapeResult, script v1.Script) (*ScriptResult, error) {
	out, err := ctx.RunTemplate(script.ToGomplate(), scriptEnv(result))
	if err != nil {
		return nil, err
	}

	configs, err := unmarshalConfigsFromString(out, result)
	if err != nil {
		return nil, err
	}

	ctx.Logger.V(3).Infof("script produced %d config(s)", len(configs))
	return &ScriptResult{
		Results:   configs,
		RawInput:  result.Config,
		RawOutput: out,
		Expr:      script.ToGomplate().Expression,
	}, nil
}

func unmarshalConfigsFromString(s string, parent v1.ScrapeResult) ([]v1.ScrapeResult, error) {
	var maps = []map[string]interface{}{}
	if err := json.Unmarshal([]byte(s), &maps); err != nil {
		if logger.V(5).Enabled() {
			logger.Infof("Failed to unmarshal script output into map: %v\n%s\n", err, lo.Ellipsis(s, 2000))
		}
		return nil, err
	}

	// Best-effort struct decode: lets transforms that emit typed top-level
	// fields (id, name, config_type, …) have them lifted directly. When the
	// output doesn't fit the struct (e.g. numeric id lifted from the source
	// config), fall back to empty structs and let the downstream spec-level
	// extractors (id: $.id, etc.) populate fields from the retained map.
	structs := make([]v1.ScrapeResult, len(maps))
	if err := json.Unmarshal([]byte(s), &structs); err != nil {
		if logger.V(5).Enabled() {
			logger.Infof("Script output not a ScrapeResult shape, falling back to map-only: %v\n%s\n", err, lo.Ellipsis(s, 2000))
		}
		structs = make([]v1.ScrapeResult, len(maps))
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
