package db

import (
	"fmt"
	"time"

	"github.com/flanksource/config-db/api"
	dutymodels "github.com/flanksource/duty/models"
	"github.com/google/uuid"
	"github.com/lib/pq"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("external user alias mapping resolution", Ordered, func() {
	var canonicalID uuid.UUID
	var historicalID uuid.UUID
	var staleAlias string
	var ctx api.ScrapeContext

	BeforeAll(func() {
		ctx = api.NewScrapeContext(DefaultContext)
		canonicalID = uuid.New()
		historicalID = uuid.New()
		canonical := dutymodels.ExternalUser{
			ID:   canonicalID,
			Name: "canonical mapped user",
			Aliases: pq.StringArray{
				"github://canonical-user",
				"github://old-user",
				historicalID.String(),
			},
			UserType:  "user",
			CreatedAt: time.Now(),
		}
		Expect(DefaultContext.DB().Create(&canonical).Error).NotTo(HaveOccurred())

		for _, alias := range []string{historicalID.String(), "github://old-user"} {
			var existing struct{ ID uuid.UUID }
			Expect(DefaultContext.DB().Table("external_user_aliases").
				Select("id").
				Where("external_user_id = ? AND alias = ? AND deleted_at IS NULL", canonicalID, alias).
				Take(&existing).Error).NotTo(HaveOccurred())
			Expect(existing.ID).NotTo(Equal(uuid.Nil))
		}

		// Simulate a stale derived-index row. Resolution must ignore it because
		// the alias is not present in external_users.aliases.
		staleAlias = "github://stale-index-only"
		staleMappingID := uuid.New()
		Expect(DefaultContext.DB().Exec(`
			INSERT INTO external_user_aliases (id, external_user_id, alias, source)
			VALUES (?, ?, ?, 'merge')
		`, staleMappingID, canonicalID, staleAlias).Error).NotTo(HaveOccurred())

		ExternalUserCache.Flush()
		ExternalUserIDCache.Flush()
	})

	AfterAll(func() {
		ExternalUserCache.Flush()
		ExternalUserIDCache.Flush()
		Expect(DefaultContext.DB().Exec(
			"DELETE FROM external_user_aliases WHERE external_user_id = ?", canonicalID,
		).Error).NotTo(HaveOccurred())
		Expect(DefaultContext.DB().Exec("DELETE FROM external_users WHERE id = ?", canonicalID).Error).NotTo(HaveOccurred())
	})

	It("warms alias and historical ID redirects into cache", func() {
		Expect(RefreshExternalUserCaches(DefaultContext)).To(Succeed())
		mappedByAlias, ok := ExternalUserCache.Get("github://old-user")
		Expect(ok).To(BeTrue())
		Expect(mappedByAlias).To(Equal(canonicalID))
		mappedByID, ok := ExternalUserIDCache.Get(historicalID.String())
		Expect(ok).To(BeTrue())
		Expect(mappedByID).To(Equal(canonicalID))
	})

	It("fails refresh without clearing the previous cache when the index is unavailable", func() {
		Expect(RefreshExternalUserCaches(DefaultContext)).To(Succeed())
		Expect(DefaultContext.DB().Exec(
			"ALTER TABLE external_user_aliases RENAME TO external_user_aliases_unavailable",
		).Error).NotTo(HaveOccurred())
		DeferCleanup(func() {
			Expect(DefaultContext.DB().Exec(
				"ALTER TABLE external_user_aliases_unavailable RENAME TO external_user_aliases",
			).Error).NotTo(HaveOccurred())
		})

		Expect(RefreshExternalUserCaches(DefaultContext)).ToNot(Succeed())
		mapped, ok := ExternalUserCache.Get("github://old-user")
		Expect(ok).To(BeTrue())
		Expect(mapped).To(Equal(canonicalID))
	})

	It("ignores a lookup-index row missing from the source aliases array", func() {
		Expect(RefreshExternalUserCaches(DefaultContext)).To(Succeed())
		_, ok := ExternalUserCache.Get(staleAlias)
		Expect(ok).To(BeFalse())

		candidateID := uuid.New()
		user := dutymodels.ExternalUser{
			ID:      candidateID,
			Name:    "stale index candidate",
			Aliases: pq.StringArray{staleAlias},
		}
		Expect(applyExternalUserAliasMapping(ctx, &user)).To(Succeed())
		Expect(user.ID).To(Equal(candidateID))
	})

	It("keeps lookup misses cache-only until the next refresh", func() {
		lateAlias := "github://added-after-refresh"
		Expect(DefaultContext.DB().Exec(`
			UPDATE external_users
			SET aliases = array_append(COALESCE(aliases, '{}'::text[]), ?)
			WHERE id = ?
		`, lateAlias, canonicalID).Error).NotTo(HaveOccurred())

		candidateID := uuid.New()
		beforeRefresh := dutymodels.ExternalUser{ID: candidateID, Aliases: pq.StringArray{lateAlias}}
		Expect(applyExternalUserAliasMapping(ctx, &beforeRefresh)).To(Succeed())
		Expect(beforeRefresh.ID).To(Equal(candidateID))

		Expect(RefreshExternalUserCaches(DefaultContext)).To(Succeed())
		afterRefresh := dutymodels.ExternalUser{ID: candidateID, Aliases: pq.StringArray{lateAlias}}
		Expect(applyExternalUserAliasMapping(ctx, &afterRefresh)).To(Succeed())
		Expect(afterRefresh.ID).To(Equal(canonicalID))
	})

	It("rewrites a scraper-provided historical ID to the canonical user", func() {
		email := "Should.Not.Override@example.com"
		user := dutymodels.ExternalUser{
			ID:      historicalID,
			Name:    "old external identity",
			Aliases: pq.StringArray{"github://old-user"},
			Email:   &email,
		}

		Expect(applyExternalUserAliasMapping(ctx, &user)).To(Succeed())
		Expect(user.ID).To(Equal(canonicalID))
		Expect([]string(user.Aliases)).To(ContainElements("github://old-user", historicalID.String()))
		Expect([]string(user.Aliases)).NotTo(ContainElement("should.not.override@example.com"))
	})

	It("resolves direct references to a historical user ID", func() {
		Expect(RefreshExternalUserCaches(DefaultContext)).To(Succeed())
		resolved, err := findExternalEntityByID[dutymodels.ExternalUser](ctx, historicalID)
		Expect(err).NotTo(HaveOccurred())
		Expect(resolved).NotTo(BeNil(), fmt.Sprintf("historical ID %s should resolve", historicalID))
		Expect(*resolved).To(Equal(canonicalID))
	})

	It("uses email as an alias only when stronger keys have no mapping", func() {
		email := "  New.User@Example.com "
		user := dutymodels.ExternalUser{ID: uuid.New(), Name: "new user", Email: &email}
		Expect(applyExternalUserAliasMapping(ctx, &user)).To(Succeed())
		Expect([]string(user.Aliases)).To(Equal([]string{"new.user@example.com"}))
	})
})
