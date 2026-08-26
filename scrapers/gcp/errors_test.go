// Covers the reduction of Google API errors to a reason and the resource they name.
package gcp

import (
	"errors"
	"testing"

	dutyCtx "github.com/flanksource/duty/context"
	"github.com/onsi/gomega"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
)

// serviceDisabledError rebuilds the shape the Security Center API returns when it has not
// been enabled: prose, plus a structured ErrorInfo carrying the facts worth acting on.
func serviceDisabledError(t *testing.T, reason string) error {
	t.Helper()
	st, err := status.New(codes.PermissionDenied,
		"Security Command Center API has not been used in project 532356464728 before or it is disabled.").
		WithDetails(&errdetails.ErrorInfo{
			Reason: reason,
			Domain: "googleapis.com",
			Metadata: map[string]string{
				"service":  "securitycenter.googleapis.com",
				"consumer": "projects/532356464728",
			},
		})
	if err != nil {
		t.Fatalf("building test error: %v", err)
	}
	return st.Err()
}

func TestSummarizeAPIError(t *testing.T) {
	g := gomega.NewWithT(t)

	summary, ok := summarizeAPIError(serviceDisabledError(t, "SERVICE_DISABLED"))
	g.Expect(ok).To(gomega.BeTrue())
	g.Expect(summary).To(gomega.Equal(
		"API is not enabled (SERVICE_DISABLED) service=securitycenter.googleapis.com consumer=projects/532356464728"))

	// A reason that is not a configuration state keeps its full text.
	_, ok = summarizeAPIError(serviceDisabledError(t, "RESOURCE_EXHAUSTED"))
	g.Expect(ok).To(gomega.BeFalse())

	// So does anything that is not a Google API error at all.
	_, ok = summarizeAPIError(errors.New("connection reset"))
	g.Expect(ok).To(gomega.BeFalse())
}

func TestReportAPIErrorBecomesAScrapeWarning(t *testing.T) {
	g := gomega.NewWithT(t)
	ctx := &GCPContext{ScrapeContext: api.NewScrapeContext(dutyCtx.New())}

	var results v1.ScrapeResults
	reportAPIError(ctx, &results, serviceDisabledError(t, "SERVICE_DISABLED"),
		"skipping GCP Security Center findings for %s", "organizations/1092049285013")

	// A disabled API is a deployment choice, so it must not be recorded as a scrape error.
	g.Expect(results).To(gomega.HaveLen(1))
	g.Expect(results[0].Error).To(gomega.BeNil())

	// It rides the scrape summary, which is what an operator sees on a run.
	g.Expect(results[0].Warnings).To(gomega.HaveLen(1))
	warning := results[0].Warnings[0].Error
	g.Expect(warning).To(gomega.ContainSubstring("organizations/1092049285013"))
	g.Expect(warning).To(gomega.ContainSubstring("SERVICE_DISABLED"))
	g.Expect(warning).To(gomega.ContainSubstring("securitycenter.googleapis.com"))
	g.Expect(warning).To(gomega.ContainSubstring("projects/532356464728"))
}

// The summary folds repeats together, so the same API disabled across many projects reads
// as one warning with a count rather than one line per project.
func TestRepeatedSkipsCollapseInTheSummary(t *testing.T) {
	g := gomega.NewWithT(t)
	ctx := &GCPContext{ScrapeContext: api.NewScrapeContext(dutyCtx.New())}

	var results v1.ScrapeResults
	for i := 0; i < 3; i++ {
		reportAPIError(ctx, &results, serviceDisabledError(t, "SERVICE_DISABLED"),
			"skipping GCP Security Center findings for %s", "organizations/1092049285013")
	}

	var summary v1.ScrapeSummary
	for _, result := range results {
		for _, w := range result.Warnings {
			summary.AddScrapeWarning(w)
		}
	}
	g.Expect(summary.Warnings).To(gomega.HaveLen(1))
	g.Expect(summary.Warnings[0].Count).To(gomega.Equal(3))
}

func TestReportAPIErrorKeepsUnexpectedFailures(t *testing.T) {
	g := gomega.NewWithT(t)
	ctx := &GCPContext{ScrapeContext: api.NewScrapeContext(dutyCtx.New())}

	var results v1.ScrapeResults
	reportAPIError(ctx, &results, errors.New("connection reset"), "reading %s", "assets")

	// An unexpected failure stays an error and keeps its text.
	g.Expect(results).To(gomega.HaveLen(1))
	g.Expect(results[0].Error).To(gomega.MatchError(gomega.ContainSubstring("connection reset")))
	g.Expect(results[0].Warnings).To(gomega.BeEmpty())
}
