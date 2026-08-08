package slack

import (
	"fmt"
	"strings"
	"time"

	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty/models"
	"github.com/flanksource/duty/types"
	"github.com/lib/pq"
	"github.com/samber/lo"
)

const (
	ConfigTypeWorkspace = "Slack::Workspace"
	ConfigTypeChannel   = "Slack::Channel"

	channelMembershipSource = "slack/channel-membership"

	roleMember  = "member"
	roleCreator = "creator"
)

// workspaceScrape is everything read from the Slack API for a single workspace.
type workspaceScrape struct {
	Workspace Workspace
	Channels  []ChannelDetail

	// Users are the workspace users indexed by user id.
	Users map[string]UserInfo

	// Members maps a channel id to the ids of its members. Channels absent from
	// the map contribute no access rows.
	Members map[string][]string

	// Warnings are reported against the workspace config item.
	Warnings []v1.Warning
}

func workspaceExternalID(teamID string) string {
	return fmt.Sprintf("slack/%s", teamID)
}

func channelExternalID(teamID, channelID string) string {
	return fmt.Sprintf("slack/%s/%s", teamID, channelID)
}

func userAlias(teamID, userID string) string {
	return fmt.Sprintf("slack://user/%s/%s", teamID, userID)
}

func channelRoleAlias(teamID, role string) string {
	return fmt.Sprintf("slack://channel-role/%s/%s", teamID, role)
}

// buildWorkspaceResults maps a workspace, its channels and their members onto
// config items and config access records. The users and roles referenced by the
// access records are emitted once, on the workspace result.
func buildWorkspaceResults(base v1.BaseScraper, scrape workspaceScrape) v1.ScrapeResults {
	builder := workspaceBuilder{scrape: scrape, seenUsers: map[string]struct{}{}, seenRoles: map[string]struct{}{}}

	var channels v1.ScrapeResults
	for _, channel := range scrape.Channels {
		if channel.IsDirectMessage() {
			continue
		}
		channels = append(channels, builder.channelResult(base, channel))
	}

	workspace := v1.ScrapeResult{
		BaseScraper:   base,
		Type:          ConfigTypeWorkspace,
		ID:            workspaceExternalID(scrape.Workspace.ID),
		Name:          lo.CoalesceOrEmpty(scrape.Workspace.Name, scrape.Workspace.ID),
		ConfigClass:   "Workspace",
		Config:        scrape.Workspace,
		ExternalUsers: builder.users,
		ExternalRoles: builder.roles,
		Warnings:      scrape.Warnings,
	}
	if scrape.Workspace.URL != "" {
		workspace.Properties = append(workspace.Properties, urlProperty(scrape.Workspace.URL))
	}

	return append(v1.ScrapeResults{workspace}, channels...)
}

type workspaceBuilder struct {
	scrape workspaceScrape

	users     []models.ExternalUser
	roles     []models.ExternalRole
	seenUsers map[string]struct{}
	seenRoles map[string]struct{}
}

func (b *workspaceBuilder) channelResult(base v1.BaseScraper, channel ChannelDetail) v1.ScrapeResult {
	teamID := b.scrape.Workspace.ID

	result := v1.ScrapeResult{
		BaseScraper: base,
		Type:        ConfigTypeChannel,
		ID:          channelExternalID(teamID, channel.ID),
		Name:        lo.CoalesceOrEmpty(channel.Name, channel.ID),
		ConfigClass: "Channel",
		Config:      channel,
		Description: lo.CoalesceOrEmpty(channel.Topic.Value, channel.Purpose.Value),
		Status:      channelStatus(channel),
		Tags:        v1.JSONStringMap{"workspace": teamID},
		Parents:     []v1.ConfigExternalKey{{Type: ConfigTypeWorkspace, ExternalID: workspaceExternalID(teamID)}},
		Properties: []*types.Property{
			{Name: "Members", Type: "number", Text: fmt.Sprintf("%d", channel.NumMembers)},
		},
	}

	if channel.Created > 0 {
		result.CreatedAt = lo.ToPtr(time.Unix(channel.Created, 0).UTC())
	}
	if url := channelURL(b.scrape.Workspace.URL, channel.ID); url != "" {
		result.Properties = append(result.Properties, urlProperty(url))
	}

	for _, member := range b.scrape.Members[channel.ID] {
		role := roleMember
		if member == channel.Creator {
			role = roleCreator
		}

		result.ConfigAccess = append(result.ConfigAccess, v1.ExternalConfigAccess{
			Source:              lo.ToPtr(channelMembershipSource),
			ConfigExternalID:    v1.ExternalID{ConfigType: ConfigTypeChannel, ExternalID: result.ID},
			ExternalUserAliases: []string{b.addUser(member)},
			ExternalRoleAliases: []string{b.addRole(role)},
		})
	}

	return result
}

func (b *workspaceBuilder) addUser(userID string) string {
	teamID := b.scrape.Workspace.ID
	alias := userAlias(teamID, userID)
	if _, ok := b.seenUsers[alias]; ok {
		return alias
	}
	b.seenUsers[alias] = struct{}{}

	// Members that users.list did not return (restricted or newly created
	// accounts) are still recorded so their access resolves.
	user := b.scrape.Users[userID]
	external := models.ExternalUser{
		Aliases:  pq.StringArray{alias},
		Name:     userName(userID, user),
		Tenant:   teamID,
		UserType: lo.Ternary(user.IsBot, "Bot", "User"),
	}
	if email := strings.TrimSpace(user.Profile.Email); email != "" {
		external.Email = lo.ToPtr(email)
	}
	b.users = append(b.users, external)

	return alias
}

func (b *workspaceBuilder) addRole(role string) string {
	teamID := b.scrape.Workspace.ID
	alias := channelRoleAlias(teamID, role)
	if _, ok := b.seenRoles[alias]; ok {
		return alias
	}
	b.seenRoles[alias] = struct{}{}

	b.roles = append(b.roles, models.ExternalRole{
		Aliases:  pq.StringArray{alias},
		Tenant:   teamID,
		RoleType: ConfigTypeChannel,
		Name:     role,
	})

	return alias
}

func userName(userID string, user UserInfo) string {
	return lo.CoalesceOrEmpty(user.RealName, user.Profile.RealName, user.Profile.DisplayName, user.Name, userID)
}

func channelStatus(channel ChannelDetail) string {
	switch {
	case channel.IsArchived:
		return "archived"
	case channel.IsPrivate:
		return "private"
	default:
		return "public"
	}
}

func channelURL(workspaceURL, channelID string) string {
	if workspaceURL == "" {
		return ""
	}
	return fmt.Sprintf("%s/archives/%s", strings.TrimSuffix(workspaceURL, "/"), channelID)
}

func urlProperty(url string) *types.Property {
	return &types.Property{Name: "URL", Type: "url", Text: url, Links: []types.Link{{URL: url, Type: "url"}}}
}
