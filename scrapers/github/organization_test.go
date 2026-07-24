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

var _ = Describe("GitHub organization settings", func() {
	// settingsScrape wires an organizationScrape against a stub API so the
	// settings fetch can be exercised without a token.
	settingsScrape := func(organization *gogithub.Organization, routes map[string]string) *organizationScrape {
		server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			body, ok := routes[request.URL.Path]
			if !ok {
				http.NotFound(response, request)
				return
			}
			_, _ = response.Write([]byte(body))
		}))
		DeferCleanup(server.Close)

		baseURL, err := url.Parse(server.URL + "/")
		Expect(err).NotTo(HaveOccurred())
		apiClient := gogithub.NewClient(server.Client())
		apiClient.BaseURL = baseURL

		return &organizationScrape{
			client:       &GitHubClient{Client: apiClient, owner: "acme"},
			org:          v1.GitHubOrganization{Name: "acme", Settings: true},
			organization: organization,
		}
	}

	It("collects actions, rulesets and role holders for an owner token", func() {
		settings, errs := fetchOrganizationSettings(
			api.NewScrapeContext(dutyCtx.New()),
			settingsScrape(
				&gogithub.Organization{
					Login:                 gogithub.Ptr("acme"),
					DefaultRepoPermission: gogithub.Ptr("read"),
				},
				map[string]string{
					"/orgs/acme/actions/permissions":                  `{"enabled_repositories":"all","allowed_actions":"selected"}`,
					"/orgs/acme/actions/permissions/selected-actions": `{"github_owned_allowed":true,"verified_allowed":false,"patterns_allowed":["acme/*"]}`,
					"/orgs/acme/rulesets":                             `[{"id":11,"name":"protect-default-branch","enforcement":"active"}]`,
					"/orgs/acme/organization-roles": `{"total_count":1,` +
						`"roles":[{"id":8132,"name":"security_manager","organization":{"login":"acme"}}]}`,
					"/orgs/acme/organization-roles/8132/teams": `[{"id":202,"name":"Security","slug":"security"}]`,
					"/orgs/acme/organization-roles/8132/users": `[{"id":101,"login":"alice"}]`,
				},
			),
		)

		Expect(errs).To(BeEmpty())
		Expect(settings.Actions.Permissions.GetAllowedActions()).To(Equal("selected"))
		Expect(settings.Actions.Allowed.PatternsAllowed).To(Equal([]string{"acme/*"}))
		Expect(settings.Rulesets).To(HaveLen(1))
		Expect(settings.Rulesets[0].Name).To(Equal("protect-default-branch"))
		Expect(settings.Roles).To(Equal([]organizationRole{{
			Role:  &gogithub.CustomOrgRoles{ID: gogithub.Ptr[int64](8132), Name: gogithub.Ptr("security_manager")},
			Teams: []string{"security"},
			Users: []string{"alice"},
		}}))
	})

	It("fails loudly when the token cannot read the organization policy fields", func() {
		// GitHub omits the policy fields rather than failing when the caller is
		// not an owner holding admin:org, so an absent default repository
		// permission is the only signal that the payload is incomplete.
		_, errs := fetchOrganizationSettings(
			api.NewScrapeContext(dutyCtx.New()),
			settingsScrape(&gogithub.Organization{Login: gogithub.Ptr("acme")}, nil),
		)

		Expect(errs).To(HaveLen(1))
		Expect(errs[0].Error).To(MatchError(ContainSubstring("admin:org")))
	})

	It("skips policy surfaces the organization does not have", func() {
		settings, errs := fetchOrganizationSettings(
			api.NewScrapeContext(dutyCtx.New()),
			settingsScrape(
				&gogithub.Organization{
					Login:                 gogithub.Ptr("acme"),
					DefaultRepoPermission: gogithub.Ptr("read"),
				},
				nil,
			),
		)

		Expect(errs).To(BeEmpty())
		Expect(settings).To(Equal(organizationSettings{}))
	})

	It("renders an unreadable boolean setting as unknown rather than false", func() {
		properties := organizationSettingsProperties(&gogithub.Organization{}, organizationSettings{})

		Expect(properties[0].Name).To(Equal("2FA Required"))
		Expect(properties[0].Text).To(Equal("unknown"))
	})

	It("drops organization fields that are not on the allow list", func() {
		sanitized := sanitizeOrganization(&gogithub.Organization{
			Login:                       gogithub.Ptr("acme"),
			BillingEmail:                gogithub.Ptr("billing@example.com"),
			AvatarURL:                   gogithub.Ptr("https://avatars.example.com/u/1"),
			ReposURL:                    gogithub.Ptr("https://api.github.com/orgs/acme/repos"),
			Plan:                        &gogithub.Plan{Name: gogithub.Ptr("enterprise")},
			TwoFactorRequirementEnabled: gogithub.Ptr(true),
		})

		Expect(sanitized.BillingEmail).To(BeNil())
		Expect(sanitized.AvatarURL).To(BeNil())
		Expect(sanitized.ReposURL).To(BeNil())
		Expect(sanitized.Plan).To(BeNil())
		Expect(sanitized.GetTwoFactorRequirementEnabled()).To(BeTrue())
	})
})
