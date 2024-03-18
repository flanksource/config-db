package scrapers

import (
	gocontext "context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
	"github.com/flanksource/config-db/db/models"
	dutymodels "github.com/flanksource/duty/models"
	"github.com/flanksource/duty/query"
	"github.com/flanksource/duty/types"
	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"

	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("Scrapers test", Ordered, func() {
	Describe("DB initialization", func() {

		It("Gorm can connect", func() {
			var people int64
			Expect(DefaultContext.DB().Table("people").Count(&people).Error).ToNot(HaveOccurred())
			Expect(people).To(BeNumerically(">=", 1))
		})
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
				Kind:      v1.RelationshipLookup{Value: "ConfigMap"},
				Name:      v1.RelationshipLookup{Label: "flanksource/name"},
				Namespace: v1.RelationshipLookup{Label: "flanksource/namespace"},
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
			_, err := db.PersistScrapeConfigFromFile(ctx, config)
			Expect(err).To(BeNil())

			ctx := api.NewScrapeContext(DefaultContext).WithScrapeConfig(&config)
			ctx, err = ctx.InitTempCache()
			Expect(err).To(BeNil())

			results, err := Run(ctx)
			Expect(err).To(BeNil())

			err = db.SaveResults(ctx, results)
			Expect(err).To(BeNil())

			configItemID, err := db.GetConfigItem(ctx, "Car", "A123")
			Expect(err).To(BeNil())
			Expect(configItemID).ToNot(BeNil())

			storedConfigItem, err = db.GetConfigItemFromID(ctx, configItemID.ID)
			Expect(err).To(BeNil())
			Expect(storedConfigItem).ToNot(BeNil())
		})

		It("should store the changes from the config", func() {
			config := getConfigSpec("file-car-change")

			ctx := api.NewScrapeContext(DefaultContext).WithScrapeConfig(&config)

			results, err := Run(ctx)
			Expect(err).To(BeNil())

			err = db.SaveResults(ctx, results)
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

			configItem, err := db.GetConfigItemFromID(ctx, configItemID.ID)
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
				Type:        lo.ToPtr("Test"),
				ScraperID:   &dummyScraper.ID,
			}
			configItemID2 := uuid.New().String()
			dummyCI2 := models.ConfigItem{
				ID:          configItemID2,
				ConfigClass: "Test",
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
				{ConfigID: configItemID2, ChangeType: "TestDiff", ExternalChangeId: uuid.New().String()},
				{ConfigID: configItemID2, ChangeType: "TestDiff", ExternalChangeId: uuid.New().String()},
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
