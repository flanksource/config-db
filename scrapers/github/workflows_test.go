package github

import (
	"testing"

	"github.com/onsi/gomega"
)

func TestWorkflowURL(t *testing.T) {
	tests := []struct {
		htmlURL  string
		expected string
	}{
		{
			htmlURL:  "https://github.com/flanksource/duty/blob/main/.github/workflows/release.yml",
			expected: "https://github.com/flanksource/duty/actions/workflows/release.yml",
		},
		{
			htmlURL:  "https://github.com/flanksource/duty/blob/main/.github/workflows/test.yaml",
			expected: "https://github.com/flanksource/duty/actions/workflows/test.yaml",
		},
	}

	g := gomega.NewWithT(t)
	for _, test := range tests {
		actual, err := getWorkflowURL(test.htmlURL)
		g.Expect(err).To(gomega.BeNil())
		g.Expect(actual).To(gomega.Equal(test.expected))
	}
}
