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

var _ = Describe("DescriptorAliases form coverage", func() {
	It("a vssgp descriptor reduces to just the SID", func() {
		vssgp := SIDToVssgp("S-1-9-1234")
		Expect(DescriptorAliases(vssgp)).To(Equal([]string{"S-1-9-1234"}))
	})

	It("a TF Identity descriptor reduces to just the SID", func() {
		tf := "Microsoft.TeamFoundation.Identity;S-1-9-1234"
		Expect(DescriptorAliases(tf)).To(Equal([]string{"S-1-9-1234"}))
	})

	It("an unrecognized descriptor is returned as-is", func() {
		Expect(DescriptorAliases("cuckoo.something")).To(Equal([]string{"cuckoo.something"}))
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
	It("uses default roles when no mapping configured", func() {
		roles := ResolveGitRoles([]string{"Read"}, nil)
		Expect(roles).To(ConsistOf("Viewer"))
	})

	It("maps Contribute to Developer via defaults", func() {
		roles := ResolveGitRoles([]string{"Contribute", "CreateBranch"}, nil)
		Expect(roles).To(ConsistOf("Developer"))
	})

	It("maps ManagePermissions to Admin via defaults", func() {
		roles := ResolveGitRoles([]string{"ManagePermissions"}, nil)
		Expect(roles).To(ConsistOf("Admin"))
	})

	It("matches custom role mapping", func() {
		mapping := map[string][]string{
			"Developer": {"Git:Read", "Git:Contribute", "Git:CreateBranch"},
			"Reader":    {"Git:Read"},
		}
		roles := ResolveGitRoles([]string{"Read", "Contribute", "CreateBranch", "CreateTag"}, mapping)
		Expect(roles).To(ConsistOf("Developer", "Reader"))
	})

	It("matches role when identity has any of the listed permissions", func() {
		mapping := map[string][]string{
			"Admin": {"Git:ManagePermissions", "Git:DeleteRepository"},
		}
		roles := ResolveGitRoles([]string{"Read", "ManagePermissions"}, mapping)
		Expect(roles).To(ConsistOf("Admin"))
	})

	It("returns empty when no roles match custom mapping", func() {
		mapping := map[string][]string{
			"Admin": {"Git:ManagePermissions", "Git:DeleteRepository"},
		}
		roles := ResolveGitRoles([]string{"Read"}, mapping)
		Expect(roles).To(BeEmpty())
	})
})

var _ = Describe("ParseBuildPermissions", func() {
	It("extracts permissions from effective allow bits", func() {
		acls := []AccessControlList{{
			AcesDictionary: map[string]AccessControlEntry{
				"vssgp.test-group": {
					Allow: BuildPermissionQueueBuilds | BuildPermissionViewBuilds,
					ExtendedInfo: &AccessControlExtendedInfo{
						EffectiveAllow: BuildPermissionQueueBuilds | BuildPermissionViewBuilds | BuildPermissionStopBuilds,
					},
				},
			},
		}}

		perms := ParseBuildPermissions(acls)
		Expect(perms).To(HaveLen(1))
		Expect(perms[0].IdentityType).To(Equal("group"))
		Expect(perms[0].Permissions).To(ConsistOf("ViewBuilds", "QueueBuilds", "StopBuilds"))
	})

	It("skips entries with no permissions", func() {
		acls := []AccessControlList{{
			AcesDictionary: map[string]AccessControlEntry{
				"user.test": {Allow: 0},
			},
		}}
		Expect(ParseBuildPermissions(acls)).To(BeEmpty())
	})

	It("identifies user vs group by descriptor prefix", func() {
		acls := []AccessControlList{{
			AcesDictionary: map[string]AccessControlEntry{
				"vssgp.group-desc": {Allow: BuildPermissionViewBuilds},
				"Microsoft.TeamFoundation.Identity;user-desc": {Allow: BuildPermissionViewBuilds},
				"aad.user-desc": {Allow: BuildPermissionViewBuilds},
			},
		}}

		perms := ParseBuildPermissions(acls)
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

var _ = Describe("ParseReleasePermissions", func() {
	It("extracts permissions from effective allow bits", func() {
		acls := []AccessControlList{{
			AcesDictionary: map[string]AccessControlEntry{
				"aad.user-1": {
					ExtendedInfo: &AccessControlExtendedInfo{
						EffectiveAllow: ReleasePermissionManageDeployments | ReleasePermissionCreateReleases | ReleasePermissionViewReleases,
					},
				},
			},
		}}

		perms := ParseReleasePermissions(acls)
		Expect(perms).To(HaveLen(1))
		Expect(perms[0].Permissions).To(ConsistOf("ManageDeployments", "ViewReleases", "CreateReleases"))
	})
})

var _ = Describe("ResolveRoles", func() {
	It("uses default roles with Pipeline prefix", func() {
		roles := ResolveRoles("Pipeline", []string{"QueueBuilds", "ViewBuilds"}, nil)
		Expect(roles).To(ConsistOf("Viewer", "Developer"))
	})

	It("uses default roles with Release prefix", func() {
		roles := ResolveRoles("Release", []string{"ManageDeployments", "ManageReleases"}, nil)
		Expect(roles).To(ConsistOf("Releaser"))
	})

	It("maps Admin permissions across types via defaults", func() {
		roles := ResolveRoles("Pipeline", []string{"AdministerBuildPermissions"}, nil)
		Expect(roles).To(ConsistOf("Admin"))
	})

	It("filters by prefix in custom mapping", func() {
		mapping := map[string][]string{
			"Ops": {"Pipeline:QueueBuilds", "Release:ManageDeployments"},
		}
		roles := ResolveRoles("Pipeline", []string{"QueueBuilds"}, mapping)
		Expect(roles).To(ConsistOf("Ops"))

		roles = ResolveRoles("Git", []string{"Read"}, mapping)
		Expect(roles).To(BeEmpty())
	})

	It("returns empty when no permissions match", func() {
		roles := ResolveRoles("Pipeline", []string{"ViewBuilds"}, map[string][]string{
			"Admin": {"Pipeline:AdministerBuildPermissions"},
		})
		Expect(roles).To(BeEmpty())
	})
})
