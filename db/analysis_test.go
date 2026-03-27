package db

import (
	"time"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty/models"
	"github.com/flanksource/duty/types"
	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
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

		analysisID := GenerateAnalysisID("ext-analysis-1")

		initial := models.ConfigAnalysis{
			ID:            analysisID,
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
			ID:           analysisID,
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
		Expect(ctx.DB().Where("id = ?", analysisID).First(&got).Error).ToNot(HaveOccurred())

		Expect(got.ID).To(Equal(analysisID))
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
})

var _ = Describe("analysis resolution", Ordered, func() {
	var _ = Context("when unseen", Ordered, func() {
		var (
			ctx       api.ScrapeContext
			configID  uuid.UUID
			scraperID uuid.UUID
		)

		BeforeAll(func() {
			scraperID = uuid.New()
			ctx = api.NewScrapeContext(DefaultContext).WithScrapeConfig(&v1.ScrapeConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "analysis-resolution-test",
					Namespace: "default",
					UID:       k8stypes.UID(scraperID.String()),
				},
				Spec: v1.ScraperSpec{
					// Use a very short retention so we can test resolution without long sleeps
					Retention: v1.RetentionSpec{StaleAnalysisAge: "1s"},
				},
			})

			Expect(ctx.DB().Create(&models.ConfigScraper{
				ID:        scraperID,
				Name:      "analysis-resolution-test",
				Namespace: "default",
				Spec:      "{}",
				Source:    models.SourceConfigFile,
			}).Error).ToNot(HaveOccurred())

			configID = uuid.New()
			Expect(ctx.DB().Create(&models.ConfigItem{
				ID:          configID,
				ScraperID:   lo.ToPtr(scraperID.String()),
				ConfigClass: models.ConfigClassVirtualMachine,
				Type:        lo.ToPtr("EC2::Instance"),
				ExternalID:  []string{"i-analysis-resolution"},
			}).Error).ToNot(HaveOccurred())
		})

		AfterAll(func() {
			Expect(ctx.DB().Where("config_id = ?", configID).Delete(&models.ConfigAnalysis{}).Error).ToNot(HaveOccurred())
			Expect(ctx.DB().Delete(&models.ConfigItem{}, configID).Error).ToNot(HaveOccurred())
			Expect(ctx.DB().Delete(&models.ConfigScraper{}, scraperID).Error).ToNot(HaveOccurred())
		})

		It("marks missing analysis as resolved on subsequent saveResults", func() {
			firstBatch := []v1.ScrapeResult{
				{
					AnalysisResult: &v1.AnalysisResult{
						ExternalID:         "i-analysis-resolution",
						ExternalAnalysisID: "analyzer-a-finding",
						ConfigType:         "EC2::Instance",
						Analyzer:           "analyzer-a",
						Summary:            "analysis a",
						Messages:           []string{"message a"},
						Severity:           models.SeverityLow,
						AnalysisType:       models.AnalysisTypeSecurity,
						Source:             "test",
					},
				},
				{
					AnalysisResult: &v1.AnalysisResult{
						ExternalID:         "i-analysis-resolution",
						ExternalAnalysisID: "analyzer-b-finding",
						ConfigType:         "EC2::Instance",
						Analyzer:           "analyzer-b",
						Summary:            "analysis b",
						Messages:           []string{"message b"},
						Severity:           models.SeverityHigh,
						AnalysisType:       models.AnalysisTypeSecurity,
						Source:             "test",
					},
				},
			}

			_, err := saveResults(ctx, firstBatch)
			Expect(err).ToNot(HaveOccurred())

			// Wait past the 1s retention window so that unseen analysis can be resolved
			time.Sleep(2 * time.Second)

			secondBatch := []v1.ScrapeResult{
				{
					AnalysisResult: &v1.AnalysisResult{
						ExternalID:         "i-analysis-resolution",
						ExternalAnalysisID: "analyzer-a-finding",
						ConfigType:         "EC2::Instance",
						Analyzer:           "analyzer-a",
						Summary:            "analysis a updated",
						Messages:           []string{"message a updated"},
						Severity:           models.SeverityMedium,
						AnalysisType:       models.AnalysisTypeSecurity,
						Source:             "test",
					},
				},
			}

			_, err = saveResults(ctx, secondBatch)
			Expect(err).ToNot(HaveOccurred())

			var analyses []models.ConfigAnalysis
			Expect(ctx.DB().Where("config_id = ?", configID).Find(&analyses).Error).ToNot(HaveOccurred())
			Expect(analyses).To(HaveLen(2))

			analysisByAnalyzer := lo.SliceToMap(analyses, func(a models.ConfigAnalysis) (string, models.ConfigAnalysis) {
				return a.Analyzer, a
			})

			Expect(analysisByAnalyzer["analyzer-a"].Status).To(Equal(models.AnalysisStatusOpen))
			Expect(analysisByAnalyzer["analyzer-b"].Status).To(Equal(models.AnalysisStatusResolved))
		})
	})

	var _ = Describe("not immediately resolved within window", Ordered, func() {
		var (
			ctx       api.ScrapeContext
			configID  uuid.UUID
			scraperID uuid.UUID
		)

		BeforeAll(func() {
			scraperID = uuid.New()
			// Use a long retention (1 hour) so analysis is NOT resolved between two back-to-back scrapes
			ctx = api.NewScrapeContext(DefaultContext).WithScrapeConfig(&v1.ScrapeConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "analysis-retention-window-test",
					Namespace: "default",
					UID:       k8stypes.UID(scraperID.String()),
				},
				Spec: v1.ScraperSpec{
					Retention: v1.RetentionSpec{StaleAnalysisAge: "1h"},
				},
			})

			Expect(ctx.DB().Create(&models.ConfigScraper{
				ID:        scraperID,
				Name:      "analysis-retention-window-test",
				Namespace: "default",
				Spec:      "{}",
				Source:    models.SourceConfigFile,
			}).Error).ToNot(HaveOccurred())

			configID = uuid.New()
			Expect(ctx.DB().Create(&models.ConfigItem{
				ID:          configID,
				ScraperID:   lo.ToPtr(scraperID.String()),
				ConfigClass: models.ConfigClassVirtualMachine,
				Type:        lo.ToPtr("EC2::Instance"),
				ExternalID:  []string{"i-retention-window"},
			}).Error).ToNot(HaveOccurred())
		})

		AfterAll(func() {
			Expect(ctx.DB().Where("config_id = ?", configID).Delete(&models.ConfigAnalysis{}).Error).ToNot(HaveOccurred())
			Expect(ctx.DB().Delete(&models.ConfigItem{}, configID).Error).ToNot(HaveOccurred())
			Expect(ctx.DB().Delete(&models.ConfigScraper{}, scraperID).Error).ToNot(HaveOccurred())
		})

		It("does not resolve analysis that is still within the retention window", func() {
			firstBatch := []v1.ScrapeResult{
				{
					AnalysisResult: &v1.AnalysisResult{
						ExternalID:         "i-retention-window",
						ExternalAnalysisID: "analyzer-x-finding",
						ConfigType:         "EC2::Instance",
						Analyzer:           "analyzer-x",
						Summary:            "analysis x",
						Messages:           []string{"message x"},
						Severity:           models.SeverityLow,
						AnalysisType:       models.AnalysisTypeSecurity,
						Source:             "test",
					},
				},
				{
					AnalysisResult: &v1.AnalysisResult{
						ExternalID:         "i-retention-window",
						ExternalAnalysisID: "analyzer-y-finding",
						ConfigType:         "EC2::Instance",
						Analyzer:           "analyzer-y",
						Summary:            "analysis y",
						Messages:           []string{"message y"},
						Severity:           models.SeverityHigh,
						AnalysisType:       models.AnalysisTypeSecurity,
						Source:             "test",
					},
				},
			}

			_, err := saveResults(ctx, firstBatch)
			Expect(err).ToNot(HaveOccurred())

			// Second scrape only includes analyzer-x; analyzer-y is missing but within the 1h window
			secondBatch := []v1.ScrapeResult{
				{
					AnalysisResult: &v1.AnalysisResult{
						ExternalID:         "i-retention-window",
						ExternalAnalysisID: "analyzer-x-finding",
						ConfigType:         "EC2::Instance",
						Analyzer:           "analyzer-x",
						Summary:            "analysis x updated",
						Messages:           []string{"message x updated"},
						Severity:           models.SeverityMedium,
						AnalysisType:       models.AnalysisTypeSecurity,
						Source:             "test",
					},
				},
			}

			_, err = saveResults(ctx, secondBatch)
			Expect(err).ToNot(HaveOccurred())

			var analyses []models.ConfigAnalysis
			Expect(ctx.DB().Where("config_id = ?", configID).Find(&analyses).Error).ToNot(HaveOccurred())
			Expect(analyses).To(HaveLen(2))

			analysisByAnalyzer := lo.SliceToMap(analyses, func(a models.ConfigAnalysis) (string, models.ConfigAnalysis) {
				return a.Analyzer, a
			})

			// analyzer-y should still be open because it's within the 1h retention window
			Expect(analysisByAnalyzer["analyzer-x"].Status).To(Equal(models.AnalysisStatusOpen))
			Expect(analysisByAnalyzer["analyzer-y"].Status).To(Equal(models.AnalysisStatusOpen))
		})
	})

	var _ = Describe("keep disables auto-resolution", Ordered, func() {
		var (
			ctx       api.ScrapeContext
			configID  uuid.UUID
			scraperID uuid.UUID
		)

		BeforeAll(func() {
			scraperID = uuid.New()
			ctx = api.NewScrapeContext(DefaultContext).WithScrapeConfig(&v1.ScrapeConfig{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "analysis-keep-test",
					Namespace: "default",
					UID:       k8stypes.UID(scraperID.String()),
				},
				Spec: v1.ScraperSpec{
					Retention: v1.RetentionSpec{StaleAnalysisAge: "keep"},
				},
			})

			Expect(ctx.DB().Create(&models.ConfigScraper{
				ID:        scraperID,
				Name:      "analysis-keep-test",
				Namespace: "default",
				Spec:      "{}",
				Source:    models.SourceConfigFile,
			}).Error).ToNot(HaveOccurred())

			configID = uuid.New()
			Expect(ctx.DB().Create(&models.ConfigItem{
				ID:          configID,
				ScraperID:   lo.ToPtr(scraperID.String()),
				ConfigClass: models.ConfigClassVirtualMachine,
				Type:        lo.ToPtr("EC2::Instance"),
				ExternalID:  []string{"i-keep-test"},
			}).Error).ToNot(HaveOccurred())
		})

		AfterAll(func() {
			Expect(ctx.DB().Where("config_id = ?", configID).Delete(&models.ConfigAnalysis{}).Error).ToNot(HaveOccurred())
			Expect(ctx.DB().Delete(&models.ConfigItem{}, configID).Error).ToNot(HaveOccurred())
			Expect(ctx.DB().Delete(&models.ConfigScraper{}, scraperID).Error).ToNot(HaveOccurred())
		})

		It("never resolves analysis when analysisAge is set to 'keep'", func() {
			firstBatch := []v1.ScrapeResult{
				{
					AnalysisResult: &v1.AnalysisResult{
						ExternalID:         "i-keep-test",
						ExternalAnalysisID: "analyzer-keep-finding",
						ConfigType:         "EC2::Instance",
						Analyzer:           "analyzer-keep",
						Summary:            "analysis keep",
						Messages:           []string{"message keep"},
						Severity:           models.SeverityLow,
						AnalysisType:       models.AnalysisTypeSecurity,
						Source:             "test",
					},
				},
			}

			_, err := saveResults(ctx, firstBatch)
			Expect(err).ToNot(HaveOccurred())

			// Second scrape with no analysis at all
			_, err = saveResults(ctx, []v1.ScrapeResult{})
			Expect(err).ToNot(HaveOccurred())

			var analyses []models.ConfigAnalysis
			Expect(ctx.DB().Where("config_id = ?", configID).Find(&analyses).Error).ToNot(HaveOccurred())
			Expect(analyses).To(HaveLen(1))

			// Should remain open because "keep" disables auto-resolution
			Expect(analyses[0].Status).To(Equal(models.AnalysisStatusOpen))
		})
	})
})
