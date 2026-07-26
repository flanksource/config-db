package github

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	dutyCtx "github.com/flanksource/duty/context"
	"github.com/flanksource/duty/models"
	gogithub "github.com/google/go-github/v73/github"
	"github.com/lib/pq"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("GitHub app installations", func() {
	DescribeTable("collapses an installation permission set into a repository role",
		func(permissions map[string]string, expectedRole string) {
			role, err := effectiveInstallationRole(permissions)
			Expect(err).NotTo(HaveOccurred())
			Expect(role).To(Equal(expectedRole))
		},
		Entry("repository administration", map[string]string{"administration": "write", "contents": "read"}, "admin"),
		Entry("explicit admin level", map[string]string{"contents": "admin"}, "admin"),
		Entry("any writable repository permission", map[string]string{"contents": "write", "metadata": "read"}, "write"),
		Entry("read only", map[string]string{"contents": "read", "metadata": "read"}, "read"),
		Entry(
			"organization scoped writes do not imply repository access",
			map[string]string{"organization_administration": "write", "members": "write", "metadata": "read"},
			"read",
		),
	)

	It("rejects an installation with no repository permissions", func() {
		_, err := effectiveInstallationRole(map[string]string{"organization_administration": "write"})
		Expect(err).To(MatchError(ContainSubstring("effective repository role")))
	})

	It("flattens the installation permission struct to its wire form", func() {
		permissions, err := installationPermissionMap(&gogithub.InstallationPermissions{
			Administration: gogithub.Ptr("write"),
			Contents:       gogithub.Ptr("read"),
			Metadata:       gogithub.Ptr("read"),
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(permissions).To(Equal(map[string]string{
			"administration": "write",
			"contents":       "read",
			"metadata":       "read",
		}))
	})

	It("maps installations to config items and repository access rows", func() {
		result, err := buildAppInstallations(appInstallationInput{
			Owner: "acme",
			Installations: []installationRepositories{{
				Installation: &gogithub.Installation{
					ID:                  gogithub.Ptr[int64](5001),
					AppID:               gogithub.Ptr[int64](301),
					AppSlug:             gogithub.Ptr("release-bot"),
					RepositorySelection: gogithub.Ptr("selected"),
					Permissions: &gogithub.InstallationPermissions{
						Contents: gogithub.Ptr("write"),
						Metadata: gogithub.Ptr("read"),
					},
				},
				Repositories: []string{"telemetry"},
			}},
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(result.Users).To(Equal([]models.ExternalUser{{
			Aliases:  pq.StringArray{"github://app/301"},
			Name:     "release-bot",
			Tenant:   "acme",
			UserType: "GitHub::App",
		}}))
		Expect(result.Roles).To(Equal([]models.ExternalRole{{
			Tenant:   "acme",
			Aliases:  pq.StringArray{"github://repository-role/acme/write"},
			RoleType: "GitHub::Repository",
			Name:     "write",
		}}))
		Expect(result.Access).To(Equal([]v1.ExternalConfigAccess{{
			Source:              gogithub.Ptr(appInstallationSource),
			ConfigExternalID:    v1.ExternalID{ConfigType: ConfigTypeRepository, ExternalID: "github/acme/telemetry"},
			ExternalUserAliases: []string{"github://app/301"},
			ExternalRoleAliases: []string{"github://repository-role/acme/write"},
		}}))

		Expect(result.Results).To(HaveLen(1))
		installation := result.Results[0]
		Expect(installation.Type).To(Equal(ConfigTypeAppInstallation))
		Expect(installation.ID).To(Equal("github/acme/installation/5001"))
		Expect(installation.Name).To(Equal("release-bot"))
		Expect(installation.RelationshipResults).To(Equal(v1.RelationshipResults{
			{
				ConfigExternalID: v1.ExternalID{
					ConfigType: ConfigTypeOrganization,
					ExternalID: "github/acme",
					ScraperID:  "all",
				},
				RelatedExternalID: v1.ExternalID{
					ConfigType: ConfigTypeAppInstallation,
					ExternalID: "github/acme/installation/5001",
				},
				Relationship: RelationshipGitHubOrganizationAppInstallation,
			},
			{
				ConfigExternalID: v1.ExternalID{
					ConfigType: ConfigTypeAppInstallation,
					ExternalID: "github/acme/installation/5001",
				},
				RelatedExternalID: v1.ExternalID{
					ConfigType: ConfigTypeRepository,
					ExternalID: "github/acme/telemetry",
				},
				Relationship: RelationshipGitHubAppInstallationRepository,
			},
		}))
	})

	It("rejects an installation without a stable app ID", func() {
		_, err := buildAppInstallations(appInstallationInput{
			Owner: "acme",
			Installations: []installationRepositories{{
				Installation: &gogithub.Installation{
					ID:      gogithub.Ptr[int64](5001),
					AppSlug: gogithub.Ptr("release-bot"),
				},
			}},
		})
		Expect(err).To(MatchError(ContainSubstring("stable github app ID")))
	})

	Context("resolving the repositories an installation reaches", func() {
		scrapeFor := func(client *GitHubClient) *organizationScrape {
			return &organizationScrape{
				client: client,
				org:    v1.GitHubOrganization{Name: "acme"},
				repositories: organizationRepositories("acme", []v1.GitHubRepository{
					{Owner: "acme", Repo: "telemetry"},
					{Owner: "acme", Repo: "billing"},
				}),
			}
		}

		It("uses the scraped repository set for an all-repositories installation", func() {
			scrape := scrapeFor(&GitHubClient{Client: gogithub.NewClient(nil), owner: "acme"})

			repositories, err := resolveInstallationRepositories(
				api.NewScrapeContext(dutyCtx.New()),
				scrape,
				&gogithub.Installation{
					ID:                  gogithub.Ptr[int64](5001),
					RepositorySelection: gogithub.Ptr("all"),
				},
			)

			Expect(err).NotTo(HaveOccurred())
			Expect(repositories).To(Equal([]string{"billing", "telemetry"}))
		})

		It("excludes selected repositories that are outside the scrape", func() {
			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				if request.URL.Path != "/user/installations/5001/repositories" {
					http.NotFound(response, request)
					return
				}

				if request.URL.Query().Get("page") == "2" {
					_, _ = response.Write([]byte(`{"total_count":3,"repositories":[{"id":3,"name":"unscraped"}]}`))
					return
				}

				response.Header().Set(
					"Link",
					fmt.Sprintf("<%s%s?page=2&per_page=100>; rel=\"next\"", server.URL, request.URL.Path),
				)
				_, _ = response.Write(
					[]byte(`{"total_count":3,"repositories":[{"id":1,"name":"telemetry"},{"id":2,"name":"billing"}]}`),
				)
			}))
			defer server.Close()

			baseURL, err := url.Parse(server.URL + "/")
			Expect(err).NotTo(HaveOccurred())
			apiClient := gogithub.NewClient(server.Client())
			apiClient.BaseURL = baseURL
			scrape := scrapeFor(&GitHubClient{Client: apiClient, owner: "acme"})

			repositories, err := resolveInstallationRepositories(
				api.NewScrapeContext(dutyCtx.New()),
				scrape,
				&gogithub.Installation{
					ID:                  gogithub.Ptr[int64](5001),
					AppSlug:             gogithub.Ptr("release-bot"),
					RepositorySelection: gogithub.Ptr("selected"),
				},
			)

			Expect(err).NotTo(HaveOccurred())
			Expect(repositories).To(Equal([]string{"billing", "telemetry"}))
		})
	})

	It("drops installation fields that are not on the allow list", func() {
		sanitized := sanitizeInstallation(&gogithub.Installation{
			ID:              gogithub.Ptr[int64](5001),
			AppID:           gogithub.Ptr[int64](301),
			AppSlug:         gogithub.Ptr("release-bot"),
			AccessTokensURL: gogithub.Ptr("https://api.github.com/app/installations/5001/access_tokens"),
			RepositoriesURL: gogithub.Ptr("https://api.github.com/installation/repositories"),
			Permissions:     &gogithub.InstallationPermissions{Contents: gogithub.Ptr("write")},
			Account: &gogithub.User{
				Login:     gogithub.Ptr("acme"),
				AvatarURL: gogithub.Ptr("https://avatars.example.com/u/1"),
			},
		})

		Expect(sanitized.AccessTokensURL).To(BeNil())
		Expect(sanitized.RepositoriesURL).To(BeNil())
		Expect(sanitized.Permissions).To(BeNil())
		Expect(sanitized.Account.AvatarURL).To(BeNil())
		Expect(sanitized.GetAppSlug()).To(Equal("release-bot"))
	})
})
