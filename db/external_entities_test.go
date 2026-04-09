package db

import (
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
