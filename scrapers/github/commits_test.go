package github

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/flanksource/config-db/api"
	dutyCtx "github.com/flanksource/duty/context"
	gogithub "github.com/google/go-github/v73/github"
)

func TestResolveCommitMaxAge(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    time.Duration
		wantErr bool
	}{
		{name: "default", want: 30 * 24 * time.Hour},
		{name: "days", value: "30d", want: 30 * 24 * time.Hour},
		{name: "combined", value: "1w2d", want: 9 * 24 * time.Hour},
		{name: "invalid", value: "recent", wantErr: true},
		{name: "zero", value: "0", wantErr: true},
		{name: "negative", value: "-1h", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveCommitMaxAge(tt.value)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("resolveCommitMaxAge(%q) expected an error", tt.value)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveCommitMaxAge(%q) returned an error: %v", tt.value, err)
			}
			if got != tt.want {
				t.Fatalf("resolveCommitMaxAge(%q) = %s, want %s", tt.value, got, tt.want)
			}
		})
	}
}

func TestRepositoryCommitToChangeResult(t *testing.T) {
	authoredAt := time.Date(2026, time.July, 2, 9, 15, 0, 0, time.UTC)
	committedAt := authoredAt.Add(time.Minute)
	commit := &gogithub.RepositoryCommit{
		SHA:     gogithub.Ptr("abc123"),
		HTMLURL: gogithub.Ptr("https://github.com/flanksource/config-db/commit/abc123"),
		Author:  &gogithub.User{Login: gogithub.Ptr("alice")},
		Commit: &gogithub.Commit{
			Message: gogithub.Ptr("Add commit scraping\n\nInclude commit metadata."),
			Author: &gogithub.CommitAuthor{
				Name:  gogithub.Ptr("Alice Example"),
				Email: gogithub.Ptr("alice@example.com"),
				Date:  &gogithub.Timestamp{Time: authoredAt},
			},
			Committer: &gogithub.CommitAuthor{Date: &gogithub.Timestamp{Time: committedAt}},
		},
	}

	change, ok := repositoryCommitToChangeResult("github/flanksource/config-db", commit)
	if !ok {
		t.Fatal("repositoryCommitToChangeResult unexpectedly rejected a commit with a SHA")
	}
	if change.ExternalID != "github/flanksource/config-db" {
		t.Fatalf("ExternalID = %q", change.ExternalID)
	}
	if change.ConfigType != ConfigTypeRepository {
		t.Fatalf("ConfigType = %q", change.ConfigType)
	}
	if change.ExternalChangeID != "abc123" {
		t.Fatalf("ExternalChangeID = %q", change.ExternalChangeID)
	}
	if change.ChangeType != changeTypeCommit {
		t.Fatalf("ChangeType = %q", change.ChangeType)
	}
	if change.Summary != "Add commit scraping\n\nInclude commit metadata." {
		t.Fatalf("Summary = %q", change.Summary)
	}
	if change.Source != "GitHub/Commit" {
		t.Fatalf("Source = %q", change.Source)
	}
	if change.Severity != "info" {
		t.Fatalf("Severity = %q", change.Severity)
	}
	if change.CreatedBy == nil || *change.CreatedBy != "alice@example.com" {
		t.Fatalf("CreatedBy = %v", change.CreatedBy)
	}
	if change.CreatedAt == nil || !change.CreatedAt.Equal(committedAt) {
		t.Fatalf("CreatedAt = %v", change.CreatedAt)
	}
	if change.Details["sha"] != "abc123" {
		t.Fatalf("Details sha = %v", change.Details["sha"])
	}
}

func TestScrapeRepositoryCommitsPaginates(t *testing.T) {
	since := time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC)
	requestCount := 0

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		if r.URL.Path != "/repos/flanksource/config-db/commits" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if got := r.URL.Query().Get("since"); got != since.Format(time.RFC3339) {
			t.Errorf("since = %q, want %q", got, since.Format(time.RFC3339))
		}
		if got := r.URL.Query().Get("per_page"); got != "100" {
			t.Errorf("per_page = %q", got)
		}

		sha := "first"
		if requestCount == 1 {
			query := cloneQuery(r.URL.Query())
			query.Set("page", "2")
			w.Header().Set("Link", fmt.Sprintf("<%s%s?%s>; rel=\"next\"", server.URL, r.URL.Path, query.Encode()))
		} else {
			sha = "second"
			if got := r.URL.Query().Get("page"); got != "2" {
				t.Errorf("page = %q", got)
			}
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `[{"sha":%q,"commit":{"message":%q}}]`, sha, sha+" commit")
	}))
	defer server.Close()

	client := gogithub.NewClient(server.Client())
	client.BaseURL, _ = url.Parse(server.URL + "/")
	githubClient := &GitHubClient{Client: client, owner: "flanksource", repo: "config-db"}
	ctx := api.NewScrapeContext(dutyCtx.New())

	changes, err := scrapeRepositoryCommits(ctx, githubClient, "github/flanksource/config-db", since)
	if err != nil {
		t.Fatalf("scrapeRepositoryCommits returned an error: %v", err)
	}
	if requestCount != 2 {
		t.Fatalf("request count = %d, want 2", requestCount)
	}
	if len(changes) != 2 {
		t.Fatalf("change count = %d, want 2", len(changes))
	}
	if changes[0].ExternalChangeID != "first" || changes[1].ExternalChangeID != "second" {
		t.Fatalf("commit order = %q, %q", changes[0].ExternalChangeID, changes[1].ExternalChangeID)
	}
}

func TestScrapeRepositoryCommitsAllowsEmptyRepository(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"message":"Git Repository is empty."}`))
	}))
	defer server.Close()

	client := gogithub.NewClient(server.Client())
	client.BaseURL, _ = url.Parse(server.URL + "/")
	githubClient := &GitHubClient{Client: client, owner: "flanksource", repo: "empty"}
	ctx := api.NewScrapeContext(dutyCtx.New())

	changes, err := scrapeRepositoryCommits(ctx, githubClient, "github/flanksource/empty", time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("scrapeRepositoryCommits returned an error for an empty repository: %v", err)
	}
	if len(changes) != 0 {
		t.Fatalf("change count = %d, want 0", len(changes))
	}
}

func cloneQuery(query url.Values) url.Values {
	cloned := make(url.Values, len(query))
	for key, values := range query {
		cloned[key] = append([]string(nil), values...)
	}
	return cloned
}
