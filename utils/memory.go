package utils

import (
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/flanksource/commons/logger"
	"github.com/shirou/gopsutil/v3/process"
	k8sDuration "k8s.io/apimachinery/pkg/util/duration"
)

type MemoryTimer struct {
	startTime time.Time
	start     *runtime.MemStats
}

func age(d time.Duration) string {
	if d.Milliseconds() == 0 {
		return "0ms"
	}
	if d.Milliseconds() < 1000 {
		return fmt.Sprintf("%0.dms", d.Milliseconds())
	}

	return k8sDuration.HumanDuration(d)
}

func NewMemoryTimer() MemoryTimer {
	m := MemoryTimer{startTime: time.Now()}
	if logger.IsTraceEnabled() {
		s := runtime.MemStats{}
		runtime.ReadMemStats(&s)
		m.start = &s
	}
	return m
}

func (m *MemoryTimer) End() string {
	d := age(time.Since(m.startTime))
	if m.start == nil {
		return d
	}

	p, _ := process.NewProcess(int32(os.Getpid()))
	mem, _ := p.MemoryInfo()

	end := runtime.MemStats{}
	runtime.ReadMemStats(&end)
	allocs := end.Mallocs - m.start.Mallocs
	heap := end.HeapAlloc - m.start.HeapAlloc
	totalheap := end.TotalAlloc - m.start.TotalAlloc
	gc := end.NumGC - m.start.NumGC

	return fmt.Sprintf(
		"%s (allocs=%dk, heap_allocs=%dmb heap_increase=%dmb, gc_count=%d rss=%dmb)",
		d,
		allocs/1000,
		totalheap/1024/1024,
		heap/1024/1024,
		gc,
		mem.RSS/1024/1024,
	)
}
