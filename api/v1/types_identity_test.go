package v1

import (
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/lib/pq"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ExternalID.Find", func() {
	It("uses external_id_v2 first with aliases and legacy external_id fallback", func() {
		db, err := gorm.Open(postgres.New(postgres.Config{
			DSN:                  "host=localhost user=test password=test dbname=test port=5432 sslmode=disable",
			PreferSimpleProtocol: true,
		}), &gorm.Config{DryRun: true, DisableAutomaticPing: true})
		Expect(err).ToNot(HaveOccurred())

		ext := ExternalID{
			ConfigType: "AWS::EC2::Instance",
			ExternalID: "Mixed-Case-ID",
			ScraperID:  "scraper-1",
		}

		stmt := ext.Find(db.Table("config_items")).Find(&[]map[string]any{}).Statement
		sql := stmt.SQL.String()

		Expect(sql).To(ContainSubstring("external_id_v2 ="))
		Expect(sql).To(ContainSubstring("aliases @>"))
		Expect(sql).To(ContainSubstring("external_id @>"))

		Expect(len(stmt.Vars)).To(BeNumerically(">=", 3))
		Expect(stmt.Vars[0]).To(Equal(ext.ExternalID))
		Expect(stmt.Vars[1]).To(Equal(pq.StringArray{ext.ExternalID}))
		Expect(stmt.Vars[2]).To(Equal(pq.StringArray{ext.ExternalID}))
	})
})
