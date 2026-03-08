package scrapers

import (
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
	"github.com/flanksource/config-db/db/models"
	dutymodels "github.com/flanksource/duty/models"
	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	k8sTypes "k8s.io/apimachinery/pkg/types"
)

var _ = Describe("Incremental scrape preserves RBAC data", Ordered, func() {
	var scrapeConfigFull v1.ScrapeConfig
	var scraperCtx api.ScrapeContext
	var scraperModel dutymodels.ConfigScraper
	var initialUserIDs []uuid.UUID

	BeforeAll(func() {
		scrapeConfigFull = getConfigSpec("file-incremental-rbac-full")

		scModel, err := scrapeConfigFull.ToModel()
		Expect(err).NotTo(HaveOccurred())
		scModel.Source = dutymodels.SourceUI

		err = DefaultContext.DB().Create(&scModel).Error
		Expect(err).NotTo(HaveOccurred())

		scrapeConfigFull.SetUID(k8sTypes.UID(scModel.ID.String()))
		scraperCtx = api.NewScrapeContext(DefaultContext).WithScrapeConfig(&scrapeConfigFull)
		scraperModel = scModel
	})

	AfterAll(func() {
		Expect(DefaultContext.DB().Unscoped().Where("scraper_id = ?", scraperModel.ID).Delete(&dutymodels.ConfigAccess{}).Error).NotTo(HaveOccurred())
		Expect(DefaultContext.DB().Unscoped().Where("scraper_id = ?", scraperModel.ID).Delete(&dutymodels.ExternalUser{}).Error).NotTo(HaveOccurred())
		Expect(DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Delete(&models.ConfigItem{}).Error).NotTo(HaveOccurred())
		Expect(DefaultContext.DB().Delete(&scraperModel).Error).NotTo(HaveOccurred())
	})

	It("should establish baseline with full scrape", func() {
		_, err := RunScraper(scraperCtx)
		Expect(err).To(BeNil())

		var users []dutymodels.ExternalUser
		err = DefaultContext.DB().Where("scraper_id = ? AND deleted_at IS NULL", scraperModel.ID).Find(&users).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(users).To(HaveLen(2), "full scrape should create 2 users")

		initialUserIDs = lo.Map(users, func(u dutymodels.ExternalUser, _ int) uuid.UUID { return u.ID })

		var accesses []dutymodels.ConfigAccess
		err = DefaultContext.DB().Where("scraper_id = ? AND deleted_at IS NULL", scraperModel.ID).Find(&accesses).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(accesses).To(HaveLen(2), "full scrape should create 2 config access entries")
	})

	It("should not delete entities during incremental scrape with partial batch", func() {
		db.ExternalUserCache.Flush()

		partialConfig := getConfigSpec("file-incremental-rbac-partial")
		partialConfig.SetUID(k8sTypes.UID(scraperModel.ID.String()))
		incrementalCtx := api.NewScrapeContext(DefaultContext).
			WithScrapeConfig(&partialConfig).
			AsIncrementalScrape()

		_, err := RunScraper(incrementalCtx)
		Expect(err).To(BeNil())

		var activeUsers []dutymodels.ExternalUser
		err = DefaultContext.DB().Where("scraper_id = ? AND deleted_at IS NULL", scraperModel.ID).Find(&activeUsers).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(activeUsers).To(HaveLen(2), "incremental scrape should preserve all users")

		activeUserIDs := lo.Map(activeUsers, func(u dutymodels.ExternalUser, _ int) uuid.UUID { return u.ID })
		Expect(activeUserIDs).To(ConsistOf(initialUserIDs))

		var activeAccesses []dutymodels.ConfigAccess
		err = DefaultContext.DB().Where("scraper_id = ? AND deleted_at IS NULL", scraperModel.ID).Find(&activeAccesses).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(activeAccesses).To(HaveLen(2), "incremental scrape should preserve all config access entries")
	})

	It("should still delete stale entities on a full scrape", func() {
		db.ExternalUserCache.Flush()

		reducedConfig := getConfigSpec("file-incremental-rbac-partial")
		reducedConfig.SetUID(k8sTypes.UID(scraperModel.ID.String()))
		fullCtx := api.NewScrapeContext(DefaultContext).WithScrapeConfig(&reducedConfig)

		_, err := RunScraper(fullCtx)
		Expect(err).To(BeNil())

		// Users are not stale-deleted (cleanup is deferred), so both remain
		var activeUsers []dutymodels.ExternalUser
		err = DefaultContext.DB().Where("scraper_id = ? AND deleted_at IS NULL", scraperModel.ID).Find(&activeUsers).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(activeUsers).To(HaveLen(2), "users should not be stale-deleted")

		// Config access stale deletion still works on full scrapes
		var activeAccesses []dutymodels.ConfigAccess
		err = DefaultContext.DB().Where("scraper_id = ? AND deleted_at IS NULL", scraperModel.ID).Find(&activeAccesses).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(activeAccesses).To(HaveLen(1), "full scrape with reduced data should delete stale config access")
	})
})
