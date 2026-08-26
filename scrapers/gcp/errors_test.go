// Covers the reduction of Google API errors to a reason and the resource they name.
package gcp

import (
	"errors"
	"fmt"
	"testing"

	dutyCtx "github.com/flanksource/duty/context"
	"github.com/onsi/gomega"
	"google.golang.org/api/googleapi"
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
		"Security Command Center API has not been used in project 210987654321 before or it is disabled.").
		WithDetails(&errdetails.ErrorInfo{
			Reason: reason,
			Domain: "googleapis.com",
			Metadata: map[string]string{
				"service":  "securitycenter.googleapis.com",
				"consumer": "projects/210987654321",
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
		"API is not enabled (SERVICE_DISABLED) service=securitycenter.googleapis.com consumer=projects/210987654321"))

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
		"skipping GCP Security Center findings for %s", "organizations/123456789012")

	// A disabled API is a deployment choice, so it must not be recorded as a scrape error.
	g.Expect(results).To(gomega.HaveLen(1))
	g.Expect(results[0].Error).To(gomega.BeNil())

	// It rides the scrape summary, which is what an operator sees on a run.
	g.Expect(results[0].Warnings).To(gomega.HaveLen(1))
	warning := results[0].Warnings[0].Error
	g.Expect(warning).To(gomega.ContainSubstring("organizations/123456789012"))
	g.Expect(warning).To(gomega.ContainSubstring("SERVICE_DISABLED"))
	g.Expect(warning).To(gomega.ContainSubstring("securitycenter.googleapis.com"))
	g.Expect(warning).To(gomega.ContainSubstring("projects/210987654321"))
}

// The summary folds repeats together, so the same API disabled across many projects reads
// as one warning with a count rather than one line per project.
func TestRepeatedSkipsCollapseInTheSummary(t *testing.T) {
	g := gomega.NewWithT(t)
	ctx := &GCPContext{ScrapeContext: api.NewScrapeContext(dutyCtx.New())}

	var results v1.ScrapeResults
	for i := 0; i < 3; i++ {
		reportAPIError(ctx, &results, serviceDisabledError(t, "SERVICE_DISABLED"),
			"skipping GCP Security Center findings for %s", "organizations/123456789012")
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

// restErrorWith rebuilds a googleapi.Error the way the REST clients return it: details as
// decoded JSON rather than typed protos.
func restErrorWith(reason string, metadata map[string]any) error {
	return &googleapi.Error{
		Code:    403,
		Message: "denied",
		Details: []any{
			map[string]any{
				"@type":    "type.googleapis.com/google.rpc.ErrorInfo",
				"reason":   reason,
				"domain":   "cloudresourcemanager.googleapis.com",
				"metadata": metadata,
			},
			map[string]any{"@type": "type.googleapis.com/google.rpc.Help"},
		},
	}
}

// Resource Manager, Cloud Identity and SQL Admin are REST clients, so their failures never
// reach the gRPC status extractor and were reported in full until this was handled.
func TestSummarizeRESTAPIError(t *testing.T) {
	g := gomega.NewWithT(t)

	// A denied call names the permission and the resource, not a service and consumer.
	summary, ok := summarizeAPIError(restErrorWith("IAM_PERMISSION_DENIED", map[string]any{
		"permission":         "resourcemanager.organizations.getIamPolicy",
		"resource":           "organizations/123456789012",
		"troubleshooter_url": "https://console.cloud.google.com/...",
	}))
	g.Expect(ok).To(gomega.BeTrue())
	g.Expect(summary).To(gomega.Equal(
		"missing IAM permission (IAM_PERMISSION_DENIED) permission=resourcemanager.organizations.getIamPolicy resource=organizations/123456789012"))

	// A disabled API names the service and the project billed for it.
	summary, ok = summarizeAPIError(restErrorWith("SERVICE_DISABLED", map[string]any{
		"service":  "cloudidentity.googleapis.com",
		"consumer": "projects/210987654321",
	}))
	g.Expect(ok).To(gomega.BeTrue())
	g.Expect(summary).To(gomega.Equal(
		"API is not enabled (SERVICE_DISABLED) service=cloudidentity.googleapis.com consumer=projects/210987654321"))

	// An unrelated REST failure keeps its full text.
	_, ok = summarizeAPIError(&googleapi.Error{Code: 500, Message: "backend error"})
	g.Expect(ok).To(gomega.BeFalse())
}

// The error arrives wrapped by the layers it passed through, so it has to be unwrapped.
func TestSummarizeWrappedRESTError(t *testing.T) {
	g := gomega.NewWithT(t)
	wrapped := fmt.Errorf("get IAM policy for GCP organization organizations/123456789012: %w",
		restErrorWith("IAM_PERMISSION_DENIED", map[string]any{"permission": "resourcemanager.organizations.getIamPolicy"}))

	summary, ok := summarizeAPIError(wrapped)
	g.Expect(ok).To(gomega.BeTrue())
	g.Expect(summary).To(gomega.ContainSubstring("IAM_PERMISSION_DENIED"))
}
