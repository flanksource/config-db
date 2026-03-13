package db

import (
	"github.com/flanksource/duty/models"
	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

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
