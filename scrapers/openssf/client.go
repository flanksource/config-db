package openssf

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/flanksource/config-db/api"
)

const (
	OpenSSFScorecardAPIBase = "https://api.securityscorecards.dev"
	defaultTimeout          = 30 * time.Second
	maxRetries              = 3
)

// ScorecardClient handles requests to the OpenSSF Scorecard API
type ScorecardClient struct {
	httpClient *http.Client
	ctx        api.ScrapeContext
}

// NewScorecardClient creates a new OpenSSF Scorecard API client
func NewScorecardClient(ctx api.ScrapeContext) *ScorecardClient {
	return &ScorecardClient{
		httpClient: &http.Client{
			Timeout: defaultTimeout,
		},
		ctx: ctx,
	}
}

// ScorecardResponse represents the response from the OpenSSF Scorecard API
type ScorecardResponse struct {
	Date      time.Time      `json:"date"`
	Repo      RepoInfo       `json:"repo"`
	Scorecard ScorecardInfo  `json:"scorecard"`
	Score     float64        `json:"score"`
	Checks    []CheckResult  `json:"checks"`
}

// RepoInfo contains repository metadata
type RepoInfo struct {
	Name   string `json:"name"`
	Commit string `json:"commit"`
}

// ScorecardInfo contains scorecard version metadata
type ScorecardInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
}

// CheckResult represents a single security check result
type CheckResult struct {
	Name          string        `json:"name"`
	Score         int           `json:"score"`
	Reason        string        `json:"reason"`
	Details       []string      `json:"details"`
	Documentation Documentation `json:"documentation"`
}

// Documentation contains check documentation links
type Documentation struct {
	URL   string `json:"url"`
	Short string `json:"short"`
}

// GetRepositoryScorecard fetches the scorecard data for a repository
func (c *ScorecardClient) GetRepositoryScorecard(ctx context.Context, owner, repo string) (*ScorecardResponse, error) {
	url := fmt.Sprintf("%s/projects/github.com/%s/%s", OpenSSFScorecardAPIBase, owner, repo)
	repoFullName := fmt.Sprintf("%s/%s", owner, repo)

	c.ctx.Tracef("making API request to %s", url)

	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(attempt*attempt) * time.Second
			c.ctx.Logger.V(3).Infof("retrying scorecard request for %s (attempt %d/%d) after %v",
				repoFullName, attempt+1, maxRetries, backoff)
			time.Sleep(backoff)
		}

		scorecard, err := c.fetchScorecard(ctx, url)
		if err == nil {
			c.ctx.Tracef("successfully fetched scorecard for %s: score=%.1f", repoFullName, scorecard.Score)
			return scorecard, nil
		}

		lastErr = err
		c.ctx.Tracef("attempt %d failed for %s: %v", attempt+1, repoFullName, err)

		if !c.isRetryable(err) {
			c.ctx.Debugf("error not retryable for %s: %v", repoFullName, err)
			return nil, err
		}
	}

	return nil, fmt.Errorf("failed after %d attempts: %w", maxRetries, lastErr)
}

func (c *ScorecardClient) fetchScorecard(ctx context.Context, url string) (*ScorecardResponse, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("repository not found or not assessed by OpenSSF Scorecard (404)")
	}

	if resp.StatusCode >= 500 {
		return nil, fmt.Errorf("server error: %d", resp.StatusCode)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var scorecard ScorecardResponse
	if err := json.Unmarshal(body, &scorecard); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &scorecard, nil
}

func (c *ScorecardClient) isRetryable(err error) bool {
	if err == nil {
		return false
	}

	errStr := err.Error()
	return contains(errStr, "server error") ||
	       contains(errStr, "timeout") ||
	       contains(errStr, "connection")
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) &&
		(s[:len(substr)] == substr || s[len(s)-len(substr):] == substr ||
		 findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
