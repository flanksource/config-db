package github

import (
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
				Repositories:      []string{"telemetry"},
				RepositoriesKnown: true,
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

	It("keeps an organization-only installation without inventing repository access", func() {
		result, err := buildAppInstallations(appInstallationInput{
			Owner: "acme",
			Installations: []installationRepositories{{
				Installation: &gogithub.Installation{
					ID:                  gogithub.Ptr[int64](5001),
					AppID:               gogithub.Ptr[int64](301),
					AppSlug:             gogithub.Ptr("policy-bot"),
					RepositorySelection: gogithub.Ptr("selected"),
					Permissions: &gogithub.InstallationPermissions{
						OrganizationAdministration: gogithub.Ptr("write"),
					},
				},
			}},
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(result.Results).To(HaveLen(1))
		Expect(result.Users).To(HaveLen(1))
		Expect(result.Roles).To(BeEmpty())
		Expect(result.Access).To(BeEmpty())
	})

	It("uses only the organization endpoint for selected installations", func() {
		var requestedPaths []string
		server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			requestedPaths = append(requestedPaths, request.URL.Path)
			if request.URL.Path != "/orgs/acme/installations" {
				http.NotFound(response, request)
				return
			}
			_, _ = response.Write([]byte(`{"total_count":1,"installations":[{
				"id":5001,
				"app_id":301,
				"app_slug":"release-bot",
				"repository_selection":"selected",
				"permissions":{"contents":"write","metadata":"read"}
			}]}`))
		}))
		defer server.Close()

		baseURL, err := url.Parse(server.URL + "/")
		Expect(err).NotTo(HaveOccurred())
		apiClient := gogithub.NewClient(server.Client())
		apiClient.BaseURL = baseURL

		result, errs := scrapeAppInstallations(
			api.NewScrapeContext(dutyCtx.New()),
			&organizationScrape{
				client: &GitHubClient{Client: apiClient, owner: "acme"},
				org:    v1.GitHubOrganization{Name: "acme", Apps: true},
			},
		)

		Expect(errs).To(BeEmpty())
		Expect(requestedPaths).To(Equal([]string{"/orgs/acme/installations"}))
		Expect(result.Results).To(HaveLen(1))
		Expect(result.Access).To(BeEmpty())
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

			repositories, known := resolveInstallationRepositories(
				scrape,
				&gogithub.Installation{
					ID:                  gogithub.Ptr[int64](5001),
					RepositorySelection: gogithub.Ptr("all"),
				},
			)

			Expect(known).To(BeTrue())
			Expect(repositories).To(Equal([]string{"billing", "telemetry"}))
		})

		It("does not infer repository grants for a selected installation", func() {
			scrape := scrapeFor(&GitHubClient{Client: gogithub.NewClient(nil), owner: "acme"})

			repositories, known := resolveInstallationRepositories(
				scrape,
				&gogithub.Installation{
					ID:                  gogithub.Ptr[int64](5001),
					AppSlug:             gogithub.Ptr("release-bot"),
					RepositorySelection: gogithub.Ptr("selected"),
				},
			)

			Expect(known).To(BeFalse())
			Expect(repositories).To(BeEmpty())
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
