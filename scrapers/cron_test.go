package scrapers

import (
	"sync"
	"testing"
	"time"
)

func TestAtomicRunner(t *testing.T) {
	var (
		id               = "test-id"
		counter          = 0
		incrementCounter = func() {
			time.Sleep(1 * time.Second)
			counter++
		}
	)

	// Execute atomicRunner in multiple goroutines to check if it behaves as expected
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			AtomicRunner(id, incrementCounter)()
			wg.Done()
		}()
	}
	wg.Wait()

	// If AtomicRunner behaves correctly, counter should be equal to 1, because other goroutines should be blocked
	if counter != 1 {
		t.Errorf("AtomicRunner did not behave as expected, expected counter to be 1, got %d", counter)
	}
}
