// Covers the derivation of config_cost_compact from config_costs: the grain each raw row
// lands at, that a restated bucket is replaced rather than added to, and that the terminal
// 1d→30d roll sums and deletes.
package jobs

import (
	"time"

	"github.com/flanksource/commons/properties"

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
		// Older than the 90d default, so the terminal roll picks it up. cost_bucket anchors
		// level-3 buckets on the epoch rather than the calendar, so the start is snapped to
		// a bucket boundary: two consecutive days chosen off the run date straddle one
		// roughly every thirty runs, and the roll would then produce two rows, not one.
		width := int64(30 * 24 * time.Hour / time.Second)
		epoch := time.Now().UTC().AddDate(0, 0, -120).Unix()
		day := time.Unix(epoch/width*width, 0).UTC()
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

var _ = Describe("compaction level routing", func() {
	var configIDs []uuid.UUID

	createConfig := func(externalID string) uuid.UUID {
		id := uuid.New()
		configIDs = append(configIDs, id)
		Expect(DefaultContext.DB().Exec(`
			INSERT INTO config_items (id, type, config_class, external_id, created_at, updated_at)
			VALUES (?, 'Test::CostTarget', 'Test', ARRAY[?]::text[], now(), now())`, id, externalID).Error).To(Succeed())
		return id
	}

	AfterEach(func() {
		if len(configIDs) > 0 {
			Expect(DefaultContext.DB().Exec("DELETE FROM config_items WHERE id IN ?", configIDs).Error).To(Succeed())
		}
		configIDs = nil
	})

	It("does not leave a stale finer row behind when raw ages past the threshold", func() {
		configID := createConfig("routing-ageing")
		// A raw row whose period has already aged past the level-2 threshold, plus the
		// level-1 compact row an earlier pass would have written while it was still
		// young. Periods never move in reality; time passes, so this is the real shape of
		// the transition.
		hour := time.Now().UTC().Truncate(time.Hour).AddDate(0, 0, -5)
		Expect(DefaultContext.DB().Create(&models.ConfigCost{
			ID: uuid.New(), ConfigID: configID, SourceKey: "test:ageing",
			PeriodStart: hour, PeriodEnd: hour.Add(time.Hour),
			Grain: models.ConfigCostLevel1, ChargeCategory: "Usage", BillingCurrency: "USD",
			BilledCost: decimal.NewFromInt(6), EffectiveCost: decimal.NewFromInt(6),
			Fingerprint: "ageing",
		}).Error).To(Succeed())
		Expect(DefaultContext.DB().Create(&models.ConfigCostCompact{ConfigCost: models.ConfigCost{
			ID: uuid.New(), ConfigID: configID, SourceKey: "test:ageing",
			PeriodStart: hour, PeriodEnd: hour.Add(time.Hour),
			Grain: models.ConfigCostLevel1, ChargeCategory: "Usage", BillingCurrency: "USD",
			BilledCost: decimal.NewFromInt(6), EffectiveCost: decimal.NewFromInt(6),
			Fingerprint: "ageing",
		}}).Error).To(Succeed())

		Expect(CompactConfigCosts.Fn(job.New(DefaultContext))).To(Succeed())

		// The money must appear exactly once, at level 2, not once per level.
		var rows []models.ConfigCostCompact
		Expect(DefaultContext.DB().Where("config_id = ?", configID).Find(&rows).Error).To(Succeed())
		var total decimal.Decimal
		for _, r := range rows {
			total = total.Add(r.EffectiveCost)
			Expect(r.Grain).To(Equal(models.ConfigCostLevel2))
		}
		Expect(total.String()).To(Equal("6"), "found %d compact rows", len(rows))
	})

	It("does not drop finer rows the coarse pass skipped", func() {
		configID := createConfig("routing-stale-window")
		// Raw rows that have aged past the level-2 threshold and have not been restated
		// since — the steady state for any scraper whose schedule is longer than the
		// restatement window. updated_at is set on INSERT, which the updated_at trigger
		// leaves alone, so this is the real shape of an untouched backlog.
		day := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -5)
		for h := 0; h < 4; h++ {
			start := day.Add(time.Duration(h) * time.Hour)
			Expect(DefaultContext.DB().Exec(`
				INSERT INTO config_costs (id, config_id, source_key, period_start, period_end, grain,
					charge_category, billing_currency, billed_cost, effective_cost, fingerprint,
					created_at, updated_at)
				VALUES (?, ?, 'test:stale', ?, ?, ?, 'Usage', 'USD', 3, 3, 'stale',
					now() - interval '10 days', now() - interval '10 days')`,
				uuid.New(), configID, start, start.Add(time.Hour), models.ConfigCostLevel1).Error).To(Succeed())
			// The level-1 compact row an earlier pass wrote while the period was young.
			Expect(DefaultContext.DB().Exec(`
				INSERT INTO config_cost_compact (id, config_id, source_key, period_start, period_end, grain,
					charge_category, billing_currency, billed_cost, effective_cost, fingerprint)
				VALUES (?, ?, 'test:stale', ?, ?, ?, 'Usage', 'USD', 3, 3, 'stale')`,
				uuid.New(), configID, start, start.Add(time.Hour), models.ConfigCostLevel1).Error).To(Succeed())
		}

		Expect(CompactConfigCosts.Fn(job.New(DefaultContext))).To(Succeed())

		// The coarse pass must take aged rows whether or not they were restated recently:
		// the superseded-delete removes their level-1 copies on age alone, so anything the
		// coarse pass skips is money that no longer exists anywhere in config_cost_compact.
		var rows []models.ConfigCostCompact
		Expect(DefaultContext.DB().Where("config_id = ?", configID).Find(&rows).Error).To(Succeed())
		var total decimal.Decimal
		for _, r := range rows {
			total = total.Add(r.EffectiveCost)
		}
		Expect(total.String()).To(Equal("12"), "compaction dropped money; %d rows left", len(rows))
	})

	It("leaves an untouched backlog for the daily reconcile", func() {
		configID := createConfig("routing-backlog")
		// Aged raw rows with no level-1 copy and no recent restatement. Nothing marks
		// them as needing work, so the incremental pass has no reason to find them —
		// that is the whole point of not rescanning the aged range every run.
		day := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -5)
		for h := 0; h < 4; h++ {
			start := day.Add(time.Duration(h) * time.Hour)
			Expect(DefaultContext.DB().Exec(`
				INSERT INTO config_costs (id, config_id, source_key, period_start, period_end, grain,
					charge_category, billing_currency, billed_cost, effective_cost, fingerprint,
					created_at, updated_at)
				VALUES (?, ?, 'test:backlog', ?, ?, ?, 'Usage', 'USD', 3, 3, 'backlog',
					now() - interval '10 days', now() - interval '10 days')`,
				uuid.New(), configID, start, start.Add(time.Hour), models.ConfigCostLevel1).Error).To(Succeed())
		}

		Expect(CompactConfigCosts.Fn(job.New(DefaultContext))).To(Succeed())

		var skipped int64
		Expect(DefaultContext.DB().Model(&models.ConfigCostCompact{}).
			Where("config_id = ?", configID).Count(&skipped).Error).To(Succeed())
		Expect(skipped).To(BeZero(), "the incremental pass rescanned the aged range")

		// The daily reconcile ignores the window and rebuilds from raw, which is what
		// keeps a missed transition from becoming permanent.
		Expect(ReconcileConfigCosts.Fn(job.New(DefaultContext))).To(Succeed())

		var rows []models.ConfigCostCompact
		Expect(DefaultContext.DB().Where("config_id = ?", configID).Find(&rows).Error).To(Succeed())
		var total decimal.Decimal
		for _, r := range rows {
			total = total.Add(r.EffectiveCost)
			Expect(r.Grain).To(Equal(models.ConfigCostLevel2))
		}
		Expect(total.String()).To(Equal("12"), "reconcile did not recover the backlog; %d rows", len(rows))
	})

	It("keeps a long charge period at its own level regardless of age", func() {
		configID := createConfig("routing-long")
		// A monthly charge scraped today: bucketFor labels it level3.
		start := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -20)
		Expect(DefaultContext.DB().Create(&models.ConfigCost{
			ID: uuid.New(), ConfigID: configID, SourceKey: "test:long",
			PeriodStart: start, PeriodEnd: start.AddDate(0, 0, 30),
			Grain: models.ConfigCostLevel3, ChargeCategory: "Purchase", BillingCurrency: "USD",
			BilledCost: decimal.NewFromInt(300), EffectiveCost: decimal.NewFromInt(300),
			Fingerprint: "long-charge",
		}).Error).To(Succeed())

		Expect(CompactConfigCosts.Fn(job.New(DefaultContext))).To(Succeed())

		var row models.ConfigCostCompact
		Expect(DefaultContext.DB().Where("config_id = ?", configID).First(&row).Error).To(Succeed())
		Expect(row.Grain).To(Equal(models.ConfigCostLevel3))
		Expect(row.PeriodEnd.Sub(row.PeriodStart)).To(BeNumerically(">", 24*time.Hour))
	})
})

// Compaction thresholds and retention are independent properties, but they are not
// independent settings: raw costs must outlive the level they are compacted at, or the
// superseded-delete removes the finer copy after the source it would be rebuilt from has
// already expired.
var _ = Describe("compaction threshold coherence", func() {
	restore := func(property, value string) {
		DeferCleanup(func() { properties.Set(property, value) })
	}

	It("refuses to run when raw retention is shorter than the level-2 threshold", func() {
		restore(propCostsRetention, defaultCostsRetention)
		properties.Set(propCostsRetention, "1h")

		err := CompactConfigCosts.Fn(job.New(DefaultContext))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(propCostsRetention))
		Expect(err.Error()).To(ContainSubstring(propCompactDayAfter))
	})

	It("refuses to run when compact retention is shorter than the level-3 threshold", func() {
		restore(propCompactRetention, defaultCompactRetention)
		properties.Set(propCompactRetention, "1h")

		err := CompactConfigCosts.Fn(job.New(DefaultContext))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(propCompactRetention))
		Expect(err.Error()).To(ContainSubstring(propCompact30dAfter))
	})

	It("runs with the shipped defaults", func() {
		Expect(CompactConfigCosts.Fn(job.New(DefaultContext))).To(Succeed())
	})
})
