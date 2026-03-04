package v1

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestV1(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "V1 Suite")
}

var _ = Describe("KubernetesExclusionConfig", func() {
	DescribeTable("Filter",
		func(config KubernetesExclusionConfig, name, namespace, kind string, labels map[string]string, shouldExclude bool) {
			Expect(config.Filter(name, namespace, kind, labels)).To(Equal(shouldExclude))
		},
		Entry("exclusion by name",
			KubernetesExclusionConfig{Names: []string{"junit-*"}},
			"junit-123", "", "", nil, true),
		Entry("exclusion by namespace",
			KubernetesExclusionConfig{Namespaces: []string{"*-canaries"}},
			"", "customer-canaries", "", nil, true),
		Entry("exclusion by kind",
			KubernetesExclusionConfig{Kinds: []string{"*Chart"}},
			"", "", "HelmChart", nil, true),
		Entry("exclusion by labels | exact match",
			KubernetesExclusionConfig{Labels: map[string]string{"prod": "env"}},
			"", "", "", map[string]string{"prod": "env"}, true),
		Entry("exclusion by labels | one matches",
			KubernetesExclusionConfig{Labels: map[string]string{"prod": "env", "is-billed": "true", "trace-enabled": "true"}},
			"", "", "", map[string]string{"prod": "env", "trace-enabled": "false"}, true),
		Entry("no exclusions",
			KubernetesExclusionConfig{},
			"test-foo", "default", "", nil, false),
	)
})
