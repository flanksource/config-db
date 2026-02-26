package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty/models"
	"github.com/flanksource/is-healthy/pkg/health"
)

const (
	OpenSSFScorecardAPIBase = "https://api.securityscorecards.dev"
	scorecardCacheTTL       = 24 * time.Hour
	maxRetries              = 3

	scorecardStatusFailing = "failing"
	scorecardStatusPassing = "passing"
)

var LastScorecardScrapeTime = sync.Map{}

type ScorecardResponse struct {
	Date      time.Time     `json:"date"`
	Repo      RepoInfo      `json:"repo"`
	Scorecard ScorecardInfo `json:"scorecard"`
	Score     float64       `json:"score"`
	Checks    []CheckResult `json:"checks"`
}

type RepoInfo struct {
	Name   string `json:"name"`
	Commit string `json:"commit"`
}

type ScorecardInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
}

type CheckResult struct {
	Name          string        `json:"name"`
	Score         int           `json:"score"`
	Reason        string        `json:"reason"`
	Details       []string      `json:"details"`
	Documentation Documentation `json:"documentation"`
}

type Documentation struct {
	URL   string `json:"url"`
	Short string `json:"short"`
}

func scrapeOpenSSFScorecard(ctx api.ScrapeContext, repoConfig v1.GitHubRepository) (*ScorecardResponse, error) {
	repoFullName := fmt.Sprintf("%s/%s", repoConfig.Owner, repoConfig.Repo)

	if lastScrape, ok := LastScorecardScrapeTime.Load(repoFullName); ok {
		if t, ok := lastScrape.(time.Time); ok && time.Since(t) < scorecardCacheTTL {
			ctx.Debugf("skipping OpenSSF for %s: cached within TTL", repoFullName)
			return nil, nil
		}
	}

	scorecard, err := fetchScorecard(ctx, repoConfig.Owner, repoConfig.Repo)
	if err != nil {
		return nil, err
	}

	LastScorecardScrapeTime.Store(repoFullName, time.Now())
	return scorecard, nil
}

func fetchScorecard(ctx context.Context, owner, repo string) (*ScorecardResponse, error) {
	url := fmt.Sprintf("%s/projects/github.com/%s/%s", OpenSSFScorecardAPIBase, owner, repo)
	httpClient := &http.Client{Timeout: 30 * time.Second}

	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			timer := time.NewTimer(time.Duration(attempt*attempt) * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
		}

		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}

		resp, err := httpClient.Do(req)
		if err != nil {
			lastErr = err
			if isRetryable(err) {
				continue
			}
			return nil, err
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf("repository not found or not assessed by OpenSSF Scorecard (404)")
		}
		if resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("server error: %d", resp.StatusCode)
			continue
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

	return nil, fmt.Errorf("failed after %d attempts: %w", maxRetries, lastErr)
}

func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	return strings.Contains(err.Error(), "server error") || strings.Contains(err.Error(), "connection")
}

func createScorecardAnalyses(_ api.ScrapeContext, results *v1.ScrapeResults, configID string, _ v1.GitHubRepository, scorecard *ScorecardResponse) {
	for _, check := range scorecard.Checks {
		a := results.Analysis(check.Name, ConfigTypeRepository, configID)
		a.AnalysisType = models.AnalysisTypeSecurity
		a.Severity = mapCheckScoreToSeverity(check.Score)
		a.Source = "OpenSSF Scorecard"
		a.Summary = check.Reason
		a.FirstObserved = &scorecard.Date
		a.LastObserved = &scorecard.Date

		if check.Score < 10 {
			a.Status = scorecardStatusFailing
		} else {
			a.Status = scorecardStatusPassing
		}

		for _, detail := range check.Details {
			a.Message(detail)
		}

		a.Analysis = map[string]any{
			"check_name": check.Name,
			"score":      check.Score,
			"max_score":  10,
			"reason":     check.Reason,
			"details":    check.Details,
			"documentation": map[string]string{
				"url":   check.Documentation.URL,
				"short": check.Documentation.Short,
			},
		}
	}
}

func calculateScorecardHealthStatus(scorecard *ScorecardResponse) health.HealthStatus {
	status := health.HealthStatus{Ready: true}

	criticalChecks := []string{"Code-Review", "SAST", "Token-Permissions", "Dangerous-Workflow", "Branch-Protection"}
	var failedCritical []string

	for _, check := range scorecard.Checks {
		for _, critical := range criticalChecks {
			if check.Score == 0 && check.Name == critical {
				failedCritical = append(failedCritical, check.Name)
			}
		}
	}

	switch {
	case scorecard.Score >= 7.0 && len(failedCritical) == 0:
		status.Health = health.HealthHealthy
		status.Message = fmt.Sprintf("Security score: %.1f/10", scorecard.Score)
	case scorecard.Score < 4.0 || len(failedCritical) > 0:
		status.Health = health.HealthUnhealthy
		if len(failedCritical) > 0 {
			status.Message = fmt.Sprintf("Security score: %.1f/10, critical checks failing: %s",
				scorecard.Score, strings.Join(failedCritical, ", "))
		} else {
			status.Message = fmt.Sprintf("Security score: %.1f/10", scorecard.Score)
		}
	default:
		status.Health = health.HealthWarning
		status.Message = fmt.Sprintf("Security score: %.1f/10", scorecard.Score)
	}

	return status
}

func mapCheckScoreToSeverity(score int) models.Severity {
	if score <= 3 {
		return models.SeverityCritical
	} else if score <= 6 {
		return models.SeverityHigh
	} else if score <= 9 {
		return models.SeverityMedium
	}
	return models.SeverityLow
}
