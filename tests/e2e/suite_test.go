package e2e

import (
	"testing"

	dutyContext "github.com/flanksource/duty/context"
	"github.com/flanksource/duty/tests/setup"
	ginkgo "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var DefaultContext dutyContext.Context

func TestE2E(t *testing.T) {
	RegisterFailHandler(ginkgo.Fail)
	ginkgo.RunSpecs(t, "E2E Suite")
}

var _ = ginkgo.BeforeSuite(func() {
	DefaultContext = setup.BeforeSuiteFn(setup.WithoutDummyData)
})

var _ = ginkgo.AfterSuite(setup.AfterSuiteFn)
