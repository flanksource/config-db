package api

import (
	gocontext "context"

	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db/models"
	dutycontext "github.com/flanksource/duty/context"
	"github.com/google/uuid"
	"github.com/lib/pq"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("TempCache", func() {
	It("indexes external_id_v2, aliases and legacy external_id", func() {
		ctx := NewScrapeContext(dutycontext.NewContext(gocontext.Background()))
		cache := NewTempCache()

		scraperID := uuid.New()
		canonical := "AWS::EC2::Instance/i-123"
		item := models.ConfigItem{
			ID:           "config-1",
			Type:         "AWS::EC2::Instance",
			ScraperID:    &scraperID,
			ExternalIDV2: &canonical,
			Aliases:      pq.StringArray{"Alias-One", "Alias-Two"},
			ExternalID:   pq.StringArray{"Legacy-One", "Alias-Two"},
		}

		cache.Insert(item)

		for _, externalID := range []string{"AWS::EC2::Instance/i-123", "Alias-One", "Legacy-One"} {
			found, err := cache.Find(ctx, v1.ExternalID{ConfigType: item.Type, ExternalID: externalID, ScraperID: scraperID.String()})
			Expect(err).ToNot(HaveOccurred())
			Expect(found).ToNot(BeNil())
			Expect(found.ID).To(Equal(item.ID))
		}
	})

	It("falls back to legacy external_id when v2 fields are not populated", func() {
		ctx := NewScrapeContext(dutycontext.NewContext(gocontext.Background()))
		cache := NewTempCache()

		scraperID := uuid.New()
		legacyOnly := models.ConfigItem{
			ID:         "config-2",
			Type:       "Custom::Thing",
			ScraperID:  &scraperID,
			ExternalID: pq.StringArray{"legacy-only-id"},
		}

		cache.Insert(legacyOnly)

		found, err := cache.Find(ctx, v1.ExternalID{ConfigType: legacyOnly.Type, ExternalID: "legacy-only-id", ScraperID: scraperID.String()})
		Expect(err).ToNot(HaveOccurred())
		Expect(found).ToNot(BeNil())
		Expect(found.ID).To(Equal(legacyOnly.ID))
	})
})
