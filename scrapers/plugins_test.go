package scrapers

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
	"github.com/flanksource/config-db/db/models"
	dutymodels "github.com/flanksource/duty/models"
	"github.com/flanksource/duty/types"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
	apiv1 "k8s.io/api/core/v1"
	k8sTypes "k8s.io/apimachinery/pkg/types"
)

var _ = Describe("plugins test", Ordered, func() {
	var tx *gorm.DB
	var scrapeConfig v1.ScrapeConfig
	var scraperCtx api.ScrapeContext
	var savedPlugins []dutymodels.ScrapePlugin
	var cm *apiv1.ConfigMap

	var plugins = []v1.ScrapePluginSpec{
		{
			Change: v1.TransformChange{
				Exclude: []string{`change_type == "diff" && diff.contains("restricted-keyword")`},
			},
		},
	}

	BeforeAll(func() {
		tx = DefaultContext.DB().Begin()
		Expect(tx.Error).To(BeNil())

		cm = createRandomConfigMap("first-random-config-map")

		for _, p := range plugins {
			spec, err := json.Marshal(p)
			Expect(err).To(BeNil())

			pluginModel := dutymodels.ScrapePlugin{
				Spec:   spec,
				Source: dutymodels.SourceUI,
			}
			err = tx.Create(&pluginModel).Error
			Expect(err).To(BeNil())

			savedPlugins = append(savedPlugins, pluginModel)
		}

		scrapeConfig = getConfigSpec("kubernetes")
		scrapeConfig.Spec.Kubernetes[0].Kubeconfig = &types.EnvVar{
			ValueStatic: kubeConfigPath,
		}
		scrapeConfig.Spec.Kubernetes[0].Transform.Change.Exclude = []string{}

		scModel, err := scrapeConfig.ToModel()
		Expect(err).NotTo(HaveOccurred(), "failed to convert scrape config to model")
		scModel.Source = dutymodels.SourceCRD

		scraperCtx = api.NewScrapeContext(DefaultContext.WithDB(tx, DefaultContext.Pool())).WithScrapeConfig(&scrapeConfig)

		_, err = db.ReloadAllScrapePlugins(scraperCtx.Context)
		Expect(err).To(BeNil())

		err = scraperCtx.DB().Create(&scModel).Error
		Expect(err).NotTo(HaveOccurred(), "failed to create scrape config")

		scrapeConfig.SetUID(k8sTypes.UID(scModel.ID.String()))
	})

	AfterAll(func() {
		for _, p := range savedPlugins {
			err := tx.Delete(p).Error
			Expect(err).To(BeNil())
		}

		err := tx.Rollback().Error
		Expect(err).To(BeNil())
	})

	It("should scrape the config map", func() {
		_, err := RunScraper(scraperCtx)
		Expect(err).To(BeNil())

		var found models.ConfigItem
		Expect(scraperCtx.DB().Where("id = ?", cm.GetUID()).First(&found).Error).To(BeNil())
	})

	It("should update the config item and expect diff change", func() {
		cm.Data = map[string]string{
			"key": fmt.Sprintf("value-%d", rand.Int64()),
		}
		err := k8sClient.Update(scraperCtx, cm)
		Expect(err).NotTo(HaveOccurred(), "failed to create ConfigMap")

		_, err = RunScraper(scraperCtx)
		Expect(err).To(BeNil())

		var changes []models.ConfigChange
		err = tx.Where("config_id = ?", cm.GetUID()).Find(&changes).Error
		Expect(err).To(BeNil())
		Expect(len(changes)).To(Equal(1))
	})

	It("should update the config item but the diff change should have been excluded due to the plugin", func() {
		cm.Data = map[string]string{
			"key": "restricted-keyword",
		}
		err := k8sClient.Update(scraperCtx, cm)
		Expect(err).NotTo(HaveOccurred(), "failed to create ConfigMap")

		_, err = RunScraper(scraperCtx)
		Expect(err).To(BeNil())

		var changes []models.ConfigChange
		err = tx.Where("config_id = ?", cm.GetUID()).Where("diff LIKE '%restricted%'").Find(&changes).Error
		Expect(err).To(BeNil())
		Expect(len(changes)).To(Equal(0))
	})
})
