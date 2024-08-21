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
		deduped  []models.ConfigChangeUpdate
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
			deduped: []models.ConfigChangeUpdate{
				{
					Change: &models.ConfigChange{
						ID:          "8b9d2659-7a11-46ff-bdff-1c4e8964c437",
						CreatedAt:   time.Date(2024, 04, 02, 0, 0, 0, 0, time.UTC),
						Fingerprint: lo.ToPtr("abc"),
						ConfigID:    "dae6b3f5-bc26-48ac-8ad4-06e5efbb2a7d",
						Summary:     "fourth",
						Count:       1,
					},
					CountIncrement: 3,
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
			if diff := cmp.Diff(deduped, tt.deduped); diff != "" {
				t.Errorf("%v", diff)
			}
			if !reflect.DeepEqual(nonDuped, tt.nonDuped) {
				t.Errorf("dedupChanges() = %v, nonDuped %v", nonDuped, tt.nonDuped)
			}
		})
	}
}

// go test -benchmem -run=^$ -bench ^BenchmarkDiffGenerator$ github.com/flanksource/config-db/db -count=5 -benchtime=10s -v -memprofile memprofile.out -cpuprofile profile.out
func BenchmarkDiffGenerator(b *testing.B) {
	// With: github.com/kylelemons/godebug
	//
	//
	// 19:02:50.112 INF Loaded 3 change rules
	// goos: linux
	// goarch: amd64
	// pkg: github.com/flanksource/config-db/db
	// cpu: Intel(R) Core(TM) i9-14900K
	// BenchmarkDiffGenerator
	// BenchmarkDiffGenerator-32          25059            206413 ns/op          185370 B/op       1127 allocs/op
	// BenchmarkDiffGenerator-32          26265            235656 ns/op          185358 B/op       1127 allocs/op
	// BenchmarkDiffGenerator-32          26893            212836 ns/op          185350 B/op       1127 allocs/op
	// BenchmarkDiffGenerator-32          26440            214815 ns/op          185338 B/op       1127 allocs/op
	// BenchmarkDiffGenerator-32          28935            204746 ns/op          185331 B/op       1127 allocs/op
	// PASS
	// ok      github.com/flanksource/config-db/db     40.295s

	// With "github.com/hexops/gotextdiff/myers" (32 GB total)
	//
	//
	// 19:04:03.564 INF Loaded 3 change rules
	// goos: linux
	// goarch: amd64
	// pkg: github.com/flanksource/config-db/db
	// cpu: Intel(R) Core(TM) i9-14900K
	// BenchmarkDiffGenerator
	// BenchmarkDiffGenerator-32          22522            257656 ns/op          199094 B/op       1142 allocs/op
	// BenchmarkDiffGenerator-32          23818            226056 ns/op          199070 B/op       1142 allocs/op
	// BenchmarkDiffGenerator-32          24613            234897 ns/op          199048 B/op       1142 allocs/op
	// BenchmarkDiffGenerator-32          26799            229082 ns/op          199041 B/op       1142 allocs/op
	// BenchmarkDiffGenerator-32          25690            221835 ns/op          199037 B/op       1142 allocs/op
	// PASS
	// ok      github.com/flanksource/config-db/db     41.324s

	// With: 	"github.com/sergi/go-diff/diffmatchpatch" (28GB total)
	//
	//
	// 19:07:53.211 INF Loaded 3 change rules
	// goos: linux
	// goarch: amd64
	// pkg: github.com/flanksource/config-db/db
	// cpu: Intel(R) Core(TM) i9-14900K
	// BenchmarkDiffGenerator
	// BenchmarkDiffGenerator-32          24148            234239 ns/op          167802 B/op       1502 allocs/op
	// BenchmarkDiffGenerator-32          27184            227818 ns/op          167774 B/op       1502 allocs/op
	// BenchmarkDiffGenerator-32          24052            231717 ns/op          167725 B/op       1502 allocs/op
	// BenchmarkDiffGenerator-32          24493            250147 ns/op          167792 B/op       1502 allocs/op
	// BenchmarkDiffGenerator-32          25670            218644 ns/op          167732 B/op       1502 allocs/op
	// PASS
	// ok      github.com/flanksource/config-db/db     41.500s

	before, err := os.ReadFile("testdata/echo-server.json")
	if err != nil {
		b.Fatalf("failed to open file echo-server.json: %v", err)
	}

	after, err := os.ReadFile("testdata/echo-server-new.json")
	if err != nil {
		b.Fatalf("failed to open file echo-server.json: %v", err)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := generateDiff(string(before), string(after))
		if err != nil {
			b.Fatalf("generateDiff() error = %v", err)
		}
	}
}
