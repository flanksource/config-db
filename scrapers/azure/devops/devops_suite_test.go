package devops

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestDevops(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Azure DevOps Suite")
}
