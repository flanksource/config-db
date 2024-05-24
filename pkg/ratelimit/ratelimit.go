package ratelimit

import (
	"time"

	sw "github.com/RussellLuo/slidingwindow"
)

// LocalWindow represents a window that ignores sync behavior entirely
// and only stores counters in memory.
//
// NOTE: It's an exact copy of the LocalWindow provided by RussellLuo/slidingwindow
// with an added capability of setting a custom start time.
type LocalWindow struct {
	// The start boundary (timestamp in nanoseconds) of the window.
	// [start, start + size)
	start int64

	// The total count of events happened in the window.
	count int64
}

func NewLocalWindow() (*LocalWindow, sw.StopFunc) {
	return &LocalWindow{}, func() {}
}

func (w *LocalWindow) SetStart(s time.Time) {
	w.start = s.UnixNano()
}

func (w *LocalWindow) Start() time.Time {
	return time.Unix(0, w.start)
}

func (w *LocalWindow) Count() int64 {
	return w.count
}

func (w *LocalWindow) AddCount(n int64) {
	w.count += n
}

func (w *LocalWindow) Reset(s time.Time, c int64) {
	w.start = s.UnixNano()
	w.count = c
}

func (w *LocalWindow) Sync(now time.Time) {}
