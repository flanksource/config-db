package processors

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/pkg/errors"
	"github.com/robertkrimen/otto"

	"github.com/flanksource/commons/logger"
	"github.com/flanksource/commons/text"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/robertkrimen/otto/registry"
	_ "github.com/robertkrimen/otto/underscore"
)

func LoadSharedLibrary(source string) error {
	source = strings.TrimSpace(source)
	data, err := os.ReadFile(source)
	if err != nil {
		return fmt.Errorf("failed to read shared library %s: %s", source, err)
	}
	logger.Tracef("Loaded %s: \n%s", source, string(data))
	registry.Register(func() string { return string(data) })
	return nil
}

func RunScript(result v1.ScrapeResult, script v1.Script) ([]v1.ScrapeResult, error) {
	var out []v1.ScrapeResult
	// javascript
	if script.Javascript != "" {
		vm := otto.New()
		if err := vm.Set("config", result.Config); err != nil {
			return nil, err
		}
		if err := vm.Set("result", result); err != nil {
			return nil, err
		}
		_out, err := vm.Run(script.Javascript)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to run javascript")
		}

		if s, err := _out.ToString(); err != nil {
			return nil, errors.Wrapf(err, "failed to cast output to string")
		} else if configs, err := unmarshalConfigsFromString(s); err != nil {
			return nil, err
		} else {
			out = append(out, configs...)
		}
	} else if script.GoTemplate != "" {
		ctx := map[string]interface{}{
			"config": result.Config,
			"result": result,
		}
		if s, err := text.Template(script.GoTemplate, ctx); err != nil {
			return nil, err
		} else if configs, err := unmarshalConfigsFromString(s); err != nil {
			return nil, err
		} else {
			out = append(out, configs...)
		}
	}

	return out, nil
}

func unmarshalConfigsFromString(s string) ([]v1.ScrapeResult, error) {
	var configs []v1.ScrapeResult

	var results = []map[string]interface{}{}

	if err := json.Unmarshal([]byte(s), &results); err != nil {
		return nil, err
	}
	for _, result := range results {
		configs = append(configs, v1.ScrapeResult{
			Config: result,
		})
	}
	return configs, nil
}
