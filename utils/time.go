package utils

import (
	"math/rand/v2"
	"time"
)

// timeNow is a variable that can be overridden in tests
var timeNow = time.Now

// Now returns the current time, can be mocked in tests
func Now() time.Time {
	return timeNow()
}

// MockTime sets a fixed time for testing and returns a function to restore the original
func MockTime(mockTime time.Time) func() {
	original := timeNow
	timeNow = func() time.Time { return mockTime }
	return func() { timeNow = original }
}

// RandomDurationBetween returns a duration uniformly chosen in [minDur, maxDur].
// If maxDur <= minDur, minDur is returned.
func RandomDurationBetween(minDur, maxDur time.Duration) time.Duration {
	if maxDur <= minDur {
		return minDur
	}
	return minDur + time.Duration(rand.Int64N(int64(maxDur-minDur)+1))
}
