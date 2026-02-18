package scrapers

import (
	"github.com/flanksource/config-db/api"
	dutymodels "github.com/flanksource/duty/models"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	k8sTypes "k8s.io/apimachinery/pkg/types"
)

var _ = Describe("CRD Sync test", Ordered, func() {
	var configItem dutymodels.ConfigItem
	var playbook dutymodels.Playbook
	var scrapeConfigModel dutymodels.ConfigScraper
	var scraperCtx api.ScrapeContext

	BeforeAll(func() {
		scrapeConfig := getConfigSpec("file-crd-sync")

		scModel, err := scrapeConfig.ToModel()
		Expect(err).NotTo(HaveOccurred(), "failed to convert scrape config to model")
		scModel.Source = dutymodels.SourceUI

		err = DefaultContext.DB().Create(&scModel).Error
		Expect(err).NotTo(HaveOccurred(), "failed to create scrape config")

		scrapeConfig.SetUID(k8sTypes.UID(scModel.ID.String()))
		scraperCtx = api.NewScrapeContext(DefaultContext).WithScrapeConfig(&scrapeConfig)

		scrapeConfigModel = scModel
	})

	AfterAll(func() {
		err := DefaultContext.DB().Delete(configItem).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete config item")

		err = DefaultContext.DB().Delete(playbook).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete playbook")

		err = DefaultContext.DB().Delete(scrapeConfigModel).Error
		Expect(err).NotTo(HaveOccurred(), "failed to delete scrape config")
	})

	It("should scrape", func() {
		output, err := RunScraper(scraperCtx)
		Expect(err).To(BeNil())

		Expect(output.Total).To(Equal(1))
	})

	It("should have the updated the db", func() {
		err := DefaultContext.DB().Where("name = ?", "echo-input-name").First(&configItem).Error
		Expect(err).NotTo(HaveOccurred(), "failed to find configmap")

		err = DefaultContext.DB().Where("name = ?", "echo-input-name").First(&playbook).Error
		Expect(err).NotTo(HaveOccurred(), "failed to find playbook")
		Expect(playbook.Source).To(Equal(dutymodels.SourceCRDSync))
	})
})
