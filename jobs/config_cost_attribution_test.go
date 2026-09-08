// Covers the re-attribution pass: a charge frozen against the account root moves onto its
// resource once the catalog has it, and an ambiguous or absent resource is left alone.
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

var _ = Describe("config cost reattribution", func() {
	var configIDs []uuid.UUID

	// createConfig writes a catalog entry carrying externalID as an alias. A nil
	// deletedAt leaves it live.
	createConfig := func(configType, externalID string, deleted bool) uuid.UUID {
		id := uuid.New()
		configIDs = append(configIDs, id)
		deletedAt := "NULL"
		if deleted {
			deletedAt = "now()"
		}
		Expect(DefaultContext.DB().Exec(`
			INSERT INTO config_items (id, type, config_class, external_id, deleted_at, created_at, updated_at)
			VALUES (?, ?, 'Test', ARRAY[?]::text[], `+deletedAt+`, now(), now())`,
			id, configType, externalID).Error).To(Succeed())
		return id
	}

	// bookedCost writes one charge into both cost tables, attributed to configID and
	// naming externalID as the resource it was for.
	bookedCost := func(configID uuid.UUID, externalID, fingerprint string) {
		start := time.Now().UTC().Truncate(time.Hour).Add(-3 * time.Hour)
		amount := decimal.RequireFromString("4.5")
		raw := models.ConfigCost{
			ID: uuid.New(), ConfigID: configID, SourceKey: "test:reattribute",
			ExternalID:              &externalID,
			ExternalConfigScraperID: ptr("all"),
			PeriodStart:             start, PeriodEnd: start.Add(time.Hour),
			Grain: models.ConfigCostLevel1, ChargeCategory: "Usage", BillingCurrency: "USD",
			BilledCost: amount, EffectiveCost: amount, Fingerprint: fingerprint,
		}
		Expect(DefaultContext.DB().Create(&raw).Error).To(Succeed())
		compact := models.ConfigCostCompact{ConfigCost: raw}
		compact.ID = uuid.New()
		Expect(DefaultContext.DB().Create(&compact).Error).To(Succeed())
	}

	targetsOf := func(fingerprint string) []uuid.UUID {
		GinkgoHelper()
		var targets []uuid.UUID
		for _, table := range costAttributionTables {
			var booked []uuid.UUID
			Expect(DefaultContext.DB().Table(table).Where("fingerprint = ?", fingerprint).
				Pluck("config_id", &booked).Error).To(Succeed())
			Expect(booked).To(HaveLen(1), "one charge per table in %s", table)
			targets = append(targets, booked[0])
		}
		return targets
	}

	AfterEach(func() {
		if len(configIDs) > 0 {
			// Both cost tables cascade from config_items.
			Expect(DefaultContext.DB().Exec("DELETE FROM config_items WHERE id IN ?", configIDs).Error).To(Succeed())
		}
		configIDs = nil
	})

	It("moves a root-booked charge onto the resource the catalog now has", func() {
		root := createConfig("Test::Account", "reattribute-root", false)
		resource := createConfig("Test::Resource", "i-late-discovery", false)
		bookedCost(root, "i-late-discovery", "reattribute-late")

		Expect(ReattributeConfigCosts.Fn(job.New(DefaultContext))).To(Succeed())

		Expect(targetsOf("reattribute-late")).To(Equal([]uuid.UUID{resource, resource}))
	})

	It("moves a charge onto a resource the catalog has soft deleted", func() {
		root := createConfig("Test::Account", "reattribute-deleted-root", false)
		resource := createConfig("Test::Resource", "i-retired-late", true)
		bookedCost(root, "i-retired-late", "reattribute-retired")

		Expect(ReattributeConfigCosts.Fn(job.New(DefaultContext))).To(Succeed())

		Expect(targetsOf("reattribute-retired")).To(Equal([]uuid.UUID{resource, resource}))
	})

	It("leaves a charge naming a resource the catalog does not have", func() {
		root := createConfig("Test::Account", "reattribute-absent-root", false)
		bookedCost(root, "i-never-scraped", "reattribute-absent")

		Expect(ReattributeConfigCosts.Fn(job.New(DefaultContext))).To(Succeed())

		Expect(targetsOf("reattribute-absent")).To(Equal([]uuid.UUID{root, root}))
	})

	It("leaves a charge whose resource id matches more than one config item", func() {
		root := createConfig("Test::Account", "reattribute-ambiguous-root", false)
		createConfig("Test::Resource", "i-ambiguous-late", false)
		createConfig("Test::OtherResource", "i-ambiguous-late", false)
		bookedCost(root, "i-ambiguous-late", "reattribute-ambiguous")

		Expect(ReattributeConfigCosts.Fn(job.New(DefaultContext))).To(Succeed())

		Expect(targetsOf("reattribute-ambiguous")).To(Equal([]uuid.UUID{root, root}))
	})

	It("is idempotent", func() {
		root := createConfig("Test::Account", "reattribute-idempotent-root", false)
		resource := createConfig("Test::Resource", "i-idempotent", false)
		bookedCost(root, "i-idempotent", "reattribute-idempotent")

		Expect(ReattributeConfigCosts.Fn(job.New(DefaultContext))).To(Succeed())
		Expect(ReattributeConfigCosts.Fn(job.New(DefaultContext))).To(Succeed())

		Expect(targetsOf("reattribute-idempotent")).To(Equal([]uuid.UUID{resource, resource}))
	})
})

func ptr(s string) *string { return &s }
