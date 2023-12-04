package scrapers

import (
	gocontext "context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/flanksource/commons/logger"
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
	"github.com/flanksource/config-db/db/models"
	"github.com/flanksource/duty"
	"github.com/flanksource/duty/context"
	dutymodels "github.com/flanksource/duty/models"
	"github.com/flanksource/duty/types"
	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("Scrapers test", Ordered, func() {
	Describe("DB initialization", func() {
		It("should be able to run migrations", func() {
			logger.Infof("Running migrations against %s", pgUrl)
			if err := duty.Migrate(pgUrl, nil); err != nil {
				Fail(err.Error())
			}
		})

		It("Gorm can connect", func() {
			var people int64
			Expect(gormDB.Table("people").Count(&people).Error).ToNot(HaveOccurred())
			Expect(people).To(Equal(int64(1)))
		})
	})

	Describe("Test kubernetes relationship", func() {
		var scrapeConfig v1.ScrapeConfig

		It("should prepare scrape config", func() {
			scrapeConfig = getConfigSpec("kubernetes")
			scrapeConfig.Spec.Kubernetes[0].Kubeconfig = &types.EnvVar{
				ValueStatic: kubeConfigPath,
			}
			scrapeConfig.Spec.Kubernetes[0].Relationships = append(scrapeConfig.Spec.Kubernetes[0].Relationships, v1.KubernetesRelationship{
				Kind:      v1.KubernetesRelationshipLookup{Value: "ConfigMap"},
				Name:      v1.KubernetesRelationshipLookup{Label: "flanksource/name"},
				Namespace: v1.KubernetesRelationshipLookup{Label: "flanksource/namespace"},
			})
		})

		It("should save a configMap", func() {
			first := &apiv1.ConfigMap{
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

			err := k8sClient.Create(gocontext.TODO(), first)
			Expect(err).NotTo(HaveOccurred(), "failed to create test MyKind resource")
		})

		It("should save second configMap", func() {
			first := &apiv1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "second-config",
					Namespace: "default",
				},
				Data: map[string]string{"key": "value"},
			}

			err := k8sClient.Create(gocontext.TODO(), first)
			Expect(err).NotTo(HaveOccurred(), "failed to create test MyKind resource")
		})

		It("should successfully complete first scrape run", func() {
			scraperCtx := api.NewScrapeContext(gocontext.TODO(), gormDB, nil).WithScrapeConfig(&scrapeConfig)
			_, err := RunScraper(scraperCtx)
			Expect(err).To(BeNil())
		})

		It("should have saved the two config items to database", func() {
			var configItems []models.ConfigItem
			err := gormDB.Where("name IN (?, ?)", "first-config", "second-config").Find(&configItems).Error
			Expect(err).To(BeNil())

			Expect(len(configItems)).To(Equal(2))
		})

		It("should correctly setup kubernetes relationship", func() {
			scraperCtx := api.NewScrapeContext(gocontext.TODO(), gormDB, nil).WithScrapeConfig(&scrapeConfig)
			_, err := RunScraper(scraperCtx)
			Expect(err).To(BeNil())

			var configRelationships []models.ConfigRelationship
			err = gormDB.Find(&configRelationships).Error
			Expect(err).To(BeNil())

			Expect(len(configRelationships)).To(Equal(1))
			Expect(configRelationships[0].Relation).To(Equal("ConfigMapConfigMap"))
		})
	})

	Describe("Testing file fixtures", func() {
		fixtures := []string{
			"file-git",
			"file-script",
			"file-script-gotemplate",
			"file-mask",
			"file-postgres-properties",
		}

		for _, fixtureName := range fixtures {
			fixture := fixtureName
			It(fixture, func() {
				config := getConfigSpec(fixture)
				expected := getFixtureResult(fixture)
				ctx := api.NewScrapeContext(gocontext.Background(), nil, nil).WithScrapeConfig(&config)

				results, err := Run(ctx)
				Expect(err).To(BeNil())

				err = db.SaveResults(ctx, results)
				Expect(err).To(BeNil())

				if len(results) != len(expected) {
					Fail(fmt.Sprintf("expected %d results, got: %d", len(expected), len(results)))
					return
				}

				for i := 0; i < len(expected); i++ {
					want := expected[i]
					got := results[i]

					Expect(want.ID).To(Equal(got.ID))
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
			configScraper, err := db.PersistScrapeConfigFromFile(config)
			Expect(err).To(BeNil())

			ctx := api.NewScrapeContext(gocontext.Background(), nil, nil).WithScrapeConfig(&config)

			results, err := Run(ctx)
			Expect(err).To(BeNil())

			logger.Infof("SCRAPER ID: %s", configScraper.ID)
			err = db.SaveResults(ctx, results)
			Expect(err).To(BeNil())

			configItemID, err := db.FindConfigItemID(v1.ExternalID{
				ConfigType: "",               // Comes from file-car.yaml
				ExternalID: []string{"A123"}, // Comes from the config mentioned in file-car.yaml
			})
			Expect(err).To(BeNil())
			Expect(configItemID).ToNot(BeNil())

			storedConfigItem, err = db.GetConfigItemFromID(*configItemID)
			Expect(err).To(BeNil())
			Expect(storedConfigItem).ToNot(BeNil())
		})

		It("should store the changes from the config", func() {
			config := getConfigSpec("file-car-change")

			ctx := api.NewScrapeContext(gocontext.Background(), nil, nil).WithScrapeConfig(&config)

			results, err := Run(ctx)
			Expect(err).To(BeNil())

			err = db.SaveResults(ctx, results)
			Expect(err).To(BeNil())

			configItemID, err := db.FindConfigItemID(v1.ExternalID{
				ConfigType: "",               // Comes from file-car.yaml
				ExternalID: []string{"A123"}, // Comes from the config mentioned in file-car.yaml
			})
			Expect(err).To(BeNil())
			Expect(configItemID).ToNot(BeNil())

			// Expect the config_changes to be stored
			items, err := db.FindConfigChangesByItemID(gocontext.Background(), *configItemID)
			Expect(err).To(BeNil())
			Expect(len(items)).To(Equal(1))
			Expect(items[0].ConfigID).To(Equal(storedConfigItem.ID))
		})

		It("should not change the original config", func() {
			configItemID, err := db.FindConfigItemID(v1.ExternalID{
				ConfigType: "",               // Comes from file-car.yaml
				ExternalID: []string{"A123"}, // Comes from the config mentioned in file-car.yaml
			})
			Expect(err).To(BeNil())
			Expect(configItemID).ToNot(BeNil())

			configItem, err := db.GetConfigItemFromID(*configItemID)
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
			err := db.DefaultDB().Create(&dummyScraper).Error
			Expect(err).To(BeNil())

			configItemID := uuid.New().String()
			dummyCI := models.ConfigItem{
				ID:          configItemID,
				ConfigClass: "Test",
				ScraperID:   &dummyScraper.ID,
			}
			err = db.DefaultDB().Create(&dummyCI).Error
			Expect(err).To(BeNil())

			twoDaysAgo := time.Now().Add(-2 * 24 * time.Hour)
			fiveDaysAgo := time.Now().Add(-5 * 24 * time.Hour)
			tenDaysAgo := time.Now().Add(-10 * 24 * time.Hour)
			configChanges := []models.ConfigChange{
				{ConfigID: configItemID, ChangeType: "TestDiff", ExternalChangeId: uuid.New().String()},
				{ConfigID: configItemID, ChangeType: "TestDiff", ExternalChangeId: uuid.New().String()},
				{ConfigID: configItemID, ChangeType: "TestDiff", ExternalChangeId: uuid.New().String()},
				{ConfigID: configItemID, ChangeType: "TestDiff", ExternalChangeId: uuid.New().String()},
				{ConfigID: configItemID, ChangeType: "TestDiff", ExternalChangeId: uuid.New().String()},
				{ConfigID: configItemID, ChangeType: "TestDiff", CreatedAt: &twoDaysAgo, ExternalChangeId: uuid.New().String()},
				{ConfigID: configItemID, ChangeType: "TestDiff", CreatedAt: &twoDaysAgo, ExternalChangeId: uuid.New().String()},
				{ConfigID: configItemID, ChangeType: "TestDiff", CreatedAt: &twoDaysAgo, ExternalChangeId: uuid.New().String()},
				{ConfigID: configItemID, ChangeType: "TestDiff", CreatedAt: &twoDaysAgo, ExternalChangeId: uuid.New().String()},
				{ConfigID: configItemID, ChangeType: "TestDiff", CreatedAt: &fiveDaysAgo, ExternalChangeId: uuid.New().String()},
				{ConfigID: configItemID, ChangeType: "TestDiff", CreatedAt: &fiveDaysAgo, ExternalChangeId: uuid.New().String()},
				{ConfigID: configItemID, ChangeType: "TestDiff", CreatedAt: &fiveDaysAgo, ExternalChangeId: uuid.New().String()},
				{ConfigID: configItemID, ChangeType: "TestDiff", CreatedAt: &fiveDaysAgo, ExternalChangeId: uuid.New().String()},
				{ConfigID: configItemID, ChangeType: "TestDiff", CreatedAt: &fiveDaysAgo, ExternalChangeId: uuid.New().String()},
				{ConfigID: configItemID, ChangeType: "TestDiff", CreatedAt: &fiveDaysAgo, ExternalChangeId: uuid.New().String()},
				{ConfigID: configItemID, ChangeType: "TestDiff", CreatedAt: &tenDaysAgo, ExternalChangeId: uuid.New().String()},
				{ConfigID: configItemID, ChangeType: "TestDiff", CreatedAt: &tenDaysAgo, ExternalChangeId: uuid.New().String()},
			}

			err = db.DefaultDB().Table("config_changes").Create(&configChanges).Error
			Expect(err).To(BeNil())

			var currentCount int
			err = db.DefaultDB().
				Raw(`SELECT COUNT(*) FROM config_changes WHERE change_type = ? AND config_id = ?`, "TestDiff", configItemID).
				Scan(&currentCount).
				Error
			Expect(err).To(BeNil())
			Expect(currentCount).To(Equal(len(configChanges)))

			ctx := context.NewContext(gocontext.Background()).WithDB(db.DefaultDB(), db.Pool)

			// Everything older than 8 days should be removed
			err = ProcessChangeRetention(ctx, dummyScraper.ID, v1.ChangeRetentionSpec{Name: "TestDiff", Age: "8d"})
			Expect(err).To(BeNil())
			var count1 int
			err = db.DefaultDB().
				Raw(`SELECT COUNT(*) FROM config_changes WHERE change_type = ? AND config_id = ?`, "TestDiff", configItemID).
				Scan(&count1).
				Error
			Expect(err).To(BeNil())
			Expect(count1).To(Equal(15))

			// Only keep latest 12 config changes
			err = ProcessChangeRetention(ctx, dummyScraper.ID, v1.ChangeRetentionSpec{Name: "TestDiff", Count: 12})
			Expect(err).To(BeNil())
			var count2 int
			err = db.DefaultDB().
				Raw(`SELECT COUNT(*) FROM config_changes WHERE change_type = ? AND config_id = ?`, "TestDiff", configItemID).
				Scan(&count2).
				Error
			Expect(err).To(BeNil())
			Expect(count2).To(Equal(12))

			// Keep config changes which are newer than 3 days and max count can be 10
			err = ProcessChangeRetention(ctx, dummyScraper.ID, v1.ChangeRetentionSpec{Name: "TestDiff", Age: "3d", Count: 10})
			Expect(err).To(BeNil())
			var count3 int
			err = db.DefaultDB().
				Raw(`SELECT COUNT(*) FROM config_changes WHERE change_type = ? AND config_id = ?`, "TestDiff", configItemID).
				Scan(&count3).
				Error
			Expect(err).To(BeNil())
			Expect(count3).To(Equal(9))

			// No params in ChangeRetentionSpec should fail
			err = ProcessChangeRetention(ctx, dummyScraper.ID, v1.ChangeRetentionSpec{Name: "TestDiff"})
			Expect(err).ToNot(BeNil())
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
