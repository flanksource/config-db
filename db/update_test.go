package db

import (
	"os"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func Test_generateDiff(t *testing.T) {
	type args struct {
		newConf string
		prev    string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "simple",
			args: args{
				newConf: "testdata/simple-new.json",
				prev:    "testdata/simple-old.json",
			},
			want: "testdata/simple.diff",
		},
		{
			name: "person",
			args: args{
				newConf: "testdata/person-new.json",
				prev:    "testdata/person-old.json",
			},
			want: "testdata/person.diff",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := generateDiff(readFile(t, tt.args.newConf), readFile(t, tt.args.prev))
			if err != nil {
				t.Errorf("generateDiff() error = %v", err)
				return
			}

			wan := readFile(t, tt.want)
			if diff := cmp.Diff(wan, got); diff != "" {
				t.Errorf("generateDiff() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	f, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to open file (path=%s): %v", path, err)
	}

	return string(f)
}
