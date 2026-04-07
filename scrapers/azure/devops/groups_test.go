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
				"vssgp.group-desc": {Allow: GitPermissionRead},
				"Microsoft.TeamFoundation.Identity;user-desc": {Allow: GitPermissionRead},
				"aad.user-desc": {Allow: GitPermissionRead},
			},
		}}

		perms := ParseGitPermissions(acls)
		Expect(perms).To(HaveLen(3))
		types := map[string]bool{}
		for _, p := range perms {
			types[p.IdentityType] = true
		}
		Expect(types).To(HaveKey("group"))
		Expect(types).To(HaveKey("unknown"))
		Expect(types).To(HaveKey("user"))
	})
})

var _ = Describe("DescriptorAliases", func() {
	It("converts vssgp descriptor to both forms", func() {
		// vssgp.Uy0xLTktMTIzNA encodes "S-1-9-1234"
		vssgp := SIDToVssgp("S-1-9-1234")
		aliases := DescriptorAliases(vssgp)
		Expect(aliases).To(HaveLen(2))
		Expect(aliases).To(ContainElement(vssgp))
		Expect(aliases).To(ContainElement("Microsoft.TeamFoundation.Identity;S-1-9-1234"))
	})

	It("converts TF identity descriptor to both forms", func() {
		tf := "Microsoft.TeamFoundation.Identity;S-1-9-1234"
		aliases := DescriptorAliases(tf)
		Expect(aliases).To(HaveLen(2))
		Expect(aliases).To(ContainElement(tf))
		Expect(aliases).To(ContainElement(SIDToVssgp("S-1-9-1234")))
	})

	It("returns original for unknown descriptors", func() {
		aliases := DescriptorAliases("aad.some-user")
		Expect(aliases).To(Equal([]string{"aad.some-user"}))
	})
})

var _ = Describe("BuildIdentityMap", func() {
	It("maps all descriptor forms to the same identity", func() {
		id := ResolvedIdentity{
			Descriptor:          "vssgp.Uy0xLTktMTIzNA",
			SubjectDescriptor:   "vssgp.other",
			ProviderDisplayName: "Test Group",
			IsContainer:         true,
		}
		m := BuildIdentityMap([]ResolvedIdentity{id})

		// Should be reachable via the vssgp descriptor
		Expect(m).To(HaveKey("vssgp.Uy0xLTktMTIzNA"))
		// Should be reachable via the TF identity form of the same SID
		Expect(m).To(HaveKey("Microsoft.TeamFoundation.Identity;S-1-9-1234"))
		// Should be reachable via SubjectDescriptor
		Expect(m).To(HaveKey("vssgp.other"))
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
