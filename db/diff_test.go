package db

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/shirou/gopsutil/v3/process"
)

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
