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
	var tableCreated bool
	var canonicalID uuid.UUID
	var historicalID uuid.UUID
	var mappingIDs []uuid.UUID
	var ctx api.ScrapeContext

	BeforeAll(func() {
		ctx = api.NewScrapeContext(DefaultContext)
		exists, err := externalUserAliasTableExists(DefaultContext.DB())
		Expect(err).NotTo(HaveOccurred())
		if !exists {
			tableCreated = true
			Expect(DefaultContext.DB().Exec(`
				CREATE TABLE external_user_aliases (
					id uuid PRIMARY KEY,
					external_user_id uuid NOT NULL,
					alias text NOT NULL,
					source text NOT NULL DEFAULT 'scrape',
					created_at timestamptz NOT NULL DEFAULT now(),
					deleted_at timestamptz NULL
				)
			`).Error).NotTo(HaveOccurred())
		}

		canonicalID = uuid.New()
		historicalID = uuid.New()
		canonical := dutymodels.ExternalUser{
			ID:        canonicalID,
			Name:      "canonical mapped user",
			Aliases:   pq.StringArray{"github://canonical-user"},
			UserType:  "user",
			CreatedAt: time.Now(),
		}
		Expect(DefaultContext.DB().Create(&canonical).Error).NotTo(HaveOccurred())

		for _, alias := range []string{historicalID.String(), "github://old-user"} {
			mappingID := uuid.New()
			mappingIDs = append(mappingIDs, mappingID)
			Expect(DefaultContext.DB().Exec(`
				INSERT INTO external_user_aliases (id, external_user_id, alias, source)
				VALUES (?, ?, ?, 'merge')
			`, mappingID, canonicalID, alias).Error).NotTo(HaveOccurred())
		}
		ExternalUserCache.Flush()
		ExternalUserIDCache.Flush()
	})

	AfterAll(func() {
		ExternalUserCache.Flush()
		ExternalUserIDCache.Flush()
		Expect(DefaultContext.DB().Exec("DELETE FROM external_user_aliases WHERE id IN ?", mappingIDs).Error).NotTo(HaveOccurred())
		Expect(DefaultContext.DB().Exec("DELETE FROM external_users WHERE id = ?", canonicalID).Error).NotTo(HaveOccurred())
		if tableCreated {
			Expect(DefaultContext.DB().Exec("DROP TABLE external_user_aliases").Error).NotTo(HaveOccurred())
		}
	})

	It("warms alias and historical ID redirects into cache", func() {
		RefreshExternalUserCaches(DefaultContext)
		mappedByAlias, ok := ExternalUserCache.Get("github://old-user")
		Expect(ok).To(BeTrue())
		Expect(mappedByAlias).To(Equal(canonicalID))
		mappedByID, ok := ExternalUserIDCache.Get(historicalID.String())
		Expect(ok).To(BeTrue())
		Expect(mappedByID).To(Equal(canonicalID))
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
		ExternalUserIDCache.Delete(historicalID.String())
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
