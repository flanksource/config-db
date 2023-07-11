package scrapers

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
	"github.com/flanksource/config-db/db/models"
	"github.com/flanksource/duty"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Scrapers test", func() {
	Describe("DB initialization", func() {
		It("should be able to run migrations", func() {
			logger.Infof("Running migrations against %s", pgUrl)
			if err := duty.Migrate(pgUrl, nil); err != nil {
				Fail(err.Error())
			}
		})

		It("Gorm can connect", func() {
			gorm, err := duty.NewGorm(pgUrl, duty.DefaultGormConfig())
			Expect(err).ToNot(HaveOccurred())
			var people int64
			Expect(gorm.Table("people").Count(&people).Error).ToNot(HaveOccurred())
			Expect(people).To(Equal(int64(1)))
		})
	})

	Describe("Testing file fixtures", func() {
		fixtures := []string{
			"file-git",
			"file-script",
			"file-script-gotemplate",
			"file-mask",
		}

		for _, fixtureName := range fixtures {
			fixture := fixtureName
			It(fixture, func() {
				config := getConfig(fixture)
				expected := getFixtureResult(fixture)
				ctx := &v1.ScrapeContext{Context: context.Background()}

				results, err := Run(ctx, config)
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
			config := getConfig("file-car")
			configScraper, err := db.PersistScrapeConfigFromFile(config)
			Expect(err).To(BeNil())

			ctx := &v1.ScrapeContext{ScraperID: &configScraper.ID, Context: context.Background()}

			results, err := Run(ctx, config)
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
			config := getConfig("file-car-change")
			ctx := &v1.ScrapeContext{Context: context.Background()}

			results, err := Run(ctx, config)
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
			items, err := db.FindConfigChangesByItemID(context.Background(), *configItemID)
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
	})
})

func getConfig(name string) v1.ConfigScraper {
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
