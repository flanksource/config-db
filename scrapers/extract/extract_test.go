package extract

import (
	"encoding/json"
	"os"
	"path/filepath"

	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/gomplate/v3"
	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"
)

func resultToEnv(result ExtractedConfig) map[string]any {
	raw, _ := json.Marshal(result)
	var env map[string]any
	_ = json.Unmarshal(raw, &env)
	return env
}

type prePopulate struct {
	Users   map[string]string `yaml:"users"`   // alias -> UUID
	Roles   map[string]string `yaml:"roles"`   // alias -> UUID
	Groups  map[string]string `yaml:"groups"`  // alias -> UUID
	Configs map[string]string `yaml:"configs"` // "Type/ExternalID" -> UUID
}

type extractionFixture struct {
	Input       map[string]any `yaml:"input"`
	Assertions  []string       `yaml:"assertions"`
	PrePopulate prePopulate    `yaml:"pre_populate"`
}

var _ = Describe("Extraction fixtures", func() {
	fixtures, err := filepath.Glob("testdata/unit/*.yaml")
	if err != nil {
		panic(err)
	}

	for _, fixturePath := range fixtures {
		name := filepath.Base(fixturePath)

		It("fixture: "+name, func() {
			data, err := os.ReadFile(fixturePath)
			Expect(err).ToNot(HaveOccurred())

			var fixture extractionFixture
			Expect(yaml.Unmarshal(data, &fixture)).To(Succeed())
			Expect(fixture.Assertions).ToNot(BeEmpty(), "fixture %s has no assertions", name)

			resolver := NewMockResolver()
			for alias, id := range fixture.PrePopulate.Users {
				resolver.Users[alias] = uuid.MustParse(id)
			}
			for alias, id := range fixture.PrePopulate.Roles {
				resolver.Roles[alias] = uuid.MustParse(id)
			}
			for alias, id := range fixture.PrePopulate.Groups {
				resolver.Groups[alias] = uuid.MustParse(id)
			}
			for key, id := range fixture.PrePopulate.Configs {
				resolver.Configs[key] = uuid.MustParse(id)
			}

			scraperID := uuid.MustParse("00000000-0000-0000-0000-ffffffffffff")
			result, err := ExtractConfigChangesFromConfig(nil, nil, fixture.Input)
			Expect(err).ToNot(HaveOccurred())

			Expect(SyncEntities(resolver, &scraperID, fixture.Input, &result)).To(Succeed())
			Expect(ResolveAccess(resolver, &scraperID, &result)).To(Succeed())

			env := resultToEnv(result)
			// Ensure all slice keys exist for CEL assertions
			for _, key := range []string{"changes", "analysis", "access_logs", "config_access", "external_users", "external_groups", "external_user_groups", "external_roles", "warnings"} {
				if _, ok := env[key]; !ok {
					env[key] = []any{}
				}
			}
			if _, ok := env["config"]; !ok {
				env["config"] = nil
			}
			ctx := result.Pretty().String()
			for _, expr := range fixture.Assertions {
				ok, err := gomplate.RunTemplateBool(env, gomplate.Template{Expression: expr})
				Expect(err).ToNot(HaveOccurred(), "CEL error in %s: %s\n%s", name, expr, ctx)
				Expect(ok).To(BeTrue(), "assertion failed in %s: %s\n%s", name, expr, ctx)
			}
		})
	}
})

var _ = Describe("ExtractedConfig warnings", func() {
	It("deduplicates warnings inline and increments count", func() {
		result := ExtractedConfig{}
		result.SetTransformContext("input-a", "output-a", "expr-a")

		result.AddWarning(v1.Warning{Error: "duplicate warning"})
		result.AddWarning(v1.Warning{Error: "duplicate warning", Result: "ignored"})

		Expect(result.Warnings).To(HaveLen(1))
		Expect(result.Warnings[0].Count).To(Equal(2))
		Expect(result.Warnings[0].Input).To(Equal("input-a"))
		Expect(result.Warnings[0].Output).To(Equal("output-a"))
		Expect(result.Warnings[0].Expr).To(Equal("expr-a"))
	})

	It("merges warning counts without replacing the first context example", func() {
		left := ExtractedConfig{}
		left.SetTransformContext("left-input", "left-output", "left-expr")
		left.AddWarning(v1.Warning{Error: "duplicate warning"})

		right := ExtractedConfig{}
		right.SetTransformContext("right-input", "right-output", "right-expr")
		right.AddWarning(v1.Warning{Error: "duplicate warning"})

		merged := left.Merge(right)

		Expect(merged.Warnings).To(HaveLen(1))
		Expect(merged.Warnings[0].Count).To(Equal(2))
		Expect(merged.Warnings[0].Input).To(Equal("left-input"))
		Expect(merged.Warnings[0].Output).To(Equal("left-output"))
		Expect(merged.Warnings[0].Expr).To(Equal("left-expr"))
	})
})
