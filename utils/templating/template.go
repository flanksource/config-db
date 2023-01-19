package templating

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	gotemplate "text/template"

	"github.com/dop251/goja"
	"github.com/google/cel-go/cel"
	"github.com/pkg/errors"

	"github.com/flanksource/commons/logger"
	"github.com/flanksource/commons/text"
	v1 "github.com/flanksource/config-db/api/v1"
)

func LoadSharedLibrary(vm *goja.Runtime, source string) error {
	source = strings.TrimSpace(source)
	data, err := os.ReadFile(source)
	if err != nil {
		return fmt.Errorf("failed to read shared library %s: %w", source, err)
	}
	logger.Tracef("Loaded %s: \n%s", source, string(data))

	_, err = vm.RunScript(source, string(data))
	if err != nil {
		return fmt.Errorf("vm.RunScript(); %w", err)
	}
	return nil
}

func Template(environment map[string]interface{}, template v1.Template) (string, error) {
	// javascript
	if template.Javascript != "" {
		// FIXME: whitelist allowed files
		vm := goja.New()
		for k, v := range environment {
			if err := vm.Set(k, v); err != nil {
				return "", errors.Wrapf(err, "error setting %s", k)
			}
		}
		vmOut, err := vm.RunString(template.Javascript)
		if err != nil {
			return "", errors.Wrapf(err, "failed to run javascript")
		}

		if s, ok := vmOut.Export().(string); !ok {
			return "", fmt.Errorf("failed to cast output to string; it is of type %s", vmOut.ExportType().Name())
		} else {
			return s, nil
		}
	}

	// gotemplate
	if template.Template != "" {
		tpl := gotemplate.New("")
		tpl, err := tpl.Funcs(text.GetTemplateFuncs()).Parse(template.Template)
		if err != nil {
			return "", err
		}

		// marshal data from interface{} to map[string]interface{}
		data, _ := json.Marshal(environment)
		unstructured := make(map[string]interface{})
		if err := json.Unmarshal(data, &unstructured); err != nil {
			return "", err
		}

		var buf bytes.Buffer
		if err := tpl.Execute(&buf, unstructured); err != nil {
			return "", fmt.Errorf("error executing template %s: %v", strings.Split(template.Template, "\n")[0], err)
		}
		return strings.TrimSpace(buf.String()), nil
	}

	// exprv
	if template.Expression != "" {
		env, err := cel.NewEnv(makeCelEnv(environment)...)
		if err != nil {
			return "", err
		}

		ast, issues := env.Compile(template.Expression)
		if issues != nil && issues.Err() != nil {
			return "", fmt.Errorf("compile error: %s", issues.Err())
		}

		prg, err := env.Program(ast)
		if err != nil {
			return "", err
		}

		out, _, err := prg.Eval(environment)
		if err != nil {
			return "", err
		}

		return fmt.Sprint(out), nil
	}

	// if template.GSONPath != "" {
	// 	return gjson.Get(jsonContent, template.GSONPath).String()
	// }
	return "", nil
}

func makeCelEnv(env map[string]interface{}) []cel.EnvOption {
	opts := make([]cel.EnvOption, 0, len(env))
	for k := range env {
		opts = append(opts, cel.Variable(k, cel.DynType))
	}

	return opts
}
