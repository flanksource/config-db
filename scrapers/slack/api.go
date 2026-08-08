package slack

import (
	"context"
	"fmt"
	nethttp "net/http"
	"time"

	"github.com/flanksource/commons/http"
	"github.com/flanksource/config-db/api"
	"github.com/flanksource/duty/connection"
	"github.com/flanksource/duty/types"
)

type GetConversationHistoryParameters struct {
	Cursor string
	Oldest string
}

// ResponseMetadata holds pagination metadata
type ResponseMetadata struct {
	Cursor   string   `json:"next_cursor"`
	Messages []string `json:"messages"`
	Warnings []string `json:"warnings"`
}

// BotProfile contains information about a bot
type BotProfile struct {
	ID     string `json:"id,omitempty"`
	Name   string `json:"name,omitempty"`
	TeamID string `json:"team_id,omitempty"`
}

type Message struct {
	ClientMsgID string      `json:"client_msg_id,omitempty"`
	Type        string      `json:"type,omitempty"`
	Channel     string      `json:"channel,omitempty"`
	User        string      `json:"user,omitempty"`
	Text        string      `json:"text,omitempty"`
	Timestamp   string      `json:"ts,omitempty"`
	Team        string      `json:"team,omitempty"`
	BotID       string      `json:"bot_id,omitempty"`
	ReplyTo     int         `json:"reply_to,omitempty"`
	BotProfile  *BotProfile `json:"bot_profile,omitempty"`

	// channel_name, group_name
	Name string `json:"name,omitempty"`

	UserInfo UserInfo `json:"-"`
}

func (t Message) AsMap() map[string]any {
	m := map[string]any{
		"channel": t.Channel,
		"text":    t.Text,
		"user":    t.User,
	}

	if t.UserInfo.Profile.DisplayName != "" {
		m["display_name"] = t.UserInfo.Profile.DisplayName
	}

	if t.BotProfile != nil {
		m["bot_name"] = t.BotProfile.Name
	}

	return m
}

// SlackResponse handles parsing out errors from the web api.
type SlackResponse struct {
	Ok               bool             `json:"ok"`
	Error            string           `json:"error"`
	ResponseMetadata ResponseMetadata `json:"response_metadata"`
}

type GetConversationHistoryResponse struct {
	SlackResponse
	HasMore          bool `json:"has_more"`
	ResponseMetaData struct {
		NextCursor string `json:"next_cursor"`
	} `json:"response_metadata"`
	Messages []Message `json:"messages"`
}

type SlackAPI struct {
	client    *http.Client
	usersList map[string]UserInfo
}

const slackAPIBaseURL = "https://slack.com/api/"

// retryableStatuses are the responses worth another attempt. Slack answers a
// throttled call with 429 and a Retry-After header, which RetryOnStatus honours
// in preference to its own backoff.
var retryableStatuses = []int{
	nethttp.StatusTooManyRequests,
	nethttp.StatusInternalServerError,
	nethttp.StatusBadGateway,
	nethttp.StatusServiceUnavailable,
	nethttp.StatusGatewayTimeout,
}

func configureSlackClient(client *http.Client, baseURL string, maxAttempts int, retryDelay time.Duration) *http.Client {
	return client.
		BaseURL(baseURL).
		Header("Content-Type", "application/json").
		RetryStrategy(http.RetryOnStatus(maxAttempts, retryDelay, retryableStatuses...))
}

func NewSlackAPI(ctx api.ScrapeContext, token string) (*SlackAPI, error) {
	conn := connection.HTTPConnection{
		Bearer: types.EnvVar{ValueStatic: token},
	}
	client, err := connection.CreateHTTPClient(ctx, conn, types.WithFeature("slack"))
	if err != nil {
		return nil, err
	}

	configureSlackClient(client, slackAPIBaseURL,
		ctx.Properties().Int("scraper.slack.maxAttempts", 5),
		ctx.Properties().Duration("scraper.slack.retryDelay", time.Second),
	)

	return &SlackAPI{client: client}, nil
}

// intoSlackResponse decodes a slack response, turning a non-2xx status — an
// exhausted 429 retry budget, most often — into an explicit error rather than a
// JSON decode failure on an empty body.
func intoSlackResponse(response *http.Response, method string, output any) error {
	if !response.IsOK() {
		body, _ := response.AsString()
		return fmt.Errorf("%s failed with status %d: %s", method, response.StatusCode, body)
	}

	return response.Into(output)
}

func (t *SlackAPI) ConversationHistory(ctx context.Context, channel ChannelDetail, params *GetConversationHistoryParameters) ([]Message, error) {
	var output []Message
	for {
		response, err := t.getSlackConversationHistory(ctx, channel, params)
		if err != nil {
			return nil, err
		}

		output = append(output, response.Messages...)

		if response.ResponseMetaData.NextCursor == "" {
			break
		}
		params.Cursor = response.ResponseMetaData.NextCursor
	}

	return output, nil
}

// ChannelText is the topic/purpose envelope returned by conversations.list.
type ChannelText struct {
	Value   string `json:"value,omitempty"`
	Creator string `json:"creator,omitempty"`
	LastSet int64  `json:"last_set,omitempty"`
}

type ChannelDetail struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Created     int64       `json:"created,omitempty"`
	Creator     string      `json:"creator,omitempty"`
	IsChannel   bool        `json:"is_channel,omitempty"`
	IsGroup     bool        `json:"is_group,omitempty"`
	IsIM        bool        `json:"is_im,omitempty"`
	IsMpim      bool        `json:"is_mpim,omitempty"`
	IsPrivate   bool        `json:"is_private,omitempty"`
	IsArchived  bool        `json:"is_archived,omitempty"`
	IsGeneral   bool        `json:"is_general,omitempty"`
	IsShared    bool        `json:"is_shared,omitempty"`
	IsOrgShared bool        `json:"is_org_shared,omitempty"`
	NumMembers  int         `json:"num_members,omitempty"`
	Topic       ChannelText `json:"topic,omitempty"`
	Purpose     ChannelText `json:"purpose,omitempty"`
}

// IsDirectMessage reports whether the conversation is a direct or multi-party
// direct message rather than a channel.
func (t ChannelDetail) IsDirectMessage() bool {
	return t.IsIM || t.IsMpim
}

func (t ChannelDetail) String() string {
	return fmt.Sprintf("id: %s, name: %s", t.ID, t.Name)
}

type ConversationList struct {
	Ok               bool             `json:"ok"`
	Error            string           `json:"error,omitempty"`
	Channels         []ChannelDetail  `json:"channels"`
	ResponseMetadata ResponseMetadata `json:"response_metadata"`
}

func (t *SlackAPI) ListConversations(ctx context.Context) ([]ChannelDetail, error) {
	var cursor string
	var result []ChannelDetail
	for {
		response, err := t.client.R(ctx).
			QueryParam("types", "public_channel,private_channel").
			QueryParam("limit", "1000").
			QueryParam("cursor", cursor).
			Post("conversations.list", nil)
		if err != nil {
			return nil, err
		}

		var output ConversationList
		if err := intoSlackResponse(response, "conversations.list", &output); err != nil {
			return nil, err
		}

		if !output.Ok {
			return nil, fmt.Errorf("failed to list conversations: %s", output.Error)
		}

		result = append(result, output.Channels...)

		if output.ResponseMetadata.Cursor == "" {
			break
		}
		cursor = output.ResponseMetadata.Cursor
	}

	return result, nil
}

func (t *SlackAPI) getSlackConversationHistory(ctx context.Context, channel ChannelDetail, params *GetConversationHistoryParameters) (GetConversationHistoryResponse, error) {
	var output GetConversationHistoryResponse

	req := t.client.R(ctx).QueryParam("channel", channel.ID)
	if params.Cursor != "" {
		req.QueryParam("cursor", params.Cursor)
	}
	if params.Oldest != "" {
		req.QueryParam("oldest", params.Oldest)
	}
	response, err := req.Post("conversations.history", nil)
	if err != nil {
		return output, err
	}

	if !response.IsOK() {
		r, _ := response.AsString()
		return output, fmt.Errorf("failed to get conversation history (channel: %s): %s", channel, r)
	}

	if err := response.Into(&output); err != nil {
		return output, err
	}

	if output.SlackResponse.Error != "" {
		if output.SlackResponse.Error == "not_in_channel" {
			return output, nil
		}

		return output, fmt.Errorf("failed to get conversation history (channel: %s): %s", channel, output.SlackResponse.Error)
	}

	// conversation.history endpoint doesn't return the display name of the users.
	// we replace the user id with the name here.
	for i, message := range output.Messages {
		if message.BotProfile == nil {
			if info, ok := t.usersList[message.User]; ok {
				message.UserInfo = info
			}
		}

		output.Messages[i] = message
	}

	return output, nil
}

type UserInfo struct {
	ID       string      `json:"id"`
	Name     string      `json:"name"`
	RealName string      `json:"real_name,omitempty"`
	TeamID   string      `json:"team_id,omitempty"`
	Deleted  bool        `json:"deleted,omitempty"`
	IsBot    bool        `json:"is_bot,omitempty"`
	IsAdmin  bool        `json:"is_admin,omitempty"`
	IsOwner  bool        `json:"is_owner,omitempty"`
	Profile  UserProfile `json:"profile"`
}

type UserProfile struct {
	DisplayName string `json:"display_name"`
	RealName    string `json:"real_name,omitempty"`
	Email       string `json:"email,omitempty"`
	Title       string `json:"title,omitempty"`
}

type ListUsersResponse struct {
	Ok               bool             `json:"ok"`
	Error            string           `json:"error,omitempty"`
	Members          []UserInfo       `json:"members,omitempty"`
	ResponseMetadata ResponseMetadata `json:"response_metadata"`
}

// Users returns the workspace users indexed by user id. PopulateUsers must be
// called first.
func (t *SlackAPI) Users() map[string]UserInfo {
	return t.usersList
}

func (t *SlackAPI) PopulateUsers(ctx context.Context) error {
	idToNameMap := make(map[string]UserInfo)

	var cursor string
	for {
		response, err := t.client.R(ctx).
			QueryParam("limit", "200").
			QueryParam("cursor", cursor).
			Get("users.list")
		if err != nil {
			return err
		}

		var output ListUsersResponse
		if err := intoSlackResponse(response, "users.list", &output); err != nil {
			return err
		}

		if output.Error != "" {
			return fmt.Errorf("failed to list users: %s", output.Error)
		}

		for _, m := range output.Members {
			idToNameMap[m.ID] = m
		}

		if output.ResponseMetadata.Cursor == "" {
			break
		}
		cursor = output.ResponseMetadata.Cursor
	}

	t.usersList = idToNameMap
	return nil
}

// Workspace identifies the Slack workspace the token belongs to.
type Workspace struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url,omitempty"`
}

type authTestResponse struct {
	Ok     bool   `json:"ok"`
	Error  string `json:"error,omitempty"`
	URL    string `json:"url,omitempty"`
	Team   string `json:"team,omitempty"`
	TeamID string `json:"team_id,omitempty"`
}

// AuthTest identifies the workspace behind the token. Unlike team.info it
// requires no additional scope.
func (t *SlackAPI) AuthTest(ctx context.Context) (Workspace, error) {
	response, err := t.client.R(ctx).Post("auth.test", nil)
	if err != nil {
		return Workspace{}, err
	}

	var output authTestResponse
	if err := intoSlackResponse(response, "auth.test", &output); err != nil {
		return Workspace{}, err
	}

	if !output.Ok {
		return Workspace{}, fmt.Errorf("failed to identify slack workspace: %s", output.Error)
	}

	return Workspace{ID: output.TeamID, Name: output.Team, URL: output.URL}, nil
}

type conversationMembersResponse struct {
	Ok               bool             `json:"ok"`
	Error            string           `json:"error,omitempty"`
	Members          []string         `json:"members,omitempty"`
	ResponseMetadata ResponseMetadata `json:"response_metadata"`
}

// ConversationMembers returns the user ids of every member of the channel.
func (t *SlackAPI) ConversationMembers(ctx context.Context, channel ChannelDetail) ([]string, error) {
	var members []string

	var cursor string
	for {
		response, err := t.client.R(ctx).
			QueryParam("channel", channel.ID).
			QueryParam("limit", "500").
			QueryParam("cursor", cursor).
			Get("conversations.members")
		if err != nil {
			return nil, err
		}

		var output conversationMembersResponse
		if err := intoSlackResponse(response, "conversations.members", &output); err != nil {
			return nil, err
		}

		if !output.Ok {
			return nil, fmt.Errorf("failed to list members (channel: %s): %s", channel, output.Error)
		}

		members = append(members, output.Members...)

		if output.ResponseMetadata.Cursor == "" {
			break
		}
		cursor = output.ResponseMetadata.Cursor
	}

	return members, nil
}
