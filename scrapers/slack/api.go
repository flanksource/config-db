package slack

import (
	"context"
	"fmt"
	"strconv"

	"github.com/flanksource/commons/http"
)

type GetConversationHistoryParameters struct {
	Cursor string
	Oldest int64
}

// ResponseMetadata holds pagination metadata
type ResponseMetadata struct {
	Cursor   string   `json:"next_cursor"`
	Messages []string `json:"messages"`
	Warnings []string `json:"warnings"`
}

type Message struct {
	ClientMsgID string `json:"client_msg_id,omitempty"`
	Type        string `json:"type,omitempty"`
	Channel     string `json:"channel,omitempty"`
	User        string `json:"user,omitempty"`
	Text        string `json:"text,omitempty"`
	Timestamp   string `json:"ts,omitempty"`
	Team        string `json:"team,omitempty"`
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
	client *http.Client
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
	Ok       bool   `json:"ok"`
	Error    string `json:"error"`
	Channels []ChannelDetail
}

func (t *SlackAPI) ListConversations(ctx context.Context) (*ConversationList, error) {
	response, err := t.client.R(ctx).QueryParam("types", "public_channel,private_channel").Post("conversations.list", nil)
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

	return &output, nil
}

func (t *SlackAPI) getSlackConversationHistory(ctx context.Context, channel ChannelDetail, params *GetConversationHistoryParameters) (GetConversationHistoryResponse, error) {
	var output GetConversationHistoryResponse

	req := t.client.R(ctx).QueryParam("channel", channel.ID).QueryParam("inclusive", "1").QueryParam("limit", "2")
	if params.Cursor != "" {
		req.QueryParam("cursor", params.Cursor)
	}
	if params.Oldest != 0 {
		req.QueryParam("oldest", strconv.FormatInt(params.Oldest, 10))
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

	return output, nil
}
