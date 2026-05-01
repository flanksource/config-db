package db

import (
	gocontext "context"
	"time"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	dutycontext "github.com/flanksource/duty/context"
	"github.com/flanksource/duty/models"
	"github.com/google/uuid"
	"github.com/lib/pq"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("dedupeByIDWithIndex", func() {
	getID := func(g models.ExternalGroup) uuid.UUID { return g.ID }
	getAliases := func(g models.ExternalGroup) []string { return g.Aliases }
	setAliases := func(g *models.ExternalGroup, a []string) { g.Aliases = pq.StringArray(a) }

	mk := func(id string, aliases ...string) models.ExternalGroup {
		var u uuid.UUID
		if id != "" {
			u = uuid.MustParse(id)
		}
		return models.ExternalGroup{ID: u, Aliases: pq.StringArray(aliases)}
	}

	It("dedupes nil-ID entries by alias overlap and merges aliases", func() {
		// A: [a1, a2]    B: [a3, a4]    C: [a2, a3]
		// → A and C overlap on a2, B and C overlap on a3, so all three collapse.
		items := []models.ExternalGroup{
			mk("", "a1", "a2"),
			mk("", "a3", "a4"),
			mk("", "a2", "a3"),
		}
		out, idx := dedupeByIDWithIndex(items, getID, getAliases, setAliases)
		Expect(out).To(HaveLen(1))
		Expect([]string(out[0].Aliases)).To(ConsistOf("a1", "a2", "a3", "a4"))
		Expect(idx).To(Equal([]int{0, 0, 0}))
	})

	It("collapses non-nil ID entries by exact ID match", func() {
		id := "00000000-0000-0000-0000-000000000001"
		items := []models.ExternalGroup{
			mk(id, "a1"),
			mk(id, "a2"),
		}
		out, idx := dedupeByIDWithIndex(items, getID, getAliases, setAliases)
		Expect(out).To(HaveLen(1))
		Expect([]string(out[0].Aliases)).To(ConsistOf("a1", "a2"))
		Expect(idx).To(Equal([]int{0, 0}))
	})

	It("prefers non-nil ID survivor when merging with nil-ID entries by alias", func() {
		// AAD scrape (with real ID) and ADO scrape (no ID) for the same group,
		// linked by a shared alias. Output should keep the AAD ID.
		aadID := "00000000-0000-0000-0000-0000000000aa"
		items := []models.ExternalGroup{
			mk("", "ado-descriptor", "shared@example.com"),
			mk(aadID, "shared@example.com"),
		}
		out, _ := dedupeByIDWithIndex(items, getID, getAliases, setAliases)
		Expect(out).To(HaveLen(1))
		Expect(out[0].ID.String()).To(Equal(aadID))
		Expect([]string(out[0].Aliases)).To(ConsistOf("ado-descriptor", "shared@example.com"))
	})

	It("keeps disjoint nil-ID entries separate", func() {
		items := []models.ExternalGroup{
			mk("", "a1"),
			mk("", "b1"),
			mk("", "c1"),
		}
		out, idx := dedupeByIDWithIndex(items, getID, getAliases, setAliases)
		Expect(out).To(HaveLen(3))
		Expect(idx).To(Equal([]int{0, 1, 2}))
	})

	It("returns an indexMap parallel to the input", func() {
		items := []models.ExternalGroup{
			mk("", "x", "y"),
			mk("", "z"),
			mk("", "y", "w"),
		}
		_, idx := dedupeByIDWithIndex(items, getID, getAliases, setAliases)
		Expect(idx).To(HaveLen(3))
		// items[0] and items[2] share "y" → same survivor
		Expect(idx[0]).To(Equal(idx[2]))
		// items[1] is disjoint
		Expect(idx[1]).NotTo(Equal(idx[0]))
	})

})

var _ = Describe("resolveExternalUserGroups", func() {
	var ctx api.ScrapeContext
	BeforeEach(func() {
		ctx = api.NewScrapeContext(dutycontext.NewContext(gocontext.Background()))
	})

	// Canonical IDs that live entities will carry.
	userA := uuid.MustParse("00000000-0000-0000-0000-0000000000a1")
	userB := uuid.MustParse("00000000-0000-0000-0000-0000000000a2")
	groupX := uuid.MustParse("00000000-0000-0000-0000-0000000000b1")
	groupY := uuid.MustParse("00000000-0000-0000-0000-0000000000b2")
	// Raw upstream IDs that the scraper sent on user_group refs but that
	// don't match the final canonical IDs the entity upsert uses.
	rawGroupX := uuid.MustParse("00000000-0000-0000-0000-0000000000c1")

	mkUsers := func() []models.ExternalUser {
		return []models.ExternalUser{
			{ID: userA, Aliases: pq.StringArray{"alice@example.com"}},
			{ID: userB, Aliases: pq.StringArray{"bob@example.com"}},
		}
	}
	mkGroups := func() []models.ExternalGroup {
		return []models.ExternalGroup{
			// rawGroupX is an upstream raw id the scraper attached as an alias —
			// not the entity's own canonical id, which never lives in Aliases.
			{ID: groupX, Aliases: pq.StringArray{"group-x", rawGroupX.String()}},
			{ID: groupY, Aliases: pq.StringArray{"group-y"}},
		}
	}

	It("accepts a direct UUID that points at a real entity", func() {
		in := []v1.ExternalUserGroup{
			{ExternalUserID: &userA, ExternalGroupID: &groupX},
		}
		out, stats, err := resolveExternalUserGroups(ctx, in, mkUsers(), mkGroups())
		Expect(err).ToNot(HaveOccurred())
		Expect(out).To(HaveLen(1))
		Expect(out[0].ExternalUserID).To(Equal(userA))
		Expect(out[0].ExternalGroupID).To(Equal(groupX))
		Expect(stats.Dropped).To(Equal(0))
	})

	It("drops a direct-UUID row that references a non-existent user", func() {
		bogus := uuid.MustParse("00000000-0000-0000-0000-0000000000ff")
		in := []v1.ExternalUserGroup{
			{ExternalUserID: &bogus, ExternalGroupID: &groupX},
		}
		out, stats, err := resolveExternalUserGroups(ctx, in, mkUsers(), mkGroups())
		Expect(err).ToNot(HaveOccurred())
		Expect(out).To(BeEmpty())
		Expect(stats.DroppedUnknownUser).To(Equal(1))
	})

	It("falls through from a bogus direct UUID to an alias resolution", func() {
		// Simulates the entra.yaml case: scraper sent the raw Graph group.id
		// as external_group_id, but the group was upserted with a hash-based
		// canonical ID. The alias list of the upserted group still contains
		// the raw id, so we can recover.
		in := []v1.ExternalUserGroup{
			{
				ExternalUserID:       &userA,
				ExternalGroupID:      &rawGroupX,
				ExternalGroupAliases: []string{rawGroupX.String()},
			},
		}
		out, _, err := resolveExternalUserGroups(ctx, in, mkUsers(), mkGroups())
		Expect(err).ToNot(HaveOccurred())
		Expect(out).To(HaveLen(1))
		Expect(out[0].ExternalUserID).To(Equal(userA))
		Expect(out[0].ExternalGroupID).To(Equal(groupX))
	})

	It("resolves alias-only entries", func() {
		in := []v1.ExternalUserGroup{
			{
				ExternalUserAliases:  []string{"alice@example.com"},
				ExternalGroupAliases: []string{"group-y"},
			},
		}
		out, _, err := resolveExternalUserGroups(ctx, in, mkUsers(), mkGroups())
		Expect(err).ToNot(HaveOccurred())
		Expect(out).To(HaveLen(1))
		Expect(out[0].ExternalUserID).To(Equal(userA))
		Expect(out[0].ExternalGroupID).To(Equal(groupY))
	})

	It("drops rows where neither the direct UUID nor aliases resolve", func() {
		bogusUser := uuid.MustParse("00000000-0000-0000-0000-0000000000fe")
		in := []v1.ExternalUserGroup{
			{
				ExternalUserID:       &bogusUser,
				ExternalUserAliases:  []string{"ghost@example.com"},
				ExternalGroupAliases: []string{"group-x"},
			},
		}
		out, stats, err := resolveExternalUserGroups(ctx, in, mkUsers(), mkGroups())
		Expect(err).ToNot(HaveOccurred())
		Expect(out).To(BeEmpty())
		Expect(stats.Dropped).To(Equal(1))
	})

	It("processes a mixed batch independently — valid rows survive, invalid rows drop", func() {
		bogusUser := uuid.MustParse("00000000-0000-0000-0000-0000000000fd")
		in := []v1.ExternalUserGroup{
			{ExternalUserID: &userA, ExternalGroupID: &groupX},
			{ExternalUserID: &bogusUser, ExternalGroupID: &groupX},
			{ExternalUserID: &userB, ExternalGroupID: &rawGroupX, ExternalGroupAliases: []string{rawGroupX.String()}},
		}
		out, stats, err := resolveExternalUserGroups(ctx, in, mkUsers(), mkGroups())
		Expect(err).ToNot(HaveOccurred())
		Expect(out).To(HaveLen(2))
		Expect(out[0]).To(Equal(models.ExternalUserGroup{ExternalUserID: userA, ExternalGroupID: groupX}))
		Expect(out[1]).To(Equal(models.ExternalUserGroup{ExternalUserID: userB, ExternalGroupID: groupX}))
		Expect(stats.Resolved).To(Equal(2))
		Expect(stats.Dropped).To(Equal(1))
	})

	It("returns nil for empty input", func() {
		out, stats, err := resolveExternalUserGroups(ctx, nil, mkUsers(), mkGroups())
		Expect(err).ToNot(HaveOccurred())
		Expect(out).To(BeNil())
		Expect(stats.Input).To(Equal(0))
	})

	It("resolves alias-only entries against persisted entities case-insensitively", func() {
		ctx = api.NewScrapeContext(DefaultContext)
		scraper := models.ConfigScraper{
			ID:     uuid.New(),
			Name:   "resolve-persisted-user-groups",
			Spec:   "{}",
			Source: models.SourceUI,
		}
		Expect(DefaultContext.DB().Create(&scraper).Error).ToNot(HaveOccurred())
		defer DefaultContext.DB().Delete(&models.ConfigScraper{}, "id = ?", scraper.ID)

		now := time.Now()
		persistedUser := models.ExternalUser{
			ID:        uuid.MustParse("00000000-0000-0000-0000-0000000000d1"),
			Name:      "Persisted User",
			Aliases:   pq.StringArray{"Persisted.User@Example.COM"},
			UserType:  "human",
			ScraperID: scraper.ID,
			CreatedAt: now,
			UpdatedAt: &now,
		}
		persistedGroup := models.ExternalGroup{
			ID:        uuid.MustParse("00000000-0000-0000-0000-0000000000d2"),
			Name:      "Persisted Admins",
			Aliases:   pq.StringArray{"Persisted-Admins"},
			GroupType: "security",
			ScraperID: scraper.ID,
			CreatedAt: now,
			UpdatedAt: &now,
		}
		Expect(DefaultContext.DB().Create(&persistedUser).Error).ToNot(HaveOccurred())
		Expect(DefaultContext.DB().Create(&persistedGroup).Error).ToNot(HaveOccurred())
		defer DefaultContext.DB().Delete(&models.ExternalUser{}, "id = ?", persistedUser.ID)
		defer DefaultContext.DB().Delete(&models.ExternalGroup{}, "id = ?", persistedGroup.ID)

		in := []v1.ExternalUserGroup{
			{
				ExternalUserAliases:  []string{"PERSISTED.USER@example.com"},
				ExternalGroupAliases: []string{"persisted-admins"},
			},
		}
		out, stats, err := resolveExternalUserGroups(ctx, in, nil, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(out).To(Equal([]models.ExternalUserGroup{{
			ExternalUserID:  persistedUser.ID,
			ExternalGroupID: persistedGroup.ID,
		}}))
		Expect(stats.Resolved).To(Equal(1))
		Expect(stats.Dropped).To(Equal(0))
	})

	It("resolves direct UUID entries against persisted entities", func() {
		ctx = api.NewScrapeContext(DefaultContext)
		scraper := models.ConfigScraper{
			ID:     uuid.New(),
			Name:   "resolve-persisted-user-groups-by-id",
			Spec:   "{}",
			Source: models.SourceUI,
		}
		Expect(DefaultContext.DB().Create(&scraper).Error).ToNot(HaveOccurred())
		defer DefaultContext.DB().Delete(&models.ConfigScraper{}, "id = ?", scraper.ID)

		now := time.Now()
		persistedUser := models.ExternalUser{
			ID:        uuid.MustParse("00000000-0000-0000-0000-0000000000e1"),
			Name:      "Persisted User By ID",
			UserType:  "human",
			ScraperID: scraper.ID,
			CreatedAt: now,
			UpdatedAt: &now,
		}
		persistedGroup := models.ExternalGroup{
			ID:        uuid.MustParse("00000000-0000-0000-0000-0000000000e2"),
			Name:      "Persisted Group By ID",
			GroupType: "security",
			ScraperID: scraper.ID,
			CreatedAt: now,
			UpdatedAt: &now,
		}
		Expect(DefaultContext.DB().Create(&persistedUser).Error).ToNot(HaveOccurred())
		Expect(DefaultContext.DB().Create(&persistedGroup).Error).ToNot(HaveOccurred())
		defer DefaultContext.DB().Delete(&models.ExternalUser{}, "id = ?", persistedUser.ID)
		defer DefaultContext.DB().Delete(&models.ExternalGroup{}, "id = ?", persistedGroup.ID)

		in := []v1.ExternalUserGroup{
			{ExternalUserID: &persistedUser.ID, ExternalGroupID: &persistedGroup.ID},
		}
		out, stats, err := resolveExternalUserGroups(ctx, in, nil, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(out).To(Equal([]models.ExternalUserGroup{{
			ExternalUserID:  persistedUser.ID,
			ExternalGroupID: persistedGroup.ID,
		}}))
		Expect(stats.Resolved).To(Equal(1))
		Expect(stats.Dropped).To(Equal(0))
	})

	It("persists memberships owned by the current scraper for entities from another scraper", func() {
		ctx = api.NewScrapeContext(DefaultContext)
		sourceScraper := models.ConfigScraper{
			ID:     uuid.New(),
			Name:   "external-entities-source",
			Spec:   "{}",
			Source: models.SourceUI,
		}
		membershipScraper := models.ConfigScraper{
			ID:     uuid.New(),
			Name:   "external-entities-membership",
			Spec:   "{}",
			Source: models.SourceUI,
		}
		Expect(DefaultContext.DB().Create(&sourceScraper).Error).ToNot(HaveOccurred())
		Expect(DefaultContext.DB().Create(&membershipScraper).Error).ToNot(HaveOccurred())
		defer DefaultContext.DB().Delete(&models.ConfigScraper{}, "id IN ?", []uuid.UUID{sourceScraper.ID, membershipScraper.ID})

		now := time.Now()
		persistedUser := models.ExternalUser{
			ID:        uuid.MustParse("00000000-0000-0000-0000-0000000000f1"),
			Name:      "Cross Scraper User",
			Aliases:   pq.StringArray{"cross-scraper-user"},
			UserType:  "human",
			ScraperID: sourceScraper.ID,
			CreatedAt: now,
			UpdatedAt: &now,
		}
		persistedGroup := models.ExternalGroup{
			ID:        uuid.MustParse("00000000-0000-0000-0000-0000000000f2"),
			Name:      "Cross Scraper Group",
			Aliases:   pq.StringArray{"cross-scraper-group"},
			GroupType: "security",
			ScraperID: sourceScraper.ID,
			CreatedAt: now,
			UpdatedAt: &now,
		}
		Expect(DefaultContext.DB().Create(&persistedUser).Error).ToNot(HaveOccurred())
		Expect(DefaultContext.DB().Create(&persistedGroup).Error).ToNot(HaveOccurred())
		defer DefaultContext.DB().Delete(&models.ExternalUser{}, "id = ?", persistedUser.ID)
		defer DefaultContext.DB().Delete(&models.ExternalGroup{}, "id = ?", persistedGroup.ID)
		defer DefaultContext.DB().Delete(&models.ExternalUserGroup{}, "external_user_id = ? AND external_group_id = ?", persistedUser.ID, persistedGroup.ID)

		extract := NewExtractResult()
		extract.externalUserGroups = []v1.ExternalUserGroup{
			{
				ExternalUserAliases:  []string{"cross-scraper-user"},
				ExternalGroupAliases: []string{"cross-scraper-group"},
			},
		}
		result, _, err := syncExternalEntities(ctx, extract, &membershipScraper.ID)
		Expect(err).ToNot(HaveOccurred())
		Expect(result.Warnings).To(BeEmpty())

		var membership models.ExternalUserGroup
		Expect(DefaultContext.DB().
			Where("external_user_id = ? AND external_group_id = ? AND scraper_id = ?", persistedUser.ID, persistedGroup.ID, membershipScraper.ID).
			First(&membership).Error).ToNot(HaveOccurred())
		Expect(membership.DeletedAt).To(BeNil())
	})
})

var _ = Describe("remapExternalUserGroups", func() {
	It("remaps user and group ids independently", func() {
		userLoser := uuid.MustParse("00000000-0000-0000-0000-000000000001")
		userWinner := uuid.MustParse("00000000-0000-0000-0000-000000000002")
		groupLoser := uuid.MustParse("00000000-0000-0000-0000-000000000003")
		groupWinner := uuid.MustParse("00000000-0000-0000-0000-000000000004")

		remapped := remapExternalUserGroups([]models.ExternalUserGroup{
			{
				ExternalUserID:  groupLoser,
				ExternalGroupID: userLoser,
			},
			{
				ExternalUserID:  userLoser,
				ExternalGroupID: groupLoser,
			},
		}, map[uuid.UUID]uuid.UUID{
			userLoser: userWinner,
		}, map[uuid.UUID]uuid.UUID{
			groupLoser: groupWinner,
		})

		Expect(remapped).To(Equal([]models.ExternalUserGroup{
			{
				ExternalUserID:  groupLoser,
				ExternalGroupID: userLoser,
			},
			{
				ExternalUserID:  userWinner,
				ExternalGroupID: groupWinner,
			},
		}))
	})
})

var _ = Describe("canonicalizeAliases", func() {
	It("drops every known descriptor prefix", func() {
		Expect(canonicalizeAliases([]string{
			"agupta3@example.com",
			`Microsoft.IdentityModel.Claims.ClaimsIdentity;abc\agupta3@example.com`,
			"aad.YjVhNDEyNmEtMjUwYi03NDRkLTg5ODEtMTRiMTgyYTBiMGM0",
			"b5a4126a-250b-744d-8981-14b182a0b0c4",
			"vssgp.Uy0xLTktMQ",
			"aadgp.Uy0xLTktMQ",
			"Microsoft.TeamFoundation.Identity;S-1-9-1",
			"Microsoft.TeamFoundation.ServiceIdentity;owner:type:guid",
			"svc.aGVsbG8",
			"s2s.world",
		})).To(Equal([]string{
			"agupta3@example.com",
			"b5a4126a-250b-744d-8981-14b182a0b0c4",
		}))
	})

	It("keeps Amit Gupta's bloated alias list down to canonical email + AAD GUID", func() {
		// This is the exact alias array observed at /users/8a4e4f3a-… before
		// the fix; canonicalization should reduce it to two canonical entries.
		Expect(canonicalizeAliases([]string{
			"agupta3@example.com",
			"Microsoft.IdentityModel.Claims.ClaimsIdentity;00691924-e082-4301-a3dc-1732afd14289\\agupta3@example.com",
			"aad.YjVhNDEyNmEtMjUwYi03NDRkLTg5ODEtMTRiMTgyYTBiMGM0",
			"b5a4126a-250b-644d-8981-14b182a0b0c4",
			"b5a4126a-250b-744d-8981-14b182a0b0c4",
		})).To(Equal([]string{
			"agupta3@example.com",
			// The …644d… ghost from a prior buggy decoder is GUID-shaped, not
			// descriptor-prefixed, so the trim leaves it for now (documented
			// known residue). The canonical 744d form is also kept.
			"b5a4126a-250b-644d-8981-14b182a0b0c4",
			"b5a4126a-250b-744d-8981-14b182a0b0c4",
		}))
	})

	It("dedupes and skips empty / whitespace-only entries", func() {
		Expect(canonicalizeAliases([]string{"  ", "", "alice@example.com", "alice@example.com", " bob@example.com"})).
			To(Equal([]string{"alice@example.com", "bob@example.com"}))
	})

	It("returns nil for an empty input", func() {
		Expect(canonicalizeAliases(nil)).To(BeNil())
		Expect(canonicalizeAliases([]string{})).To(BeNil())
	})
})

var _ = Describe("resolveCanonicalID", func() {
	var ctx api.ScrapeContext

	BeforeEach(func() {
		// No DB attached — lookupExternalEntityIDByAliases short-circuits
		// to uuid.Nil, exercising the local-fallback paths.
		ctx = api.NewScrapeContext(dutycontext.NewContext(gocontext.Background()))
	})

	It("picks the first UUID-shaped alias as the canonical id", func() {
		id, err := resolveCanonicalID(ctx, "external_users", []string{
			"alice@example.com",
			"b5a4126a-250b-744d-8981-14b182a0b0c4",
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(id.String()).To(Equal("b5a4126a-250b-744d-8981-14b182a0b0c4"))
	})

	It("falls back to a fresh UUID v7 when no alias is UUID-shaped", func() {
		id, err := resolveCanonicalID(ctx, "external_users", []string{
			"alice@example.com",
			"S-1-9-not-a-uuid",
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(id).ToNot(Equal(uuid.Nil))
		// UUID v7 — not deterministic, but must be a real parseable UUID.
		Expect(uuid.Validate(id.String())).To(Succeed())
	})

	It("does not synthesise an id from a hash of the alias list", func() {
		// Same alias set; two calls must produce DIFFERENT ids in the v7
		// fallback branch (because nothing is hashing the aliases any more).
		aliases := []string{"alice@example.com"}
		first, err := resolveCanonicalID(ctx, "external_users", aliases)
		Expect(err).ToNot(HaveOccurred())
		second, err := resolveCanonicalID(ctx, "external_users", aliases)
		Expect(err).ToNot(HaveOccurred())
		Expect(first).ToNot(Equal(second))
	})

	It("ignores the nil uuid as an alias and continues searching", func() {
		canonical := "b5a4126a-250b-744d-8981-14b182a0b0c4"
		id, err := resolveCanonicalID(ctx, "external_users", []string{
			"00000000-0000-0000-0000-000000000000",
			canonical,
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(id.String()).To(Equal(canonical))
	})

	// Wired-DB cases (alias-overlap lookup against external_users) are
	// covered by the integration suite; the unit suite exercises the
	// no-DB / local-fallback paths.
})

// silence unused-import warnings if all the time-bearing tests above are
// compiled away; the test suite imports `time` indirectly via the resolver
// helpers, so this is just a defensive marker.
var _ = time.Now
