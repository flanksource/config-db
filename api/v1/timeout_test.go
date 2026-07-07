package v1

import (
	"testing"
	"time"
)

func TestScraperSpecTimeout(t *testing.T) {
	defaultTimeout := 4 * time.Hour

	t.Run("default", func(t *testing.T) {
		got, err := (ScraperSpec{}).TimeoutDuration(defaultTimeout)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != defaultTimeout {
			t.Fatalf("expected %s, got %s", defaultTimeout, got)
		}
	})

	t.Run("explicit timeout shorter than default", func(t *testing.T) {
		got, err := (ScraperSpec{Timeout: "30m"}).TimeoutDuration(defaultTimeout)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != 30*time.Minute {
			t.Fatalf("expected 30m, got %s", got)
		}
	})

	t.Run("extended duration units", func(t *testing.T) {
		got, err := (ScraperSpec{Timeout: "1d"}).TimeoutDuration(defaultTimeout)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != 24*time.Hour {
			t.Fatalf("expected 24h, got %s", got)
		}
	})

	t.Run("invalid timeout", func(t *testing.T) {
		_, err := (ScraperSpec{Timeout: "soon"}).TimeoutDuration(defaultTimeout)
		if err == nil {
			t.Fatal("expected error")
		}
	})
}
