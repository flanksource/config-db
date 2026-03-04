package scrapers

import (
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db"
	"github.com/flanksource/config-db/db/models"

	"github.com/flanksource/config-db/api"
	dutymodels "github.com/flanksource/duty/models"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	k8sTypes "k8s.io/apimachinery/pkg/types"
)

var _ = Describe("Permission change tracking", Ordered, func() {
	var scrapeConfig v1.ScrapeConfig
	var scraperCtx api.ScrapeContext
	var scraperModel dutymodels.ConfigScraper
	var configItemID string

	BeforeAll(func() {
		// Reset global external-user cache to avoid flaky results from prior tests
		db.ExternalUserCache.Flush()

		scrapeConfig = getConfigSpec("file-permission-changes")

		scModel, err := scrapeConfig.ToModel()
		Expect(err).NotTo(HaveOccurred())
		scModel.Source = dutymodels.SourceUI

		err = DefaultContext.DB().Create(&scModel).Error
		Expect(err).NotTo(HaveOccurred())

		scrapeConfig.SetUID(k8sTypes.UID(scModel.ID.String()))
		scraperCtx = api.NewScrapeContext(DefaultContext).WithScrapeConfig(&scrapeConfig)
		scraperModel = scModel
	})

	AfterAll(func() {
		Expect(DefaultContext.DB().Unscoped().Where("scraper_id = ?", scraperModel.ID).Delete(&dutymodels.ConfigAccess{}).Error).NotTo(HaveOccurred())
		Expect(DefaultContext.DB().Unscoped().Where("scraper_id = ?", scraperModel.ID).Delete(&dutymodels.ExternalUser{}).Error).NotTo(HaveOccurred())
		if configItemID != "" {
			Expect(DefaultContext.DB().Where("config_id = ? AND change_type IN (?, ?)", configItemID, v1.ChangeTypePermissionAdded, v1.ChangeTypePermissionRemoved).
				Delete(&models.ConfigChange{}).Error).NotTo(HaveOccurred())
		}
		Expect(DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Delete(&models.ConfigItem{}).Error).NotTo(HaveOccurred())
		Expect(DefaultContext.DB().Delete(&scraperModel).Error).NotTo(HaveOccurred())
	})

	It("should emit PermissionAdded changes on first scrape", func() {
		_, err := RunScraper(scraperCtx)
		Expect(err).To(BeNil())

		var configItem models.ConfigItem
		err = DefaultContext.DB().Where("type = ? AND scraper_id = ?", "Organization", scraperModel.ID).First(&configItem).Error
		Expect(err).NotTo(HaveOccurred())
		configItemID = configItem.ID

		var changes []models.ConfigChange
		err = DefaultContext.DB().Where("config_id = ? AND change_type = ?", configItemID, v1.ChangeTypePermissionAdded).
			Find(&changes).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(changes).To(HaveLen(2), "expected 2 PermissionAdded changes for 2 new config access entries")

		summaries := make([]string, len(changes))
		for i, c := range changes {
			summaries[i] = c.Summary
			Expect(c.Summary).To(ContainSubstring("user "), "change summary should include the user name")
		}
		Expect(summaries).To(ContainElement(ContainSubstring("Perm User One")))
		Expect(summaries).To(ContainElement(ContainSubstring("Perm User Two")))
	})

	It("should not emit duplicate changes on re-scrape", func() {
		db.ExternalUserCache.Flush()

		_, err := RunScraper(scraperCtx)
		Expect(err).To(BeNil())

		var changes []models.ConfigChange
		err = DefaultContext.DB().Where("config_id = ? AND change_type = ?", configItemID, v1.ChangeTypePermissionAdded).
			Find(&changes).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(changes).To(HaveLen(2), "re-scrape should not create additional PermissionAdded changes")
	})

	It("should emit PermissionRemoved when permission is revoked", func() {
		db.ExternalUserCache.Flush()

		reducedConfig := getConfigSpec("file-permission-changes-reduced")
		reducedConfig.SetUID(k8sTypes.UID(scraperModel.ID.String()))
		reducedCtx := api.NewScrapeContext(DefaultContext).WithScrapeConfig(&reducedConfig)

		_, err := RunScraper(reducedCtx)
		Expect(err).To(BeNil())

		var removedChanges []models.ConfigChange
		err = DefaultContext.DB().Where("config_id = ? AND change_type = ?", configItemID, v1.ChangeTypePermissionRemoved).
			Find(&removedChanges).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(removedChanges).To(HaveLen(1), "expected 1 PermissionRemoved change for revoked access")
		Expect(removedChanges[0].Summary).To(ContainSubstring("Perm User Two"))

		var deletedAccess []dutymodels.ConfigAccess
		err = DefaultContext.DB().Where("scraper_id = ? AND deleted_at IS NOT NULL", scraperModel.ID).Find(&deletedAccess).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(deletedAccess).To(HaveLen(1), "one config access should be soft-deleted")
	})

	It("should emit PermissionAdded when revoked permission is re-granted", func() {
		db.ExternalUserCache.Flush()

		_, err := RunScraper(scraperCtx)
		Expect(err).To(BeNil())

		var addedChanges []models.ConfigChange
		err = DefaultContext.DB().Where("config_id = ? AND change_type = ?", configItemID, v1.ChangeTypePermissionAdded).
			Find(&addedChanges).Error
		Expect(err).NotTo(HaveOccurred())
		Expect(addedChanges).To(HaveLen(3), "expected 3 total PermissionAdded changes: 2 initial + 1 re-grant")
	})
})
