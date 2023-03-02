package utils

import "testing"

func TestSha256Hex(t *testing.T) {
	tests := []struct {
		name string
		args string
		want string
	}{
		{name: "first", args: "flanksource", want: "bba09cfc0321b05968bd39bb2e96e4a6bb5f5d3069dcf74ab0772118b7f7258f"},
		{name: "first", args: "programmer", want: "7bd9ca7a756115eabdff2ab281ee9d8c22f44b51d97a6801169d65d90ff16327"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Sha256Hex(tt.args); got != tt.want {
				t.Errorf("Sha256Hex() = %v, want %v", got, tt.want)
			}
		})
	}
}
