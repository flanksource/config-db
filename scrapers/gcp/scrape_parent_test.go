// Covers which passes scrapeParent reaches for a given include list, and in particular
// that the resource hierarchy is emitted exactly once however the scrape is narrowed.
package gcp

import (
	"errors"
	"slices"

	dutyCtx "github.com/flanksource/duty/context"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
)

var errContext = errors.New("resource manager unavailable")

var _ = Describe("scrapeParent", func() {
	const parent = "projects/demo"

	type callCounts struct {
		assets, backups, hierarchy, iam, groups int
	}

	var (
		ctx        *GCPContext
		calls      callCounts
		failed     error
		assetsFail error
		iamFail    error
	)

	projectItem := v1.ScrapeResult{Type: v1.GCPProject, ID: parent}

	BeforeEach(func() {
		ctx = &GCPContext{ScrapeContext: api.NewScrapeContext(dutyCtx.New())}
		calls = callCounts{}
		failed, assetsFail, iamFail = nil, nil, nil
	})

	// Every pass is stubbed, so a spec asserts only which of them the include list reaches.
	stubs := func() parentScrapers {
		return parentScrapers{
			// Cloud Asset Inventory returns only the types it was asked for, so the project
			// appears only when GetAssetTypes requested it. Returning it unconditionally
			// would let these specs pass against a build that never asks.
			fetchAssets: func(_ *GCPContext, config v1.GCP, _ string) (v1.ScrapeResults, error) {
				calls.assets++
				if assetsFail != nil {
					return nil, assetsFail
				}
				assets := v1.ScrapeResults{{Type: "GCP::Instance", ID: "i-1"}}
				types := config.GetAssetTypes()
				if len(types) == 0 || slices.Contains(types, v1.ProjectAssetType) {
					assets = append(assets, projectItem)
				}
				return assets, nil
			},
			fetchSQLBackups: func(*GCPContext, v1.GCP, string, v1.ScrapeResults) (v1.ScrapeResults, error) {
				calls.backups++
				return nil, nil
			},
			// buildResourceManagerHierarchy links to the project rather than emitting one,
			// so this returns the organization it does emit. The project comes from the
			// asset pass, which is why GetAssetTypes always asks for it.
			fetchHierarchy: func(*GCPContext, v1.GCP, string) (v1.ScrapeResults, error) {
				calls.hierarchy++
				if failed != nil {
					return nil, failed
				}
				return v1.ScrapeResults{{Type: v1.GCPOrganization, ID: "organizations/123456789012"}}, nil
			},
			fetchIAMPolicies: func(*GCPContext, v1.GCP, string) (iamPolicyResult, error) {
				calls.iam++
				if iamFail != nil {
					return iamPolicyResult{}, iamFail
				}
				// The IAM pass carries the hierarchy itself, which is why the standalone
				// pass has to stay out of its way. Like the standalone pass it links to
				// the project rather than emitting one.
				return iamPolicyResult{Results: v1.ScrapeResults{
					{Type: v1.GCPOrganization, ID: "organizations/123456789012"},
				}}, nil
			},
			fetchGroups: func(*GCPContext, v1.GCP, iamScope, []string) (v1.ScrapeResults, error) {
				calls.groups++
				return nil, nil
			},
		}
	}

	projectItems := func(results v1.ScrapeResults) []v1.ScrapeResult {
		var found []v1.ScrapeResult
		for _, r := range results {
			if r.Type == v1.GCPProject {
				found = append(found, r)
			}
		}
		return found
	}

	It("emits the project when the include list names only asset types", func() {
		// The include list is a strict allowlist, so narrowing it to assets switches the
		// IAM pass off. The project still has to exist: it anchors every asset's parent
		// edge and is the root unresolved spend is booked against.
		config := v1.GCP{Include: []string{"compute.googleapis.com/Instance"}}

		results := Scraper{}.scrapeParentWith(ctx, config, parent, stubs())

		Expect(calls.hierarchy).To(Equal(1))
		Expect(calls.iam).To(BeZero())
		Expect(calls.assets).To(Equal(1))
		Expect(projectItems(results)).To(HaveLen(1))
	})

	It("leaves the hierarchy to the IAM pass when that runs", func() {
		// An empty include list runs everything, so the IAM pass supplies the hierarchy.
		// Running the standalone pass as well would emit the project twice.
		results := Scraper{}.scrapeParentWith(ctx, v1.GCP{}, parent, stubs())

		Expect(calls.iam).To(Equal(1))
		Expect(calls.hierarchy).To(BeZero())
		Expect(projectItems(results)).To(HaveLen(1))
	})

	It("scrapes the project even when the include list names only feature flags", func() {
		// Only the asset pass emits a project — the hierarchy links to one. So an include
		// list of just IAMPolicy still has to reach the asset pass, which GetAssetTypes
		// guarantees by always asking for the project asset type.
		config := v1.GCP{Include: []string{v1.IncludeIAMPolicy}}

		results := Scraper{}.scrapeParentWith(ctx, config, parent, stubs())

		Expect(calls.assets).To(Equal(1), "the project asset type keeps the asset pass alive")
		Expect(calls.iam).To(Equal(1))
		Expect(calls.hierarchy).To(BeZero())
		Expect(projectItems(results)).To(HaveLen(1))
	})

	It("reports a hierarchy failure without dropping the assets", func() {
		failed = errContext
		config := v1.GCP{Include: []string{"compute.googleapis.com/Instance"}}

		results := Scraper{}.scrapeParentWith(ctx, config, parent, stubs())

		Expect(results).To(ContainElement(HaveField("Error", MatchError(ContainSubstring("resource hierarchy")))))
		// The assets scraped before the failure are still returned, project included.
		Expect(results).To(ContainElement(HaveField("ID", "i-1")))
		Expect(projectItems(results)).To(HaveLen(1))
	})

	It("skips group expansion when it is excluded", func() {
		config := v1.GCP{Exclude: []string{v1.IncludeGroupMembers}}

		Scraper{}.scrapeParentWith(ctx, config, parent, stubs())

		Expect(calls.iam).To(Equal(1))
		Expect(calls.groups).To(BeZero())
	})

	It("loses the project when the asset pass cannot run, and says so", func() {
		// Only the asset pass emits a project, so a disabled asset API means there is none
		// and every cost row roots against a config item that does not exist. The
		// hierarchy still runs and still yields the organization, which is what
		// project-less spend falls back to.
		assetsFail = errContext
		iamFail = errContext

		results := Scraper{}.scrapeParentWith(ctx, v1.GCP{}, parent, stubs())

		Expect(calls.assets).To(Equal(1))
		Expect(calls.iam).To(Equal(1))
		Expect(calls.hierarchy).To(Equal(1), "the hierarchy has to run once the IAM pass failed to supply it")
		Expect(projectItems(results)).To(BeEmpty())

		// Not silent: the failure is on the run rather than only in the logs.
		Expect(results).To(ContainElement(HaveField("Error", MatchError(ContainSubstring("GCP assets")))))
	})

	It("does not abandon later passes when the asset pass fails", func() {
		assetsFail = errContext

		results := Scraper{}.scrapeParentWith(ctx, v1.GCP{}, parent, stubs())

		// The IAM pass reads a different API, so it still runs.
		Expect(calls.iam).To(Equal(1))
		// The backup pass reads the instances the asset pass found, so it has nothing to do.
		Expect(calls.backups).To(BeZero())
		// The project is gone with the asset pass, which is the cost of it failing.
		Expect(projectItems(results)).To(BeEmpty())
	})

	It("skips group expansion when the IAM pass failed", func() {
		iamFail = errContext

		Scraper{}.scrapeParentWith(ctx, v1.GCP{}, parent, stubs())

		Expect(calls.groups).To(BeZero())
		Expect(calls.hierarchy).To(Equal(1))
	})
})
