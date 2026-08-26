// Condenses the Google API errors that report a configuration state rather than a failure.
package gcp

import (
	"fmt"
	"strings"

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

// apiErrorInfo returns the structured detail Google attaches to its API errors.
func apiErrorInfo(err error) *errdetails.ErrorInfo {
	st, ok := status.FromError(err)
	if !ok {
		return nil
	}
	for _, detail := range st.Details() {
		if info, ok := detail.(*errdetails.ErrorInfo); ok {
			return info
		}
	}
	return nil
}

// summarizeAPIError reduces a Google API error to the facts worth acting on: what was
// refused, for which service, and on whose behalf.
//
// The full text says the same thing four times over — a prose message, an ErrorInfo, a
// Help link and a localized copy — and runs past a dozen lines, which buries every other
// error in the run.
func summarizeAPIError(err error) (string, bool) {
	info := apiErrorInfo(err)
	if info == nil {
		return "", false
	}
	explanation, ok := configurationReasons[info.GetReason()]
	if !ok {
		return "", false
	}

	parts := []string{fmt.Sprintf("%s (%s)", explanation, info.GetReason())}
	if service := info.GetMetadata()["service"]; service != "" {
		parts = append(parts, "service="+service)
	}
	if consumer := info.GetMetadata()["consumer"]; consumer != "" {
		parts = append(parts, "consumer="+consumer)
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
	summary, configured := summarizeAPIError(err)
	if !configured {
		results.Errorf(err, msg, args...)
		return
	}

	warning := fmt.Sprintf("%s: %s", fmt.Sprintf(msg, args...), summary)
	ctx.Warnf("%s", warning)
	*results = append(*results, v1.ScrapeResult{Warnings: []v1.Warning{{Error: warning}}})
}
