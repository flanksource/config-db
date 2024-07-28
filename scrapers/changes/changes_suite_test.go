package changes

import (
	"testing"

	dutycontext "github.com/flanksource/duty/context"
	"github.com/flanksource/duty/tests/setup"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	// +kubebuilder:scaffold:imports
)

func TestRunScrapers(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Changes Suite")
}

var (
	DefaultContext dutycontext.Context
)

var _ = BeforeSuite(func() {
	DefaultContext = setup.BeforeSuiteFn().WithTrace()
})

var _ = AfterSuite(func() {
	setup.AfterSuiteFn()
})
