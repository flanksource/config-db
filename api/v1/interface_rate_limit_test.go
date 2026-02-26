package v1

import (
	"errors"
	"testing"
	"time"
)

func TestRateLimited(t *testing.T) {
	resetAt := time.Now().Add(10 * time.Minute)
	var results ScrapeResults
	results.RateLimited("test rate limit", &resetAt)

	if !results.IsRateLimited() {
		t.Fatal("expected IsRateLimited() to return true")
	}

	if !results.HasErr() {
		t.Fatal("expected HasErr() to return true for rate limited results")
	}

	got := results.GetRateLimitResetAt()
	if got == nil {
		t.Fatal("expected GetRateLimitResetAt() to return non-nil")
	}
	if !got.Equal(resetAt) {
		t.Fatalf("expected reset at %v, got %v", resetAt, got)
	}

	if !errors.Is(results[0].Error, ErrRateLimited) {
		t.Fatal("expected error to wrap ErrRateLimited")
	}
}

func TestRateLimitedNilResetAt(t *testing.T) {
	var results ScrapeResults
	results.RateLimited("rate limit without reset", nil)

	if !results.IsRateLimited() {
		t.Fatal("expected IsRateLimited() to return true")
	}

	if results.GetRateLimitResetAt() != nil {
		t.Fatal("expected GetRateLimitResetAt() to return nil")
	}
}

func TestIsRateLimitedFalseForNormalResults(t *testing.T) {
	results := ScrapeResults{
		{ID: "test-1", Name: "normal"},
	}
	if results.IsRateLimited() {
		t.Fatal("expected IsRateLimited() to return false for normal results")
	}

	if results.GetRateLimitResetAt() != nil {
		t.Fatal("expected GetRateLimitResetAt() to return nil for normal results")
	}
}

func TestIsRateLimitedFalseForNonRateLimitErrors(t *testing.T) {
	results := ScrapeResults{
		{Error: errors.New("some other error")},
	}
	if results.IsRateLimited() {
		t.Fatal("expected IsRateLimited() to return false for non-rate-limit errors")
	}
}

func TestRateLimitedWithExistingResults(t *testing.T) {
	resetAt := time.Now().Add(5 * time.Minute)
	results := ScrapeResults{
		{ID: "existing-1", Name: "existing"},
	}
	results.RateLimited("hit limit", &resetAt)

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	if !results.IsRateLimited() {
		t.Fatal("expected IsRateLimited() to return true")
	}
}
