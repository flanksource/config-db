package slack

import (
	"context"
	"fmt"

	"github.com/flanksource/commons/http"
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

func NewSlackAPI(token string) *SlackAPI {
	return &SlackAPI{
		client: http.NewClient().BaseURL("https://slack.com/api/").
			Header("Authorization", "Bearer "+token).
			Header("Content-Type", "application/json"),
	}
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

type ChannelDetail struct {
	ID   string `json:"id"`
	Name string `json:"name"`
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
			QueryParam("types", "public_channel,private_channel,mpim,im").
			QueryParam("limit", "1000").
			QueryParam("cursor", cursor).
			Post("conversations.list", nil)
		if err != nil {
			return nil, err
		}

		var output ConversationList
		if err := response.Into(&output); err != nil {
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
	ID      string `json:"id"`
	Name    string `json:"name"`
	Profile struct {
		DisplayName string `json:"display_name"`
	} `json:"profile"`
}

type ListUsersResponse struct {
	Ok      bool       `json:"ok"`
	Error   string     `json:"error,omitempty"`
	Members []UserInfo `json:"members,omitempty"`
}

func (t *SlackAPI) PopulateUsers(ctx context.Context) error {
	response, err := t.client.R(ctx).Get("users.list")
	if err != nil {
		return err
	}

	var output ListUsersResponse
	if err := response.Into(&output); err != nil {
		return err
	}

	if output.Error != "" {
		return fmt.Errorf("failed to list users: %s", output.Error)
	}

	idToNameMap := make(map[string]UserInfo, len(output.Members))
	for _, m := range output.Members {
		idToNameMap[m.ID] = m
	}

	t.usersList = idToNameMap
	return nil
}
