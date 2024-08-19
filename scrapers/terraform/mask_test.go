package terraform

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"testing"
)

func Test_maskSensitiveAttributes(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
	}{
		{
			name:    "cloudflare.tfstate",
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content, err := os.ReadFile(fmt.Sprintf("testdata/%s", tt.name))
			if err != nil {
				t.Fatal(err)
			}

			var state State
			if err := json.Unmarshal(content, &state); err != nil {
				t.Fatal(err)
			}

			got, err := maskSensitiveAttributes(state, content)
			if (err != nil) != tt.wantErr {
				t.Errorf("maskSensitiveAttributes() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			expected, err := os.ReadFile(fmt.Sprintf("testdata/%s.expected", tt.name))
			if err != nil {
				t.Fatal(err)
			}

			var expectedMap map[string]any
			if err := json.Unmarshal(expected, &expectedMap); err != nil {
				t.Fatal(err)
			}

			if !reflect.DeepEqual(got, expectedMap) {
				t.Errorf("maskSensitiveAttributes() = %v, want %v", got, expectedMap)
			}
		})
	}
}
