// Covers the derivation of config_cost_compact from config_costs: the grain each raw row
// lands at, that a restated bucket is replaced rather than added to, and that the terminal
// 1d→30d roll sums and deletes.
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

var _ = Describe("config cost compaction", func() {
	var configIDs []uuid.UUID

	createConfig := func(externalID string) uuid.UUID {
		id := uuid.New()
		configIDs = append(configIDs, id)
		Expect(DefaultContext.DB().Exec(`
			INSERT INTO config_items (id, type, config_class, external_id, created_at, updated_at)
			VALUES (?, 'Test::CostTarget', 'Test', ARRAY[?]::text[], now(), now())`, id, externalID).Error).To(Succeed())
		return id
	}

	// rawCost writes one row into config_costs at the given grain and period.
	rawCost := func(configID uuid.UUID, sourceKey, fingerprint, amount string, start, end time.Time, grain string) models.ConfigCost {
		value := decimal.RequireFromString(amount)
		cost := models.ConfigCost{
			ID:              uuid.New(),
			ConfigID:        configID,
			SourceKey:       sourceKey,
			PeriodStart:     start,
			PeriodEnd:       end,
			Grain:           grain,
			ChargeCategory:  "Usage",
			BillingCurrency: "USD",
			BilledCost:      value,
			EffectiveCost:   value,
			Fingerprint:     fingerprint,
		}
		Expect(DefaultContext.DB().Create(&cost).Error).To(Succeed())
		return cost
	}

	compactRow := func(configID uuid.UUID, grain string) models.ConfigCostCompact {
		GinkgoHelper()
		var row models.ConfigCostCompact
		Expect(DefaultContext.DB().Where("config_id = ? AND grain = ?", configID, grain).
			First(&row).Error).To(Succeed())
		return row
	}

	AfterEach(func() {
		if len(configIDs) > 0 {
			// config_cost_compact and config_costs both cascade from config_items.
			Expect(DefaultContext.DB().Exec("DELETE FROM config_items WHERE id IN ?", configIDs).Error).To(Succeed())
		}
		configIDs = nil
	})

	It("summarises recent raw rows at hour grain", func() {
		configID := createConfig("compact-hourly")
		hour := time.Now().UTC().Truncate(time.Hour).Add(-2 * time.Hour)
		// Two half-hour rows inside one clock hour.
		rawCost(configID, "test:hourly", "hourly", "1.25", hour, hour.Add(30*time.Minute), models.ConfigCostLevel1)
		rawCost(configID, "test:hourly", "hourly", "0.75", hour.Add(30*time.Minute), hour.Add(time.Hour), models.ConfigCostLevel1)

		Expect(CompactConfigCosts.Fn(job.New(DefaultContext))).To(Succeed())

		row := compactRow(configID, models.ConfigCostLevel1)
		Expect(row.PeriodStart.UTC()).To(Equal(hour))
		Expect(row.PeriodEnd.UTC()).To(Equal(hour.Add(time.Hour)))
		Expect(row.EffectiveCost.String()).To(Equal("2"))
	})

	It("summarises raw rows past the day threshold at day grain", func() {
		configID := createConfig("compact-daily")
		day := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -5)
		for h := 0; h < 3; h++ {
			start := day.Add(time.Duration(h) * time.Hour)
			rawCost(configID, "test:daily", "daily", "2", start, start.Add(time.Hour), models.ConfigCostLevel1)
		}

		Expect(CompactConfigCosts.Fn(job.New(DefaultContext))).To(Succeed())

		row := compactRow(configID, models.ConfigCostLevel2)
		Expect(row.PeriodStart.UTC()).To(Equal(day))
		Expect(row.PeriodEnd.UTC()).To(Equal(day.AddDate(0, 0, 1)))
		Expect(row.EffectiveCost.String()).To(Equal("6"))
	})

	It("replaces a restated bucket rather than adding to it", func() {
		// config_costs keeps its rows, and providers restate open billing periods for
		// weeks. Re-running compaction on a restated bucket must recompute the total, not
		// double it — this is the single property most likely to silently inflate spend.
		configID := createConfig("compact-restated")
		hour := time.Now().UTC().Truncate(time.Hour).Add(-2 * time.Hour)
		cost := rawCost(configID, "test:restated", "restated", "10", hour, hour.Add(time.Hour), models.ConfigCostLevel1)

		Expect(CompactConfigCosts.Fn(job.New(DefaultContext))).To(Succeed())
		Expect(compactRow(configID, models.ConfigCostLevel1).EffectiveCost.String()).To(Equal("10"))

		// The provider restates the same hour with a higher running total.
		Expect(DefaultContext.DB().Model(&models.ConfigCost{}).Where("id = ?", cost.ID).
			Updates(map[string]any{
				"billed_cost":    decimal.NewFromInt(17),
				"effective_cost": decimal.NewFromInt(17),
				"updated_at":     time.Now(),
			}).Error).To(Succeed())

		Expect(CompactConfigCosts.Fn(job.New(DefaultContext))).To(Succeed())
		Expect(compactRow(configID, models.ConfigCostLevel1).EffectiveCost.String()).To(Equal("17"))

		var count int64
		Expect(DefaultContext.DB().Model(&models.ConfigCostCompact{}).
			Where("config_id = ?", configID).Count(&count).Error).To(Succeed())
		Expect(count).To(Equal(int64(1)))
	})

	It("is idempotent when nothing has been restated", func() {
		configID := createConfig("compact-idempotent")
		hour := time.Now().UTC().Truncate(time.Hour).Add(-2 * time.Hour)
		rawCost(configID, "test:idem", "idem", "4", hour, hour.Add(time.Hour), models.ConfigCostLevel1)

		Expect(CompactConfigCosts.Fn(job.New(DefaultContext))).To(Succeed())
		Expect(CompactConfigCosts.Fn(job.New(DefaultContext))).To(Succeed())

		Expect(compactRow(configID, models.ConfigCostLevel1).EffectiveCost.String()).To(Equal("4"))
	})

	It("rolls day rows past the coarsening threshold into a 30d bucket and deletes them", func() {
		configID := createConfig("compact-rolled")
		// Older than the 90d default, so the terminal roll picks it up.
		day := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -120)
		for i := 0; i < 2; i++ {
			start := day.AddDate(0, 0, i)
			Expect(DefaultContext.DB().Create(&models.ConfigCostCompact{ConfigCost: models.ConfigCost{
				ID:              uuid.New(),
				ConfigID:        configID,
				SourceKey:       "test:rolled",
				PeriodStart:     start,
				PeriodEnd:       start.AddDate(0, 0, 1),
				Grain:           models.ConfigCostLevel2,
				ChargeCategory:  "Usage",
				BillingCurrency: "USD",
				BilledCost:      decimal.NewFromInt(5),
				EffectiveCost:   decimal.NewFromInt(5),
				Fingerprint:     "rolled",
			}}).Error).To(Succeed())
		}

		Expect(CompactConfigCosts.Fn(job.New(DefaultContext))).To(Succeed())

		// The two days summed into one 30d bucket; unlike the raw passes this one adds,
		// because config_costs no longer holds the source rows.
		row := compactRow(configID, models.ConfigCostLevel3)
		Expect(row.EffectiveCost.String()).To(Equal("10"))

		var remaining int64
		Expect(DefaultContext.DB().Model(&models.ConfigCostCompact{}).
			Where("config_id = ? AND grain = ?", configID, models.ConfigCostLevel2).
			Count(&remaining).Error).To(Succeed())
		Expect(remaining).To(BeZero())
	})
})
