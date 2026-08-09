package github

import (
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty/models"
	gogithub "github.com/google/go-github/v73/github"
	"github.com/lib/pq"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("GitHub organization RBAC", func() {
	newUser := func(id int64, login string) *gogithub.User {
		return &gogithub.User{ID: gogithub.Ptr(id), Login: gogithub.Ptr(login), Type: gogithub.Ptr("User")}
	}

	Describe("team hierarchy", func() {
		platform := &gogithub.Team{ID: gogithub.Ptr[int64](201), Name: gogithub.Ptr("Platform"), Slug: gogithub.Ptr("platform")}
		developers := &gogithub.Team{
			ID:     gogithub.Ptr[int64](202),
			Name:   gogithub.Ptr("Developers"),
			Slug:   gogithub.Ptr("developers"),
			Parent: &gogithub.Team{ID: gogithub.Ptr[int64](201)},
		}
		onCall := &gogithub.Team{
			ID:     gogithub.Ptr[int64](203),
			Name:   gogithub.Ptr("On Call"),
			Slug:   gogithub.Ptr("on-call"),
			Parent: &gogithub.Team{ID: gogithub.Ptr[int64](202)},
		}

		It("gives every descendant team the members it inherits from its ancestors", func() {
			effective, err := effectiveTeamMembers([]organizationTeam{
				{Team: platform, Members: []*gogithub.User{newUser(101, "alice")}},
				{Team: developers, Members: []*gogithub.User{newUser(102, "bob")}},
				{Team: onCall, Members: []*gogithub.User{newUser(103, "carol"), newUser(101, "alice")}},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(logins(effective[201])).To(Equal([]string{"alice"}))
			Expect(logins(effective[202])).To(Equal([]string{"bob", "alice"}))
			Expect(logins(effective[203])).To(Equal([]string{"carol", "alice", "bob"}))
		})

		It("rejects a parent cycle instead of looping", func() {
			_, err := effectiveTeamMembers([]organizationTeam{
				{
					Team: &gogithub.Team{
						ID:     gogithub.Ptr[int64](201),
						Name:   gogithub.Ptr("Platform"),
						Parent: &gogithub.Team{ID: gogithub.Ptr[int64](202)},
					},
				},
				{
					Team: &gogithub.Team{
						ID:     gogithub.Ptr[int64](202),
						Name:   gogithub.Ptr("Developers"),
						Parent: &gogithub.Team{ID: gogithub.Ptr[int64](201)},
					},
				},
			})

			Expect(err).To(MatchError(ContainSubstring("parent cycle")))
		})
	})

	Describe("a team that grants access to nobody", func() {
		grantingTeam := func(members []*gogithub.User, repos []*gogithub.Repository) organizationRBACInput {
			return organizationRBACInput{
				Owner: "acme",
				Teams: []organizationTeam{{
					Team: &gogithub.Team{
						ID:   gogithub.Ptr[int64](201),
						Name: gogithub.Ptr("Developers"),
						Slug: gogithub.Ptr("developers"),
					},
					Members: members,
					Repos:   repos,
				}},
				Repositories: organizationRepositories("acme", []v1.GitHubRepository{{Owner: "acme", Repo: "telemetry"}}),
			}
		}
		telemetry := []*gogithub.Repository{{Name: gogithub.Ptr("telemetry"), RoleName: gogithub.Ptr("push")}}

		It("warns, because the permissions land while no membership does", func() {
			// The grants still reach config_access, so the scrape looks successful; without a
			// membership row every one of them belongs to a principal no per-user view reaches.
			result, err := buildOrganizationRBAC(grantingTeam(nil, telemetry))

			Expect(err).NotTo(HaveOccurred())
			Expect(result.Warnings).To(ConsistOf(ContainSubstring(`team "Developers" grants 1 repository(s) but reports no members`)))
			Expect(result.Warnings[0]).To(ContainSubstring("Members: read"))
			Expect(result.Access).ToNot(BeEmpty())
			Expect(result.UserGroups).To(BeEmpty())
		})

		It("stays quiet for an empty team that grants nothing", func() {
			// An empty team holding no repositories withholds no access from anyone, and
			// warning about it would bury the case that does.
			result, err := buildOrganizationRBAC(grantingTeam(nil, nil))

			Expect(err).NotTo(HaveOccurred())
			Expect(result.Warnings).To(BeEmpty())
		})

		It("stays quiet when every repository it grants is outside the scrape", func() {
			// No ConfigAccess record is emitted for a repository the scraper never
			// collects, so there is no unattributed grant to warn about.
			result, err := buildOrganizationRBAC(grantingTeam(nil,
				[]*gogithub.Repository{{Name: gogithub.Ptr("unscraped"), RoleName: gogithub.Ptr("push")}}))

			Expect(err).NotTo(HaveOccurred())
			Expect(result.Warnings).To(BeEmpty())
			Expect(result.Access).To(BeEmpty())
		})

		It("counts only the repositories that reach config_access", func() {
			result, err := buildOrganizationRBAC(grantingTeam(nil, []*gogithub.Repository{
				{Name: gogithub.Ptr("telemetry"), RoleName: gogithub.Ptr("push")},
				{Name: gogithub.Ptr("unscraped"), RoleName: gogithub.Ptr("admin")},
			}))

			Expect(err).NotTo(HaveOccurred())
			Expect(result.Warnings).To(ConsistOf(ContainSubstring(`team "Developers" grants 1 repository(s) but reports no members`)))
		})

		It("stays quiet when the members came through", func() {
			result, err := buildOrganizationRBAC(grantingTeam([]*gogithub.User{newUser(102, "bob")}, telemetry))

			Expect(err).NotTo(HaveOccurred())
			Expect(result.Warnings).To(BeEmpty())
			Expect(result.UserGroups).ToNot(BeEmpty())
		})
	})

	It("maps members to organization roles and teams to repository grants", func() {
		result, err := buildOrganizationRBAC(organizationRBACInput{
			Owner: "acme",
			Members: []organizationMember{
				{User: newUser(101, "alice"), Role: "admin"},
				{User: newUser(102, "bob"), Role: "member"},
			},
			Teams: []organizationTeam{{
				Team: &gogithub.Team{
					ID:   gogithub.Ptr[int64](201),
					Name: gogithub.Ptr("Developers"),
					Slug: gogithub.Ptr("developers"),
				},
				Members: []*gogithub.User{newUser(102, "bob")},
				Repos: []*gogithub.Repository{
					{Name: gogithub.Ptr("telemetry"), RoleName: gogithub.Ptr("push")},
					{Name: gogithub.Ptr("unscraped"), RoleName: gogithub.Ptr("admin")},
				},
			}},
			Repositories: organizationRepositories("acme", []v1.GitHubRepository{{Owner: "acme", Repo: "telemetry"}}),
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal(organizationRBAC{
			Users: []models.ExternalUser{
				{
					Aliases:  pq.StringArray{"github://user/101"},
					Name:     "alice",
					Tenant:   "acme",
					UserType: "GitHub::User",
				},
				{
					Aliases:  pq.StringArray{"github://user/102"},
					Name:     "bob",
					Tenant:   "acme",
					UserType: "GitHub::User",
				},
			},
			Groups: []models.ExternalGroup{{
				Tenant:    "acme",
				Aliases:   pq.StringArray{"github://team/201"},
				Name:      "Developers",
				GroupType: "GitHub::Team",
			}},
			Roles: []models.ExternalRole{
				{
					Tenant:   "acme",
					Aliases:  pq.StringArray{"github://org-role/acme/admin"},
					RoleType: "GitHub::Organization",
					Name:     "admin",
				},
				{
					Tenant:   "acme",
					Aliases:  pq.StringArray{"github://org-role/acme/member"},
					RoleType: "GitHub::Organization",
					Name:     "member",
				},
				{
					Tenant:   "acme",
					Aliases:  pq.StringArray{"github://repository-role/acme/write"},
					RoleType: "GitHub::Repository",
					Name:     "write",
				},
			},
			UserGroups: []v1.ExternalUserGroup{{
				ExternalUserAliases:  []string{"github://user/102"},
				ExternalGroupAliases: []string{"github://team/201"},
			}},
			Access: []v1.ExternalConfigAccess{
				{
					Source: gogithub.Ptr(organizationMembershipSource),
					ConfigExternalID: v1.ExternalID{
						ConfigType: ConfigTypeOrganization,
						ExternalID: "github/acme",
						ScraperID:  "all",
					},
					ExternalUserAliases: []string{"github://user/101"},
					ExternalRoleAliases: []string{"github://org-role/acme/admin"},
				},
				{
					Source: gogithub.Ptr(organizationMembershipSource),
					ConfigExternalID: v1.ExternalID{
						ConfigType: ConfigTypeOrganization,
						ExternalID: "github/acme",
						ScraperID:  "all",
					},
					ExternalUserAliases: []string{"github://user/102"},
					ExternalRoleAliases: []string{"github://org-role/acme/member"},
				},
				{
					Source:               gogithub.Ptr(teamPermissionSource),
					ConfigExternalID:     v1.ExternalID{ConfigType: ConfigTypeRepository, ExternalID: "github/acme/telemetry"},
					ExternalGroupAliases: []string{"github://team/201"},
					ExternalRoleAliases:  []string{"github://repository-role/acme/write"},
				},
			},
		}))
	})

	It("rejects a member without a stable user ID", func() {
		_, err := buildOrganizationRBAC(organizationRBACInput{
			Owner:   "acme",
			Members: []organizationMember{{User: &gogithub.User{Login: gogithub.Ptr("alice")}, Role: "member"}},
		})
		Expect(err).To(MatchError(ContainSubstring("stable github user ID")))
	})
})

func logins(users []*gogithub.User) []string {
	names := make([]string, 0, len(users))
	for _, user := range users {
		names = append(names, user.GetLogin())
	}
	return names
}
