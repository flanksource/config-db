package utils

import "time"

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