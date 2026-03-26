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
)

type scorecardCacheEntry struct {
	fetchedAt time.Time
	scorecard *ScorecardResponse
}

var scorecardCache = sync.Map{}

// flexDateFormats lists all time formats the OpenSSF Scorecard API has been
// observed to return for the "date" field, in preference order.
var flexDateFormats = []string{
	time.RFC3339,
	time.RFC3339Nano,
	"2006-01-02", // date-only, returned by older scorecard versions
}

// FlexTime is a time.Time that can unmarshal both RFC3339 and date-only
// ("2006-01-02") JSON strings to handle variation across OpenSSF scorecard
// API versions.
type FlexTime struct {
	time.Time
}

func (ft *FlexTime) UnmarshalJSON(data []byte) error {
	// JSON strings are quoted; strip the quotes.
	s := strings.Trim(string(data), `"`)
	for _, format := range flexDateFormats {
		if t, err := time.Parse(format, s); err == nil {
			ft.Time = t
			return nil
		}
	}
	return fmt.Errorf("cannot parse %q as a time (tried RFC3339, RFC3339Nano, date-only)", s)
}

type ScorecardResponse struct {
	Date      FlexTime      `json:"date"`
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

// checkRiskLevel maps OpenSSF Scorecard check names to their inherent risk severity.
// Source: https://github.com/ossf/scorecard/blob/main/docs/checks.md
var checkRiskLevel = map[string]models.Severity{
	"Binary-Artifacts":       models.SeverityHigh,
	"Branch-Protection":      models.SeverityHigh,
	"CI-Tests":               models.SeverityLow,
	"CII-Best-Practices":     models.SeverityLow,
	"Code-Review":            models.SeverityHigh,
	"Contributors":           models.SeverityLow,
	"Dangerous-Workflow":     models.SeverityCritical,
	"Dependency-Update-Tool": models.SeverityHigh,
	"Fuzzing":                models.SeverityMedium,
	"License":                models.SeverityLow,
	"Maintained":             models.SeverityHigh,
	"Packaging":              models.SeverityMedium,
	"Pinned-Dependencies":    models.SeverityMedium,
	"SAST":                   models.SeverityMedium,
	"Security-Policy":        models.SeverityMedium,
	"Signed-Releases":        models.SeverityHigh,
	"Token-Permissions":      models.SeverityHigh,
	"Vulnerabilities":        models.SeverityHigh,
	"Webhooks":               models.SeverityCritical,
}

func scrapeOpenSSFScorecard(ctx api.ScrapeContext, repoConfig v1.GitHubRepository) (*ScorecardResponse, error) {
	repoFullName := fmt.Sprintf("%s/%s", repoConfig.Owner, repoConfig.Repo)

	if cached, ok := scorecardCache.Load(repoFullName); ok {
		if entry, ok := cached.(scorecardCacheEntry); ok && time.Since(entry.fetchedAt) < scorecardCacheTTL {
			ctx.Debugf("returning cached OpenSSF scorecard for %s (age: %s)", repoFullName, time.Since(entry.fetchedAt).Round(time.Second))
			return entry.scorecard, nil
		}
	}

	scorecard, err := fetchScorecard(ctx, repoConfig.Owner, repoConfig.Repo)
	if err != nil {
		return nil, err
	}

	scorecardCache.Store(repoFullName, scorecardCacheEntry{fetchedAt: time.Now(), scorecard: scorecard})
	return scorecard, nil
}

func fetchScorecard(ctx context.Context, owner, repo string) (*ScorecardResponse, error) {
	url := fmt.Sprintf("%s/projects/github.com/%s/%s", OpenSSFScorecardAPIBase, owner, repo)
	httpClient := &http.Client{Timeout: 30 * time.Second}

	var lastErr error
	for attempt := range maxRetries {
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

		var scorecard ScorecardResponse
		if err := json.NewDecoder(resp.Body).Decode(&scorecard); err != nil {
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

func createScorecardAnalyses(ctx api.ScrapeContext, results *v1.ScrapeResults, configID string, _ v1.GitHubRepository, scorecard *ScorecardResponse) {
	for _, check := range scorecard.Checks {
		a := results.Analysis(check.Name, ConfigTypeRepository, configID)
		a.AnalysisType = models.AnalysisTypeSecurity
		if sev, ok := checkRiskLevel[check.Name]; ok {
			a.Severity = sev
		} else {
			ctx.Warnf("unknown OpenSSF check %q, defaulting severity to info", check.Name)
			a.Severity = models.SeverityInfo
		}
		a.Source = "OpenSSF Scorecard"
		a.Summary = check.Reason
		a.Status = statusFromScore(check.Score)
		observedAt := scorecard.Date.Time
		a.FirstObserved = &observedAt
		a.LastObserved = &observedAt

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

func statusFromScore(score int) string {
	if score == 10 {
		return models.AnalysisStatusResolved
	}

	return models.AnalysisStatusOpen
}

func calculateScorecardHealthStatus(scorecard *ScorecardResponse) health.HealthStatus {
	status := health.HealthStatus{Ready: true}

	var criticalChecks []string
	for name, sev := range checkRiskLevel {
		if sev == models.SeverityCritical || sev == models.SeverityHigh {
			criticalChecks = append(criticalChecks, name)
		}
	}

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
