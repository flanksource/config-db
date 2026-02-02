package scrapers

import (
	gocontext "context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"time"

	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/config-db/api"
	"github.com/flanksource/config-db/db"
	"github.com/flanksource/config-db/db/models"
	"github.com/flanksource/config-db/pkg/api"
	"github.com/flanksource/duty"
	dutymodels "github.com/flanksource/duty/models"
	"github.com/flanksource/duty/query"
	"github.com/flanksource/duty/types"
	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	"gorm.io/gorm"
	k8sTypes "k8s.io/apimachinery/pkg/types"

	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("CRD Sync test", Ordered, func() {
	var configItem dutymodels.ConfigItem
	var playbook dutymodels.Playbook
	var scrapeConfigModel dutymodels.ConfigScraper
	var scraperCtx api.ScrapeContext

	BeforeAll(func() {
		scrapeConfig := getConfigSpec("file-crd-sync")

		scModel, err := scrapeConfig.ToModel()
		Expect(err).NotTo(HaveOccurred(), "failed to convert scrape config to model")
		scModel.Source = dutymodels.SourceUI

		err = DefaultContext.DB().Create(&scModel).Error
		Expect(err).NotTo(HaveOccurred(), "failed to create scrape config")

		scrapeConfig.SetUID(k8sTypes.UID(scModel.ID.String()))
		scraperCtx = api.NewScrapeContext(DefaultContext).WithScrapeConfig(&scrapeConfig)

		scrapeConfigModel = scModel
	})

	AfterAll(func() {
		err := DefaultContext.DB().Delete(configItem).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete config item")

		err = DefaultContext.DB().Delete(playbook).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete playbook")

		err = DefaultContext.DB().Delete(scrapeConfigModel).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete scrape config")
	})

	It("should scrape", func() {
		output, err := RunScraper(scraperCtx)
		Expect(err).To(BeNil())

		Expect(output.Total).To(Equal(1))
	})

	It("should have the updated the db", func() {
		err := DefaultContext.DB().Where("name = ?", "echo-input-name").First(&configItem).Error
		Expect(err).NotTo(HaveOccurred(), "failed to find configmap")

		err = DefaultContext.DB().Where("name = ?", "echo-input-name").First(&playbook).Error
		Expect(err).NotTo(HaveOccurred(), "failed to find playbook")
		Expect(playbook.Source).To(Equal(dutymodels.SourceCRDSync))
	})
})

var _ = Describe("Dedup test", Ordered, func() {
	configA := apiv1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "change-dedup-test",
			Namespace: "default",
		},
		Data: map[string]string{
			"key": uuid.NewString(),
		},
	}

	configB := apiv1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "change-dedup-test-2",
			Namespace: "default",
		},
		Data: map[string]string{
			"key": uuid.NewString(),
		},
	}

	var cmA, cmB models.ConfigItem
	var scrapeConfig v1.ScrapeConfig
	var scraperCtx api.ScrapeContext

	BeforeAll(func() {
		scrapeConfig = getConfigSpec("kubernetes")
		scrapeConfig.Spec.Kubernetes[0].Kubeconfig = &types.EnvVar{
			ValueStatic: kubeConfigPath,
		}

		scModel, err := scrapeConfig.ToModel()
		Expect(err).NotTo(HaveOccurred(), "failed to convert scrape config to model")
		scModel.Source = dutymodels.SourceCRD

		err = DefaultContext.DB().Create(&scModel).Error
		Expect(err).NotTo(HaveOccurred(), "failed to create scrape config")

		scrapeConfig.SetUID(k8sTypes.UID(scModel.ID.String()))

		scraperCtx = api.NewScrapeContext(DefaultContext).WithScrapeConfig(&scrapeConfig)
	})

	AfterAll(func() {
		err := k8sClient.Delete(DefaultContext, &configA)
		Expect(err).NotTo(HaveOccurred(), "failed to delete ConfigMap")
	})

	var _ = Context("Populate configmap changes", func() {
		for i := 0; i < 11; i++ {
			if i == 0 {
				It("should have created the config map", func() {
					err := k8sClient.Create(DefaultContext, &configA)
					Expect(err).NotTo(HaveOccurred(), "failed to create first ConfigMap")

					err = k8sClient.Create(DefaultContext, &configB)
					Expect(err).NotTo(HaveOccurred(), "failed to create second ConfigMap")
				})
			} else {
				It(fmt.Sprintf("[%d] should update the config map", i), func() {
					configA.Data["key"] = uuid.NewString()
					err := k8sClient.Update(DefaultContext, &configA)
					Expect(err).NotTo(HaveOccurred(), "failed to update ConfigMap")

					configB.Data["key"] = uuid.NewString()
					err = k8sClient.Update(DefaultContext, &configB)
					Expect(err).NotTo(HaveOccurred(), "failed to update ConfigMap")
				})
			}

			It(fmt.Sprintf("[%d] should scrape the configmap", i), func() {
				output, err := RunScraper(scraperCtx)
				Expect(err).To(BeNil())

				Expect(output.Total).To(BeNumerically(">", 0))
			})

			It(fmt.Sprintf("[%d] should have the updated the db", i), func() {
				{
					err := DefaultContext.DB().Where("name = ?", configA.Name).First(&cmA).Error
					Expect(err).NotTo(HaveOccurred(), "failed to find configmap")

					var configMap apiv1.ConfigMap
					err = json.Unmarshal([]byte(*cmA.Config), &configMap)
					Expect(err).NotTo(HaveOccurred())
					Expect(configMap.Data["key"]).To(Equal(configA.Data["key"]))
				}

				{
					err := DefaultContext.DB().Where("name = ?", configB.Name).First(&cmB).Error
					Expect(err).NotTo(HaveOccurred(), "failed to find configmap")

					var configMap apiv1.ConfigMap
					err = json.Unmarshal([]byte(*cmB.Config), &configMap)
					Expect(err).NotTo(HaveOccurred())
					Expect(configMap.Data["key"]).To(Equal(configB.Data["key"]))
				}
			})
		}
	})

	It("should have populated 1 change with 10 counts  for config A", func() {
		var changes []models.ConfigChange
		err := DefaultContext.DB().Where("config_id = ?", cmA.ID).Find(&changes).Error
		Expect(err).NotTo(HaveOccurred(), "failed to find configmap")

		Expect(len(changes)).To(Equal(1))
		Expect(changes[0].Count).To(Equal(10))
	})

	It("should have populated 1 change with 10 counts for config B", func() {
		var changes []models.ConfigChange
		err := DefaultContext.DB().Where("config_id = ?", cmB.ID).Find(&changes).Error
		Expect(err).NotTo(HaveOccurred(), "failed to find configmap")

		Expect(len(changes)).To(Equal(1))
		Expect(changes[0].Count).To(Equal(10))
	})
})

var _ = Describe("plugins test", Ordered, func() {
	// run the entire test suite in a transaction so it doesn't affect other tests cases
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

var _ = Describe("Scrapers test", Ordered, func() {
	BeforeAll(func() {
		var people int64
		Expect(DefaultContext.DB().Table("people").Count(&people).Error).ToNot(HaveOccurred())
		Expect(people).To(BeNumerically(">=", 1))
	})

	Describe("Test kubernetes relationship", func() {
		var scrapeConfig v1.ScrapeConfig

		It("should prepare scrape config", func() {
			scrapeConfig = getConfigSpec("kubernetes")
			scrapeConfig.Spec.Kubernetes[0].Exclusions = v1.KubernetesExclusionConfig{}
			scrapeConfig.Spec.Kubernetes[0].Kubeconfig = &types.EnvVar{
				ValueStatic: kubeConfigPath,
			}
			scrapeConfig.Spec.Kubernetes[0].Relationships = append(scrapeConfig.Spec.Kubernetes[0].Relationships, v1.KubernetesRelationshipSelectorTemplate{
				Kind:      duty.Lookup{Value: "ConfigMap"},
				Name:      duty.Lookup{Label: "flanksource/name"},
				Namespace: duty.Lookup{Label: "flanksource/namespace"},
			})
		})

		It("should save a configMap", func() {
			cm1 := &apiv1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "first-config",
					Namespace: "default",
					Labels: map[string]string{
						"flanksource/name":      "second-config",
						"flanksource/namespace": "default",
					},
				},
				Data: map[string]string{"key": "value"},
			}

			err := k8sClient.Create(gocontext.Background(), cm1)
			Expect(err).NotTo(HaveOccurred(), "failed to create ConfigMap")

			sec1 := &apiv1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "first-secret",
					Namespace: "default",
					Labels: map[string]string{
						"flanksource/name":      "second-config",
						"flanksource/namespace": "default",
					},
				},
				Data: nil,
			}

			err = k8sClient.Create(gocontext.Background(), sec1)
			Expect(err).NotTo(HaveOccurred(), "failed to create Secret")
		})

		It("should save a second configMap", func() {
			cm2 := &apiv1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "second-config",
					Namespace: "default",
				},
				Data: map[string]string{"key": "value"},
			}

			err := k8sClient.Create(gocontext.Background(), cm2)
			Expect(err).NotTo(HaveOccurred(), "failed to create test MyKind resource")
		})

		It("should successfully complete first scrape run", func() {
			scraperCtx := api.NewScrapeContext(DefaultContext).WithScrapeConfig(&scrapeConfig)
			_, err := RunScraper(scraperCtx)
			Expect(err).To(BeNil())
		})

		It("should have saved the two config items to database", func() {
			var configItems []models.ConfigItem
			err := DefaultContext.DB().Where("name IN (?, ?, ?)", "first-config", "second-config", "first-secret").Find(&configItems).Error
			Expect(err).To(BeNil())

			Expect(len(configItems)).To(Equal(3))
		})

		It("should have saved all tags as labels", func() {
			var configItems []models.ConfigItem
			err := DefaultContext.DB().
				Where("NOT (labels @> tags)").
				Where("name IN (?, ?, ?)", "first-config", "second-config", "first-secret").Find(&configItems).Error
			Expect(err).To(BeNil())
			for _, c := range configItems {
				logger.Errorf("config (%s/%s) doesn't have all the tags(%v) as labels(%v)",
					c.ID, lo.FromPtr(c.Name), c.Tags, c.Labels)
			}
			Expect(len(configItems)).To(Equal(0))
		})

		It("should correctly setup kubernetes relationship", func() {
			query.FlushGettersCache()

			scraperCtx := api.NewScrapeContext(DefaultContext).WithScrapeConfig(&scrapeConfig)
			_, err := RunScraper(scraperCtx)
			Expect(err).To(BeNil())

			var configRelationships []models.ConfigRelationship
			err = scraperCtx.DB().Find(&configRelationships).Error
			Expect(err).To(BeNil())

			// 2 relationships are coming from the relationship config above &
			// the remaining 21 are coming from the relationship with the namespace.
			// eg. Namespace->ConfigMap,Namespace->Endpoints, Namespace->RoleBinding,  Namespace->Role ...
			Expect(len(configRelationships)).To(BeNumerically(">=", 23))
		})
	})

	Describe("Testing file fixtures", func() {
		fixtures := []string{
			"file-git",
			"file-script",
			"file-script-gotemplate",
			"file-mask",
			"file-exclusion",
			"file-postgres-properties",
		}

		for _, fixtureName := range fixtures {
			fixture := fixtureName
			It(fixture, func() {
				config := getConfigSpec(fixture)
				expected := getFixtureResult(fixture)
				ctx := api.NewScrapeContext(DefaultContext).WithScrapeConfig(&config)

				results, err := Run(ctx)
				Expect(err).To(BeNil())

				_, err = db.SaveResults(ctx, results)
				Expect(err).To(BeNil())

				if len(results) != len(expected) {
					Fail(fmt.Sprintf("expected %d results, got: %d", len(expected), len(results)))
					return
				}

				for i := 0; i < len(expected); i++ {
					want := expected[i]
					got := results[i]

					Expect(want.ID).To(Equal(got.ID))
					Expect(want.Type).To(Equal(got.Type))
					Expect(want.ConfigClass).To(Equal(got.ConfigClass))
					wantJSON, _ := json.Marshal(want.Config)
					gotJSON, _ := json.Marshal(got.Config)
					Expect(wantJSON).To(MatchJSON(gotJSON))
				}
			})
		}
	})

	Describe("Test full: true", func() {

		var storedConfigItem *models.ConfigItem

		It("should create a new config item", func() {
			config := getConfigSpec("file-car")
			_, err := db.PersistScrapeConfigFromFile(ctx.DutyContext(), config)
			Expect(err).To(BeNil())

			ctx := api.NewScrapeContext(DefaultContext).WithScrapeConfig(&config)
			ctx, err = ctx.InitTempCache()
			Expect(err).To(BeNil())

			results, err := Run(ctx)
			Expect(err).To(BeNil())

			_, err = db.SaveResults(ctx, results)
			Expect(err).To(BeNil())

			configItemID, err := db.GetConfigItem(ctx, "Car", "A123")
			Expect(err).To(BeNil())
			Expect(configItemID).ToNot(BeNil())

			storedConfigItem, err = GetConfigItemFromID(ctx, configItemID.ID)
			Expect(err).To(BeNil())
			Expect(storedConfigItem).ToNot(BeNil())
		})

		It("should store the changes from the config", func() {
			config := getConfigSpec("file-car-change")

			ctx := api.NewScrapeContext(DefaultContext).WithScrapeConfig(&config)

			results, err := Run(ctx)
			Expect(err).To(BeNil())

			_, err = db.SaveResults(ctx, results)
			Expect(err).To(BeNil())

			configItemID, err := db.GetConfigItem(ctx, "Car", "A123")
			Expect(err).To(BeNil())
			Expect(configItemID).ToNot(BeNil())

			// Expect the config_changes to be stored
			items, err := db.FindConfigChangesByItemID(ctx, configItemID.ID)
			Expect(err).To(BeNil())
			Expect(len(items)).To(Equal(1))
			Expect(items[0].ConfigID).To(Equal(storedConfigItem.ID))
		})

		It("should not change the original config", func() {
			configItemID, err := db.GetConfigItem(ctx, "Car", "A123")
			Expect(err).To(BeNil())
			Expect(configItemID).ToNot(BeNil())

			configItem, err := GetConfigItemFromID(ctx, configItemID.ID)
			Expect(err).To(BeNil())
			Expect(storedConfigItem).ToNot(BeNil())

			Expect(configItem, storedConfigItem)
		})

		It("should retain config changes as per the spec", func() {
			dummyScraper := dutymodels.ConfigScraper{
				Name:   "Test",
				Spec:   `{"foo":"bar"}`,
				Source: dutymodels.SourceConfigFile,
			}
			err := ctx.DB().Create(&dummyScraper).Error
			Expect(err).To(BeNil())

			configItemID := uuid.New().String()
			dummyCI := models.ConfigItem{
				ID:          configItemID,
				ConfigClass: "Test",
				Type:        "Test",
				ScraperID:   &dummyScraper.ID,
			}
			configItemID2 := uuid.New().String()
			dummyCI2 := models.ConfigItem{
				ID:          configItemID2,
				ConfigClass: "Test",
				Type:        "Test",
				ScraperID:   &dummyScraper.ID,
			}
			err = ctx.DB().Create(&dummyCI).Error
			Expect(err).To(BeNil())
			err = ctx.DB().Create(&dummyCI2).Error
			Expect(err).To(BeNil())

			twoDaysAgo := time.Now().Add(-2 * 24 * time.Hour)
			fiveDaysAgo := time.Now().Add(-5 * 24 * time.Hour)
			tenDaysAgo := time.Now().Add(-10 * 24 * time.Hour)
			configChanges := []models.ConfigChange{
				{ConfigID: configItemID, ChangeType: "TestDiff", ExternalChangeID: lo.ToPtr(uuid.New().String())},
				{ConfigID: configItemID, ChangeType: "TestDiff", ExternalChangeID: lo.ToPtr(uuid.New().String())},
				{ConfigID: configItemID, ChangeType: "TestDiff", ExternalChangeID: lo.ToPtr(uuid.New().String())},
				{ConfigID: configItemID, ChangeType: "TestDiff", ExternalChangeID: lo.ToPtr(uuid.New().String())},
				{ConfigID: configItemID, ChangeType: "TestDiff", ExternalChangeID: lo.ToPtr(uuid.New().String())},
				{ConfigID: configItemID, ChangeType: "TestDiff", CreatedAt: twoDaysAgo, ExternalChangeID: lo.ToPtr(uuid.New().String())},
				{ConfigID: configItemID, ChangeType: "TestDiff", CreatedAt: twoDaysAgo, ExternalChangeID: lo.ToPtr(uuid.New().String())},
				{ConfigID: configItemID, ChangeType: "TestDiff", CreatedAt: twoDaysAgo, ExternalChangeID: lo.ToPtr(uuid.New().String())},
				{ConfigID: configItemID, ChangeType: "TestDiff", CreatedAt: twoDaysAgo, ExternalChangeID: lo.ToPtr(uuid.New().String())},
				{ConfigID: configItemID, ChangeType: "TestDiff", CreatedAt: fiveDaysAgo, ExternalChangeID: lo.ToPtr(uuid.New().String())},
				{ConfigID: configItemID, ChangeType: "TestDiff", CreatedAt: fiveDaysAgo, ExternalChangeID: lo.ToPtr(uuid.New().String())},
				{ConfigID: configItemID, ChangeType: "TestDiff", CreatedAt: fiveDaysAgo, ExternalChangeID: lo.ToPtr(uuid.New().String())},
				{ConfigID: configItemID, ChangeType: "TestDiff", CreatedAt: fiveDaysAgo, ExternalChangeID: lo.ToPtr(uuid.New().String())},
				{ConfigID: configItemID, ChangeType: "TestDiff", CreatedAt: fiveDaysAgo, ExternalChangeID: lo.ToPtr(uuid.New().String())},
				{ConfigID: configItemID, ChangeType: "TestDiff", CreatedAt: fiveDaysAgo, ExternalChangeID: lo.ToPtr(uuid.New().String())},
				{ConfigID: configItemID, ChangeType: "TestDiff", CreatedAt: tenDaysAgo, ExternalChangeID: lo.ToPtr(uuid.New().String())},
				{ConfigID: configItemID, ChangeType: "TestDiff", CreatedAt: tenDaysAgo, ExternalChangeID: lo.ToPtr(uuid.New().String())},
				{ConfigID: configItemID2, ChangeType: "TestDiff", ExternalChangeID: lo.ToPtr(uuid.New().String())},
				{ConfigID: configItemID2, ChangeType: "TestDiff", ExternalChangeID: lo.ToPtr(uuid.New().String())},
			}

			err = ctx.DB().Table("config_changes").Create(&configChanges).Error
			Expect(err).To(BeNil())

			var currentCount int
			err = ctx.DB().
				Raw(`SELECT COUNT(*) FROM config_changes WHERE change_type = ?`, "TestDiff").
				Scan(&currentCount).
				Error
			Expect(err).To(BeNil())
			Expect(currentCount).To(Equal(len(configChanges)))

			// Everything older than 8 days should be removed
			err = ProcessChangeRetention(ctx.Context, dummyScraper.ID, v1.ChangeRetentionSpec{Name: "TestDiff", Age: "8d"})
			Expect(err).To(BeNil())
			var count1 int
			err = ctx.DB().
				Raw(`SELECT COUNT(*) FROM config_changes WHERE change_type = ? AND config_id = ?`, "TestDiff", configItemID).
				Scan(&count1).
				Error
			Expect(err).To(BeNil())
			Expect(count1).To(Equal(15))

			// The other config item should not be touched
			var otherCount1 int
			err = ctx.DB().
				Raw(`SELECT COUNT(*) FROM config_changes WHERE change_type = ? AND config_id = ?`, "TestDiff", configItemID2).
				Scan(&otherCount1).
				Error
			Expect(err).To(BeNil())
			Expect(otherCount1).To(Equal(2))

			// Only keep latest 12 config changes
			err = ProcessChangeRetention(ctx.Context, dummyScraper.ID, v1.ChangeRetentionSpec{Name: "TestDiff", Count: 12})
			Expect(err).To(BeNil())
			var count2 int
			err = ctx.DB().
				Raw(`SELECT COUNT(*) FROM config_changes WHERE change_type = ? AND config_id = ?`, "TestDiff", configItemID).
				Scan(&count2).
				Error
			Expect(err).To(BeNil())
			Expect(count2).To(Equal(12))

			// The other config item should not be touched
			var otherCount2 int
			err = ctx.DB().
				Raw(`SELECT COUNT(*) FROM config_changes WHERE change_type = ? AND config_id = ?`, "TestDiff", configItemID2).
				Scan(&otherCount2).
				Error
			Expect(err).To(BeNil())
			Expect(otherCount2).To(Equal(2))

			// Keep config changes which are newer than 3 days and max count can be 10
			err = ProcessChangeRetention(ctx.Context, dummyScraper.ID, v1.ChangeRetentionSpec{Name: "TestDiff", Age: "3d", Count: 10})
			Expect(err).To(BeNil())
			var count3 int
			err = ctx.DB().
				Raw(`SELECT COUNT(*) FROM config_changes WHERE change_type = ? AND config_id = ?`, "TestDiff", configItemID).
				Scan(&count3).
				Error
			Expect(err).To(BeNil())
			Expect(count3).To(Equal(9))

			// No params in ChangeRetentionSpec should fail
			err = ProcessChangeRetention(ctx.Context, dummyScraper.ID, v1.ChangeRetentionSpec{Name: "TestDiff"})
			Expect(err).ToNot(BeNil())

			// The other config item should not be touched
			var otherCount3 int
			err = ctx.DB().
				Raw(`SELECT COUNT(*) FROM config_changes WHERE change_type = ? AND config_id = ?`, "TestDiff", configItemID2).
				Scan(&otherCount3).
				Error
			Expect(err).To(BeNil())
			Expect(otherCount3).To(Equal(2))

		})
	})
})

func getConfigSpec(name string) v1.ScrapeConfig {
	configs, err := v1.ParseConfigs("fixtures/" + name + ".yaml")
	if err != nil {
		Fail(fmt.Sprintf("Failed to parse config: %v", err))
	}
	return configs[0]
}

func getFixtureResult(fixture string) []v1.ScrapeResult {
	data, err := os.ReadFile("fixtures/expected/" + fixture + ".json")
	if err != nil {
		Fail(fmt.Sprintf("Failed to read fixture: %v", err))
	}
	var results []v1.ScrapeResult

	if err := json.Unmarshal(data, &results); err != nil {
		Fail(fmt.Sprintf("Failed to unmarshal fixture: %v", err))
	}
	return results
}

func createRandomConfigMap(name string) *apiv1.ConfigMap {
	cm1 := &apiv1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Data: map[string]string{
			"key": fmt.Sprintf("value-%d", rand.Int64()),
		},
	}

	err := k8sClient.Create(gocontext.Background(), cm1)
	Expect(err).NotTo(HaveOccurred(), "failed to create ConfigMap")

	return cm1
}

// GetConfigItemFromID returns a single config item result
func GetConfigItemFromID(ctx api.ScrapeContext, id string) (*models.ConfigItem, error) {
	var ci models.ConfigItem
	err := ctx.DB().Limit(1).Omit("config").Find(&ci, "id = ?", id).Error
	return &ci, err
}

var _ = Describe("External users e2e test", Ordered, func() {
	var scrapeConfig v1.ScrapeConfig
	var scraperCtx api.ScrapeContext
	var scraperModel dutymodels.ConfigScraper

	BeforeAll(func() {
		scrapeConfig = getConfigSpec("file-external-users")

		scModel, err := scrapeConfig.ToModel()
		Expect(err).NotTo(HaveOccurred(), "failed to convert scrape config to model")
		scModel.Source = dutymodels.SourceUI

		err = DefaultContext.DB().Create(&scModel).Error
		Expect(err).NotTo(HaveOccurred(), "failed to create scrape config")

		scrapeConfig.SetUID(k8sTypes.UID(scModel.ID.String()))
		scraperCtx = api.NewScrapeContext(DefaultContext).WithScrapeConfig(&scrapeConfig)

		scraperModel = scModel
	})

	AfterAll(func() {
		// Clean up external users
		err := DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Delete(&dutymodels.ExternalUser{}).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete external users")

		// Clean up external groups
		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Delete(&dutymodels.ExternalGroup{}).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete external groups")

		// Clean up external roles
		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Delete(&dutymodels.ExternalRole{}).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete external roles")

		// Clean up scraper
		err = DefaultContext.DB().Delete(&scraperModel).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete scrape config")
	})

	It("should scrape and save external users, groups, and roles", func() {
		_, err := RunScraper(scraperCtx)
		Expect(err).To(BeNil())
	})

	It("should have saved external users to the database with aliases", func() {
		var users []dutymodels.ExternalUser
		err := DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Find(&users).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(users).To(HaveLen(2))

		userNames := lo.Map(users, func(u dutymodels.ExternalUser, _ int) string { return u.Name })
		Expect(userNames).To(ContainElements("John Doe", "Service Bot"))

		// Verify aliases are saved
		for _, user := range users {
			switch user.Name {
			case "John Doe":
				Expect(user.Aliases).To(ContainElements("john-doe", "jdoe@example.com"))
			case "Service Bot":
				Expect(user.Aliases).To(ContainElements("service-bot", "bot-001"))
			}
		}
	})

	It("should have saved external groups to the database", func() {
		var groups []dutymodels.ExternalGroup
		err := DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Find(&groups).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(groups).To(HaveLen(2))

		groupNames := lo.Map(groups, func(g dutymodels.ExternalGroup, _ int) string { return g.Name })
		Expect(groupNames).To(ContainElements("Administrators", "Developers"))
	})

	It("should have saved external roles to the database", func() {
		var roles []dutymodels.ExternalRole
		err := DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Find(&roles).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(roles).To(HaveLen(2))

		roleNames := lo.Map(roles, func(r dutymodels.ExternalRole, _ int) string { return r.Name })
		Expect(roleNames).To(ContainElements("Admin", "Reader"))
	})

	It("should upsert external users by alias on second scrape (not create duplicates)", func() {
		// Get existing user IDs before second scrape
		var usersBefore []dutymodels.ExternalUser
		err := DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Find(&usersBefore).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(usersBefore).To(HaveLen(2))

		userIDsBefore := lo.Map(usersBefore, func(u dutymodels.ExternalUser, _ int) uuid.UUID { return u.ID })

		// Clear cache to ensure we test DB lookup path
		db.ExternalUserCache.Flush()

		// Run scraper again
		_, err = RunScraper(scraperCtx)
		Expect(err).To(BeNil())

		// Verify same number of users (no duplicates)
		var usersAfter []dutymodels.ExternalUser
		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Find(&usersAfter).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(usersAfter).To(HaveLen(2))

		// Verify same IDs were used (upsert, not insert)
		userIDsAfter := lo.Map(usersAfter, func(u dutymodels.ExternalUser, _ int) uuid.UUID { return u.ID })
		Expect(userIDsAfter).To(ConsistOf(userIDsBefore))
	})

	It("should use cache for external user lookup on subsequent scrapes", func() {
		// Clear cache first
		db.ExternalUserCache.Flush()

		// Run scraper to populate cache
		_, err := RunScraper(scraperCtx)
		Expect(err).To(BeNil())

		// Verify cache is populated for all aliases (key is just the alias)
		johnDoeID, found := db.ExternalUserCache.Get("john-doe")
		Expect(found).To(BeTrue())
		Expect(johnDoeID).NotTo(Equal(uuid.Nil))

		jdoeID, found := db.ExternalUserCache.Get("jdoe@example.com")
		Expect(found).To(BeTrue())
		Expect(jdoeID).To(Equal(johnDoeID)) // Same user, same ID

		serviceBotID, found := db.ExternalUserCache.Get("service-bot")
		Expect(found).To(BeTrue())
		Expect(serviceBotID).NotTo(Equal(uuid.Nil))

		bot001ID, found := db.ExternalUserCache.Get("bot-001")
		Expect(found).To(BeTrue())
		Expect(bot001ID).To(Equal(serviceBotID)) // Same user, same ID
	})
})

var _ = Describe("Config access with external_user_aliases test", Ordered, func() {
	var scrapeConfig v1.ScrapeConfig
	var scraperCtx api.ScrapeContext
	var scraperModel dutymodels.ConfigScraper

	BeforeAll(func() {
		scrapeConfig = getConfigSpec("file-config-access")

		scModel, err := scrapeConfig.ToModel()
		Expect(err).NotTo(HaveOccurred(), "failed to convert scrape config to model")
		scModel.Source = dutymodels.SourceUI

		err = DefaultContext.DB().Create(&scModel).Error
		Expect(err).NotTo(HaveOccurred(), "failed to create scrape config")

		scrapeConfig.SetUID(k8sTypes.UID(scModel.ID.String()))
		scraperCtx = api.NewScrapeContext(DefaultContext).WithScrapeConfig(&scrapeConfig)

		scraperModel = scModel
	})

	AfterAll(func() {
		// Clean up config access
		err := DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Delete(&dutymodels.ConfigAccess{}).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete config access")

		// Clean up external users
		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Delete(&dutymodels.ExternalUser{}).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete external users")

		// Clean up config items
		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Delete(&models.ConfigItem{}).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete config items")

		// Clean up scraper
		err = DefaultContext.DB().Delete(&scraperModel).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete scrape config")
	})

	It("should scrape and save external users and config access", func() {
		_, err := RunScraper(scraperCtx)
		Expect(err).To(BeNil())
	})

	It("should have saved external users to the database", func() {
		var users []dutymodels.ExternalUser
		err := DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Find(&users).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(users).To(HaveLen(2))

		userNames := lo.Map(users, func(u dutymodels.ExternalUser, _ int) string { return u.Name })
		Expect(userNames).To(ContainElements("Alice Smith", "Bob Jones"))
	})

	It("should have saved config access with resolved external_user_id from aliases", func() {
		// Get the external users to find their IDs
		var users []dutymodels.ExternalUser
		err := DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Find(&users).Error
		Expect(err).NotTo(HaveOccurred())

		userByName := make(map[string]dutymodels.ExternalUser)
		for _, u := range users {
			userByName[u.Name] = u
		}

		// Get the config access records
		var configAccesses []dutymodels.ConfigAccess
		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Find(&configAccesses).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(configAccesses).To(HaveLen(2))

		// Verify each config access has the correct external_user_id
		accessByID := make(map[string]dutymodels.ConfigAccess)
		for _, ca := range configAccesses {
			accessByID[ca.ID] = ca
		}

		// access-001 should have Alice's user ID (resolved from "alice-smith" alias)
		access001 := accessByID["access-001"]
		Expect(access001.ExternalUserID).NotTo(BeNil())
		Expect(*access001.ExternalUserID).To(Equal(userByName["Alice Smith"].ID))

		// access-002 should have Bob's user ID (resolved from "bob-jones" or "bob@example.com" alias)
		access002 := accessByID["access-002"]
		Expect(access002.ExternalUserID).NotTo(BeNil())
		Expect(*access002.ExternalUserID).To(Equal(userByName["Bob Jones"].ID))
	})

	It("should have linked config access to the config item via external_config_id", func() {
		// Get the config item
		var configItem models.ConfigItem
		err := DefaultContext.DB().Where("type = ? AND scraper_id = ?", "Organization", scraperModel.ID).First(&configItem).Error
		Expect(err).NotTo(HaveOccurred())

		// Get the config access records
		var configAccesses []dutymodels.ConfigAccess
		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Find(&configAccesses).Error
		Expect(err).NotTo(HaveOccurred())

		// Verify all config access records are linked to the config item
		for _, ca := range configAccesses {
			Expect(ca.ConfigID.String()).To(Equal(configItem.ID))
		}
	})

	It("should resolve external_user_id from cache on second scrape", func() {
		// Clear cache first
		db.ExternalUserCache.Flush()

		// Get initial config access count
		var initialAccesses []dutymodels.ConfigAccess
		err := DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Find(&initialAccesses).Error
		Expect(err).NotTo(HaveOccurred())
		initialCount := len(initialAccesses)

		// Run scraper again
		_, err = RunScraper(scraperCtx)
		Expect(err).To(BeNil())

		// Verify no duplicates (upsert should work)
		var configAccesses []dutymodels.ConfigAccess
		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Find(&configAccesses).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(len(configAccesses)).To(Equal(initialCount))

		// Verify cache is populated with aliases
		aliceID, found := db.ExternalUserCache.Get("alice-smith")
		Expect(found).To(BeTrue())
		Expect(aliceID).NotTo(Equal(uuid.Nil))

		bobID, found := db.ExternalUserCache.Get("bob-jones")
		Expect(found).To(BeTrue())
		Expect(bobID).NotTo(Equal(uuid.Nil))
	})
})

var _ = Describe("Stale external entities deletion test", Ordered, func() {
	var scrapeConfig v1.ScrapeConfig
	var scraperCtx api.ScrapeContext
	var scraperModel dutymodels.ConfigScraper
	var initialUserIDs []uuid.UUID

	BeforeAll(func() {
		scrapeConfig = getConfigSpec("file-config-access")

		scModel, err := scrapeConfig.ToModel()
		Expect(err).NotTo(HaveOccurred(), "failed to convert scrape config to model")
		scModel.Source = dutymodels.SourceUI

		err = DefaultContext.DB().Create(&scModel).Error
		Expect(err).NotTo(HaveOccurred(), "failed to create scrape config")

		scrapeConfig.SetUID(k8sTypes.UID(scModel.ID.String()))
		scraperCtx = api.NewScrapeContext(DefaultContext).WithScrapeConfig(&scrapeConfig)

		scraperModel = scModel
	})

	AfterAll(func() {
		// Clean up config access (including soft-deleted)
		err := DefaultContext.DB().Unscoped().Where("scraper_id = ?", scraperModel.ID).Delete(&dutymodels.ConfigAccess{}).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete config access")

		// Clean up external users (including soft-deleted)
		err = DefaultContext.DB().Unscoped().Where("scraper_id = ?", scraperModel.ID).Delete(&dutymodels.ExternalUser{}).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete external users")

		// Clean up config items
		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Delete(&models.ConfigItem{}).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete config items")

		// Clean up scraper
		err = DefaultContext.DB().Delete(&scraperModel).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete scrape config")
	})

	It("should scrape and save external users initially", func() {
		_, err := RunScraper(scraperCtx)
		Expect(err).To(BeNil())

		var users []dutymodels.ExternalUser
		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Where("deleted_at IS NULL").Find(&users).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(users).To(HaveLen(2))

		initialUserIDs = lo.Map(users, func(u dutymodels.ExternalUser, _ int) uuid.UUID { return u.ID })
	})

	It("should create an additional external user directly in DB", func() {
		// Add a user that won't be in subsequent scrapes (simulating a stale user)
		staleUser := dutymodels.ExternalUser{
			Name:      "Stale User",
			AccountID: "org-456",
			UserType:  "human",
			ScraperID: scraperModel.ID,
			Aliases:   []string{"stale-user"},
		}
		err := DefaultContext.DB().Create(&staleUser).Error
		Expect(err).NotTo(HaveOccurred())

		// Verify we now have 3 users
		var users []dutymodels.ExternalUser
		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Where("deleted_at IS NULL").Find(&users).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(users).To(HaveLen(3))
	})

	It("should soft delete stale external user on next scrape", func() {
		// Clear cache to ensure fresh lookup
		db.ExternalUserCache.Flush()

		// Run scraper again - this should soft delete the stale user
		_, err := RunScraper(scraperCtx)
		Expect(err).To(BeNil())

		// Verify only 2 users remain active (non-deleted)
		var activeUsers []dutymodels.ExternalUser
		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Where("deleted_at IS NULL").Find(&activeUsers).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(activeUsers).To(HaveLen(2))

		// Verify the original users are still there
		activeUserIDs := lo.Map(activeUsers, func(u dutymodels.ExternalUser, _ int) uuid.UUID { return u.ID })
		Expect(activeUserIDs).To(ConsistOf(initialUserIDs))

		// Verify the stale user was soft deleted
		var deletedUsers []dutymodels.ExternalUser
		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Where("deleted_at IS NOT NULL").Find(&deletedUsers).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(deletedUsers).To(HaveLen(1))
		Expect(deletedUsers[0].Name).To(Equal("Stale User"))
	})
})

var _ = Describe("External roles with aliases e2e test", Ordered, func() {
	var scrapeConfig v1.ScrapeConfig
	var scraperCtx api.ScrapeContext
	var scraperModel dutymodels.ConfigScraper

	BeforeAll(func() {
		scrapeConfig = getConfigSpec("file-external-users")

		scModel, err := scrapeConfig.ToModel()
		Expect(err).NotTo(HaveOccurred(), "failed to convert scrape config to model")
		scModel.Source = dutymodels.SourceUI

		err = DefaultContext.DB().Create(&scModel).Error
		Expect(err).NotTo(HaveOccurred(), "failed to create scrape config")

		scrapeConfig.SetUID(k8sTypes.UID(scModel.ID.String()))
		scraperCtx = api.NewScrapeContext(DefaultContext).WithScrapeConfig(&scrapeConfig)

		scraperModel = scModel
	})

	AfterAll(func() {
		// Clean up external roles
		err := DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Delete(&dutymodels.ExternalRole{}).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete external roles")

		// Clean up external users
		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Delete(&dutymodels.ExternalUser{}).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete external users")

		// Clean up external groups
		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Delete(&dutymodels.ExternalGroup{}).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete external groups")

		// Clean up scraper
		err = DefaultContext.DB().Delete(&scraperModel).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete scrape config")
	})

	It("should scrape and save external roles with aliases", func() {
		_, err := RunScraper(scraperCtx)
		Expect(err).To(BeNil())
	})

	It("should have saved external roles to the database with aliases", func() {
		var roles []dutymodels.ExternalRole
		err := DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Find(&roles).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(roles).To(HaveLen(2))

		roleNames := lo.Map(roles, func(r dutymodels.ExternalRole, _ int) string { return r.Name })
		Expect(roleNames).To(ContainElements("Admin", "Reader"))

		// Verify aliases are saved
		for _, role := range roles {
			switch role.Name {
			case "Admin":
				Expect(role.Aliases).To(ContainElements("admin-role", "administrator"))
			case "Reader":
				Expect(role.Aliases).To(ContainElements("reader-role", "read-only"))
			}
		}
	})

	It("should upsert external roles by alias on second scrape (not create duplicates)", func() {
		// Get existing role IDs before second scrape
		var rolesBefore []dutymodels.ExternalRole
		err := DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Find(&rolesBefore).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(rolesBefore).To(HaveLen(2))

		roleIDsBefore := lo.Map(rolesBefore, func(r dutymodels.ExternalRole, _ int) uuid.UUID { return r.ID })

		// Clear cache to ensure we test DB lookup path
		db.ExternalRoleCache.Flush()

		// Run scraper again
		_, err = RunScraper(scraperCtx)
		Expect(err).To(BeNil())

		// Verify same number of roles (no duplicates)
		var rolesAfter []dutymodels.ExternalRole
		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Find(&rolesAfter).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(rolesAfter).To(HaveLen(2))

		// Verify same IDs were used (upsert, not insert)
		roleIDsAfter := lo.Map(rolesAfter, func(r dutymodels.ExternalRole, _ int) uuid.UUID { return r.ID })
		Expect(roleIDsAfter).To(ConsistOf(roleIDsBefore))
	})

	It("should use cache for external role lookup on subsequent scrapes", func() {
		// Clear cache first
		db.ExternalRoleCache.Flush()

		// Run scraper to populate cache
		_, err := RunScraper(scraperCtx)
		Expect(err).To(BeNil())

		// Verify cache is populated for all aliases
		adminID, found := db.ExternalRoleCache.Get("admin-role")
		Expect(found).To(BeTrue())
		Expect(adminID).NotTo(Equal(uuid.Nil))

		administratorID, found := db.ExternalRoleCache.Get("administrator")
		Expect(found).To(BeTrue())
		Expect(administratorID).To(Equal(adminID)) // Same role, same ID

		readerID, found := db.ExternalRoleCache.Get("reader-role")
		Expect(found).To(BeTrue())
		Expect(readerID).NotTo(Equal(uuid.Nil))

		readOnlyID, found := db.ExternalRoleCache.Get("read-only")
		Expect(found).To(BeTrue())
		Expect(readOnlyID).To(Equal(readerID)) // Same role, same ID
	})
})

var _ = Describe("Config access with external_role_aliases test", Ordered, func() {
	var scrapeConfig v1.ScrapeConfig
	var scraperCtx api.ScrapeContext
	var scraperModel dutymodels.ConfigScraper

	BeforeAll(func() {
		scrapeConfig = getConfigSpec("file-config-access-roles")

		scModel, err := scrapeConfig.ToModel()
		Expect(err).NotTo(HaveOccurred(), "failed to convert scrape config to model")
		scModel.Source = dutymodels.SourceUI

		err = DefaultContext.DB().Create(&scModel).Error
		Expect(err).NotTo(HaveOccurred(), "failed to create scrape config")

		scrapeConfig.SetUID(k8sTypes.UID(scModel.ID.String()))
		scraperCtx = api.NewScrapeContext(DefaultContext).WithScrapeConfig(&scrapeConfig)

		scraperModel = scModel
	})

	AfterAll(func() {
		// Clean up config access
		err := DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Delete(&dutymodels.ConfigAccess{}).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete config access")

		// Clean up external groups
		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Delete(&dutymodels.ExternalGroup{}).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete external groups")

		// Clean up external roles
		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Delete(&dutymodels.ExternalRole{}).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete external roles")

		// Clean up external users
		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Delete(&dutymodels.ExternalUser{}).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete external users")

		// Clean up config items
		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Delete(&models.ConfigItem{}).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete config items")

		// Clean up scraper
		err = DefaultContext.DB().Delete(&scraperModel).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete scrape config")
	})

	It("should scrape and save external users, roles, groups, and config access", func() {
		_, err := RunScraper(scraperCtx)
		Expect(err).To(BeNil())
	})

	It("should have saved external roles to the database", func() {
		var roles []dutymodels.ExternalRole
		err := DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Find(&roles).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(roles).To(HaveLen(2))

		roleNames := lo.Map(roles, func(r dutymodels.ExternalRole, _ int) string { return r.Name })
		Expect(roleNames).To(ContainElements("Editor", "Viewer"))
	})

	It("should have saved config access with resolved external_role_id from aliases", func() {
		// Get the external roles to find their IDs
		var roles []dutymodels.ExternalRole
		err := DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Find(&roles).Error
		Expect(err).NotTo(HaveOccurred())

		roleByName := make(map[string]dutymodels.ExternalRole)
		for _, r := range roles {
			roleByName[r.Name] = r
		}

		// Get the config access records
		var configAccesses []dutymodels.ConfigAccess
		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Find(&configAccesses).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(configAccesses).To(HaveLen(2))

		// Verify each config access has the correct external_role_id
		accessByID := make(map[string]dutymodels.ConfigAccess)
		for _, ca := range configAccesses {
			accessByID[ca.ID] = ca
		}

		// role-access-001 should have Editor's role ID (resolved from "editor-role" alias)
		access001 := accessByID["role-access-001"]
		Expect(access001.ExternalRoleID).NotTo(BeNil())
		Expect(*access001.ExternalRoleID).To(Equal(roleByName["Editor"].ID))

		// role-access-002 should have Viewer's role ID (resolved from "viewer-role" or "view-only" alias)
		access002 := accessByID["role-access-002"]
		Expect(access002.ExternalRoleID).NotTo(BeNil())
		Expect(*access002.ExternalRoleID).To(Equal(roleByName["Viewer"].ID))
	})

	It("should resolve external_role_id from cache on second scrape", func() {
		// Clear cache first
		db.ExternalRoleCache.Flush()

		// Run scraper again
		_, err := RunScraper(scraperCtx)
		Expect(err).To(BeNil())

		// Verify cache is populated with aliases
		editorID, found := db.ExternalRoleCache.Get("editor-role")
		Expect(found).To(BeTrue())
		Expect(editorID).NotTo(Equal(uuid.Nil))

		viewerID, found := db.ExternalRoleCache.Get("viewer-role")
		Expect(found).To(BeTrue())
		Expect(viewerID).NotTo(Equal(uuid.Nil))
	})

	It("should have saved external groups to the database with aliases", func() {
		var groups []dutymodels.ExternalGroup
		err := DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Find(&groups).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(groups).To(HaveLen(2))

		groupNames := lo.Map(groups, func(g dutymodels.ExternalGroup, _ int) string { return g.Name })
		Expect(groupNames).To(ContainElements("Editors Group", "Viewers Group"))
	})

	It("should have saved config access with resolved external_group_id from aliases", func() {
		// Get the external groups to find their IDs
		var groups []dutymodels.ExternalGroup
		err := DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Find(&groups).Error
		Expect(err).NotTo(HaveOccurred())

		groupByName := make(map[string]dutymodels.ExternalGroup)
		for _, g := range groups {
			groupByName[g.Name] = g
		}

		// Get the config access records
		var configAccesses []dutymodels.ConfigAccess
		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Find(&configAccesses).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(configAccesses).To(HaveLen(2))

		// Verify each config access has the correct external_group_id
		accessByID := make(map[string]dutymodels.ConfigAccess)
		for _, ca := range configAccesses {
			accessByID[ca.ID] = ca
		}

		// role-access-001 should have Editors Group's ID (resolved from "editors-group" alias)
		access001 := accessByID["role-access-001"]
		Expect(access001.ExternalGroupID).NotTo(BeNil())
		Expect(*access001.ExternalGroupID).To(Equal(groupByName["Editors Group"].ID))

		// role-access-002 should have Viewers Group's ID (resolved from "viewers-group" or "view-team" alias)
		access002 := accessByID["role-access-002"]
		Expect(access002.ExternalGroupID).NotTo(BeNil())
		Expect(*access002.ExternalGroupID).To(Equal(groupByName["Viewers Group"].ID))
	})

	It("should resolve external_group_id from cache on second scrape", func() {
		// Clear cache first
		db.ExternalGroupCache.Flush()

		// Run scraper again
		_, err := RunScraper(scraperCtx)
		Expect(err).To(BeNil())

		// Verify cache is populated with aliases
		editorsID, found := db.ExternalGroupCache.Get("editors-group")
		Expect(found).To(BeTrue())
		Expect(editorsID).NotTo(Equal(uuid.Nil))

		viewersID, found := db.ExternalGroupCache.Get("viewers-group")
		Expect(found).To(BeTrue())
		Expect(viewersID).NotTo(Equal(uuid.Nil))
	})
})

var _ = Describe("Config access with combined user, role, and group aliases test", Ordered, func() {
	var scrapeConfig v1.ScrapeConfig
	var scraperCtx api.ScrapeContext
	var scraperModel dutymodels.ConfigScraper

	BeforeAll(func() {
		scrapeConfig = getConfigSpec("file-config-access-roles")

		scModel, err := scrapeConfig.ToModel()
		Expect(err).NotTo(HaveOccurred(), "failed to convert scrape config to model")
		scModel.Source = dutymodels.SourceUI

		err = DefaultContext.DB().Create(&scModel).Error
		Expect(err).NotTo(HaveOccurred(), "failed to create scrape config")

		scrapeConfig.SetUID(k8sTypes.UID(scModel.ID.String()))
		scraperCtx = api.NewScrapeContext(DefaultContext).WithScrapeConfig(&scrapeConfig)

		scraperModel = scModel
	})

	AfterAll(func() {
		// Clean up config access
		err := DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Delete(&dutymodels.ConfigAccess{}).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete config access")

		// Clean up external groups
		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Delete(&dutymodels.ExternalGroup{}).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete external groups")

		// Clean up external roles
		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Delete(&dutymodels.ExternalRole{}).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete external roles")

		// Clean up external users
		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Delete(&dutymodels.ExternalUser{}).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete external users")

		// Clean up config items
		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Delete(&models.ConfigItem{}).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete config items")

		// Clean up scraper
		err = DefaultContext.DB().Delete(&scraperModel).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete scrape config")
	})

	It("should scrape and save external users, roles, groups, and config access", func() {
		_, err := RunScraper(scraperCtx)
		Expect(err).To(BeNil())
	})

	It("should have saved config access with user, role, and group all resolved", func() {
		// Get the external user
		var users []dutymodels.ExternalUser
		err := DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Find(&users).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(users).To(HaveLen(1))
		user := users[0]
		Expect(user.Name).To(Equal("Charlie Brown"))

		// Get the external roles
		var roles []dutymodels.ExternalRole
		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Find(&roles).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(roles).To(HaveLen(2))

		roleByName := make(map[string]dutymodels.ExternalRole)
		for _, r := range roles {
			roleByName[r.Name] = r
		}

		// Get the external groups
		var groups []dutymodels.ExternalGroup
		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Find(&groups).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(groups).To(HaveLen(2))

		groupByName := make(map[string]dutymodels.ExternalGroup)
		for _, g := range groups {
			groupByName[g.Name] = g
		}

		// Get the config access records
		var configAccesses []dutymodels.ConfigAccess
		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Find(&configAccesses).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(configAccesses).To(HaveLen(2))

		accessByID := make(map[string]dutymodels.ConfigAccess)
		for _, ca := range configAccesses {
			accessByID[ca.ID] = ca
		}

		// role-access-001 should have user, role, and group all resolved
		access001 := accessByID["role-access-001"]
		Expect(access001.ExternalUserID).NotTo(BeNil(), "external_user_id should be resolved")
		Expect(*access001.ExternalUserID).To(Equal(user.ID))
		Expect(access001.ExternalRoleID).NotTo(BeNil(), "external_role_id should be resolved")
		Expect(*access001.ExternalRoleID).To(Equal(roleByName["Editor"].ID))
		Expect(access001.ExternalGroupID).NotTo(BeNil(), "external_group_id should be resolved")
		Expect(*access001.ExternalGroupID).To(Equal(groupByName["Editors Group"].ID))

		// role-access-002 should also have user, role, and group all resolved
		access002 := accessByID["role-access-002"]
		Expect(access002.ExternalUserID).NotTo(BeNil(), "external_user_id should be resolved")
		Expect(*access002.ExternalUserID).To(Equal(user.ID)) // Same user, different alias
		Expect(access002.ExternalRoleID).NotTo(BeNil(), "external_role_id should be resolved")
		Expect(*access002.ExternalRoleID).To(Equal(roleByName["Viewer"].ID))
		Expect(access002.ExternalGroupID).NotTo(BeNil(), "external_group_id should be resolved")
		Expect(*access002.ExternalGroupID).To(Equal(groupByName["Viewers Group"].ID))
	})

	It("should correctly resolve all entity types from cache on second scrape", func() {
		// Clear all caches
		db.ExternalUserCache.Flush()
		db.ExternalRoleCache.Flush()
		db.ExternalGroupCache.Flush()

		// Run scraper again
		_, err := RunScraper(scraperCtx)
		Expect(err).To(BeNil())

		// Verify all caches are populated
		userID, found := db.ExternalUserCache.Get("charlie-brown")
		Expect(found).To(BeTrue())
		Expect(userID).NotTo(Equal(uuid.Nil))

		roleID, found := db.ExternalRoleCache.Get("editor-role")
		Expect(found).To(BeTrue())
		Expect(roleID).NotTo(Equal(uuid.Nil))

		groupID, found := db.ExternalGroupCache.Get("editors-group")
		Expect(found).To(BeTrue())
		Expect(groupID).NotTo(Equal(uuid.Nil))

		// Verify they are all different (no cache collision)
		Expect(userID).NotTo(Equal(roleID))
		Expect(userID).NotTo(Equal(groupID))
		Expect(roleID).NotTo(Equal(groupID))
	})
})

var _ = Describe("External groups with aliases e2e test", Ordered, func() {
	var scrapeConfig v1.ScrapeConfig
	var scraperCtx api.ScrapeContext
	var scraperModel dutymodels.ConfigScraper

	BeforeAll(func() {
		scrapeConfig = getConfigSpec("file-external-users")

		scModel, err := scrapeConfig.ToModel()
		Expect(err).NotTo(HaveOccurred(), "failed to convert scrape config to model")
		scModel.Source = dutymodels.SourceUI

		err = DefaultContext.DB().Create(&scModel).Error
		Expect(err).NotTo(HaveOccurred(), "failed to create scrape config")

		scrapeConfig.SetUID(k8sTypes.UID(scModel.ID.String()))
		scraperCtx = api.NewScrapeContext(DefaultContext).WithScrapeConfig(&scrapeConfig)

		scraperModel = scModel
	})

	AfterAll(func() {
		// Clean up external groups
		err := DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Delete(&dutymodels.ExternalGroup{}).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete external groups")

		// Clean up external roles
		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Delete(&dutymodels.ExternalRole{}).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete external roles")

		// Clean up external users
		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Delete(&dutymodels.ExternalUser{}).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete external users")

		// Clean up scraper
		err = DefaultContext.DB().Delete(&scraperModel).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete scrape config")
	})

	It("should scrape and save external groups with aliases", func() {
		_, err := RunScraper(scraperCtx)
		Expect(err).To(BeNil())
	})

	It("should have saved external groups to the database with aliases", func() {
		var groups []dutymodels.ExternalGroup
		err := DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Find(&groups).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(groups).To(HaveLen(2))

		groupNames := lo.Map(groups, func(g dutymodels.ExternalGroup, _ int) string { return g.Name })
		Expect(groupNames).To(ContainElements("Administrators", "Developers"))

		// Verify aliases are saved
		for _, group := range groups {
			switch group.Name {
			case "Administrators":
				Expect(group.Aliases).To(ContainElements("admins-group", "administrators"))
			case "Developers":
				Expect(group.Aliases).To(ContainElements("devs-group", "developers"))
			}
		}
	})

	It("should upsert external groups by alias on second scrape (not create duplicates)", func() {
		// Get existing group IDs before second scrape
		var groupsBefore []dutymodels.ExternalGroup
		err := DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Find(&groupsBefore).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(groupsBefore).To(HaveLen(2))

		groupIDsBefore := lo.Map(groupsBefore, func(g dutymodels.ExternalGroup, _ int) uuid.UUID { return g.ID })

		// Clear cache to ensure we test DB lookup path
		db.ExternalGroupCache.Flush()

		// Run scraper again
		_, err = RunScraper(scraperCtx)
		Expect(err).To(BeNil())

		// Verify same number of groups (no duplicates)
		var groupsAfter []dutymodels.ExternalGroup
		err = DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Find(&groupsAfter).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(groupsAfter).To(HaveLen(2))

		// Verify same IDs were used (upsert, not insert)
		groupIDsAfter := lo.Map(groupsAfter, func(g dutymodels.ExternalGroup, _ int) uuid.UUID { return g.ID })
		Expect(groupIDsAfter).To(ConsistOf(groupIDsBefore))
	})

	It("should use cache for external group lookup on subsequent scrapes", func() {
		// Clear cache first
		db.ExternalGroupCache.Flush()

		// Run scraper to populate cache
		_, err := RunScraper(scraperCtx)
		Expect(err).To(BeNil())

		// Verify cache is populated for all aliases
		adminsID, found := db.ExternalGroupCache.Get("admins-group")
		Expect(found).To(BeTrue())
		Expect(adminsID).NotTo(Equal(uuid.Nil))

		administratorsID, found := db.ExternalGroupCache.Get("administrators")
		Expect(found).To(BeTrue())
		Expect(administratorsID).To(Equal(adminsID)) // Same group, same ID

		devsID, found := db.ExternalGroupCache.Get("devs-group")
		Expect(found).To(BeTrue())
		Expect(devsID).NotTo(Equal(uuid.Nil))

		developersID, found := db.ExternalGroupCache.Get("developers")
		Expect(found).To(BeTrue())
		Expect(developersID).To(Equal(devsID)) // Same group, same ID
	})
})

var _ = Describe("Config access logs upsert", Ordered, func() {
	fetchAccessLogRefs := func() (dutymodels.ConfigItem, uuid.UUID, api.ScrapeContext, uuid.UUID, func()) {
		var configItem dutymodels.ConfigItem
		err := DefaultContext.DB().Where("scraper_id IS NOT NULL").First(&configItem).Error
		Expect(err).NotTo(HaveOccurred(), "failed to find config item with scraper_id")

		scraperIDValue := lo.FromPtr(configItem.ScraperID)
		Expect(scraperIDValue).NotTo(BeEmpty())

		scraperID, err := uuid.Parse(scraperIDValue)
		Expect(err).NotTo(HaveOccurred(), "failed to parse scraper id")

		err = DefaultContext.DB().First(&dutymodels.ConfigScraper{}, "id = ?", scraperID).Error
		Expect(err).NotTo(HaveOccurred(), "failed to find scraper")

		cleanupExternalUser := func() {}
		var externalUser dutymodels.ExternalUser
		err = DefaultContext.DB().Where("scraper_id = ?", scraperID).First(&externalUser).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				externalUser = dutymodels.ExternalUser{
					ID:        uuid.New(),
					Name:      "access-log-user",
					AccountID: "test-account",
					UserType:  "user",
					ScraperID: scraperID,
					CreatedAt: time.Now(),
				}
				err = DefaultContext.DB().Create(&externalUser).Error
				Expect(err).NotTo(HaveOccurred(), "failed to create external user")
				cleanupExternalUser = func() {
					err := DefaultContext.DB().Delete(&externalUser).Error
					Expect(err).NotTo(HaveOccurred(), "failed to delete external user")
				}
			} else {
				Expect(err).NotTo(HaveOccurred(), "failed to fetch external user")
			}
		}

		return configItem, scraperID, api.NewScrapeContext(DefaultContext), externalUser.ID, cleanupExternalUser
	}

	cleanupAccessLog := func(configID, externalUserID, scraperID uuid.UUID) {
		err := DefaultContext.DB().Where("config_id = ? AND external_user_id = ? AND scraper_id = ?",
			configID, externalUserID, scraperID).Delete(&dutymodels.ConfigAccessLog{}).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete access logs")
	}

	It("should update access log when newer", func() {
		configItem, scraperID, scraperCtx, externalUserID, cleanupExternalUser := fetchAccessLogRefs()
		DeferCleanup(func() {
			cleanupAccessLog(configItem.ID, externalUserID, scraperID)
			cleanupExternalUser()
		})

		olderTime := time.Now().Add(-2 * time.Hour)
		latestAccessTime := time.Now().Add(-1 * time.Hour)

		olderLog := dutymodels.ConfigAccessLog{
			ConfigID:       configItem.ID,
			ExternalUserID: externalUserID,
			ScraperID:      scraperID,
			CreatedAt:      olderTime,
			MFA:            false,
		}

		err := db.SaveConfigAccessLog(scraperCtx, &olderLog)
		Expect(err).NotTo(HaveOccurred())

		newerLog := dutymodels.ConfigAccessLog{
			ConfigID:       configItem.ID,
			ExternalUserID: externalUserID,
			ScraperID:      scraperID,
			CreatedAt:      latestAccessTime,
			MFA:            true,
		}

		err = db.SaveConfigAccessLog(scraperCtx, &newerLog)
		Expect(err).NotTo(HaveOccurred())

		var storedLog dutymodels.ConfigAccessLog
		err = DefaultContext.DB().Where("config_id = ? AND external_user_id = ? AND scraper_id = ?",
			configItem.ID, externalUserID, scraperID).First(&storedLog).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(storedLog.MFA).To(BeTrue())
		Expect(storedLog.CreatedAt).To(BeTemporally("~", latestAccessTime, time.Second))
	})

	It("should ignore older access log", func() {
		configItem, scraperID, scraperCtx, externalUserID, cleanupExternalUser := fetchAccessLogRefs()
		DeferCleanup(func() {
			cleanupAccessLog(configItem.ID, externalUserID, scraperID)
			cleanupExternalUser()
		})

		latestAccessTime := time.Now().Add(-1 * time.Hour)
		latestLog := dutymodels.ConfigAccessLog{
			ConfigID:       configItem.ID,
			ExternalUserID: externalUserID,
			ScraperID:      scraperID,
			CreatedAt:      latestAccessTime,
			MFA:            true,
		}

		err := db.SaveConfigAccessLog(scraperCtx, &latestLog)
		Expect(err).NotTo(HaveOccurred())

		olderTime := time.Now().Add(-3 * time.Hour)
		olderLog := dutymodels.ConfigAccessLog{
			ConfigID:       configItem.ID,
			ExternalUserID: externalUserID,
			ScraperID:      scraperID,
			CreatedAt:      olderTime,
			MFA:            false,
		}

		err = db.SaveConfigAccessLog(scraperCtx, &olderLog)
		Expect(err).NotTo(HaveOccurred())

		var storedLog dutymodels.ConfigAccessLog
		err = DefaultContext.DB().Where("config_id = ? AND external_user_id = ? AND scraper_id = ?",
			configItem.ID, externalUserID, scraperID).First(&storedLog).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(storedLog.MFA).To(BeTrue())
		Expect(storedLog.CreatedAt).To(BeTemporally("~", latestAccessTime, time.Second))
	})
})
