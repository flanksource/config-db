// Covers which passes scrapeParent reaches for a given include list, and in particular
// that the resource hierarchy is emitted exactly once however the scrape is narrowed.
package gcp

import (
	"errors"

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
		ctx    *GCPContext
		calls  callCounts
		failed error
	)

	projectItem := v1.ScrapeResult{Type: v1.GCPProject, ID: parent}

	BeforeEach(func() {
		ctx = &GCPContext{ScrapeContext: api.NewScrapeContext(dutyCtx.New())}
		calls = callCounts{}
		failed = nil
	})

	// Every pass is stubbed, so a spec asserts only which of them the include list reaches.
	stubs := func() parentScrapers {
		return parentScrapers{
			fetchAssets: func(*GCPContext, v1.GCP, string) (v1.ScrapeResults, error) {
				calls.assets++
				return v1.ScrapeResults{{Type: "GCP::Instance", ID: "i-1"}}, nil
			},
			fetchSQLBackups: func(*GCPContext, v1.GCP, string, v1.ScrapeResults) (v1.ScrapeResults, error) {
				calls.backups++
				return nil, nil
			},
			fetchHierarchy: func(*GCPContext, v1.GCP, string) (v1.ScrapeResults, error) {
				calls.hierarchy++
				if failed != nil {
					return nil, failed
				}
				return v1.ScrapeResults{projectItem}, nil
			},
			fetchIAMPolicies: func(*GCPContext, v1.GCP, string) (iamPolicyResult, error) {
				calls.iam++
				// The IAM pass carries the hierarchy itself, which is why the standalone
				// pass has to stay out of its way.
				return iamPolicyResult{Results: v1.ScrapeResults{projectItem}}, nil
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

	It("emits the project when the include list names only feature flags", func() {
		// No asset types, so the asset pass is skipped entirely; the project still comes
		// through, this time from the IAM pass.
		config := v1.GCP{Include: []string{v1.IncludeIAMPolicy}}

		results := Scraper{}.scrapeParentWith(ctx, config, parent, stubs())

		Expect(calls.assets).To(BeZero())
		Expect(calls.iam).To(Equal(1))
		Expect(calls.hierarchy).To(BeZero())
		Expect(projectItems(results)).To(HaveLen(1))
	})

	It("reports a hierarchy failure without dropping the assets", func() {
		failed = errContext
		config := v1.GCP{Include: []string{"compute.googleapis.com/Instance"}}

		results := Scraper{}.scrapeParentWith(ctx, config, parent, stubs())

		Expect(projectItems(results)).To(BeEmpty())
		Expect(results).To(ContainElement(HaveField("Error", MatchError(ContainSubstring("resource hierarchy")))))
		// The assets scraped before the failure are still returned.
		Expect(results).To(ContainElement(HaveField("ID", "i-1")))
	})

	It("skips group expansion when it is excluded", func() {
		config := v1.GCP{Exclude: []string{v1.IncludeGroupMembers}}

		Scraper{}.scrapeParentWith(ctx, config, parent, stubs())

		Expect(calls.iam).To(Equal(1))
		Expect(calls.groups).To(BeZero())
	})
})
