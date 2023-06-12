package changes

import (
	"bytes"
	"fmt"
	"html/template"
	"strconv"
	"time"

	"github.com/google/cel-go/cel"
	"github.com/patrickmn/go-cache"
)

var (
	programCache  = cache.New(24*time.Hour, 12*time.Hour)
	templateCache = cache.New(24*time.Hour, 12*time.Hour)
)

// getOrCompileCELProgram returns a cached or compiled cel.Program for the given cel expression.
func getOrCompileCELProgram(expression string, vars ...string) (*cel.Program, error) {
	if prg, exists := programCache.Get(expression); exists {
		return prg.(*cel.Program), nil
	}

	var opts []cel.EnvOption
	for _, v := range vars {
		opts = append(opts, cel.Variable(v, cel.AnyType))
	}

	env, err := cel.NewEnv(opts...)
	if err != nil {
		return nil, err
	}

	ast, iss := env.Compile(expression)
	if iss.Err() != nil {
		return nil, iss.Err()
	}

	prg, err := env.Program(ast)
	if err != nil {
		return nil, err
	}

	programCache.SetDefault(expression, &prg)
	return &prg, nil
}

func evaluateCelExpression(expression string, env map[string]any, vars ...string) (bool, error) {
	prg, err := getOrCompileCELProgram(expression, vars...)
	if err != nil {
		return false, err
	}

	out, _, err := (*prg).Eval(env)
	if err != nil {
		return false, err
	}

	return strconv.ParseBool(fmt.Sprint(out))
}

func evaluateGoTemplate(content string, view any) (string, error) {
	var tpl *template.Template
	var err error

	if val, exists := templateCache.Get(content); exists {
		tpl = val.(*template.Template)
	} else {
		tpl, err = template.New("").Parse(content)
		if err != nil {
			return "", fmt.Errorf("error parsing template %s: %w", content, err)
		}
		templateCache.SetDefault(content, tpl)
	}

	var rendered bytes.Buffer
	if err := tpl.Execute(&rendered, view); err != nil {
		return "", fmt.Errorf("error executing template %s: %w", content, err)
	}

	return rendered.String(), nil
}
