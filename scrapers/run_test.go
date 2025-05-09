package scrapers

import (
	gocontext "context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"time"

	"github.com/flanksource/commons/logger"
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
	"github.com/flanksource/config-db/db/models"
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
