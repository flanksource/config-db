package bench

// This benchmark exercises the CEL / gomplate template evaluation path used by
// the scraper at runtime, to measure per-call allocation churn.
//
// Motivation: a heap profile of a running config-db showed that template/CEL
// evaluation (duty Context.RunTemplate) accounts for the single largest slice of
// lifetime allocation (~31% of all bytes allocated), with location extraction alone
// at ~20%. The suspicion is that GetCelEnv (rebuilds the whole CEL environment,
// incl. the kubernetes library) and Serialize (ojg/jp.Walk over the entire env)
// run on EVERY evaluation even though the compiled program is cached.
//
// Run:
//   go test ./bench -run x -bench 'BenchmarkLocationFilter|BenchmarkRunTemplateBool' -benchmem
//
// Capture a heap profile and inspect it the same way we inspected the prod one:
//   go test ./bench -run x -bench BenchmarkLocationFilter -benchmem \
//     -memprofile /tmp/cel.mem.pprof -memprofilerate=1
//   go tool pprof -alloc_space -top -nodecount=25 /tmp/cel.mem.pprof
//   go tool pprof -alloc_space -peek 'GetCelEnv$' /tmp/cel.mem.pprof
//
// The blank import of db registers the db.* CEL env funcs into the global
// context.CelEnvFuncs map exactly as it happens in production, so the per-call
// closure construction in Context.RunTemplate is reproduced faithfully.

import (
	"fmt"
	"testing"
	"time"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	_ "github.com/flanksource/config-db/db" // registers db.* CEL env/template funcs (prod-faithful)
	"github.com/flanksource/config-db/utils"
	dutyctx "github.com/flanksource/duty/context"
	"github.com/flanksource/duty/types"
	"github.com/flanksource/gomplate/v3"
)

// benchSink prevents the compiler from optimizing away results.
var benchSink any

// benchScrapeContext builds a DB-free scrape context. RunTemplate only needs the
// context for its logger and the registered CelEnvFuncs/TemplateFuncs closures;
// the registered db.* functions are only *constructed* per call here (matching
// prod) — they are not invoked because the benchmark expressions don't call them.
func benchScrapeContext() api.ScrapeContext {
	return api.NewScrapeContext(dutyctx.New())
}

// benchPodEnv returns a config-item env map shaped like what the scraper passes
// to location extraction: a handful of scalar keys plus, optionally, a large nested
// `config` object. GetCelEnv registers one cel.Variable per top-level key and
// Serialize walks the entire structure, so env size directly drives the per-call
// allocation we are trying to measure.
func benchPodEnv(withNestedConfig bool) map[string]any {
	env := map[string]any{
		"id":           "0192f0a4-1234-7000-8000-aaaaaaaaaaaa",
		"name":         "nginx-7c5ddbdf54-abcde",
		"namespace":    "default",
		"config_type":  "Kubernetes::Pod",
		"config_class": "Pod",
		"tags": map[string]any{
			"cluster":   "production",
			"namespace": "default",
		},
	}

	if withNestedConfig {
		containers := make([]any, 0, 3)
		for i := 0; i < 3; i++ {
			containers = append(containers, map[string]any{
				"name":  fmt.Sprintf("container-%d", i),
				"image": fmt.Sprintf("registry.example.com/app:%d.2.3", i),
				"ports": []any{map[string]any{"containerPort": 8080 + i, "protocol": "TCP"}},
				"env": []any{
					map[string]any{"name": "LOG_LEVEL", "value": "info"},
					map[string]any{"name": "REGION", "value": "us-east-1"},
				},
				"resources": map[string]any{
					"limits":   map[string]any{"cpu": "500m", "memory": "512Mi"},
					"requests": map[string]any{"cpu": "100m", "memory": "128Mi"},
				},
			})
		}

		env["config"] = map[string]any{
			"apiVersion": "v1",
			"kind":       "Pod",
			"metadata": map[string]any{
				"name":      "nginx-7c5ddbdf54-abcde",
				"namespace": "default",
				"labels": map[string]any{
					"app": "nginx", "team": "platform", "env": "production", "version": "v1.2.3",
				},
				"annotations": map[string]any{
					"prometheus.io/scrape": "true",
					"prometheus.io/port":   "8080",
				},
				"ownerReferences": []any{
					map[string]any{"apiVersion": "apps/v1", "kind": "ReplicaSet", "name": "nginx-7c5ddbdf54"},
				},
			},
			"spec":   map[string]any{"containers": containers, "nodeName": "ip-10-0-1-23"},
			"status": map[string]any{"phase": "Running", "podIP": "10.0.5.12", "hostIP": "10.0.1.23"},
		}
	}

	return env
}

func benchExtractLocation(ctx api.ScrapeContext, env map[string]any, locationOrAlias []v1.LocationOrAlias, cacheKeyPrefix string) ([]string, error) {
	var output []string
	for _, l := range locationOrAlias {
		if len(l.Values) == 0 {
			continue
		}

		configType, _ := env["config_type"].(string)
		if l.Type != "" && !l.Type.Match(configType) {
			continue
		}

		if l.Filter != "" {
			filterOutput, err := ctx.RunTemplateBool(gomplate.Template{
				Expression: string(l.Filter),
				CacheKey:   cacheKeyPrefix + "processors.location.filter:" + string(l.Filter),
				CacheTime:  utils.RandomDurationBetween(24*time.Hour, 36*time.Hour),
			}, env)
			if err != nil {
				return nil, fmt.Errorf("failed to evaluate location/alias filter: %w", err)
			}

			if !filterOutput {
				continue
			}
		}

		for _, value := range l.Values {
			v, err := gomplate.RunTemplate(env, gomplate.Template{
				Template:       value,
				CacheKey:       cacheKeyPrefix + "extract.location.gomplate:" + value,
				CacheTime:      utils.RandomDurationBetween(24*time.Hour, 36*time.Hour),
				ValueFunctions: true,
				DelimSets: []gomplate.Delims{
					{Left: "{{", Right: "}}"},
					{Left: "$(", Right: ")"},
				},
			})
			if err != nil {
				return nil, fmt.Errorf("failed to template location/alias values for type:%s, filter:%s: %w", l.Type, l.Filter, err)
			}

			output = append(output, v)
		}
	}

	return output, nil
}

// BenchmarkLocationFilter benchmarks an extractLocation-equivalent call path (the #1
// template-eval caller in the prod heap profile). It runs a CEL filter and then
// templates the location value, matching the filter/value path in
// scrapers/processors/json.go.
func BenchmarkLocationFilter(b *testing.B) {
	locations := []v1.LocationOrAlias{{
		Filter: types.CelExpression(`config_type == "Kubernetes::Pod"`),
		Values: []string{"kubernetes/cluster/{{.namespace}}/{{.name}}"},
	}}

	for _, withConfig := range []bool{false, true} {
		name := "smallEnv"
		if withConfig {
			name = "largeEnv"
		}
		b.Run(name, func(b *testing.B) {
			ctx := benchScrapeContext()
			env := benchPodEnv(withConfig)
			cacheKeyPrefix := b.Name() + ":"

			if _, err := benchExtractLocation(ctx, env, locations, cacheKeyPrefix); err != nil {
				b.Fatal(err)
			}

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				out, err := benchExtractLocation(ctx, env, locations, cacheKeyPrefix)
				if err != nil {
					b.Fatal(err)
				}
				benchSink = out
			}
		})
	}
}

// BenchmarkRunTemplateBool isolates the CEL expression evaluation (no go-template
// value rendering) — i.e. one ctx.RunTemplateBool with an explicit CacheKey, so
// the compiled program is served from cache on every measured iteration after an
// untimed warm-up. Any allocation that remains is the per-call GetCelEnv +
// Serialize + closure construction overhead that runs regardless of the program
// cache.
func BenchmarkRunTemplateBool(b *testing.B) {
	tmpl := gomplate.Template{
		Expression: `config_type == "Kubernetes::Pod"`,
		CacheKey:   "bench.location.filter:config_type == Kubernetes::Pod",
	}

	for _, withConfig := range []bool{false, true} {
		name := "smallEnv"
		if withConfig {
			name = "largeEnv"
		}
		b.Run(name, func(b *testing.B) {
			ctx := benchScrapeContext()
			env := benchPodEnv(withConfig)
			caseTmpl := tmpl
			caseTmpl.CacheKey = b.Name() + ":" + tmpl.CacheKey

			if _, err := ctx.RunTemplateBool(caseTmpl, env); err != nil {
				b.Fatal(err)
			}

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				ok, err := ctx.RunTemplateBool(caseTmpl, env)
				if err != nil {
					b.Fatal(err)
				}
				benchSink = ok
			}
		})
	}
}
