package github

import "testing"

func TestGithubRateLimitPauseThreshold(t *testing.T) {
	tests := []struct {
		name  string
		limit int
		want  int
	}{
		{name: "unauthenticated", limit: 60, want: 6},
		{name: "authenticated", limit: 5000, want: 100},
		{name: "small limit", limit: 10, want: 1},
		{name: "zero limit", limit: 0, want: 1},
		{name: "negative limit", limit: -1, want: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := githubRateLimitPauseThreshold(tt.limit); got != tt.want {
				t.Fatalf("githubRateLimitPauseThreshold(%d) = %d, want %d", tt.limit, got, tt.want)
			}
		})
	}
}
