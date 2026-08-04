package gcp

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("intersectProjects", func() {
	It("keeps only the configured projects that belong to the organization", func() {
		var warnings []string
		warn := func(format string, args ...any) { warnings = append(warnings, format) }

		projects := intersectProjects(
			[]string{"gcp-proj-1", "gcp-proj-2", "gcp-proj-3"},
			[]string{"gcp-proj-2", "gcp-proj-9"},
			warn,
		)

		Expect(projects).To(Equal([]string{"gcp-proj-2"}))
		Expect(warnings).To(HaveLen(1), "expected a warning for the project outside the organization")
	})

	It("returns nothing when no configured project belongs to the organization", func() {
		projects := intersectProjects(
			[]string{"gcp-proj-1"},
			[]string{"gcp-proj-9"},
			func(string, ...any) {},
		)

		Expect(projects).To(BeEmpty())
	})

	It("keeps every configured project when all belong to the organization", func() {
		var warnings int
		projects := intersectProjects(
			[]string{"gcp-proj-1", "gcp-proj-2"},
			[]string{"gcp-proj-1", "gcp-proj-2"},
			func(string, ...any) { warnings++ },
		)

		Expect(projects).To(Equal([]string{"gcp-proj-1", "gcp-proj-2"}))
		Expect(warnings).To(BeZero())
	})
})

var _ = Describe("qualifyProjects", func() {
	It("prefixes each project id", func() {
		Expect(qualifyProjects([]string{"gcp-proj-1", "gcp-proj-2"})).To(Equal(
			[]string{"projects/gcp-proj-1", "projects/gcp-proj-2"}))
	})

	It("returns an empty list for no projects", func() {
		Expect(qualifyProjects(nil)).To(BeEmpty())
	})
})

var _ = Describe("securityCenterParents", func() {
	It("keeps narrowed organization project roots", func() {
		parents := []string{"projects/gcp-proj-1", "projects/gcp-proj-2"}
		Expect(securityCenterParents(parents)).To(Equal(parents))
	})

	It("keeps an unrestricted organization root", func() {
		parents := []string{"organizations/1234567890"}
		Expect(securityCenterParents(parents)).To(Equal(parents))
	})
})

var _ = Describe("projectFromParent", func() {
	It("extracts the project id from a project root", func() {
		Expect(projectFromParent("projects/gcp-proj-1")).To(Equal("gcp-proj-1"))
	})

	It("returns empty for an organization root", func() {
		Expect(projectFromParent("organizations/1234567890")).To(BeEmpty())
	})
})
