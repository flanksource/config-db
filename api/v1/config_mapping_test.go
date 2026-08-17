package v1

import (
	"os"

	"github.com/flanksource/duty/types"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/yaml"
)

var _ = Describe("config mappings", func() {
	It("makes a transform non-empty", func() {
		transform := Transform{
			Configs: TransformConfigs{
				Mapping: []ConfigMapping{{Match: `config_type == "old"`}},
			},
		}

		Expect(transform.IsEmpty()).To(BeFalse())
		Expect(transform.String()).To(ContainSubstring("configs="))
	})

	It("appends plugin mappings after scrape config mappings", func() {
		base := BaseScraper{
			Transform: Transform{
				Configs: TransformConfigs{Mapping: []ConfigMapping{{Match: "scrape-config"}}},
				Change:  TransformChange{Exclude: []string{"scrape-config"}},
			},
		}
		plugin := ScrapePluginSpec{
			Configs:      TransformConfigs{Mapping: []ConfigMapping{{Match: "plugin"}}},
			Change:       TransformChange{Exclude: []string{"plugin"}},
			Locations:    []LocationOrAlias{{}},
			Aliases:      []LocationOrAlias{{}},
			Relationship: []RelationshipConfig{{Filter: "true"}},
			Properties:   []ConfigProperties{{}},
		}

		got := base.ApplyPlugins(plugin)

		Expect(got.Transform.Configs.Mapping).To(HaveLen(2))
		Expect(got.Transform.Configs.Mapping[0].Match).To(Equal("scrape-config"))
		Expect(got.Transform.Configs.Mapping[1].Match).To(Equal("plugin"))
		Expect(got.Transform.Change.Exclude).To(Equal([]string{"scrape-config", "plugin"}))
		Expect(got.Transform.Locations).To(HaveLen(1))
		Expect(got.Transform.Aliases).To(HaveLen(1))
		Expect(got.Transform.Relationship).To(HaveLen(1))
		Expect(got.Properties).To(HaveLen(1))
	})

	It("loads the CNPG plugin fixture", func() {
		data, err := os.ReadFile("../../fixtures/plugins/cnpg.yaml")
		Expect(err).NotTo(HaveOccurred())

		var plugin ScrapePlugin
		Expect(yaml.Unmarshal(data, &plugin)).To(Succeed())
		Expect(plugin.Name).To(Equal("cnpg"))
		Expect(plugin.Spec.Configs.Mapping).To(HaveLen(1))
		Expect(plugin.Spec.Configs.Mapping[0].Type.Expr).To(Equal(types.CelExpression(`"CNPG::" + config.kind`)))
		Expect(plugin.Spec.Relationship).To(HaveLen(4))
	})
})
