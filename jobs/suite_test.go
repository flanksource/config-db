package jobs

import (
	"testing"

	"github.com/flanksource/duty/context"
	"github.com/flanksource/duty/job"
	"github.com/flanksource/duty/models"
	"github.com/flanksource/duty/tests/setup"
	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestJobs(t *testing.T) {
	RegisterFailHandler(ginkgo.Fail)
	ginkgo.RunSpecs(t, "Jobs Test Suite")
}

var (
	DefaultContext context.Context
)

func expectJobToPass(j *job.Job) {
	history, err := j.FindHistory()
	Expect(err).To(BeNil())
	Expect(len(history)).To(BeNumerically(">=", 1))
	Expect(history[0].Status).To(Equal(models.StatusSuccess))
}

var _ = ginkgo.BeforeSuite(func() {
	DefaultContext = setup.BeforeSuiteFn().WithTrace()
})

var _ = ginkgo.AfterSuite(setup.AfterSuiteFn)
