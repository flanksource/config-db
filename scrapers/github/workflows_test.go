package github

import (
	"testing"
	"time"

	"github.com/flanksource/duty/types"
	gogithub "github.com/google/go-github/v73/github"
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

var _ = Describe("workflowRunStatus", func() {
	DescribeTable("maps GitHub status+conclusion to canonical Status",
		func(status, conclusion string, expected types.Status) {
			Expect(workflowRunStatus(status, conclusion)).To(Equal(expected))
		},
		Entry("in_progress -> Running", "in_progress", "", types.StatusRunning),
		Entry("queued -> Running", "queued", "", types.StatusRunning),
		Entry("completed+success -> Completed", "completed", "success", types.StatusCompleted),
		Entry("completed+failure -> Failed", "completed", "failure", types.StatusFailed),
		Entry("completed+cancelled -> Failed", "completed", "cancelled", types.StatusFailed),
		Entry("completed+timed_out -> Timeout", "completed", "timed_out", types.StatusTimeout),
		Entry("completed+skipped -> Completed", "completed", "skipped", types.StatusCompleted),
	)
})

var _ = Describe("runToChangeResult", func() {
	It("wraps the run in a typed PipelineRun envelope with raw preserved", func() {
		createdAt := time.Date(2024, 6, 15, 10, 0, 0, 0, time.UTC)
		workflow := &gogithub.Workflow{
			ID:   gogithub.Ptr(int64(101)),
			Name: gogithub.Ptr("release"),
		}
		wrun := &gogithub.WorkflowRun{
			ID:              gogithub.Ptr(int64(7001)),
			Status:          gogithub.Ptr("completed"),
			Conclusion:      gogithub.Ptr("success"),
			HTMLURL:         gogithub.Ptr("https://github.com/flanksource/duty/actions/runs/7001"),
			HeadBranch:      gogithub.Ptr("main"),
			HeadSHA:         gogithub.Ptr("abc123"),
			Event:           gogithub.Ptr("push"),
			CreatedAt:       &gogithub.Timestamp{Time: createdAt},
			UpdatedAt:       &gogithub.Timestamp{Time: createdAt},
			RunNumber:       gogithub.Ptr(5),
			TriggeringActor: &gogithub.User{Login: gogithub.Ptr("alice")},
		}
		run := newRun(wrun)

		change := runToChangeResult(workflow, run)

		Expect(change.ChangeType).To(Equal("GitHubActionRunSuccess"))
		Expect(change.ExternalChangeID).To(Equal("release/101/7001"))

		Expect(change.Details["kind"]).To(Equal("PipelineRun/v1"))
		Expect(change.Details["status"]).To(Equal(string(types.StatusCompleted)))
		Expect(change.Details["url"]).To(Equal("https://github.com/flanksource/duty/actions/runs/7001"))

		properties := change.Details["properties"].(map[string]any)
		Expect(properties["branch"]).To(Equal("main"))
		Expect(properties["head_sha"]).To(Equal("abc123"))
		Expect(properties["trigger"]).To(Equal("push"))
		Expect(properties["conclusion"]).To(Equal("success"))

		raw := change.Details["raw"].(map[string]any)
		Expect(raw).To(HaveKey("id"))
	})
})
