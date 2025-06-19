package utils

import (
	"testing"
	"time"

	"github.com/onsi/gomega"
)

func TestMockTime(t *testing.T) {
	g := gomega.NewWithT(t)

	// Test that Now() returns real time by default
	realTime := time.Now()
	utilsTime := Now()
	g.Expect(utilsTime.Sub(realTime)).To(gomega.BeNumerically("<", time.Second), "Now() should return real time by default")

	// Test mocking time
	mockTime := time.Date(2025, 6, 19, 12, 0, 0, 0, time.UTC)
	restore := MockTime(mockTime)

	g.Expect(Now()).To(gomega.Equal(mockTime), "Now() should return mocked time")

	// Test restoring time
	restore()
	restoredTime := Now()
	g.Expect(restoredTime.Sub(realTime)).To(gomega.BeNumerically("<", time.Second), "Now() should return real time after restore")
}