package db

import (
	"testing"

	dutyContext "github.com/flanksource/duty/context"
	"github.com/flanksource/duty/tests/setup"
	. "github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var DefaultContext dutyContext.Context

var _ = BeforeSuite(func() {
	DefaultContext = setup.BeforeSuiteFn(setup.WithoutDummyData)
})

var _ = AfterSuite(setup.AfterSuiteFn)

func TestDB(t *testing.T) {
	gomega.RegisterFailHandler(Fail)
	RunSpecs(t, "DB Suite")
}
