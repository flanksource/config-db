package v1

import (
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

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
		Expect(sql).To(ContainSubstring("aliases @> ARRAY"))
		Expect(sql).To(ContainSubstring("external_id @> ARRAY"))

		exactCount := 0
		for _, v := range stmt.Vars {
			if s, ok := v.(string); ok && s == ext.ExternalID {
				exactCount++
			}
		}
		Expect(exactCount).To(BeNumerically(">=", 3))
	})
})
