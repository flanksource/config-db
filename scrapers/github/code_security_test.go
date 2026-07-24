package github

import (
	"net/http"
	"net/http/httptest"
	"net/url"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	dutyCtx "github.com/flanksource/duty/context"
	gogithub "github.com/google/go-github/v73/github"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("GitHub code security configurations", func() {
	scrapeFor := func(client *GitHubClient) *organizationScrape {
		return &organizationScrape{
			client: client,
			org:    v1.GitHubOrganization{Name: "acme", Settings: true},
			repositories: organizationRepositories("acme", []v1.GitHubRepository{
				{Owner: "acme", Repo: "telemetry"},
				{Owner: "acme", Repo: "billing"},
			}),
		}
	}

	It("links a configuration to the organization and to each attached repository", func() {
		result := buildCodeSecurityConfigurationResult(
			scrapeFor(nil),
			&gogithub.CodeSecurityConfiguration{
				ID:          gogithub.Ptr[int64](77),
				Name:        gogithub.Ptr("baseline"),
				Enforcement: gogithub.Ptr("enforced"),
			},
			[]string{"telemetry"},
		)

		Expect(result.Type).To(Equal(ConfigTypeCodeSecurityConfiguration))
		Expect(result.ID).To(Equal("github/acme/code-security-configuration/77"))
		Expect(result.Name).To(Equal("baseline"))
		Expect(result.Properties[0].Text).To(Equal("enforced"))
		Expect(result.Properties[1].Text).To(Equal("1"))
		Expect(result.RelationshipResults).To(Equal(v1.RelationshipResults{
			{
				ConfigExternalID: v1.ExternalID{
					ConfigType: ConfigTypeOrganization,
					ExternalID: "github/acme",
					ScraperID:  "all",
				},
				RelatedExternalID: v1.ExternalID{
					ConfigType: ConfigTypeCodeSecurityConfiguration,
					ExternalID: "github/acme/code-security-configuration/77",
				},
				Relationship: RelationshipGitHubOrganizationCodeSecurityConfiguration,
			},
			{
				ConfigExternalID: v1.ExternalID{
					ConfigType: ConfigTypeCodeSecurityConfiguration,
					ExternalID: "github/acme/code-security-configuration/77",
				},
				RelatedExternalID: v1.ExternalID{
					ConfigType: ConfigTypeRepository,
					ExternalID: "github/acme/telemetry",
				},
				Relationship: RelationshipGitHubCodeSecurityConfigurationRepository,
			},
		}))
	})

	It("excludes attached repositories that are outside the scrape", func() {
		server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			if request.URL.Path != "/orgs/acme/code-security/configurations/77/repositories" {
				http.NotFound(response, request)
				return
			}

			// go-github v73 decodes this endpoint into a flat []*Repository.
			_, _ = response.Write([]byte(
				`[{"id":1,"name":"telemetry"},{"id":2,"name":"unscraped"},{"id":3,"name":"billing"}]`,
			))
		}))
		DeferCleanup(server.Close)

		baseURL, err := url.Parse(server.URL + "/")
		Expect(err).NotTo(HaveOccurred())
		apiClient := gogithub.NewClient(server.Client())
		apiClient.BaseURL = baseURL

		repositories, err := codeSecurityConfigurationRepositories(
			api.NewScrapeContext(dutyCtx.New()),
			scrapeFor(&GitHubClient{Client: apiClient, owner: "acme"}),
			77,
		)

		Expect(err).NotTo(HaveOccurred())
		Expect(repositories).To(Equal([]string{"billing", "telemetry"}))
	})

	It("skips an organization without advanced security instead of failing it", func() {
		server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
			response.WriteHeader(http.StatusForbidden)
			_, _ = response.Write([]byte(`{"message":"Advanced Security is not enabled for this organization"}`))
		}))
		DeferCleanup(server.Close)

		baseURL, err := url.Parse(server.URL + "/")
		Expect(err).NotTo(HaveOccurred())
		apiClient := gogithub.NewClient(server.Client())
		apiClient.BaseURL = baseURL

		results := scrapeCodeSecurityConfigurations(
			api.NewScrapeContext(dutyCtx.New()),
			scrapeFor(&GitHubClient{Client: apiClient, owner: "acme"}),
		)

		Expect(results).To(BeEmpty())
	})
})
