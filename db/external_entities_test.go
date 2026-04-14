package db

import (
	gocontext "context"

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
		out := resolveExternalUserGroups(ctx, in, mkUsers(), mkGroups())
		Expect(out).To(HaveLen(1))
		Expect(out[0].ExternalUserID).To(Equal(userA))
		Expect(out[0].ExternalGroupID).To(Equal(groupX))
	})

	It("drops a direct-UUID row that references a non-existent user", func() {
		bogus := uuid.MustParse("00000000-0000-0000-0000-0000000000ff")
		in := []v1.ExternalUserGroup{
			{ExternalUserID: &bogus, ExternalGroupID: &groupX},
		}
		out := resolveExternalUserGroups(ctx, in, mkUsers(), mkGroups())
		Expect(out).To(BeEmpty())
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
		out := resolveExternalUserGroups(ctx, in, mkUsers(), mkGroups())
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
		out := resolveExternalUserGroups(ctx, in, mkUsers(), mkGroups())
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
		out := resolveExternalUserGroups(ctx, in, mkUsers(), mkGroups())
		Expect(out).To(BeEmpty())
	})

	It("processes a mixed batch independently — valid rows survive, invalid rows drop", func() {
		bogusUser := uuid.MustParse("00000000-0000-0000-0000-0000000000fd")
		in := []v1.ExternalUserGroup{
			{ExternalUserID: &userA, ExternalGroupID: &groupX},
			{ExternalUserID: &bogusUser, ExternalGroupID: &groupX},
			{ExternalUserID: &userB, ExternalGroupID: &rawGroupX, ExternalGroupAliases: []string{rawGroupX.String()}},
		}
		out := resolveExternalUserGroups(ctx, in, mkUsers(), mkGroups())
		Expect(out).To(HaveLen(2))
		Expect(out[0]).To(Equal(models.ExternalUserGroup{ExternalUserID: userA, ExternalGroupID: groupX}))
		Expect(out[1]).To(Equal(models.ExternalUserGroup{ExternalUserID: userB, ExternalGroupID: groupX}))
	})

	It("returns nil for empty input", func() {
		Expect(resolveExternalUserGroups(ctx, nil, mkUsers(), mkGroups())).To(BeNil())
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
