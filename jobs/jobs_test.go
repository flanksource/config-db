package jobs

import (
	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Job Tests", ginkgo.Ordered, func() {
	var totalconfigitems int

	ginkgo.BeforeAll(func() {
		CleanupConfigItems.Context = DefaultContext
		ConfigItemRetentionDays = 7

		err := DefaultContext.DB().Raw("SELECT COUNT(*) FROM config_items").Scan(&totalconfigitems).Error
		Expect(err).ToNot(HaveOccurred())
	})

	ginkgo.It("should not cleanup recently deleted config items", func() {
		err := DefaultContext.DB().Exec("UPDATE config_items SET deleted_at = NOW()").Error
		Expect(err).ToNot(HaveOccurred())

		CleanupConfigItems.Run()
		expectJobToPass(CleanupConfigItems)

		var after int
		err = DefaultContext.DB().Raw("SELECT COUNT(*) FROM config_items").Scan(&after).Error
		Expect(err).ToNot(HaveOccurred())
		Expect(after).To(Equal(totalconfigitems))
	})

	ginkgo.It("should delete all the existing relationship, changes & analyses", func() {
		err := DefaultContext.DB().Exec("DELETE FROM config_changes; DELETE FROM config_analysis; DELETE FROM config_relationships;").Error
		Expect(err).ToNot(HaveOccurred())
	})

	ginkgo.It("should cleanup deleted config items after reducing retention days", func() {
		ConfigItemRetentionDays = 0
		CleanupConfigItems.Run()
		expectJobToPass(CleanupConfigItems)

		var after int
		q := `
		SELECT COUNT(*) FROM config_items WHERE id NOT IN (
			SELECT DISTINCT config_id FROM evidences
			UNION SELECT DISTINCT config_id FROM playbook_runs
			UNION SELECT DISTINCT config_id FROM components
		)`

		err := DefaultContext.DB().Raw(q).Scan(&after).Error
		Expect(err).ToNot(HaveOccurred())
		Expect(after).To(Equal(0))
	})
})
