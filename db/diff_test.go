package db

import (
	"fmt"
	"os"
	"path/filepath"
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
				_ = os.WriteFile(tt.want+".actual", []byte(got), 0644)
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

		{100, 1024 * 1024 * 1024, os.Getenv("NORMALIZER")},
		// {50, 1024 * 1024 * 1024, os.Getenv("NORMALIZER")},
		// {200, 1024 * 1024 * 1024, os.Getenv("NORMALIZER")},
	} {

		p, _ := process.NewProcess(int32(os.Getpid()))

		_ = b.Run(fmt.Sprintf("GOMEMLIMIT=%dmb GOGC=%d, NORMALIZER=%s", c.memlimit/1024/1024, c.gogc, c.normalizer), func(b *testing.B) {

			debug.SetGCPercent(c.gogc)
			debug.SetMemoryLimit(c.memlimit)
			debug.FreeOSMemory()

			var start, end runtime.MemStats
			runtime.ReadMemStats(&start)

			b.ResetTimer()
			b.ReportAllocs()
			compared := 0
			var totalSize int64
			var root = "../testdata"
			for i := 0; i < b.N; i++ {
				dirs, err := os.ReadDir(root)
				if err != nil {
					b.Fatal(err)
				}
				for _, dir := range dirs {
					b.Log(dir.Name())
					files, err := os.ReadDir(filepath.Join(root, dir.Name()))
					if err != nil {
						b.Fatal(err)
					}

					if len(files) < 2 {
						continue
					}

					for i := 1; i < len(files); i++ {
						f1 := files[i-1]
						f2 := files[i]

						b1, err := os.ReadFile(filepath.Join(root, dir.Name(), f1.Name()))
						if err != nil {
							b.Fatal(err)
						}

						b2, err := os.ReadFile(filepath.Join(root, dir.Name(), f2.Name()))
						if err != nil {
							b.Fatal(err)
						}
						compared++
						info, _ := f1.Info()
						totalSize += info.Size()

						_, _ = generateDiff(c.normalizer, string(b1), string(b2))
					}
				}
			}

			runtime.ReadMemStats(&end)

			mem, _ := p.MemoryInfo()
			load, _ := p.CPUPercent()
			b.ReportMetric(float64(compared), "files")
			b.ReportMetric(float64(mem.RSS/1024/1024), "rssMB")
			b.ReportMetric(load, "cpu%")
			b.ReportMetric(float64(totalSize/int64(compared)/1024), "jsonAvgKB")
			b.ReportMetric(float64(end.NumGC-start.NumGC)/float64(b.N), "gc/op")
		})

	}
}
