package devops

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("repositoryExternalID", func() {
	It("formats correctly", func() {
		id := repositoryExternalID("myorg", "MyProject", "abc-123-def")
		Expect(id).To(Equal("azuredevops://myorg/MyProject/repository/abc-123-def"))
	})
})
