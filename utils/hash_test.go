package utils

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestUtils(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Utils Suite")
}

var _ = Describe("Sha256Hex", func() {
	DescribeTable("hashing strings",
		func(input, expected string) {
			Expect(Sha256Hex(input)).To(Equal(expected))
		},
		Entry("flanksource", "flanksource", "bba09cfc0321b05968bd39bb2e96e4a6bb5f5d3069dcf74ab0772118b7f7258f"),
		Entry("programmer", "programmer", "7bd9ca7a756115eabdff2ab281ee9d8c22f44b51d97a6801169d65d90ff16327"),
	)
})
