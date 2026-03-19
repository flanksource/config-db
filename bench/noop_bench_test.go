package bench

import (
	"hash/crc32"
	"testing"
)

var noopSink uint32

// BenchmarkNoop is a placeholder benchmark for CI wiring.
// Replace this with real benchmarks in follow-up changes.
func BenchmarkNoop(b *testing.B) {
	table := crc32.MakeTable(crc32.Castagnoli)
	payload := make([]byte, 1024)
	for i := range payload {
		payload[i] = byte(i)
	}

	for i := 0; i < b.N; i++ {
		noopSink = crc32.Update(uint32(i), table, payload)
	}
}
