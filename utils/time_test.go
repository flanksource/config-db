package utils

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("MockTime", func() {
	It("should return real time by default, mock when set, and restore", func() {
		Expect(time.Since(Now())).To(BeNumerically("<", time.Second))

		mockTime := time.Date(2025, 6, 19, 12, 0, 0, 0, time.UTC)
		restore := MockTime(mockTime)
		defer restore() // Ensure restore runs even if assertions fail

		Expect(Now()).To(Equal(mockTime))

		restore()
		Expect(time.Since(Now())).To(BeNumerically("<", time.Second))
	})
})
