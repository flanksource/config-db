package db

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"testing"
	"time"

	"github.com/flanksource/config-db/db/models"
	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/samber/lo"
	"github.com/shirou/gopsutil/v3/process"
)

func TestDB(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "DB Suite")
}

func readTestFile(path string) string {
	f, err := os.ReadFile(path)
	ExpectWithOffset(1, err).ToNot(HaveOccurred(), "failed to open file (path=%s)", path)
	return string(f)
}

var _ = Describe("generateDiff", func() {
	DescribeTable("produces correct diffs",
		func(newConf, prev, wantFile string) {
			got, err := generateDiff(readTestFile(newConf), readTestFile(prev))
			Expect(err).ToNot(HaveOccurred())
			Expect(got).To(Equal(readTestFile(wantFile)))
		},
		Entry("simple", "testdata/simple-new.json", "testdata/simple-old.json", "testdata/simple.diff"),
		Entry("person", "testdata/person-new.json", "testdata/person-old.json", "testdata/person.diff"),
	)
})

var _ = Describe("dedupChanges", func() {
	It("deduplicates changes by fingerprint and separates non-duplicates", func() {
		abcKey := changeFingeprintCacheKey("dae6b3f5-bc26-48ac-8ad4-06e5efbb2a7d", "abc")
		ChangeCacheByFingerprint.Set(abcKey, "8b9d2659-7a11-46ff-bdff-1c4e8964c437", time.Hour)
		defer func() {
			// Clean up inserted cache keys so they don't leak into other specs
			ChangeCacheByFingerprint.Delete(abcKey)
			ChangeCacheByFingerprint.Delete(changeFingeprintCacheKey("dae6b3f5-bc26-48ac-8ad4-06e5efbb2a7d", "xyz"))
		}()

		changes := []*models.ConfigChange{
			{ID: "8b9d2659-7a11-46ff-bdff-1c4e8964c437", CreatedAt: time.Date(2024, 01, 02, 0, 0, 0, 0, time.UTC), Fingerprint: lo.ToPtr("abc"), ConfigID: "dae6b3f5-bc26-48ac-8ad4-06e5efbb2a7d", Summary: "first", Count: 1},
			{ID: uuid.NewString(), CreatedAt: time.Date(2024, 02, 02, 0, 0, 0, 0, time.UTC), Fingerprint: lo.ToPtr("abc"), ConfigID: "dae6b3f5-bc26-48ac-8ad4-06e5efbb2a7d", Summary: "second", Count: 1},
			{ID: uuid.NewString(), CreatedAt: time.Date(2024, 03, 02, 0, 0, 0, 0, time.UTC), Fingerprint: lo.ToPtr("abc"), ConfigID: "dae6b3f5-bc26-48ac-8ad4-06e5efbb2a7d", Summary: "third", Count: 1},
			{ID: uuid.NewString(), CreatedAt: time.Date(2024, 04, 02, 0, 0, 0, 0, time.UTC), Fingerprint: lo.ToPtr("abc"), ConfigID: "dae6b3f5-bc26-48ac-8ad4-06e5efbb2a7d", Summary: "fourth", Count: 1},
			{ID: "01eda583-3f5e-4c44-851f-93ac73272b92", CreatedAt: time.Date(2024, 04, 02, 0, 0, 0, 0, time.UTC), Fingerprint: lo.ToPtr("xyz"), ConfigID: "dae6b3f5-bc26-48ac-8ad4-06e5efbb2a7d", Summary: "different", Count: 1},
			{ID: uuid.NewString(), CreatedAt: time.Date(2024, 04, 03, 0, 0, 0, 0, time.UTC), Fingerprint: lo.ToPtr("xyz"), ConfigID: "dae6b3f5-bc26-48ac-8ad4-06e5efbb2a7d", Summary: "different two", Count: 1},
		}

		expectedDeduped := []models.ConfigChangeUpdate{
			{
				Change: &models.ConfigChange{
					ID:          "8b9d2659-7a11-46ff-bdff-1c4e8964c437",
					CreatedAt:   time.Date(2024, 01, 02, 0, 0, 0, 0, time.UTC),
					Fingerprint: lo.ToPtr("abc"),
					ConfigID:    "dae6b3f5-bc26-48ac-8ad4-06e5efbb2a7d",
					Summary:     "first",
					Count:       1,
				},
				CountIncrement: 4,
			},
		}

		expectedNonDuped := []*models.ConfigChange{
			{
				ID:          "01eda583-3f5e-4c44-851f-93ac73272b92",
				CreatedAt:   time.Date(2024, 04, 02, 0, 0, 0, 0, time.UTC),
				Fingerprint: lo.ToPtr("xyz"),
				ConfigID:    "dae6b3f5-bc26-48ac-8ad4-06e5efbb2a7d",
				Summary:     "different",
				Count:       1,
			},
		}

		nonDuped, deduped := dedupChanges(time.Hour, changes)
		Expect(deduped).To(Equal(expectedDeduped))
		Expect(nonDuped).To(Equal(expectedNonDuped))
	})
})

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

						_, _ = generateDiff(string(b1), string(b2))
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
