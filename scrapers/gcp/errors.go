// Condenses the Google API errors that report a configuration state rather than a failure.
package gcp

import (
	"errors"
	"fmt"
	"strings"

	"google.golang.org/api/googleapi"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/status"

	v1 "github.com/flanksource/config-db/api/v1"
)

// configurationReasons are the ErrorInfo reasons that mean the scrape asked for something
// this deployment has not turned on. They are an operator's choice rather than a fault, so
// they are reported once and briefly instead of failing the run.
var configurationReasons = map[string]string{
	"SERVICE_DISABLED":                "API is not enabled",
	"IAM_PERMISSION_DENIED":           "missing IAM permission",
	"ACCESS_TOKEN_SCOPE_INSUFFICIENT": "credentials lack the required scope",
}

// apiErrorDetail is the ErrorInfo Google attaches to a failure, normalised across its two
// transports: gRPC carries it as a typed detail, REST as decoded JSON.
type apiErrorDetail struct {
	Reason   string
	Metadata map[string]string
}

// apiErrorInfo returns that detail, from whichever transport produced the error. The
// Cloud Asset and Security Center clients are gRPC; Resource Manager, Cloud Identity and
// SQL Admin are REST, and they report the same conditions in a different envelope.
func apiErrorInfo(err error) *apiErrorDetail {
	if st, ok := status.FromError(err); ok {
		for _, detail := range st.Details() {
			if info, ok := detail.(*errdetails.ErrorInfo); ok {
				return &apiErrorDetail{Reason: info.GetReason(), Metadata: info.GetMetadata()}
			}
		}
	}

	var apiErr *googleapi.Error
	if errors.As(err, &apiErr) {
		for _, detail := range apiErr.Details {
			fields, ok := detail.(map[string]any)
			if !ok || !strings.HasSuffix(fmt.Sprint(fields["@type"]), "google.rpc.ErrorInfo") {
				continue
			}
			reason, _ := fields["reason"].(string)
			metadata := map[string]string{}
			if raw, ok := fields["metadata"].(map[string]any); ok {
				for k, v := range raw {
					metadata[k] = fmt.Sprint(v)
				}
			}
			return &apiErrorDetail{Reason: reason, Metadata: metadata}
		}
	}
	return nil
}

// summarizeAPIError reduces a Google API error to the facts worth acting on: what was
// refused, and on which service or resource.
//
// The full text says the same thing several times over — a prose message, an ErrorInfo, a
// Help link and a localized copy — and runs past a dozen lines, which buries every other
// error in the run.
func summarizeAPIError(err error) (string, bool) {
	info := apiErrorInfo(err)
	if info == nil {
		return "", false
	}
	explanation, ok := configurationReasons[info.Reason]
	if !ok {
		return "", false
	}

	parts := []string{fmt.Sprintf("%s (%s)", explanation, info.Reason)}
	// A disabled API names the service and the project billed for it; a denied call names
	// the permission and what it was refused on.
	for _, key := range []string{"service", "consumer", "permission", "resource"} {
		if value := info.Metadata[key]; value != "" {
			parts = append(parts, key+"="+value)
		}
	}
	return strings.Join(parts, " "), true
}

// reportAPIError records err against results.
//
// A configuration state becomes a scrape warning rather than an error: it rides the scrape
// summary, which is what an operator sees on a run, and AddScrapeWarning folds repeats
// together with a count. Anything else keeps its full text and stays an error, because an
// unexpected failure is worth reading in full.
func reportAPIError(ctx *GCPContext, results *v1.ScrapeResults, err error, msg string, args ...any) {
	// A pass that fans out over projects returns one error per project, joined. Classifying
	// the join as a whole would let a single disabled API downgrade the entire thing to a
	// warning and take every unrelated failure with it, so each cause is judged alone.
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, cause := range joined.Unwrap() {
			reportAPIError(ctx, results, cause, msg, args...)
		}
		return
	}

	summary, configured := summarizeAPIError(err)
	if !configured {
		results.Errorf(err, msg, args...)
		return
	}

	warning := fmt.Sprintf("%s: %s", fmt.Sprintf(msg, args...), summary)
	ctx.Warnf("%s", warning)
	*results = append(*results, v1.ScrapeResult{Warnings: []v1.Warning{{Error: warning}}})
}
