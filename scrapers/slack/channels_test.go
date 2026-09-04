package slack

import (
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty/models"
	"github.com/flanksource/duty/types"
	"github.com/lib/pq"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
)

var _ = Describe("Slack channels", func() {
	workspace := Workspace{ID: "T0ACME", Name: "Acme", URL: "https://acme.slack.com/"}

	alice := UserInfo{
		ID:       "U0ALICE",
		Name:     "alice",
		RealName: "Alice Adams",
		Profile:  UserProfile{DisplayName: "alice", Email: "alice@acme.io"},
	}
	bob := UserInfo{ID: "U0BOB", Name: "bob", Profile: UserProfile{DisplayName: "bob"}}
	notifier := UserInfo{ID: "U0BOT", Name: "notifier", IsBot: true}

	general := ChannelDetail{
		ID:         "C0GENERAL",
		Name:       "general",
		Created:    1700000000,
		Creator:    alice.ID,
		IsChannel:  true,
		IsGeneral:  true,
		NumMembers: 3,
		Topic:      ChannelText{Value: "Company wide announcements"},
		Purpose:    ChannelText{Value: "Everyone"},
	}
	incidents := ChannelDetail{
		ID:        "C0INC",
		Name:      "incidents",
		Created:   1700000500,
		Creator:   bob.ID,
		IsChannel: true,
		IsPrivate: true,
	}

	scrape := workspaceScrape{
		Workspace: workspace,
		Channels:  []ChannelDetail{general, incidents},
		Users:     map[string]UserInfo{alice.ID: alice, bob.ID: bob, notifier.ID: notifier},
		Members: map[string][]string{
			general.ID:   {alice.ID, bob.ID, notifier.ID},
			incidents.ID: {bob.ID},
		},
	}

	resultsByID := func(results v1.ScrapeResults) map[string]v1.ScrapeResult {
		return lo.SliceToMap(results, func(r v1.ScrapeResult) (string, v1.ScrapeResult) { return r.ID, r })
	}

	Describe("config items", func() {
		results := buildWorkspaceResults(v1.BaseScraper{}, scrape)
		byID := resultsByID(results)

		It("emits the workspace and every channel", func() {
			Expect(lo.Keys(byID)).To(ConsistOf("slack/T0ACME", "slack/T0ACME/C0GENERAL", "slack/T0ACME/C0INC"))
		})

		It("describes the workspace", func() {
			ws := byID["slack/T0ACME"]
			Expect(ws.Type).To(Equal(ConfigTypeWorkspace))
			Expect(ws.Name).To(Equal("Acme"))
			Expect(ws.Config).To(Equal(workspace))
		})

		It("describes a channel and parents it to the workspace", func() {
			channel := byID["slack/T0ACME/C0GENERAL"]
			Expect(channel.Type).To(Equal(ConfigTypeChannel))
			Expect(channel.Name).To(Equal("general"))
			Expect(channel.Description).To(Equal("Company wide announcements"))
			Expect(channel.Config).To(Equal(general))
			Expect(channel.Tags).To(Equal(v1.JSONStringMap{"workspace": "T0ACME"}))
			Expect(channel.CreatedAt.Unix()).To(Equal(general.Created))
			Expect(channel.Parents).To(ConsistOf(v1.ConfigExternalKey{
				Type:       ConfigTypeWorkspace,
				ExternalID: "slack/T0ACME",
			}))
		})

		It("records visibility and a deep link on each channel", func() {
			private := byID["slack/T0ACME/C0INC"]
			Expect(private.Status).To(Equal("private"))
			Expect(byID["slack/T0ACME/C0GENERAL"].Status).To(Equal("public"))

			urls := lo.FilterMap(private.Properties, func(p *types.Property, _ int) (string, bool) {
				return p.Text, p.Name == "URL"
			})
			Expect(urls).To(ConsistOf("https://acme.slack.com/archives/C0INC"))
		})

		It("keeps archived channels, flagged by status", func() {
			archived := general
			archived.ID = "C0OLD"
			archived.IsArchived = true

			results := buildWorkspaceResults(v1.BaseScraper{}, workspaceScrape{
				Workspace: workspace,
				Channels:  []ChannelDetail{archived},
			})

			channel := resultsByID(results)["slack/T0ACME/C0OLD"]
			Expect(channel.Status).To(Equal("archived"))
			Expect(channel.DeletedAt).To(BeNil())
		})

		It("skips direct messages", func() {
			results := buildWorkspaceResults(v1.BaseScraper{}, workspaceScrape{
				Workspace: workspace,
				Channels: []ChannelDetail{
					{ID: "D0DM", IsIM: true},
					{ID: "G0MPIM", IsMpim: true},
				},
			})

			Expect(lo.Keys(resultsByID(results))).To(ConsistOf("slack/T0ACME"))
		})
	})

	Describe("membership access", func() {
		results := buildWorkspaceResults(v1.BaseScraper{}, scrape)
		byID := resultsByID(results)
		workspaceResult := byID["slack/T0ACME"]

		It("emits one external user per member, deduplicated across channels", func() {
			Expect(workspaceResult.ExternalUsers).To(ConsistOf(
				models.ExternalUser{
					Aliases:  pq.StringArray{"slack://user/T0ACME/U0ALICE"},
					Name:     "Alice Adams",
					Tenant:   "T0ACME",
					UserType: "User",
					Email:    lo.ToPtr("alice@acme.io"),
				},
				models.ExternalUser{
					Aliases:  pq.StringArray{"slack://user/T0ACME/U0BOB"},
					Name:     "bob",
					Tenant:   "T0ACME",
					UserType: "User",
				},
				models.ExternalUser{
					Aliases:  pq.StringArray{"slack://user/T0ACME/U0BOT"},
					Name:     "notifier",
					Tenant:   "T0ACME",
					UserType: "Bot",
				},
			))
		})

		It("emits only the roles it grants", func() {
			Expect(workspaceResult.ExternalRoles).To(ConsistOf(
				models.ExternalRole{
					Aliases:  pq.StringArray{"slack://channel-role/T0ACME/creator"},
					Tenant:   "T0ACME",
					RoleType: ConfigTypeChannel,
					Name:     "creator",
				},
				models.ExternalRole{
					Aliases:  pq.StringArray{"slack://channel-role/T0ACME/member"},
					Tenant:   "T0ACME",
					RoleType: ConfigTypeChannel,
					Name:     "member",
				},
			))
		})

		It("grants every member access to the channel they belong to", func() {
			Expect(byID["slack/T0ACME/C0GENERAL"].ConfigAccess).To(ConsistOf(
				access("C0GENERAL", "U0ALICE", "creator"),
				access("C0GENERAL", "U0BOB", "member"),
				access("C0GENERAL", "U0BOT", "member"),
			))
			Expect(byID["slack/T0ACME/C0INC"].ConfigAccess).To(ConsistOf(
				access("C0INC", "U0BOB", "creator"),
			))
		})

		It("maps membership without users.list, warning on the workspace", func() {
			results := buildWorkspaceResults(v1.BaseScraper{}, workspaceScrape{
				Workspace: workspace,
				Channels:  []ChannelDetail{incidents},
				Members:   map[string][]string{incidents.ID: {bob.ID}},
				Warnings:  []v1.Warning{{Error: "failed to list users: missing_scope"}},
			})
			byID := resultsByID(results)

			Expect(byID["slack/T0ACME"].Warnings).To(ConsistOf(v1.Warning{Error: "failed to list users: missing_scope"}))
			Expect(byID["slack/T0ACME"].ExternalUsers).To(ConsistOf(models.ExternalUser{
				Aliases:  pq.StringArray{"slack://user/T0ACME/U0BOB"},
				Name:     "U0BOB",
				Tenant:   "T0ACME",
				UserType: "User",
			}))
			Expect(byID["slack/T0ACME/C0INC"].ConfigAccess).To(ConsistOf(access("C0INC", "U0BOB", "creator")))
		})

		It("still resolves members that are missing from users.list", func() {
			results := buildWorkspaceResults(v1.BaseScraper{}, workspaceScrape{
				Workspace: workspace,
				Channels:  []ChannelDetail{incidents},
				Members:   map[string][]string{incidents.ID: {"U0GHOST"}},
			})
			byID := resultsByID(results)

			Expect(byID["slack/T0ACME"].ExternalUsers).To(ConsistOf(models.ExternalUser{
				Aliases:  pq.StringArray{"slack://user/T0ACME/U0GHOST"},
				Name:     "U0GHOST",
				Tenant:   "T0ACME",
				UserType: "User",
			}))
			Expect(byID["slack/T0ACME/C0INC"].ConfigAccess).To(ConsistOf(access("C0INC", "U0GHOST", "member")))
		})

		It("emits no users, roles or access when membership was not collected", func() {
			results := buildWorkspaceResults(v1.BaseScraper{}, workspaceScrape{
				Workspace: workspace,
				Channels:  []ChannelDetail{general},
				Users:     scrape.Users,
			})
			byID := resultsByID(results)

			Expect(byID["slack/T0ACME"].ExternalUsers).To(BeEmpty())
			Expect(byID["slack/T0ACME"].ExternalRoles).To(BeEmpty())
			Expect(byID["slack/T0ACME/C0GENERAL"].ConfigAccess).To(BeEmpty())
		})
	})
})

func access(channelID, userID, role string) v1.ExternalConfigAccess {
	return v1.ExternalConfigAccess{
		Source: lo.ToPtr(channelMembershipSource),
		ConfigExternalID: v1.ExternalID{
			ConfigType: ConfigTypeChannel,
			ExternalID: "slack/T0ACME/" + channelID,
		},
		ExternalUserAliases: []string{"slack://user/T0ACME/" + userID},
		ExternalRoleAliases: []string{"slack://channel-role/T0ACME/" + role},
	}
}
