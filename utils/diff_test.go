package utils

import (
	"os"
	"testing"
)

func TestIsReorderingDiff(t *testing.T) {
	tests := []struct {
		name string
		diff string
		want bool
	}{
		{
			name: "reordered lines",
			diff: "testdata/reordered.diff",
			want: true,
		},
		{
			name: "new line addition with reordered lines",
			diff: "testdata/non-reordered.diff",
			want: false,
		},
		{
			name: "reordered lines",
			diff: "testdata/number-reordered.diff",
			want: true,
		},
		{
			name: "config change",
			diff: "testdata/config-change.diff",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			diff, err := os.ReadFile(tt.diff)
			if err != nil {
				t.Errorf("error reading file: %v", err)
			}

			if got := IsReorderingDiff(string(diff)); got != tt.want {
				t.Errorf("IsDiffAnOrderChange() = %v, want %v", got, tt.want)
			}
		})
	}
}
