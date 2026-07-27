package github

import (
	"errors"
	"net/http"
	"strings"

	v1 "github.com/flanksource/config-db/api/v1"
	gogithub "github.com/google/go-github/v73/github"
)

type githubDoctorCheck struct {
	config        string
	resource      string
	operation     string
	required      []string
	knownDisabled func(error) bool
}

type githubDoctorProbe func() (*gogithub.Response, error)

func runGitHubDoctorProbe(
	check githubDoctorCheck,
	authenticated bool,
	probe githubDoctorProbe,
) v1.DoctorResult {
	response, err := probe()
	return githubDoctorResult(check, authenticated, response, err)
}

func githubDoctorResult(
	check githubDoctorCheck,
	authenticated bool,
	response *gogithub.Response,
	err error,
) v1.DoctorResult {
	httpResponse := githubHTTPResponse(response, err)
	result := v1.DoctorResult{
		Scraper:       "github",
		Config:        check.config,
		Resource:      check.resource,
		Operation:     check.operation,
		Required:      githubRequiredPermissions(httpResponse, check.required),
		Granted:       githubGrantedPermissions(httpResponse),
		GrantEvidence: githubGrantEvidence(authenticated, httpResponse, err),
		Status:        v1.DoctorStatusPass,
	}

	if err != nil {
		result.Status = v1.DoctorStatusFail
		result.Message = err.Error()
		if check.knownDisabled != nil && check.knownDisabled(err) {
			result.Status = v1.DoctorStatusSkip
		}
	} else if httpResponse != nil {
		result.Message = httpResponse.Status
	}

	return result
}

func githubHTTPResponse(response *gogithub.Response, err error) *http.Response {
	if response != nil {
		return response.Response
	}

	var githubError *gogithub.ErrorResponse
	if errors.As(err, &githubError) {
		return githubError.Response
	}
	return nil
}

func githubRequiredPermissions(response *http.Response, documented []string) []string {
	required := append([]string(nil), documented...)
	if response != nil {
		required = append(required, prefixedHeaderValues(
			response.Header.Get("X-Accepted-GitHub-Permissions"),
			"github:",
			";",
		)...)
		required = append(required, prefixedHeaderValues(
			response.Header.Get("X-Accepted-OAuth-Scopes"),
			"oauth:",
			",",
		)...)
	}

	seen := make(map[string]struct{}, len(required))
	unique := required[:0]
	for _, permission := range required {
		if _, ok := seen[permission]; ok {
			continue
		}
		seen[permission] = struct{}{}
		unique = append(unique, permission)
	}
	return unique
}

func githubGrantedPermissions(response *http.Response) []string {
	if response == nil {
		return nil
	}
	return prefixedHeaderValues(response.Header.Get("X-OAuth-Scopes"), "oauth:", ",")
}

func prefixedHeaderValues(value, prefix, separator string) []string {
	var values []string
	for _, item := range strings.Split(value, separator) {
		if item = strings.TrimSpace(item); item != "" {
			values = append(values, prefix+item)
		}
	}
	return values
}

func githubGrantEvidence(authenticated bool, response *http.Response, err error) string {
	if err != nil {
		return "request denied"
	}
	if len(githubGrantedPermissions(response)) > 0 {
		return "reported OAuth scopes"
	}
	if authenticated {
		return "authenticated request succeeded"
	}
	return "anonymous request succeeded"
}
