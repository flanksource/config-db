package github

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestGithub(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Github Suite")
}

var _ = Describe("getWorkflowURL", func() {
	DescribeTable("converts blob URL to actions URL",
		func(htmlURL, expected string) {
			actual, err := getWorkflowURL(htmlURL)
			Expect(err).ToNot(HaveOccurred())
			Expect(actual).To(Equal(expected))
		},
		Entry("release.yml",
			"https://github.com/flanksource/duty/blob/main/.github/workflows/release.yml",
			"https://github.com/flanksource/duty/actions/workflows/release.yml",
		),
		Entry("test.yaml",
			"https://github.com/flanksource/duty/blob/main/.github/workflows/test.yaml",
			"https://github.com/flanksource/duty/actions/workflows/test.yaml",
		),
	)
})
