package scrapers

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"

	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
	"github.com/flanksource/config-db/db/models"
	dutymodels "github.com/flanksource/duty/models"
	"github.com/flanksource/gomplate/v3"
	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	"gopkg.in/yaml.v3"
)

type e2ePrePopulateConfig struct {
	ConfigClass string   `yaml:"config_class"`
	Type        string   `yaml:"type"`
	ExternalID  []string `yaml:"external_id"`
	Config      string   `yaml:"config"`
}

type e2ePrePopulate struct {
	Configs []e2ePrePopulateConfig `yaml:"configs"`
}

type e2eFixture struct {
	Spec        map[string]any  `yaml:"spec"`
	PrePopulate e2ePrePopulate  `yaml:"pre_populate"`
	Assertions  []string        `yaml:"assertions"`
}

var _ = Describe("e2e extraction fixtures", func() {
	fixtures, err := filepath.Glob("extract/testdata/e2e/*.yaml")
	if err != nil {
		panic(err)
	}

	for _, fixturePath := range fixtures {
		name := filepath.Base(fixturePath)

		It("e2e fixture: "+name, func() {
			// At test time CWD is repo root (BeforeSuite does os.Chdir(".."))
			data, err := os.ReadFile("scrapers/" + fixturePath)
			Expect(err).ToNot(HaveOccurred())

			var fixture e2eFixture
			Expect(yaml.Unmarshal(data, &fixture)).To(Succeed())
			Expect(fixture.Spec).ToNot(BeNil(), "e2e fixture %s must have a spec field", name)
			Expect(fixture.Assertions).ToNot(BeEmpty(), "fixture %s has no assertions", name)

			// Validate spec has no unknown fields
			specJSON, err := json.Marshal(fixture.Spec)
			Expect(err).ToNot(HaveOccurred())
			decoder := json.NewDecoder(bytes.NewReader(specJSON))
			decoder.DisallowUnknownFields()
			var specValidation v1.ScraperSpec
			Expect(decoder.Decode(&specValidation)).To(Succeed(), "spec in %s contains unknown fields", name)

			// Build ScrapeConfig YAML from spec
			scrapeConfigYAML := buildScrapeConfigYAML(name, fixture.Spec)
			tmpFile, err := os.CreateTemp("", "e2e-fixture-*.yaml")
			Expect(err).ToNot(HaveOccurred())
			defer func() { _ = os.Remove(tmpFile.Name()) }()
			_, err = tmpFile.Write(scrapeConfigYAML)
			Expect(err).ToNot(HaveOccurred())
			Expect(tmpFile.Close()).To(Succeed())

			configs, err := v1.ParseConfigs(tmpFile.Name())
			Expect(err).ToNot(HaveOccurred())
			Expect(configs).ToNot(BeEmpty())
			config := configs[0]

			// Pre-populate configs in DB
			var createdItems []string
			scraperModel, err := db.PersistScrapeConfigFromFile(DefaultContext, config)
			Expect(err).ToNot(HaveOccurred())

			for _, preConfig := range fixture.PrePopulate.Configs {
				ci := &models.ConfigItem{
					ID:          uuid.NewString(),
					ConfigClass: preConfig.ConfigClass,
					Type:        preConfig.Type,
					ExternalID:  preConfig.ExternalID,
					ScraperID:   &scraperModel.ID,
					Config:      lo.ToPtr(preConfig.Config),
				}
				Expect(DefaultContext.DB().Create(ci).Error).ToNot(HaveOccurred())
				createdItems = append(createdItems, ci.ID)
			}

			// Cleanup after test
			defer func() {
				for _, id := range createdItems {
					DefaultContext.DB().Where("config_id = ?", id).Delete(&models.ConfigChange{})
					DefaultContext.DB().Delete(&models.ConfigItem{}, "id = ?", id)
				}
				DefaultContext.DB().Where("id = ?", scraperModel.ID).Delete(&dutymodels.ConfigScraper{})
			}()

			// Run scraper
			scraperCtx := ctx.WithScrapeConfig(&config)
			scraperCtx, err = scraperCtx.InitTempCache()
			Expect(err).ToNot(HaveOccurred())

			results, err := Run(scraperCtx)
			Expect(err).ToNot(HaveOccurred())

			summary, err := db.SaveResults(scraperCtx, results)
			Expect(err).ToNot(HaveOccurred())

			// Build CEL env by aggregating all results
			env := buildE2EEnv(results, summary)

			for _, expr := range fixture.Assertions {
				ok, err := gomplate.RunTemplateBool(env, gomplate.Template{Expression: expr})
				Expect(err).ToNot(HaveOccurred(), "CEL error in %s: %s", name, expr)
				Expect(ok).To(BeTrue(), "assertion failed in %s: %s\nenv: %v", name, expr, env)
			}
		})
	}
})

func buildScrapeConfigYAML(name string, spec map[string]any) []byte {
	doc := map[string]any{
		"apiVersion": "configs.flanksource.com/v1",
		"kind":       "ScrapeConfig",
		"metadata": map[string]any{
			"name": "e2e-" + name,
		},
		"spec": spec,
	}
	out, err := yaml.Marshal(doc)
	Expect(err).ToNot(HaveOccurred())
	return out
}

func buildE2EEnv(results []v1.ScrapeResult, summary v1.ScrapeSummary) map[string]any {
	var allChanges []v1.ChangeResult
	for _, r := range results {
		allChanges = append(allChanges, r.Changes...)
	}

	env := map[string]any{
		"config": nil,
	}

	// Marshal changes to map form for CEL
	changesRaw, _ := json.Marshal(allChanges)
	var changesSlice []any
	_ = json.Unmarshal(changesRaw, &changesSlice)
	if changesSlice == nil {
		changesSlice = []any{}
	}
	env["changes"] = changesSlice

	for _, key := range []string{"analysis", "access_logs", "config_access", "external_users", "external_groups", "external_user_groups", "external_roles", "warnings"} {
		env[key] = []any{}
	}

	// Build summary compatible with ExtractionSummary shape used by lightweight fixtures
	totals := summary.Totals()
	env["summary"] = map[string]any{
		"changes": map[string]any{
			"scraped": len(allChanges),
			"saved":   totals.Changes,
		},
	}

	return env
}
