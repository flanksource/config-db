package scrapeui

import "sync"

// SafeBuffer is a concurrency-safe string buffer used for log fan-out.
// It implements io.Writer and protects concurrent reads/writes.
type SafeBuffer struct {
	mu  sync.RWMutex
	buf []byte
}

func (b *SafeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	b.buf = append(b.buf, p...)
	b.mu.Unlock()
	return len(p), nil
}

func (b *SafeBuffer) String() string {
	b.mu.RLock()
	s := string(b.buf)
	b.mu.RUnlock()
	return s
}
