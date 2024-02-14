package utils

import (
	"testing"
)

func TestLockedSlice(t *testing.T) {
	// Initialize LockedSlice
	ls := NewSyncBuffer[int](10)

	// Test Append method
	ls.Append(1)
	ls.Append(2)
	ls.Append(3)

	// Test Drain method
	drained := ls.Drain()
	expected := []int{1, 2, 3}
	if len(drained) != len(expected) {
		t.Errorf("Expected drained slice length %d, got %d", len(expected), len(drained))
	}
	for i, v := range expected {
		if drained[i] != v {
			t.Errorf("Expected value %d at index %d, got %d", v, i, drained[i])
		}
	}
}
