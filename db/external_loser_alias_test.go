package db

import (
	"fmt"
	"strings"
	"time"

	"github.com/flanksource/config-db/api"
	dutymodels "github.com/flanksource/duty/models"
	"github.com/google/uuid"
	"github.com/lib/pq"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
)

var _ = Describe("merge_and_upsert_external_users loser id alias migration", func() {
	It("folds the loser id into the winner's aliases and exposes it via findExternalEntityByID", func() {
		ctx := api.NewScrapeContext(DefaultContext)

		now := time.Now().UTC().Truncate(time.Microsecond)
		winnerID := uuid.New()
		loserID := uuid.New()
		bridgeID := uuid.New()
		sharedAlias := fmt.Sprintf("loser-alias-%s", uuid.NewString()[:8])
		winnerAlias := fmt.Sprintf("winner-alias-%s", uuid.NewString()[:8])

		winner := dutymodels.ExternalUser{
			ID:        winnerID,
			Name:      "winner",
			Aliases:   pq.StringArray{winnerAlias},
			UserType:  "user",
			CreatedAt: now,
			UpdatedAt: lo.ToPtr(now),
		}
		loser := dutymodels.ExternalUser{
			ID:        loserID,
			Name:      "loser",
			Aliases:   pq.StringArray{sharedAlias},
			UserType:  "user",
			CreatedAt: now,
			UpdatedAt: lo.ToPtr(now),
		}
		Expect(DefaultContext.DB().Create(&winner).Error).NotTo(HaveOccurred())
		Expect(DefaultContext.DB().Create(&loser).Error).NotTo(HaveOccurred())

		defer func() {
			DefaultContext.DB().Unscoped().Delete(&dutymodels.ExternalUser{}, "id IN ?", []uuid.UUID{winnerID, loserID, bridgeID})
		}()

		tx := DefaultContext.DB().Begin()
		Expect(tx.Error).NotTo(HaveOccurred())

		tempTable := fmt.Sprintf("_merge_users_%s", strings.ReplaceAll(uuid.NewString(), "-", "_"))
		Expect(tx.Exec(fmt.Sprintf(
			`CREATE TEMP TABLE %s (LIKE external_users INCLUDING ALL) ON COMMIT DROP`,
			tempTable,
		)).Error).NotTo(HaveOccurred())

		bridge := dutymodels.ExternalUser{
			ID:        bridgeID,
			Name:      "bridge",
			Aliases:   pq.StringArray{winnerAlias, sharedAlias},
			UserType:  "user",
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

		// All three rows form one connected component, so the merges all share
		// the same survivor; everything else is a loser.
		Expect(merges).NotTo(BeEmpty(), "expected at least one merge")
		survivorID := merges[0].WinnerID
		var losers []uuid.UUID
		for _, m := range merges {
			Expect(m.WinnerID).To(Equal(survivorID), "all merges must share one survivor")
			losers = append(losers, m.LoserID)
		}

		var survivor dutymodels.ExternalUser
		Expect(DefaultContext.DB().First(&survivor, "id = ?", survivorID).Error).NotTo(HaveOccurred())
		Expect(survivor.DeletedAt).To(BeNil())
		for _, lid := range losers {
			Expect([]string(survivor.Aliases)).To(
				ContainElement(lid.String()),
				"loser id %s must appear in survivor.aliases after merge", lid,
			)
		}

		// And the helper should resolve the loser id back to the survivor.
		// Bypass the id-cache by deleting any stale entry for the loser.
		for _, lid := range losers {
			ExternalUserIDCache.Delete(lid.String())
			resolved, err := findExternalEntityByID[dutymodels.ExternalUser](ctx, lid)
			Expect(err).NotTo(HaveOccurred())
			Expect(resolved).NotTo(BeNil(), "findExternalEntityByID(loser=%s) must return a survivor", lid)
			Expect(*resolved).To(Equal(survivorID))
		}
	})
})
