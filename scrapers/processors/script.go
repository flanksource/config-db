package processors

import (
	"encoding/json"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
)

func RunScript(ctx api.ScrapeContext, result v1.ScrapeResult, script v1.Script) ([]v1.ScrapeResult, error) {
	env := map[string]interface{}{
		"config": result.Config,
		"result": result,
	}

	out, err := ctx.RunTemplate(script.ToGomplate(), env)
	if err != nil {
		return nil, err
	}

	configs, err := unmarshalConfigsFromString(out, result)
	if err != nil {
		return nil, err
	}

	return configs, nil
}

func unmarshalConfigsFromString(s string, parent v1.ScrapeResult) ([]v1.ScrapeResult, error) {
	var configs []v1.ScrapeResult
	var results = []map[string]interface{}{}
	if err := json.Unmarshal([]byte(s), &results); err != nil {
		return nil, err
	}

	for _, result := range results {
		configs = append(configs, v1.ScrapeResult{
			BaseScraper: parent.BaseScraper.WithoutTransform(),
			Config:      result,
		})
	}

	return configs, nil
}
