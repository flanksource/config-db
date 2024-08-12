package db

import (
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/flanksource/config-db/db/models"
	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/samber/lo"
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

func Test_dedupChanges(t *testing.T) {
	type args struct {
		window  time.Duration
		changes []*models.ConfigChange
	}
	tests := []struct {
		name     string
		args     args
		deduped  []*models.ConfigChange
		nonDuped []*models.ConfigChange
	}{
		{
			name: "",
			args: args{
				window: time.Hour,
				changes: []*models.ConfigChange{
					{ID: "8b9d2659-7a11-46ff-bdff-1c4e8964c437", CreatedAt: time.Date(2024, 01, 02, 0, 0, 0, 0, time.UTC), Fingerprint: lo.ToPtr("abc"), ConfigID: "dae6b3f5-bc26-48ac-8ad4-06e5efbb2a7d", Summary: "first", Count: 1},
					{ID: uuid.NewString(), CreatedAt: time.Date(2024, 02, 02, 0, 0, 0, 0, time.UTC), Fingerprint: lo.ToPtr("abc"), ConfigID: "dae6b3f5-bc26-48ac-8ad4-06e5efbb2a7d", Summary: "second", Count: 1},
					{ID: uuid.NewString(), CreatedAt: time.Date(2024, 03, 02, 0, 0, 0, 0, time.UTC), Fingerprint: lo.ToPtr("abc"), ConfigID: "dae6b3f5-bc26-48ac-8ad4-06e5efbb2a7d", Summary: "third", Count: 1},
					{ID: uuid.NewString(), CreatedAt: time.Date(2024, 04, 02, 0, 0, 0, 0, time.UTC), Fingerprint: lo.ToPtr("abc"), ConfigID: "dae6b3f5-bc26-48ac-8ad4-06e5efbb2a7d", Summary: "fourth", Count: 1},
					{ID: "01eda583-3f5e-4c44-851f-93ac73272b92", CreatedAt: time.Date(2024, 04, 02, 0, 0, 0, 0, time.UTC), Fingerprint: lo.ToPtr("xyz"), ConfigID: "dae6b3f5-bc26-48ac-8ad4-06e5efbb2a7d", Summary: "different", Count: 1},
				},
			},
			deduped: []*models.ConfigChange{
				{
					ID:            "8b9d2659-7a11-46ff-bdff-1c4e8964c437",
					CreatedAt:     time.Date(2024, 04, 02, 0, 0, 0, 0, time.UTC),
					FirstObserved: lo.ToPtr(time.Date(2024, 01, 02, 0, 0, 0, 0, time.UTC)),
					Fingerprint:   lo.ToPtr("abc"),
					ConfigID:      "dae6b3f5-bc26-48ac-8ad4-06e5efbb2a7d",
					Summary:       "fourth",
					Count:         4,
				},
			},
			nonDuped: []*models.ConfigChange{
				{
					ID:          "01eda583-3f5e-4c44-851f-93ac73272b92",
					CreatedAt:   time.Date(2024, 04, 02, 0, 0, 0, 0, time.UTC),
					Fingerprint: lo.ToPtr("xyz"),
					ConfigID:    "dae6b3f5-bc26-48ac-8ad4-06e5efbb2a7d",
					Summary:     "different",
					Count:       1,
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nonDuped, deduped := dedupChanges(tt.args.window, tt.args.changes)
			if !reflect.DeepEqual(deduped, tt.deduped) {
				t.Errorf("dedupChanges() = %v, deduped %v", deduped, tt.deduped)
			}
			if !reflect.DeepEqual(nonDuped, tt.nonDuped) {
				t.Errorf("dedupChanges() = %v, nonDuped %v", nonDuped, tt.nonDuped)
			}
		})
	}
}
