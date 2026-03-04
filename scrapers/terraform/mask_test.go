package terraform

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestTerraform(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Terraform Suite")
}

var _ = Describe("maskSensitiveAttributes", func() {
	DescribeTable("masks sensitive fields",
		func(name string) {
			content, err := os.ReadFile(fmt.Sprintf("testdata/%s", name))
			Expect(err).ToNot(HaveOccurred())

			var state State
			Expect(json.Unmarshal(content, &state)).To(Succeed())

			got, err := maskSensitiveAttributes(state, content)
			Expect(err).ToNot(HaveOccurred())

			expected, err := os.ReadFile(fmt.Sprintf("testdata/%s.expected", name))
			Expect(err).ToNot(HaveOccurred())

			var expectedMap map[string]any
			Expect(json.Unmarshal(expected, &expectedMap)).To(Succeed())

			Expect(got).To(Equal(expectedMap))
		},
		Entry("cloudflare", "cloudflare.tfstate"),
	)
})
