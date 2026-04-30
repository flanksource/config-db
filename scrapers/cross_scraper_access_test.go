package scrapers

import (
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/api"
	"github.com/flanksource/config-db/db/models"
	dutymodels "github.com/flanksource/duty/models"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	k8sTypes "k8s.io/apimachinery/pkg/types"
)

var _ = Describe("Config access cross-scraper resolution via scraper_id: all", Ordered, func() {
	var (
		ownerScrape   v1.ScrapeConfig
		ownerCtx      api.ScrapeContext
		ownerModel    dutymodels.ConfigScraper
		emitterScrape v1.ScrapeConfig
		emitterCtx    api.ScrapeContext
		emitterModel  dutymodels.ConfigScraper
	)

	BeforeAll(func() {
		ownerScrape = getConfigSpec("cross-scraper-org")
		ownerScrapeModel, err := ownerScrape.ToModel()
		Expect(err).NotTo(HaveOccurred())
		ownerScrapeModel.Source = dutymodels.SourceUI
		Expect(DefaultContext.DB().Create(&ownerScrapeModel).Error).To(Succeed())
		ownerScrape.SetUID(k8sTypes.UID(ownerScrapeModel.ID.String()))
		ownerCtx = api.NewScrapeContext(DefaultContext).WithScrapeConfig(&ownerScrape)
		ownerModel = ownerScrapeModel

		emitterScrape = getConfigSpec("cross-scraper-access")
		emitterScrapeModel, err := emitterScrape.ToModel()
		Expect(err).NotTo(HaveOccurred())
		emitterScrapeModel.Source = dutymodels.SourceUI
		Expect(DefaultContext.DB().Create(&emitterScrapeModel).Error).To(Succeed())
		emitterScrape.SetUID(k8sTypes.UID(emitterScrapeModel.ID.String()))
		emitterCtx = api.NewScrapeContext(DefaultContext).WithScrapeConfig(&emitterScrape)
		emitterModel = emitterScrapeModel
	})

	AfterAll(func() {
		Expect(DefaultContext.DB().Where("scraper_id IN ?", []any{ownerModel.ID, emitterModel.ID}).Delete(&dutymodels.ConfigAccess{}).Error).To(Succeed())
		Expect(DefaultContext.DB().Where("scraper_id IN ?", []any{ownerModel.ID, emitterModel.ID}).Delete(&dutymodels.ExternalUser{}).Error).To(Succeed())
		Expect(DefaultContext.DB().Where("scraper_id IN ?", []any{ownerModel.ID, emitterModel.ID}).Delete(&dutymodels.ExternalRole{}).Error).To(Succeed())
		Expect(DefaultContext.DB().Where("scraper_id IN ?", []any{ownerModel.ID, emitterModel.ID}).Delete(&models.ConfigItem{}).Error).To(Succeed())
		Expect(DefaultContext.DB().Delete(&ownerModel).Error).To(Succeed())
		Expect(DefaultContext.DB().Delete(&emitterModel).Error).To(Succeed())
	})

	It("scraper-A creates the Organization config", func() {
		_, err := RunScraper(ownerCtx)
		Expect(err).To(BeNil())

		var orgs []models.ConfigItem
		Expect(DefaultContext.DB().Where("scraper_id = ? AND type = ?", ownerModel.ID, "Organization").Find(&orgs).Error).To(Succeed())
		Expect(orgs).To(HaveLen(1))
	})

	It("scraper-B saves only the access row with scraper_id: all", func() {
		_, err := RunScraper(emitterCtx)
		Expect(err).To(BeNil())

		var accesses []dutymodels.ConfigAccess
		Expect(DefaultContext.DB().Where("scraper_id = ?", emitterModel.ID).Find(&accesses).Error).To(Succeed())

		Expect(accesses).To(HaveLen(1), "only the scraper_id:all access entry should be saved; the entry without scraper_id must be skipped")
		Expect(accesses[0].ID).To(Equal("cross-scraper-access-allowed"))

		var ownerOrg models.ConfigItem
		Expect(DefaultContext.DB().Where("scraper_id = ? AND type = ?", ownerModel.ID, "Organization").First(&ownerOrg).Error).To(Succeed())
		Expect(accesses[0].ConfigID.String()).To(Equal(ownerOrg.ID),
			"resolved config_id should point at scraper-A's Organization row")
	})
})
