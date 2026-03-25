package db

import (
	"time"

	"github.com/flanksource/config-db/api"
	"github.com/flanksource/duty/models"
	"github.com/flanksource/duty/types"
	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
)

var _ = Describe("CreateAnalysis", Ordered, func() {
	var (
		ctx      api.ScrapeContext
		configID uuid.UUID
	)

	BeforeAll(func() {
		ctx = api.NewScrapeContext(DefaultContext)

		configID = uuid.New()
		Expect(ctx.DB().Create(&models.ConfigItem{
			ID:          configID,
			ConfigClass: models.ConfigClassVirtualMachine,
			Type:        lo.ToPtr("EC2::Instance"),
		}).Error).ToNot(HaveOccurred())
	})

	AfterAll(func() {
		Expect(ctx.DB().Delete(&models.ConfigItem{}, configID).Error).ToNot(HaveOccurred())
	})

	It("refreshes all mutable fields on existing record", func() {
		firstObserved := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)
		lastObserved := firstObserved.Add(10 * time.Minute)
		attemptedLastObserved := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Second)

		initial := models.ConfigAnalysis{
			ID:            uuid.New(),
			ConfigID:      configID,
			Analyzer:      "demo-analyzer",
			Message:       "old message",
			Summary:       "old summary",
			Status:        models.AnalysisStatusOpen,
			Severity:      models.SeverityLow,
			AnalysisType:  models.AnalysisTypeSecurity,
			Analysis:      types.JSONMap{"version": "1"},
			Source:        "source-old",
			FirstObserved: &firstObserved,
			LastObserved:  &lastObserved,
			IsPushed:      false,
		}
		Expect(CreateAnalysis(ctx, initial)).ToNot(HaveOccurred())

		updated := models.ConfigAnalysis{
			ID:           uuid.New(),
			ConfigID:     configID,
			Analyzer:     "demo-analyzer",
			Message:      "new message",
			Summary:      "new summary",
			Status:       models.AnalysisStatusResolved,
			Severity:     models.SeverityCritical,
			AnalysisType: models.AnalysisTypeCost,
			Analysis:     types.JSONMap{"version": "2"},
			Source:       "source-new",
			LastObserved: &attemptedLastObserved,
			IsPushed:     true,
		}
		Expect(CreateAnalysis(ctx, updated)).ToNot(HaveOccurred())

		var got models.ConfigAnalysis
		Expect(ctx.DB().Where("config_id = ? AND analyzer = ?", configID, "demo-analyzer").First(&got).Error).ToNot(HaveOccurred())

		Expect(got.ID).To(Equal(initial.ID))
		Expect(got.Message).To(Equal(updated.Message))
		Expect(got.Summary).To(Equal(updated.Summary))
		Expect(got.Status).To(Equal(updated.Status))
		Expect(got.Severity).To(Equal(updated.Severity))
		Expect(got.AnalysisType).To(Equal(updated.AnalysisType))
		Expect(got.Source).To(Equal(updated.Source))
		Expect(got.Analysis).To(Equal(updated.Analysis))

		// These fields shouldn't be updated
		Expect(got.FirstObserved).ToNot(BeNil())
		Expect(got.FirstObserved.Truncate(time.Second)).To(BeTemporally("==", firstObserved))
		Expect(got.IsPushed).To(BeFalse())
		Expect(got.LastObserved).ToNot(BeNil())
		Expect(got.LastObserved.Truncate(time.Second)).ToNot(BeTemporally("==", attemptedLastObserved))
	})

	It("uses current time for last_observed on create", func() {
		now := time.Now().UTC()
		attemptedLastObserved := now.Add(-48 * time.Hour).Truncate(time.Second)
		firstObserved := now.Add(-72 * time.Hour).Truncate(time.Second)

		analysis := models.ConfigAnalysis{
			ID:            uuid.New(),
			ConfigID:      configID,
			Analyzer:      "create-last-observed",
			Message:       "message",
			Summary:       "summary",
			Status:        models.AnalysisStatusOpen,
			Severity:      models.SeverityMedium,
			AnalysisType:  models.AnalysisTypeSecurity,
			Analysis:      types.JSONMap{"version": "1"},
			Source:        "source",
			FirstObserved: &firstObserved,
			LastObserved:  &attemptedLastObserved,
		}
		Expect(CreateAnalysis(ctx, analysis)).ToNot(HaveOccurred())

		var got models.ConfigAnalysis
		Expect(ctx.DB().Where("config_id = ? AND analyzer = ?", configID, "create-last-observed").First(&got).Error).ToNot(HaveOccurred())
		Expect(got.LastObserved).ToNot(BeNil())
		Expect(got.LastObserved.Truncate(time.Second)).ToNot(BeTemporally("==", attemptedLastObserved))
		Expect(*got.LastObserved).To(BeTemporally(">=", now.Add(-5*time.Second)))
		Expect(*got.LastObserved).To(BeTemporally("<=", time.Now().UTC().Add(5*time.Second)))
	})
})
