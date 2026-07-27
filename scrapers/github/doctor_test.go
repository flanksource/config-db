package github

import (
	"net/http"
	"net/http/httptest"
	"net/url"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	dutycontext "github.com/flanksource/duty/context"
	gogithub "github.com/google/go-github/v73/github"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("GitHub doctor probe results", func() {
	It("records accepted permissions and explicitly reported OAuth grants", func() {
		response := &gogithub.Response{Response: &http.Response{
			Status:     "200 OK",
			StatusCode: http.StatusOK,
			Header: http.Header{
				"X-Accepted-Github-Permissions": []string{"metadata=read"},
				"X-Accepted-Oauth-Scopes":       []string{"repo"},
				"X-Oauth-Scopes":                []string{"repo, read:org"},
			},
		}}

		result := githubDoctorResult(githubDoctorCheck{
			config:    "github",
			resource:  "acme/widgets",
			operation: "repository metadata",
		}, true, response, nil)

		Expect(result).To(Equal(v1.DoctorResult{
			Scraper:       "github",
			Config:        "github",
			Resource:      "acme/widgets",
			Operation:     "repository metadata",
			Required:      []string{"github:metadata=read", "oauth:repo"},
			Granted:       []string{"oauth:repo", "oauth:read:org"},
			GrantEvidence: "reported OAuth scopes",
			Status:        v1.DoctorStatusPass,
			Message:       "200 OK",
		}))
	})

	It("does not infer fine-grained grants from a successful request", func() {
		response := &gogithub.Response{Response: &http.Response{
			Status:     "200 OK",
			StatusCode: http.StatusOK,
			Header: http.Header{
				"X-Accepted-Github-Permissions": []string{"metadata=read"},
			},
		}}

		result := githubDoctorResult(githubDoctorCheck{
			config:    "github",
			resource:  "acme/widgets",
			operation: "repository metadata",
		}, true, response, nil)

		Expect(result.Granted).To(BeEmpty())
		Expect(result.GrantEvidence).To(Equal("authenticated request succeeded"))
		Expect(result.Status).To(Equal(v1.DoctorStatusPass))
	})

	It("retains documented requirements when GitHub omits permission headers", func() {
		response := &gogithub.Response{Response: &http.Response{
			Status:     "403 Forbidden",
			StatusCode: http.StatusForbidden,
			Header:     http.Header{},
		}}

		result := githubDoctorResult(githubDoctorCheck{
			config:    "github",
			resource:  "acme",
			operation: "organization repository rulesets",
			required:  []string{"github:organization_administration=write"},
		}, true, response, &gogithub.ErrorResponse{
			Response: response.Response,
			Message:  "Resource not accessible by personal access token",
		})

		Expect(result.Required).To(Equal([]string{"github:organization_administration=write"}))
		Expect(result.Status).To(Equal(v1.DoctorStatusFail))
		Expect(result.GrantEvidence).To(Equal("request denied"))
	})

	It("classifies a known disabled feature as skipped", func() {
		response := &gogithub.Response{Response: &http.Response{
			Status:     "404 Not Found",
			StatusCode: http.StatusNotFound,
			Header: http.Header{
				"X-Accepted-Github-Permissions": []string{"dependabot_alerts=read"},
			},
		}}

		result := githubDoctorResult(githubDoctorCheck{
			config:        "github",
			resource:      "acme/widgets",
			operation:     "dependabot alerts",
			knownDisabled: isDependabotDoctorDisabled,
		}, true, response, &gogithub.ErrorResponse{
			Response: response.Response,
			Message:  "Not Found",
		})

		Expect(result.Status).To(Equal(v1.DoctorStatusSkip))
		Expect(result.Required).To(Equal([]string{"github:dependabot_alerts=read"}))
		Expect(result.GrantEvidence).To(Equal("request denied"))
	})
})

var _ = Describe("GitHub organization doctor gates", func() {
	It("keeps rulesets out of the settings checks", func() {
		client := newDoctorTestClient(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			http.Error(response, `{"message":"not available"}`, http.StatusNotFound)
		}))

		results := doctorGitHubOrganizationSettings(
			api.NewScrapeContext(dutycontext.New()),
			client,
			"github",
			"acme",
		)

		Expect(results).NotTo(ContainElement(HaveField("Operation", "organization repository rulesets")))
	})

	It("checks app installations through the organization endpoint", func() {
		var requestedPath string
		client := newDoctorTestClient(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			requestedPath = request.URL.Path
			_, _ = response.Write([]byte(`{"total_count":0,"installations":[]}`))
		}))

		results := doctorGitHubOrganizationApps(
			api.NewScrapeContext(dutycontext.New()),
			client,
			"github",
			"acme",
		)

		Expect(requestedPath).To(Equal("/orgs/acme/installations"))
		Expect(results).To(HaveLen(1))
		Expect(results[0].Required).To(Equal([]string{"github:organization_administration=read"}))
	})
})

func newDoctorTestClient(handler http.Handler) *GitHubClient {
	server := httptest.NewServer(handler)
	DeferCleanup(server.Close)

	baseURL, err := url.Parse(server.URL + "/")
	Expect(err).NotTo(HaveOccurred())
	client := gogithub.NewClient(server.Client())
	client.BaseURL = baseURL
	return &GitHubClient{Client: client, authenticated: true}
}
