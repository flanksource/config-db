package github

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"

	"github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty/models"
	gogithub "github.com/google/go-github/v73/github"
	"github.com/lib/pq"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("GitHub repository RBAC", func() {
	It("paginates repository collaborators and teams", func(ctx SpecContext) {
		var server *httptest.Server
		server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			if request.URL.Query().Get("page") == "" {
				response.Header().Set(
					"Link",
					fmt.Sprintf("<%s%s?page=2&per_page=100>; rel=\"next\"", server.URL, request.URL.Path),
				)
			}

			switch request.URL.Path {
			case "/repos/acme/telemetry/collaborators":
				if request.URL.Query().Get("page") == "2" {
					_, _ = response.Write([]byte(`[{"id":102,"login":"release-bot","role_name":"admin"}]`))
				} else {
					_, _ = response.Write([]byte(`[{"id":101,"login":"alice","role_name":"maintain"}]`))
				}
			case "/repos/acme/telemetry/teams":
				if request.URL.Query().Get("page") == "2" {
					_, _ = response.Write([]byte(`[{"id":202,"name":"Security","permission":"maintain"}]`))
				} else {
					_, _ = response.Write([]byte(`[{"id":201,"name":"Developers","permission":"push"}]`))
				}
			default:
				http.NotFound(response, request)
			}
		}))
		defer server.Close()

		baseURL, err := url.Parse(server.URL + "/")
		Expect(err).NotTo(HaveOccurred())
		apiClient := gogithub.NewClient(server.Client())
		apiClient.BaseURL = baseURL
		client := GitHubClient{Client: apiClient, owner: "acme", repo: "telemetry"}

		collaborators, err := client.ListRepositoryCollaborators(ctx)
		Expect(err).NotTo(HaveOccurred())
		teams, err := client.ListRepositoryTeams(ctx)
		Expect(err).NotTo(HaveOccurred())

		Expect(collaborators).To(HaveLen(2))
		Expect(collaborators[0].GetLogin()).To(Equal("alice"))
		Expect(collaborators[1].GetLogin()).To(Equal("release-bot"))
		Expect(teams).To(HaveLen(2))
		Expect(teams[0].GetName()).To(Equal("Developers"))
		Expect(teams[1].GetName()).To(Equal("Security"))
	})

	It("maps effective collaborators and teams to repository access", func() {
		result, err := buildRepositoryAccess(repositoryAccessInput{
			Owner:      "acme",
			Repository: "telemetry",
			Collaborators: []*gogithub.User{
				{
					ID:       gogithub.Ptr[int64](101),
					Login:    gogithub.Ptr("alice"),
					Email:    gogithub.Ptr("alice@example.com"),
					Type:     gogithub.Ptr("User"),
					RoleName: gogithub.Ptr("maintain"),
				},
				{
					ID:          gogithub.Ptr[int64](102),
					Login:       gogithub.Ptr("release-bot"),
					Type:        gogithub.Ptr("Bot"),
					Permissions: map[string]bool{"admin": true, "push": true, "pull": true},
				},
			},
			Teams: []*gogithub.Team{
				{
					ID:         gogithub.Ptr[int64](201),
					Name:       gogithub.Ptr("Developers"),
					Slug:       gogithub.Ptr("developers"),
					Permission: gogithub.Ptr("push"),
				},
				{
					ID:          gogithub.Ptr[int64](202),
					Name:        gogithub.Ptr("Security"),
					Slug:        gogithub.Ptr("security"),
					Permissions: map[string]bool{"maintain": true, "pull": true},
				},
			},
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal(repositoryAccess{
			Users: []models.ExternalUser{
				{
					Aliases:  pq.StringArray{"github://user/101"},
					Name:     "alice",
					Tenant:   "acme",
					UserType: "GitHub::User",
					Email:    gogithub.Ptr("alice@example.com"),
				},
				{
					Aliases:  pq.StringArray{"github://user/102"},
					Name:     "release-bot",
					Tenant:   "acme",
					UserType: "GitHub::Bot",
				},
			},
			Groups: []models.ExternalGroup{
				{
					Tenant:    "acme",
					Aliases:   pq.StringArray{"github://team/201"},
					Name:      "Developers",
					GroupType: "GitHub::Team",
				},
				{
					Tenant:    "acme",
					Aliases:   pq.StringArray{"github://team/202"},
					Name:      "Security",
					GroupType: "GitHub::Team",
				},
			},
			Roles: []models.ExternalRole{
				{
					Tenant:   "acme",
					Aliases:  pq.StringArray{"github://repository-role/acme/maintain"},
					RoleType: "GitHub::Repository",
					Name:     "maintain",
				},
				{
					Tenant:   "acme",
					Aliases:  pq.StringArray{"github://repository-role/acme/admin"},
					RoleType: "GitHub::Repository",
					Name:     "admin",
				},
				{
					Tenant:   "acme",
					Aliases:  pq.StringArray{"github://repository-role/acme/write"},
					RoleType: "GitHub::Repository",
					Name:     "write",
				},
			},
			Access: []v1.ExternalConfigAccess{
				{
					Source:              gogithub.Ptr(repositoryPermissionSource),
					ConfigExternalID:    v1.ExternalID{ConfigType: ConfigTypeRepository, ExternalID: "github/acme/telemetry"},
					ExternalUserAliases: []string{"github://user/101"},
					ExternalRoleAliases: []string{"github://repository-role/acme/maintain"},
				},
				{
					Source:              gogithub.Ptr(repositoryPermissionSource),
					ConfigExternalID:    v1.ExternalID{ConfigType: ConfigTypeRepository, ExternalID: "github/acme/telemetry"},
					ExternalUserAliases: []string{"github://user/102"},
					ExternalRoleAliases: []string{"github://repository-role/acme/admin"},
				},
				{
					Source:               gogithub.Ptr(repositoryPermissionSource),
					ConfigExternalID:     v1.ExternalID{ConfigType: ConfigTypeRepository, ExternalID: "github/acme/telemetry"},
					ExternalGroupAliases: []string{"github://team/201"},
					ExternalRoleAliases:  []string{"github://repository-role/acme/write"},
				},
				{
					Source:               gogithub.Ptr(repositoryPermissionSource),
					ConfigExternalID:     v1.ExternalID{ConfigType: ConfigTypeRepository, ExternalID: "github/acme/telemetry"},
					ExternalGroupAliases: []string{"github://team/202"},
					ExternalRoleAliases:  []string{"github://repository-role/acme/maintain"},
				},
			},
		}))
	})

	DescribeTable("rejects incomplete provider identities",
		func(collaborators []*gogithub.User, teams []*gogithub.Team, expectedError string) {
			_, err := buildRepositoryAccess(repositoryAccessInput{
				Owner:         "acme",
				Repository:    "telemetry",
				Collaborators: collaborators,
				Teams:         teams,
			})
			Expect(err).To(MatchError(ContainSubstring(expectedError)))
		},
		Entry(
			"collaborator without a stable ID",
			[]*gogithub.User{{Login: gogithub.Ptr("alice"), RoleName: gogithub.Ptr("pull")}},
			nil,
			"alice",
		),
		Entry(
			"collaborator without an effective role",
			[]*gogithub.User{{ID: gogithub.Ptr[int64](101), Login: gogithub.Ptr("alice")}},
			nil,
			"effective repository role",
		),
		Entry(
			"team without a stable ID",
			nil,
			[]*gogithub.Team{{Name: gogithub.Ptr("Developers"), Permission: gogithub.Ptr("push")}},
			"Developers",
		),
		Entry(
			"team without an effective role",
			nil,
			[]*gogithub.Team{{ID: gogithub.Ptr[int64](201), Name: gogithub.Ptr("Developers")}},
			"effective repository role",
		),
	)
})
