package github

import (
	"errors"
	"fmt"
	gohttp "net/http"
	"strings"
	"time"

	"github.com/flanksource/commons/duration"
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/google/go-github/v73/github"
)

const (
	changeTypeCommit    = "Commit"
	defaultCommitMaxAge = 30 * 24 * time.Hour
)

func resolveCommitMaxAge(value string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return defaultCommitMaxAge, nil
	}

	parsed, err := duration.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("invalid maxAge %q: %w", value, err)
	}
	if parsed <= 0 {
		return 0, fmt.Errorf("maxAge must be greater than zero")
	}

	return time.Duration(parsed), nil
}

func scrapeRepositoryCommits(
	ctx api.ScrapeContext,
	client *GitHubClient,
	externalConfigID string,
	since time.Time,
) ([]v1.ChangeResult, error) {
	options := &github.CommitsListOptions{
		Since: since,
		ListOptions: github.ListOptions{
			PerPage: 100,
		},
	}

	var changes []v1.ChangeResult
	for {
		commits, response, err := client.Client.Repositories.ListCommits(ctx, client.owner, client.repo, options)
		if err != nil {
			if isEmptyRepository(err) {
				return changes, nil
			}
			return nil, err
		}

		for _, commit := range commits {
			if change, ok := repositoryCommitToChangeResult(externalConfigID, commit); ok {
				changes = append(changes, change)
			}
		}

		if response == nil || response.NextPage == 0 {
			break
		}
		options.Page = response.NextPage
	}

	return changes, nil
}

func repositoryCommitToChangeResult(externalConfigID string, commit *github.RepositoryCommit) (v1.ChangeResult, bool) {
	if commit == nil || commit.GetSHA() == "" {
		return v1.ChangeResult{}, false
	}

	return v1.ChangeResult{
		ExternalID:       externalConfigID,
		ConfigType:       ConfigTypeRepository,
		ExternalChangeID: commit.GetSHA(),
		ChangeType:       changeTypeCommit,
		Summary:          strings.TrimSpace(commit.GetCommit().GetMessage()),
		Severity:         "info",
		Source:           "GitHub/Commit",
		CreatedBy:        repositoryCommitCreatedBy(commit),
		CreatedAt:        repositoryCommitCreatedAt(commit),
		Details:          v1.NewJSON(commit),
	}, true
}

func repositoryCommitCreatedBy(commit *github.RepositoryCommit) *string {
	if email := commit.GetCommit().GetAuthor().GetEmail(); email != "" {
		return &email
	}
	if login := commit.GetAuthor().GetLogin(); login != "" {
		return &login
	}
	if name := commit.GetCommit().GetAuthor().GetName(); name != "" {
		return &name
	}
	return nil
}

func repositoryCommitCreatedAt(commit *github.RepositoryCommit) *time.Time {
	if committedAt := commit.GetCommit().GetCommitter().GetDate().Time; !committedAt.IsZero() {
		return &committedAt
	}
	if authoredAt := commit.GetCommit().GetAuthor().GetDate().Time; !authoredAt.IsZero() {
		return &authoredAt
	}
	return nil
}

func isEmptyRepository(err error) bool {
	var response *github.ErrorResponse
	return errors.As(err, &response) &&
		response.Response != nil &&
		response.Response.StatusCode == gohttp.StatusConflict &&
		strings.Contains(strings.ToLower(response.Message), "repository is empty")
}
