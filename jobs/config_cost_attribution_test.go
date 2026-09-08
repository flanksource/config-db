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

	// createScraper registers a scraper the catalog entries and charges can belong to.
	createScraper := func() uuid.UUID {
		id := uuid.New()
		Expect(DefaultContext.DB().Exec(`
			INSERT INTO config_scrapers (id, name, namespace, spec, source)
			VALUES (?, ?, 'default', '{}', 'ConfigFile')`, id, "reattribute-"+id.String()).Error).To(Succeed())
		DeferCleanup(func() { DefaultContext.DB().Exec("DELETE FROM config_scrapers WHERE id = ?", id) })
		return id
	}

	// createScrapedConfig is createConfig for an entry a particular scraper owns.
	createScrapedConfig := func(scraperID uuid.UUID, configType, externalID string) uuid.UUID {
		id := uuid.New()
		configIDs = append(configIDs, id)
		Expect(DefaultContext.DB().Exec(`
			INSERT INTO config_items (id, scraper_id, type, config_class, external_id, created_at, updated_at)
			VALUES (?, ?, ?, 'Test', ARRAY[?]::text[], now(), now())`,
			id, scraperID, configType, externalID).Error).To(Succeed())
		return id
	}

	// scopedCost writes one charge carrying the lookup scope it was originally resolved
	// under, which is what the re-attribution pass has to reproduce.
	scopedCost := func(configID uuid.UUID, externalID, fingerprint string, scope models.ConfigCost) {
		start := time.Now().UTC().Truncate(time.Hour).Add(-3 * time.Hour)
		amount := decimal.RequireFromString("4.5")
		raw := scope
		raw.ID, raw.ConfigID, raw.SourceKey = uuid.New(), configID, "test:reattribute"
		raw.ExternalID = &externalID
		raw.PeriodStart, raw.PeriodEnd = start, start.Add(time.Hour)
		raw.Grain, raw.ChargeCategory, raw.BillingCurrency = models.ConfigCostLevel1, "Usage", "USD"
		raw.BilledCost, raw.EffectiveCost, raw.Fingerprint = amount, amount, fingerprint
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

	It("attributes to a soft-deleted resource in scope over a live one outside it", func() {
		// The live/deleted preference only ranks candidates that already passed the scope.
		// A live item the scope excludes is not a candidate at all, so it cannot suppress
		// the deleted one that does match — which is how findConfigMatches reads it.
		root := createConfig("Test::Account", "scoped-deleted-root", false)
		createConfig("Test::OtherType", "i-scope-split", false)
		wanted := createConfig("Test::Resource", "i-scope-split", true)
		scopedCost(root, "i-scope-split", "reattribute-scope-split", models.ConfigCost{
			ExternalConfigType:      ptr("Test::Resource"),
			ExternalConfigScraperID: ptr("all"),
		})

		Expect(ReattributeConfigCosts.Fn(job.New(DefaultContext))).To(Succeed())

		Expect(targetsOf("reattribute-scope-split")).To(Equal([]uuid.UUID{wanted, wanted}))
	})

	It("falls back to a GCP resource basename when only its numeric id is aliased", func() {
		// GCP bills some resources under a name whose last segment is a numeric provider
		// id, while inventory stores that id as a standalone alias. resolveCostTarget
		// tries the basename when the full name matches nothing.
		root := createConfig("Test::Account", "basename-root", false)
		resource := createConfig("Test::Resource", "987654321", false)
		scopedCost(root, "//compute.googleapis.com/projects/210987654321/zones/us-central1-a/disks/987654321",
			"reattribute-basename", models.ConfigCost{ExternalConfigScraperID: ptr("all")})

		Expect(ReattributeConfigCosts.Fn(job.New(DefaultContext))).To(Succeed())

		Expect(targetsOf("reattribute-basename")).To(Equal([]uuid.UUID{resource, resource}))
	})

	It("prefers the full resource name over a basename that matches something else", func() {
		root := createConfig("Test::Account", "basename-collision-root", false)
		full := "//container.googleapis.com/projects/demo/locations/europe-west1/clusters/prod"
		wanted := createConfig("Test::Resource", full, false)
		createConfig("Test::OtherResource", "prod", false)
		scopedCost(root, full, "reattribute-basename-collision",
			models.ConfigCost{ExternalConfigScraperID: ptr("all")})

		Expect(ReattributeConfigCosts.Fn(job.New(DefaultContext))).To(Succeed())

		Expect(targetsOf("reattribute-basename-collision")).To(Equal([]uuid.UUID{wanted, wanted}))
	})

	It("scopes to the emitting scraper when the charge names none", func() {
		// With no scope on the charge, resolveCostTarget falls back to the scraper doing
		// the scrape. Ignoring that here would attribute a charge to another scraper's
		// resource that happens to share an id.
		mine, theirs := createScraper(), createScraper()
		root := createConfig("Test::Account", "scraper-scope-root", false)
		scopedCost(root, "i-shared-across-scrapers", "reattribute-foreign-scraper",
			models.ConfigCost{ScraperID: &mine})
		createScrapedConfig(theirs, "Test::Resource", "i-shared-across-scrapers")

		Expect(ReattributeConfigCosts.Fn(job.New(DefaultContext))).To(Succeed())

		Expect(targetsOf("reattribute-foreign-scraper")).To(Equal([]uuid.UUID{root, root}))
	})

	It("attributes to a resource belonging to the emitting scraper", func() {
		mine := createScraper()
		root := createConfig("Test::Account", "scraper-own-root", false)
		scopedCost(root, "i-own-scraper", "reattribute-own-scraper",
			models.ConfigCost{ScraperID: &mine})
		resource := createScrapedConfig(mine, "Test::Resource", "i-own-scraper")

		Expect(ReattributeConfigCosts.Fn(job.New(DefaultContext))).To(Succeed())

		Expect(targetsOf("reattribute-own-scraper")).To(Equal([]uuid.UUID{resource, resource}))
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
