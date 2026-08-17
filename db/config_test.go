package db

import (
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty"
	dutymodels "github.com/flanksource/duty/models"
	"github.com/google/uuid"
	"github.com/lib/pq"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
)

var _ = Describe("FindConfigsByRelationshipSelector", func() {
	It("resolves normalized external IDs without dropping the other selector fields", func() {
		id := uuid.New()
		configType := "CNPG::Cluster"
		externalID := "Kubernetes/production/Cluster/database/Orders"
		item := dutymodels.ConfigItem{
			ID:          id,
			ConfigClass: configType,
			ExternalID:  pq.StringArray{v1.NormalizeExternalID(externalID)},
			Type:        lo.ToPtr(configType),
			Name:        lo.ToPtr("Orders"),
		}
		Expect(DefaultContext.DB().Create(&item).Error).To(Succeed())
		DeferCleanup(func() {
			Expect(DefaultContext.DB().Delete(&dutymodels.ConfigItem{}, id).Error).To(Succeed())
		})

		configs, err := FindConfigsByRelationshipSelector(DefaultContext, duty.RelationshipSelector{
			ExternalID: externalID,
			Type:       configType,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(configs).To(HaveLen(1))
		Expect(configs[0].ID).To(Equal(id))

		configs, err = FindConfigsByRelationshipSelector(DefaultContext, duty.RelationshipSelector{
			ExternalID: externalID,
			Type:       "CNPG::Backup",
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(configs).To(BeEmpty())
	})
})
