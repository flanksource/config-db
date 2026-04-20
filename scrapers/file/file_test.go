package file

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestFile(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "File Suite")
}

var _ = Describe("stripPrefix", func() {
	DescribeTable("should strip known prefixes",
		func(input, expected string) {
			Expect(stripPrefix(input)).To(Equal(expected))
		},
		Entry("file:// prefix", "file://foo", "foo"),
		Entry("git:: prefix", "git::foo", "foo"),
		Entry("git:: with https", "git::https://foo", "https://foo"),
		Entry("no prefix", "foo", "foo"),
		Entry("empty string", "", ""),
	)
})

var _ = Describe("convertToLocalPath", func() {
	DescribeTable("should convert URLs to local paths with hash suffix",
		func(input, expected string) {
			Expect(convertToLocalPath(input)).To(Equal(expected))
		},
		Entry("file:// prefix", "file://foo", "foo-2c26b46b"),
		Entry("git:: prefix", "git::foo", "foo-2c26b46b"),
		Entry("git:: with URL and query", "git::https://foo/path?query=abc", "foo-path-90c2b34a"),
		Entry("plain path", "foo", "foo-2c26b46b"),
	)
})
