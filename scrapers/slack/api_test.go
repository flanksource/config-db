package slack

import (
	gocontext "context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"time"

	commonshttp "github.com/flanksource/commons/http"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
)

var _ = Describe("Slack API", func() {
	// pages serves one response per call to the same endpoint, so a test can
	// assert that every page is followed. The returned map records the query of
	// every call, keyed by endpoint.
	newServer := func(pages map[string][]any) (*SlackAPI, map[string][]url.Values) {
		calls := map[string][]url.Values{}
		served := map[string]int{}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			endpoint := r.URL.Path
			calls[endpoint] = append(calls[endpoint], r.URL.Query())

			responses, ok := pages[endpoint]
			Expect(ok).To(BeTrue(), "unexpected call to %s", endpoint)
			Expect(served[endpoint]).To(BeNumerically("<", len(responses)), "too many calls to %s", endpoint)

			w.Header().Set("Content-Type", "application/json")
			Expect(json.NewEncoder(w).Encode(responses[served[endpoint]])).To(Succeed())
			served[endpoint]++
		}))
		DeferCleanup(server.Close)

		// Configured exactly as NewSlackAPI does, minus the bearer token, so the
		// specs exercise the real retry behaviour.
		return &SlackAPI{client: configureSlackClient(commonshttp.NewClient(), server.URL, 3, time.Millisecond)}, calls
	}

	cursors := func(calls []url.Values) []string {
		return lo.Map(calls, func(q url.Values, _ int) string { return q.Get("cursor") })
	}

	It("identifies the workspace behind the token", func() {
		client, _ := newServer(map[string][]any{
			"/auth.test": {map[string]any{"ok": true, "team": "Acme", "team_id": "T0ACME", "url": "https://acme.slack.com/"}},
		})

		Expect(client.AuthTest(gocontext.Background())).To(Equal(Workspace{
			ID:   "T0ACME",
			Name: "Acme",
			URL:  "https://acme.slack.com/",
		}))
	})

	It("surfaces a slack error instead of an empty workspace", func() {
		client, _ := newServer(map[string][]any{
			"/auth.test": {map[string]any{"ok": false, "error": "invalid_auth"}},
		})

		_, err := client.AuthTest(gocontext.Background())
		Expect(err).To(MatchError(ContainSubstring("invalid_auth")))
	})

	It("follows every page of users.list", func() {
		client, calls := newServer(map[string][]any{
			"/users.list": {
				map[string]any{
					"ok":                true,
					"members":           []UserInfo{{ID: "U0ALICE", Name: "alice"}},
					"response_metadata": map[string]string{"next_cursor": "page2"},
				},
				map[string]any{
					"ok":      true,
					"members": []UserInfo{{ID: "U0BOB", Name: "bob"}},
				},
			},
		})

		Expect(client.PopulateUsers(gocontext.Background())).To(Succeed())
		Expect(lo.Keys(client.Users())).To(ConsistOf("U0ALICE", "U0BOB"))
		Expect(cursors(calls["/users.list"])).To(Equal([]string{"", "page2"}))
	})

	It("follows every page of conversations.members", func() {
		client, calls := newServer(map[string][]any{
			"/conversations.members": {
				map[string]any{
					"ok":                true,
					"members":           []string{"U0ALICE"},
					"response_metadata": map[string]string{"next_cursor": "page2"},
				},
				map[string]any{
					"ok":      true,
					"members": []string{"U0BOB"},
				},
			},
		})

		Expect(client.ConversationMembers(gocontext.Background(), ChannelDetail{ID: "C0GENERAL"})).
			To(Equal([]string{"U0ALICE", "U0BOB"}))
		Expect(cursors(calls["/conversations.members"])).To(Equal([]string{"", "page2"}))
	})

	It("reports which channel could not be listed", func() {
		client, _ := newServer(map[string][]any{
			"/conversations.members": {map[string]any{"ok": false, "error": "channel_not_found"}},
		})

		_, err := client.ConversationMembers(gocontext.Background(), ChannelDetail{ID: "C0GONE", Name: "gone"})
		Expect(err).To(MatchError(ContainSubstring("channel_not_found")))
		Expect(err).To(MatchError(ContainSubstring("C0GONE")))
	})

	It("decodes the channel fields the config item is built from", func() {
		client, _ := newServer(map[string][]any{
			"/conversations.list": {map[string]any{
				"ok": true,
				"channels": []map[string]any{{
					"id":          "C0GENERAL",
					"name":        "general",
					"created":     1700000000,
					"creator":     "U0ALICE",
					"is_channel":  true,
					"is_private":  true,
					"is_archived": true,
					"num_members": 42,
					"topic":       map[string]any{"value": "Company wide announcements"},
					"purpose":     map[string]any{"value": "Everyone"},
				}},
			}},
		})

		Expect(client.ListConversations(gocontext.Background())).To(Equal([]ChannelDetail{{
			ID:         "C0GENERAL",
			Name:       "general",
			Created:    1700000000,
			Creator:    "U0ALICE",
			IsChannel:  true,
			IsPrivate:  true,
			IsArchived: true,
			NumMembers: 42,
			Topic:      ChannelText{Value: "Company wide announcements"},
			Purpose:    ChannelText{Value: "Everyone"},
		}}))
	})

	Describe("rate limiting", func() {
		// newThrottledServer answers with 429 for the first rateLimited calls and
		// then serves members, recording what Retry-After it advertised.
		newThrottledServer := func(rateLimited int, retryAfter string) (*SlackAPI, *int) {
			attempts := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempts++
				if attempts <= rateLimited {
					w.Header().Set("Retry-After", retryAfter)
					w.WriteHeader(http.StatusTooManyRequests)
					return
				}

				w.Header().Set("Content-Type", "application/json")
				Expect(json.NewEncoder(w).Encode(map[string]any{"ok": true, "members": []string{"U0ALICE"}})).To(Succeed())
			}))
			DeferCleanup(server.Close)

			return &SlackAPI{client: configureSlackClient(commonshttp.NewClient(), server.URL, 3, time.Millisecond)}, &attempts
		}

		It("retries a 429 and returns the members once slack lets it through", func() {
			client, attempts := newThrottledServer(2, "0")

			Expect(client.ConversationMembers(gocontext.Background(), ChannelDetail{ID: "C0GENERAL"})).
				To(Equal([]string{"U0ALICE"}))
			Expect(*attempts).To(Equal(3))
		})

		It("waits for the Retry-After the header asks for", func() {
			client, _ := newThrottledServer(1, "1")

			start := time.Now()
			_, err := client.ConversationMembers(gocontext.Background(), ChannelDetail{ID: "C0GENERAL"})
			Expect(err).NotTo(HaveOccurred())
			Expect(time.Since(start)).To(BeNumerically(">=", time.Second))
		})

		It("gives up after the attempt budget with an explicit error", func() {
			client, attempts := newThrottledServer(99, "0")

			_, err := client.ConversationMembers(gocontext.Background(), ChannelDetail{ID: "C0GENERAL"})
			Expect(err).To(MatchError(ContainSubstring("429")))
			Expect(err).To(MatchError(ContainSubstring("conversations.members")))
			Expect(*attempts).To(Equal(3))
		})
	})

	It("never asks slack for direct messages", func() {
		client, calls := newServer(map[string][]any{
			"/conversations.list": {map[string]any{"ok": true, "channels": []map[string]any{}}},
		})

		_, err := client.ListConversations(gocontext.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(calls["/conversations.list"]).To(HaveLen(1))
		Expect(calls["/conversations.list"][0].Get("types")).To(Equal("public_channel,private_channel"))
	})
})
