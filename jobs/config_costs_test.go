package jobs

import (
	"time"

	"github.com/flanksource/duty/job"
	"github.com/flanksource/duty/models"
	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/shopspring/decimal"
)

var _ = Describe("config cost reconciliation", func() {
	var configIDs []uuid.UUID

	createConfig := func(externalID string) uuid.UUID {
		id := uuid.New()
		configIDs = append(configIDs, id)
		Expect(DefaultContext.DB().Exec(`
			INSERT INTO config_items (id, type, config_class, external_id, created_at, updated_at)
			VALUES (?, 'Test::CostTarget', 'Test', ARRAY[?]::text[], now(), now())`, id, externalID).Error).To(Succeed())
		return id
	}

	createCost := func(configID *uuid.UUID, externalID, sourceKey, fingerprint, amount string, updatedAt time.Time) models.ConfigCost {
		value := decimal.RequireFromString(amount)
		cost := models.ConfigCost{
			ID:              uuid.New(),
			ConfigID:        configID,
			SourceKey:       sourceKey,
			PeriodStart:     time.Now().UTC().Truncate(24 * time.Hour).Add(-24 * time.Hour),
			PeriodEnd:       time.Now().UTC().Truncate(24 * time.Hour),
			Grain:           models.ConfigCostGrainDay,
			ChargeCategory:  "Usage",
			BillingCurrency: "USD",
			BilledCost:      value,
			EffectiveCost:   value,
			Fingerprint:     fingerprint,
			UpdatedAt:       updatedAt,
		}
		if externalID != "" {
			cost.ExternalID = &externalID
		}
		Expect(DefaultContext.DB().Create(&cost).Error).To(Succeed())
		Expect(DefaultContext.DB().Model(&models.ConfigCost{}).Where("id = ?", cost.ID).
			Update("updated_at", updatedAt).Error).To(Succeed())
		return cost
	}

	AfterEach(func() {
		if len(configIDs) > 0 {
			Expect(DefaultContext.DB().Exec("DELETE FROM config_items WHERE id IN ?", configIDs).Error).To(Succeed())
		}
		configIDs = nil
	})

	It("attaches a uniquely matched orphan", func() {
		configID := createConfig("cost-target-unique")
		orphan := createCost(nil, "cost-target-unique", "test:unique", "unique", "7", time.Now().Add(-time.Minute))

		count, ambiguous, err := attachUnmatchedCosts(job.New(DefaultContext))
		Expect(err).ToNot(HaveOccurred())
		Expect(ambiguous).To(BeEmpty())
		Expect(count).To(Equal(1))

		var stored models.ConfigCost
		Expect(DefaultContext.DB().First(&stored, "id = ?", orphan.ID).Error).To(Succeed())
		Expect(stored.ConfigID).To(Equal(&configID))
		Expect(stored.EffectiveCost.String()).To(Equal("7"))
	})

	It("keeps the newest duplicate without adding the amounts", func() {
		configID := createConfig("cost-target-duplicate")
		older := createCost(&configID, "cost-target-duplicate", "test:duplicate", "duplicate", "4", time.Now().Add(-time.Hour))
		newer := createCost(nil, "cost-target-duplicate", "test:duplicate", "duplicate", "9", time.Now())

		_, ambiguous, err := attachUnmatchedCosts(job.New(DefaultContext))
		Expect(err).ToNot(HaveOccurred())
		Expect(ambiguous).To(BeEmpty())

		var rows []models.ConfigCost
		Expect(DefaultContext.DB().Where("source_key = ? AND fingerprint = ?", "test:duplicate", "duplicate").Find(&rows).Error).To(Succeed())
		Expect(rows).To(HaveLen(1))
		Expect(rows[0].ID).To(Equal(newer.ID))
		Expect(rows[0].ID).ToNot(Equal(older.ID))
		Expect(rows[0].ConfigID).To(Equal(&configID))
		Expect(rows[0].EffectiveCost.String()).To(Equal("9"))
	})

	It("reports an ambiguous config without blocking other attachments", func() {
		createConfig("cost-target-ambiguous")
		createConfig("cost-target-ambiguous")
		orphan := createCost(nil, "cost-target-ambiguous", "test:ambiguous", "ambiguous", "5", time.Now())
		uniqueConfigID := createConfig("cost-target-after-ambiguous")
		unique := createCost(nil, "cost-target-after-ambiguous", "test:after-ambiguous", "after-ambiguous", "6", time.Now())

		count, ambiguous, err := attachUnmatchedCosts(job.New(DefaultContext))
		Expect(err).ToNot(HaveOccurred())
		Expect(count).To(Equal(1))
		Expect(ambiguous).To(HaveLen(1))
		Expect(ambiguous[0].OrphanID).To(Equal(orphan.ID))
		Expect(ambiguous[0].ConfigIDs).To(HaveLen(2))

		var stored models.ConfigCost
		Expect(DefaultContext.DB().First(&stored, "id = ?", orphan.ID).Error).To(Succeed())
		Expect(stored.ConfigID).To(BeNil())
		Expect(DefaultContext.DB().First(&stored, "id = ?", unique.ID).Error).To(Succeed())
		Expect(stored.ConfigID).To(Equal(&uniqueConfigID))
	})
})
