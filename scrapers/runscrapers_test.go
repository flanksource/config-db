package scrapers

import (
	"encoding/json"
	"fmt"
	"os"

	jsonpatch "github.com/evanphx/json-patch"
	"github.com/flanksource/commons/logger"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
	"github.com/flanksource/duty"
	"github.com/flanksource/duty/models"
	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/lib/pq"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

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

var _ = Describe("Scrapers test", func() {
	Describe("DB initialization", func() {
		It("should be able to run migrations", func() {
			logger.Infof("Running migrations against %s", pgUrl)
			if err := duty.Migrate(pgUrl); err != nil {
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

		It("should insert a config item", func() {
			external := "incident_commander"
			ci := models.ConfigItem{
				ID:           uuid.NewString(),
				ExternalID:   pq.StringArray{external},
				ExternalType: &external,
			}
			err := db.DefaultDB().Create(&ci).Error
			Expect(err).To(BeNil())
		})
	})

	Describe("Testing fixtures", func() {
		fixtures := []string{
			"file-git",
			"file-script",
			"file-mask",
		}

		for _, fixtureName := range fixtures {
			fixture := fixtureName
			It(fixture, func() {
				config := getConfig(fixture)
				expected := getFixtureResult(fixture)
				ctx := &v1.ScrapeContext{}

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
					Expect(want.Type).To(Equal(got.Type))
					Expect(compare(want.Config, got.Config)).To(Equal(""))

					if config.Full {
						Expect(cmp.Diff(want.Changes, got.Changes)).To(Equal(""))
					}
				}
			})
		}
	})

	Describe("Testing saving changes from config", func() {
		fixture := "file-full"
		It(fixture, func() {
			config := getConfig(fixture)
			expected := getFixtureResult(fixture)
			ctx := &v1.ScrapeContext{}

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
				Expect(want.Type).To(Equal(got.Type))
				Expect(compare(want.Config, got.Config)).To(Equal(""))

				if config.Full {
					Expect(cmp.Diff(want.Changes, got.Changes)).To(Equal(""))
				}
			}
		})
	})
})

func toJSON(i interface{}) []byte {
	switch v := i.(type) {
	case string:
		return []byte(v)
	}

	b, _ := json.Marshal(i)
	return b
}

func compare(a interface{}, b interface{}) string {

	patch, err := jsonpatch.CreateMergePatch(toJSON(a), toJSON(b))
	if err != nil {
		return err.Error()
	}

	if len(patch) <= 2 { // no patch or empty array
		return ""
	}

	return string(patch)
}
