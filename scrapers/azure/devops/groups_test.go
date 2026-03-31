package devops

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ParseGitPermissions", func() {
	It("extracts permissions from effective allow bits", func() {
		acls := []AccessControlList{{
			AcesDictionary: map[string]AccessControlEntry{
				"vssgp.test-group": {
					Allow: GitPermissionRead | GitPermissionContribute,
					ExtendedInfo: &AccessControlExtendedInfo{
						EffectiveAllow: GitPermissionRead | GitPermissionContribute | GitPermissionCreateBranch,
					},
				},
			},
		}}

		perms := ParseGitPermissions(acls)
		Expect(perms).To(HaveLen(1))
		Expect(perms[0].IdentityType).To(Equal("group"))
		Expect(perms[0].Permissions).To(ConsistOf("Read", "Contribute", "CreateBranch"))
	})

	It("skips entries with no permissions", func() {
		acls := []AccessControlList{{
			AcesDictionary: map[string]AccessControlEntry{
				"user.test": {Allow: 0},
			},
		}}
		Expect(ParseGitPermissions(acls)).To(BeEmpty())
	})

	It("identifies user vs group by descriptor prefix", func() {
		acls := []AccessControlList{{
			AcesDictionary: map[string]AccessControlEntry{
				"vssgp.group-desc":                             {Allow: GitPermissionRead},
				"Microsoft.TeamFoundation.Identity;user-desc":  {Allow: GitPermissionRead},
			},
		}}

		perms := ParseGitPermissions(acls)
		Expect(perms).To(HaveLen(2))
		types := map[string]bool{}
		for _, p := range perms {
			types[p.IdentityType] = true
		}
		Expect(types).To(HaveKey("group"))
		Expect(types).To(HaveKey("user"))
	})
})

var _ = Describe("ResolveGitRoles", func() {
	It("falls back to Git:: prefixed permissions when no role mapping", func() {
		roles := ResolveGitRoles([]string{"Read", "Contribute"}, nil)
		Expect(roles).To(ConsistOf("Git::Read", "Git::Contribute"))
	})

	It("matches role when identity has all required permissions", func() {
		mapping := map[string][]string{
			"Developer": {"Read", "Contribute", "CreateBranch"},
			"Reader":    {"Read"},
		}
		roles := ResolveGitRoles([]string{"Read", "Contribute", "CreateBranch", "CreateTag"}, mapping)
		Expect(roles).To(ConsistOf("Developer", "Reader"))
	})

	It("matches role when identity has any of the listed permissions", func() {
		mapping := map[string][]string{
			"Admin": {"ManagePermissions", "DeleteRepository"},
		}
		roles := ResolveGitRoles([]string{"Read", "ManagePermissions"}, mapping)
		Expect(roles).To(ConsistOf("Admin"))
	})

	It("falls back when no roles match", func() {
		mapping := map[string][]string{
			"Admin": {"ManagePermissions", "DeleteRepository"},
		}
		roles := ResolveGitRoles([]string{"Read"}, mapping)
		Expect(roles).To(ConsistOf("Git::Read"))
	})
})
