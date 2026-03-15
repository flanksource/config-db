package scrapers

import (
	"fmt"
	"strings"
	"time"

	"github.com/flanksource/config-db/db/models"
	dutymodels "github.com/flanksource/duty/models"
	"github.com/google/uuid"
	"github.com/lib/pq"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	k8sTypes "k8s.io/apimachinery/pkg/types"
)

var _ = Describe("external entity merges", func() {
	It("merges user dependents without violating unique constraints", func() {
		scrapeConfig := getConfigSpec("file-config-access")

		scraperModel, err := scrapeConfig.ToModel()
		Expect(err).NotTo(HaveOccurred())
		scraperModel.Source = dutymodels.SourceUI
		Expect(DefaultContext.DB().Create(&scraperModel).Error).NotTo(HaveOccurred())
		scrapeConfig.SetUID(k8sTypes.UID(scraperModel.ID.String()))

		configID := uuid.New()
		configItem := models.ConfigItem{
			ID:          configID.String(),
			ScraperID:   &scraperModel.ID,
			ConfigClass: "Test",
			Type:        "Config",
			ExternalID:  pq.StringArray{"merge-collision"},
		}
		Expect(DefaultContext.DB().Create(&configItem).Error).NotTo(HaveOccurred())

		now := time.Now().UTC().Truncate(time.Microsecond)
		winnerID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
		loserID := uuid.MustParse("00000000-0000-0000-0000-000000000002")
		bridgeID := uuid.MustParse("00000000-0000-0000-0000-000000000003")

		winner := dutymodels.ExternalUser{
			ID:        winnerID,
			Name:      "winner",
			Aliases:   pq.StringArray{"winner-alias"},
			UserType:  "user",
			ScraperID: scraperModel.ID,
			CreatedAt: now,
			UpdatedAt: lo.ToPtr(now),
		}
		loser := dutymodels.ExternalUser{
			ID:        loserID,
			Name:      "loser",
			Aliases:   pq.StringArray{"loser-alias"},
			UserType:  "user",
			ScraperID: scraperModel.ID,
			CreatedAt: now,
			UpdatedAt: lo.ToPtr(now),
		}
		Expect(DefaultContext.DB().Create(&winner).Error).NotTo(HaveOccurred())
		Expect(DefaultContext.DB().Create(&loser).Error).NotTo(HaveOccurred())

		source := "merge-test"
		winnerAccess := dutymodels.ConfigAccess{
			ID:             "winner-access",
			ConfigID:       configID,
			ExternalUserID: &winnerID,
			ScraperID:      &scraperModel.ID,
			Source:         &source,
			CreatedAt:      now.Add(-2 * time.Hour),
		}
		loserAccess := dutymodels.ConfigAccess{
			ID:             "loser-access",
			ConfigID:       configID,
			ExternalUserID: &loserID,
			ScraperID:      &scraperModel.ID,
			Source:         &source,
			CreatedAt:      now.Add(-time.Hour),
		}
		Expect(DefaultContext.DB().Create(&winnerAccess).Error).NotTo(HaveOccurred())
		Expect(DefaultContext.DB().Create(&loserAccess).Error).NotTo(HaveOccurred())

		winnerLogCount := 2
		loserLogCount := 3
		winnerLog := dutymodels.ConfigAccessLog{
			ConfigID:       configID,
			ExternalUserID: winnerID,
			ScraperID:      scraperModel.ID,
			CreatedAt:      now.Add(-2 * time.Hour),
			Count:          &winnerLogCount,
		}
		loserLog := dutymodels.ConfigAccessLog{
			ConfigID:       configID,
			ExternalUserID: loserID,
			ScraperID:      scraperModel.ID,
			CreatedAt:      now.Add(-30 * time.Minute),
			MFA:            true,
			Count:          &loserLogCount,
		}
		Expect(DefaultContext.DB().Create(&winnerLog).Error).NotTo(HaveOccurred())
		Expect(DefaultContext.DB().Create(&loserLog).Error).NotTo(HaveOccurred())

		defer func() {
			Expect(DefaultContext.DB().Where("config_id = ? AND scraper_id = ?", configID, scraperModel.ID).Delete(&dutymodels.ConfigAccessLog{}).Error).NotTo(HaveOccurred())
			Expect(DefaultContext.DB().Where("config_id = ? AND scraper_id = ?", configID, scraperModel.ID).Delete(&dutymodels.ConfigAccess{}).Error).NotTo(HaveOccurred())
			Expect(DefaultContext.DB().Where("scraper_id = ?", scraperModel.ID).Delete(&dutymodels.ExternalUser{}).Error).NotTo(HaveOccurred())
			Expect(DefaultContext.DB().Delete(&models.ConfigItem{}, "id = ?", configItem.ID).Error).NotTo(HaveOccurred())
			Expect(DefaultContext.DB().Delete(&scraperModel).Error).NotTo(HaveOccurred())
		}()

		tx := DefaultContext.DB().Begin()
		Expect(tx.Error).NotTo(HaveOccurred())

		tempTable := fmt.Sprintf("_merge_users_%s", strings.ReplaceAll(uuid.NewString(), "-", "_"))
		Expect(tx.Exec(fmt.Sprintf(`CREATE TEMP TABLE %s (LIKE external_users INCLUDING ALL) ON COMMIT DROP`, tempTable)).Error).NotTo(HaveOccurred())

		bridge := dutymodels.ExternalUser{
			ID:        bridgeID,
			Name:      "bridge",
			Aliases:   pq.StringArray{"winner-alias", "loser-alias"},
			UserType:  "user",
			ScraperID: scraperModel.ID,
			CreatedAt: now,
			UpdatedAt: lo.ToPtr(now),
		}
		Expect(tx.Table(tempTable).Create(&bridge).Error).NotTo(HaveOccurred())

		var merges []struct {
			LoserID  uuid.UUID `gorm:"column:loser_id"`
			WinnerID uuid.UUID `gorm:"column:winner_id"`
		}
		Expect(tx.Raw("SELECT * FROM merge_and_upsert_external_users(?)", tempTable).Scan(&merges).Error).NotTo(HaveOccurred())
		Expect(tx.Commit().Error).NotTo(HaveOccurred())

		Expect(merges).To(ContainElement(struct {
			LoserID  uuid.UUID `gorm:"column:loser_id"`
			WinnerID uuid.UUID `gorm:"column:winner_id"`
		}{
			LoserID:  loserID,
			WinnerID: winnerID,
		}))

		var activeAccesses []dutymodels.ConfigAccess
		Expect(DefaultContext.DB().
			Where("config_id = ? AND deleted_at IS NULL", configID).
			Find(&activeAccesses).Error).NotTo(HaveOccurred())
		Expect(activeAccesses).To(HaveLen(1))
		Expect(activeAccesses[0].ExternalUserID).NotTo(BeNil())
		Expect(*activeAccesses[0].ExternalUserID).To(Equal(winnerID))

		var logs []dutymodels.ConfigAccessLog
		Expect(DefaultContext.DB().
			Where("config_id = ? AND scraper_id = ?", configID, scraperModel.ID).
			Find(&logs).Error).NotTo(HaveOccurred())
		Expect(logs).To(HaveLen(1))
		Expect(logs[0].ExternalUserID).To(Equal(winnerID))
		Expect(lo.FromPtr(logs[0].Count)).To(Equal(winnerLogCount + loserLogCount))
		Expect(logs[0].CreatedAt.Equal(loserLog.CreatedAt)).To(BeTrue())

		var mergedLoser dutymodels.ExternalUser
		Expect(DefaultContext.DB().First(&mergedLoser, "id = ?", loserID).Error).NotTo(HaveOccurred())
		Expect(mergedLoser.DeletedAt).NotTo(BeNil())
	})
})
