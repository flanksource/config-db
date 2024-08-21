package db

import (
	"fmt"
	"os"
	"reflect"
	"runtime"
	"runtime/debug"
	"testing"
	"time"

	"github.com/flanksource/config-db/db/models"
	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/samber/lo"
	"github.com/shirou/gopsutil/v3/process"
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
			got, err := generateDiff("go", readFile(t, tt.args.newConf), readFile(t, tt.args.prev))
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
	for _, c := range []struct {
		gogc       int
		memlimit   int64
		normalizer string
	}{
		// {100, 1024 * 1024 * 1024, "go"},
		// {50, 1024 * 1024 * 1024, "go"},
		// {200, 1024 * 1024 * 1024, "go"},

		{100, 1024 * 1024 * 1024, "oj"},
		{50, 1024 * 1024 * 1024, "oj"},
		{200, 1024 * 1024 * 1024, "oj"},

		{100, 1024 * 1024 * 1024, "jq"},
		{50, 1024 * 1024 * 1024, "jq"},
		{200, 1024 * 1024 * 1024, "jq"},
	} {

		p, _ := process.NewProcess(int32(os.Getpid()))

		_ = b.Run(fmt.Sprintf("GOMEMLIMIT=%dmb GOGC=%d, NORMALIZER=%s", c.memlimit/1024/1024, c.gogc, c.normalizer), func(b *testing.B) {

			before, err := os.ReadFile("testdata/6mb.json")
			if err != nil {
				b.Fatalf("failed to open file echo-server.json: %v", err)
			}

			after, err := os.ReadFile("testdata/6mb-new.json")
			if err != nil {
				b.Fatalf("failed to open file echo-server.json: %v", err)
			}

			debug.SetGCPercent(c.gogc)
			debug.SetMemoryLimit(c.memlimit)
			debug.FreeOSMemory()

			var start, end runtime.MemStats
			runtime.ReadMemStats(&start)

			b.ResetTimer()
			b.ReportAllocs()

			for i := 0; i < b.N; i++ {
				_, _ = generateDiff(c.normalizer, string(before), string(after))
			}

			runtime.ReadMemStats(&end)

			mem, _ := p.MemoryInfo()
			load, _ := p.CPUPercent()

			b.ReportMetric(float64(mem.RSS/1024/1024), "rssMB")
			b.ReportMetric(load, "cpu%")
			b.ReportMetric(float64(len(before)/1024), "jsonKB")
			b.ReportMetric(float64(end.NumGC-start.NumGC)/float64(b.N), "gc/op")
		})

	}
}
