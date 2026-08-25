package db

import (
	"context"
	"testing"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db/models"
	dutycontext "github.com/flanksource/duty/context"
	dutymodels "github.com/flanksource/duty/models"
	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
)

func newLocationTestContext(t *testing.T) api.ScrapeContext {
	t.Helper()

	scraperID := uuid.New()
	return api.NewScrapeContext(dutycontext.NewContext(context.Background())).WithScrapeConfig(&v1.ScrapeConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "config-location-test",
			Namespace: "default",
			UID:       k8stypes.UID(scraperID.String()),
		},
	})
}

func TestExtractConfigLocationsUsesCanonicalAliasID(t *testing.T) {
	ctx := newLocationTestContext(t)
	existingID := uuid.New()
	scraperID := ctx.ScrapeConfig().GetPersistedID()
	ctx.TempCache().Insert(models.ConfigItem{
		ID:         existingID.String(),
		ScraperID:  scraperID,
		Type:       "Kubernetes::Pod",
		ExternalID: []string{"provider/old-id", "cluster/pod/default/example"},
	})

	extracted, err := extractConfigsAndChangesFromResults(ctx, []v1.ScrapeResult{{
		ID:        "provider/new-id",
		Aliases:   []string{"cluster/pod/default/example"},
		Type:      "Kubernetes::Pod",
		Name:      "example",
		Config:    map[string]any{"metadata": map[string]any{"name": "example"}},
		Locations: []string{"kubernetes://cluster/default/pod/example"},
	}})
	if err != nil {
		t.Fatalf("extract results: %v", err)
	}

	if len(extracted.newConfigs) != 0 {
		t.Fatalf("expected alias match to update the existing config, got %d new configs", len(extracted.newConfigs))
	}
	if len(extracted.configsToUpdate) != 1 {
		t.Fatalf("expected one config update, got %d", len(extracted.configsToUpdate))
	}
	if got := extracted.configsToUpdate[0].New.ID; got != existingID.String() {
		t.Fatalf("config ID = %s, want canonical ID %s", got, existingID)
	}
	if len(extracted.locations) != 1 {
		t.Fatalf("expected one config location, got %d", len(extracted.locations))
	}
	if got := extracted.locations[0].ID; got != existingID {
		t.Fatalf("location config ID = %s, want canonical ID %s", got, existingID)
	}
}

var _ = Describe("saving config locations", func() {
	It("uses the existing config ID resolved through a stable alias", func() {
		scraperID := uuid.New()
		existingID := uuid.New()
		configJSON := `{}`
		location := "kubernetes://cluster/default/pod/example"
		scrapeConfig := &v1.ScrapeConfig{ObjectMeta: metav1.ObjectMeta{
			Name:      "config-location-integration-test",
			Namespace: "default",
			UID:       k8stypes.UID(scraperID.String()),
		}}

		Expect(DefaultContext.DB().Create(&dutymodels.ConfigScraper{
			ID:        scraperID,
			Name:      "config-location-integration-test",
			Namespace: "default",
			Spec:      "{}",
			Source:    dutymodels.SourceConfigFile,
		}).Error).ToNot(HaveOccurred())
		DeferCleanup(func() {
			Expect(DefaultContext.DB().Delete(&dutymodels.ConfigScraper{}, "id = ?", scraperID).Error).ToNot(HaveOccurred())
		})

		Expect(DefaultContext.DB().Create(&models.ConfigItem{
			ID:         existingID.String(),
			ScraperID:  &scraperID,
			Type:       "Kubernetes::Pod",
			ExternalID: []string{"provider/old-id", "cluster/pod/default/example"},
			Config:     &configJSON,
		}).Error).ToNot(HaveOccurred())
		DeferCleanup(func() {
			Expect(DefaultContext.DB().Exec("DELETE FROM config_items WHERE id = ?", existingID).Error).ToNot(HaveOccurred())
		})

		ctx, err := api.NewScrapeContext(DefaultContext).WithScrapeConfig(scrapeConfig).InitTempCache()
		Expect(err).ToNot(HaveOccurred())

		_, err = SaveResults(ctx, []v1.ScrapeResult{{
			ID:        "provider/new-id",
			Aliases:   []string{"cluster/pod/default/example"},
			Type:      "Kubernetes::Pod",
			Name:      "example",
			Config:    map[string]any{"metadata": map[string]any{"name": "example"}},
			Locations: []string{location},
		}})
		Expect(err).ToNot(HaveOccurred())

		var saved dutymodels.ConfigLocation
		Expect(DefaultContext.DB().Where("location = ?", location).First(&saved).Error).ToNot(HaveOccurred())
		Expect(saved.ID).To(Equal(existingID))

		var configCount int64
		Expect(DefaultContext.DB().Model(&models.ConfigItem{}).
			Where("scraper_id = ? AND type = ?", scraperID, "Kubernetes::Pod").
			Count(&configCount).Error).ToNot(HaveOccurred())
		Expect(configCount).To(Equal(int64(1)))
	})

	It("skips locations whose config item does not exist", func() {
		missingID := uuid.New()
		location := "kubernetes://cluster/default/pod/" + uuid.NewString()

		missing, err := saveConfigLocations(api.NewScrapeContext(DefaultContext), []dutymodels.ConfigLocation{{
			ID:       missingID,
			Location: location,
		}})
		Expect(err).ToNot(HaveOccurred())
		Expect(missing).To(Equal([]dutymodels.ConfigLocation{{ID: missingID, Location: location}}))

		var locationCount int64
		Expect(DefaultContext.DB().Model(&dutymodels.ConfigLocation{}).
			Where("id = ? AND location = ?", missingID, location).
			Count(&locationCount).Error).ToNot(HaveOccurred())
		Expect(locationCount).To(BeZero())
	})

	It("can skip a missing config location inside a caller transaction", func() {
		missingID := uuid.New()
		location := "kubernetes://cluster/default/pod/" + uuid.NewString()
		tx := DefaultContext.DB().Begin()
		Expect(tx.Error).ToNot(HaveOccurred())
		DeferCleanup(func() {
			_ = tx.Rollback().Error
		})
		ctx := api.NewScrapeContext(DefaultContext.WithDB(tx, DefaultContext.Pool()))

		missing, err := saveConfigLocations(ctx, []dutymodels.ConfigLocation{{ID: missingID, Location: location}})
		Expect(err).ToNot(HaveOccurred())
		Expect(missing).To(HaveLen(1))
		Expect(tx.Exec("SELECT 1").Error).ToNot(HaveOccurred())
	})

	It("warns instead of failing when a cached config is missing from the database", func() {
		missingID := uuid.New()
		scraperID := uuid.New()
		location := "kubernetes://cluster/default/pod/" + uuid.NewString()
		ctx := api.NewScrapeContext(DefaultContext).WithScrapeConfig(&v1.ScrapeConfig{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "stale-cache-config-location-integration-test",
				Namespace: "default",
				UID:       k8stypes.UID(scraperID.String()),
			},
		})
		ctx.TempCache().Insert(models.ConfigItem{
			ID:         missingID.String(),
			ScraperID:  &scraperID,
			Type:       "Kubernetes::Pod",
			ExternalID: []string{missingID.String()},
		})

		summary, err := SaveResults(ctx, []v1.ScrapeResult{{
			ID:        missingID.String(),
			Type:      "Kubernetes::Pod",
			Locations: []string{location},
		}})
		Expect(err).ToNot(HaveOccurred())
		Expect(summary.Warnings).To(HaveLen(1))
		Expect(summary.Warnings[0].Error).To(Equal("skipping config location because its config item does not exist"))
		Expect(summary.Warnings[0].Count).To(Equal(1))

		cached, err := ctx.TempCache().Get(ctx, missingID.String())
		Expect(err).ToNot(HaveOccurred())
		Expect(cached).To(BeNil())
	})

	It("skips repeated locations for an unknown metadata-only config", func() {
		missingID := "provider/missing-" + uuid.NewString()
		location := "kubernetes://cluster/default/pod/" + uuid.NewString()
		ctx := api.NewScrapeContext(DefaultContext).WithScrapeConfig(&v1.ScrapeConfig{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "missing-config-location-integration-test",
				Namespace: "default",
				UID:       k8stypes.UID(uuid.NewString()),
			},
		})

		summary, err := SaveResults(ctx, []v1.ScrapeResult{
			{ID: missingID, Type: "Kubernetes::Pod", Locations: []string{location}},
			{ID: missingID, Type: "Kubernetes::Pod", Locations: []string{location + "-again"}},
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(summary.Warnings).To(HaveLen(1))
		Expect(summary.Warnings[0].Count).To(Equal(2))

		var locationCount int64
		Expect(DefaultContext.DB().Model(&dutymodels.ConfigLocation{}).
			Where("location IN ?", []string{location, location + "-again"}).
			Count(&locationCount).Error).ToNot(HaveOccurred())
		Expect(locationCount).To(BeZero())
	})
})

func TestExtractConfigLocationsSkipsRepeatedUnknownMetadataOnlyConfig(t *testing.T) {
	ctx := newLocationTestContext(t)
	results := []v1.ScrapeResult{
		{
			ID:        "provider/missing-id",
			Type:      "Kubernetes::Pod",
			Locations: []string{"kubernetes://cluster/default/pod/missing"},
		},
		{
			ID:        "provider/missing-id",
			Type:      "Kubernetes::Pod",
			Locations: []string{"kubernetes://cluster/default/pod/missing-again"},
		},
	}

	extracted, err := extractConfigsAndChangesFromResults(ctx, results)
	if err != nil {
		t.Fatalf("extract results: %v", err)
	}

	if len(extracted.locations) != 0 {
		t.Fatalf("expected unresolved locations to be skipped, got %d", len(extracted.locations))
	}
	if len(extracted.warnings) != len(results) {
		t.Fatalf("expected %d warnings for skipped locations, got %d", len(results), len(extracted.warnings))
	}
}
