package devops

import (
	"time"

	v1 "github.com/flanksource/config-db/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("effectiveSince", func() {
	maxAge := 7 * 24 * time.Hour

	It("returns now-maxAge on first scrape (zero lastRun)", func() {
		since := effectiveSince(maxAge, time.Time{})
		Expect(time.Since(since)).To(BeNumerically("~", maxAge, time.Second))
	})

	It("returns lastRun when recent (within maxAge window)", func() {
		recentLastRun := time.Now().Add(-24 * time.Hour)
		since := effectiveSince(maxAge, recentLastRun)
		Expect(since).To(BeTemporally("~", recentLastRun, time.Second))
	})

	It("returns cutoff when lastRun is older than maxAge", func() {
		oldLastRun := time.Now().Add(-30 * 24 * time.Hour)
		since := effectiveSince(maxAge, oldLastRun)
		Expect(time.Since(since)).To(BeNumerically("~", maxAge, time.Second))
	})
})

var _ = Describe("maxAge run filter", func() {
	It("keeps only runs within the maxAge window", func() {
		maxAge := 7 * 24 * time.Hour
		cutoff := time.Now().Add(-maxAge)

		runs := []Run{
			{ID: 1, CreatedDate: time.Now().Add(-1 * 24 * time.Hour)},
			{ID: 2, CreatedDate: time.Now().Add(-5 * 24 * time.Hour)},
			{ID: 3, CreatedDate: time.Now().Add(-8 * 24 * time.Hour)},
			{ID: 4, CreatedDate: time.Now().Add(-30 * 24 * time.Hour)},
		}

		var passedIDs []int
		for _, run := range runs {
			if !run.CreatedDate.Before(cutoff) {
				passedIDs = append(passedIDs, run.ID)
			}
		}

		Expect(passedIDs).To(HaveLen(2))
		Expect(passedIDs).To(ConsistOf(1, 2))
	})
})

var _ = Describe("runChangeType", func() {
	DescribeTable("maps state/result/pendingApproval to change type",
		func(state, result string, hasPendingApproval bool, want string) {
			run := Run{State: state, Result: result}
			Expect(runChangeType(run, hasPendingApproval)).To(Equal(want))
		},
		Entry("in progress", RunStateInProgress, "", false, ChangeTypeInProgress),
		Entry("in progress with pending approval", RunStateInProgress, "", true, ChangeTypePendingApproval),
		Entry("cancelling", RunStateCancelling, "", false, ChangeTypeCancelling),
		Entry("completed succeeded", RunStateCompleted, RunResultSucceeded, false, ChangeTypeSucceeded),
		Entry("completed failed", RunStateCompleted, RunResultFailed, false, ChangeTypeFailed),
		Entry("completed canceled", RunStateCompleted, RunResultCanceled, false, ChangeTypeCancelled),
		Entry("completed timed out", RunStateCompleted, RunResultTimedOut, false, ChangeTypeTimedOut),
		Entry("completed unknown result", RunStateCompleted, "unknown", false, ChangeTypeInProgress),
	)
})

var _ = Describe("isTerminalRun", func() {
	DescribeTable("terminal runs",
		func(state, result string) {
			Expect(isTerminalRun(Run{State: state, Result: result})).To(BeTrue())
		},
		Entry("succeeded", RunStateCompleted, RunResultSucceeded),
		Entry("failed", RunStateCompleted, RunResultFailed),
		Entry("canceled", RunStateCompleted, RunResultCanceled),
		Entry("timed out", RunStateCompleted, RunResultTimedOut),
	)

	DescribeTable("non-terminal runs",
		func(state, result string) {
			Expect(isTerminalRun(Run{State: state, Result: result})).To(BeFalse())
		},
		Entry("in progress", RunStateInProgress, ""),
		Entry("cancelling", RunStateCancelling, ""),
		Entry("completed unknown", RunStateCompleted, "unknown"),
		Entry("completed empty", RunStateCompleted, ""),
	)
})

var _ = Describe("runCache", func() {
	It("reports miss on empty cache and hit after add", func() {
		c := &runCache{}
		const id = "MyProject/42/100"

		Expect(c.has(id)).To(BeFalse())
		c.add(id)
		Expect(c.has(id)).To(BeTrue())
	})

	It("detects stale cache when loaded beyond TTL", func() {
		pipelineKey := "stale/1"
		c := &runCache{pipelineLastLoad: map[string]time.Time{pipelineKey: time.Now().Add(-2 * time.Hour)}}
		c.ids = map[string]struct{}{"stale/1/1": {}}

		Expect(c.has("stale/1/1")).To(BeTrue())

		c.RLock()
		isStale := time.Since(c.pipelineLastLoad[pipelineKey]) >= time.Hour
		c.RUnlock()
		Expect(isStale).To(BeTrue())
	})

	It("detects fresh cache when loaded within TTL", func() {
		freshKey := "fresh/1"
		c := &runCache{pipelineLastLoad: map[string]time.Time{freshKey: time.Now().Add(-30 * time.Minute)}}
		c.ids = map[string]struct{}{"fresh/1/1": {}}

		Expect(c.has("fresh/1/1")).To(BeTrue())

		c.RLock()
		isFresh := time.Since(c.pipelineLastLoad[freshKey]) < time.Hour
		c.RUnlock()
		Expect(isFresh).To(BeTrue())
	})
})

var _ = Describe("defCache", func() {
	It("returns miss on empty cache and hit after set", func() {
		c := &defCache{}

		got, ok := c.get("myorg", "myproject", 7, 3)
		Expect(ok).To(BeFalse())
		Expect(got).To(BeNil())

		def := &PipelineDefinition{YamlPath: "build.yaml"}
		c.set("myorg", "myproject", 7, 3, def)

		got, ok = c.get("myorg", "myproject", 7, 3)
		Expect(ok).To(BeTrue())
		Expect(got).To(Equal(def))
	})

	It("returns miss for different revision", func() {
		c := &defCache{}
		c.set("myorg", "myproject", 7, 3, &PipelineDefinition{YamlPath: "build.yaml"})

		got, ok := c.get("myorg", "myproject", 7, 4)
		Expect(ok).To(BeFalse())
		Expect(got).To(BeNil())
	})

	It("returns miss for different org", func() {
		c := &defCache{}
		c.set("myorg", "myproject", 7, 3, &PipelineDefinition{YamlPath: "build.yaml"})

		got, ok := c.get("otherorg", "myproject", 7, 3)
		Expect(ok).To(BeFalse())
		Expect(got).To(BeNil())
	})

	It("returns miss for different project", func() {
		c := &defCache{}
		c.set("myorg", "myproject", 7, 3, &PipelineDefinition{YamlPath: "build.yaml"})

		got, ok := c.get("myorg", "otherproject", 7, 3)
		Expect(ok).To(BeFalse())
		Expect(got).To(BeNil())
	})

	It("replaces old revision when updated", func() {
		c := &defCache{}
		def1 := &PipelineDefinition{YamlPath: "v1.yaml"}
		def2 := &PipelineDefinition{YamlPath: "v2.yaml"}

		c.set("myorg", "myproject", 5, 1, def1)
		c.set("myorg", "myproject", 5, 2, def2)

		got, ok := c.get("myorg", "myproject", 5, 2)
		Expect(ok).To(BeTrue())
		Expect(got).To(Equal(def2))

		_, ok = c.get("myorg", "myproject", 5, 1)
		Expect(ok).To(BeFalse())
	})
})

var _ = Describe("Pipeline.GetID", func() {
	It("preserves definitionId from web href", func() {
		p := Pipeline{
			URL:   "https://dev.azure.com/myorg/myproject/_apis/pipelines/42?revision=7",
			Links: map[string]Link{"web": {Href: "https://dev.azure.com/myorg/myproject/_build/definition?definitionId=42"}},
		}
		Expect(p.GetID()).To(Equal("https://dev.azure.com/myorg/myproject/_build/definition?definitionId=42"))
	})

	It("strips revision but preserves definitionId from web href", func() {
		p := Pipeline{
			URL:   "https://dev.azure.com/myorg/myproject/_apis/pipelines/42?revision=7",
			Links: map[string]Link{"web": {Href: "https://dev.azure.com/myorg/myproject/_build/definition?definitionId=42&revision=3"}},
		}
		Expect(p.GetID()).To(Equal("https://dev.azure.com/myorg/myproject/_build/definition?definitionId=42"))
	})

	It("returns clean web href as-is", func() {
		p := Pipeline{
			URL:   "https://dev.azure.com/myorg/myproject/_apis/pipelines/42?revision=7",
			Links: map[string]Link{"web": {Href: "https://dev.azure.com/myorg/myproject/_build/definition"}},
		}
		Expect(p.GetID()).To(Equal("https://dev.azure.com/myorg/myproject/_build/definition"))
	})

	It("strips revision from URL when no web link", func() {
		p := Pipeline{URL: "https://dev.azure.com/myorg/myproject/_apis/pipelines/42?revision=7"}
		Expect(p.GetID()).To(Equal("https://dev.azure.com/myorg/myproject/_apis/pipelines/42"))
	})

	It("produces same ID for different revisions", func() {
		p1 := Pipeline{URL: "https://dev.azure.com/myorg/myproject/_apis/pipelines/42?revision=7"}
		p2 := Pipeline{URL: "https://dev.azure.com/myorg/myproject/_apis/pipelines/42?revision=8"}
		Expect(p1.GetID()).To(Equal(p2.GetID()))
	})

	It("produces different IDs for different definitionIds", func() {
		p1 := Pipeline{
			Links: map[string]Link{"web": {Href: "https://dev.azure.com/myorg/myproject/_build/definition?definitionId=42"}},
		}
		p2 := Pipeline{
			Links: map[string]Link{"web": {Href: "https://dev.azure.com/myorg/myproject/_build/definition?definitionId=99"}},
		}
		Expect(p1.GetID()).ToNot(Equal(p2.GetID()))
	})
})

var _ = Describe("project approval lookup", func() {
	It("groups approvals by run ID and excludes nil pipeline refs", func() {
		approvals := []PipelineApproval{
			{ID: "a1", Pipeline: &ApprovalPipelineRef{ID: 10}, Steps: []ApprovalStep{{Status: "pending", AssignedApprover: IdentityRef{UniqueName: "u@example.com"}}}},
			{ID: "a2", Pipeline: &ApprovalPipelineRef{ID: 10}, Steps: []ApprovalStep{{Status: "approved", AssignedApprover: IdentityRef{UniqueName: "v@example.com"}}}},
			{ID: "a3", Pipeline: &ApprovalPipelineRef{ID: 20}, Steps: []ApprovalStep{{Status: "pending", AssignedApprover: IdentityRef{UniqueName: "w@example.com"}}}},
			{ID: "a4", Pipeline: nil},
		}

		byRunID := make(map[int][]PipelineApproval)
		for _, a := range approvals {
			if a.Pipeline != nil {
				byRunID[a.Pipeline.ID] = append(byRunID[a.Pipeline.ID], a)
			}
		}

		Expect(byRunID[10]).To(HaveLen(2))
		Expect(byRunID[20]).To(HaveLen(1))
		Expect(byRunID[99]).To(HaveLen(0))

		total := 0
		for _, v := range byRunID {
			total += len(v)
		}
		Expect(total).To(Equal(3))
	})
})

var _ = Describe("hasPendingApprovals", func() {
	It("returns false for empty approvals", func() {
		Expect(hasPendingApprovals([]PipelineApproval{})).To(BeFalse())
	})

	It("returns false when all steps are approved", func() {
		approvals := []PipelineApproval{{
			Steps: []ApprovalStep{{AssignedApprover: IdentityRef{UniqueName: "user@example.com"}, Status: "approved"}},
		}}
		Expect(hasPendingApprovals(approvals)).To(BeFalse())
	})

	It("returns true when a step is pending", func() {
		approvals := []PipelineApproval{{
			Steps: []ApprovalStep{{AssignedApprover: IdentityRef{UniqueName: "user@example.com"}, Status: "pending"}},
		}}
		Expect(hasPendingApprovals(approvals)).To(BeTrue())
	})
})

var _ = Describe("releaseDisplayName", func() {
	DescribeTable("formats path and name",
		func(path, name, want string) {
			Expect(releaseDisplayName(ReleaseDefinition{Path: path, Name: name})).To(Equal(want))
		},
		Entry("root path", `\`, "Deploy", "Deploy"),
		Entry("single folder", `\Production`, "Deploy", "Production / Deploy"),
		Entry("nested folders", `\Production\EU`, "Deploy", `Production\EU / Deploy`),
		Entry("empty path", "", "Deploy", "Deploy"),
	)
})

var _ = Describe("releaseEnvStatusToChangeType", func() {
	DescribeTable("maps known statuses",
		func(status, want string) {
			got, ok := releaseEnvStatusToChangeType[status]
			Expect(ok).To(BeTrue())
			Expect(got).To(Equal(want))
		},
		Entry("succeeded", "succeeded", ChangeTypeSucceeded),
		Entry("partiallySucceeded", "partiallySucceeded", ChangeTypeFailed),
		Entry("canceled", "canceled", ChangeTypeCancelled),
		Entry("rejected", "rejected", ChangeTypeFailed),
		Entry("inProgress", "inProgress", ChangeTypeInProgress),
		Entry("queued", "queued", ChangeTypeInProgress),
		Entry("scheduled", "scheduled", ChangeTypeInProgress),
	)

	DescribeTable("excludes skipped statuses",
		func(status string) {
			_, ok := releaseEnvStatusToChangeType[status]
			Expect(ok).To(BeFalse())
		},
		Entry("notStarted", "notStarted"),
		Entry("failed", "failed"),
		Entry("notDeployed", "notDeployed"),
	)
})

var _ = Describe("approvalSummary", func() {
	It("excludes automated and skipped approvals", func() {
		approvals := []ReleaseApproval{
			{IsAutomated: true, Status: "approved", Approver: &IdentityRef{UniqueName: "system"}},
			{IsAutomated: false, Status: "approved", Approver: &IdentityRef{UniqueName: "alice@example.com"}, ApprovedBy: &IdentityRef{UniqueName: "alice@example.com"}},
			{IsAutomated: false, Status: "skipped", Approver: &IdentityRef{UniqueName: "bob@example.com"}, Comments: "not needed"},
		}
		got := approvalSummary(approvals)
		Expect(got).To(HaveLen(1))
		Expect(got[0]["approver"]).To(Equal("alice@example.com"))
		Expect(got[0]["status"]).To(Equal("approved"))
	})
})

var _ = Describe("buildReleaseResult", func() {
	It("produces changes with correct ExternalID, ConfigType, and details", func() {
		project := Project{Name: "MyProject"}
		def := ReleaseDefinition{ID: 7, Name: "Deploy", Path: `\`}
		webURL := "https://dev.azure.com/myorg/myproject/_release?releaseId=1"
		releaseCreatedAt := time.Now().Add(-1 * time.Hour)

		releases := []Release{{
			ID:        1,
			Name:      "Release-1",
			CreatedOn: releaseCreatedAt,
			CreatedBy: &IdentityRef{UniqueName: "user@example.com"},
			Links:     map[string]Link{"web": {Href: webURL}},
			Environments: []ReleaseEnvironment{
				{
					ID: 10, Name: "Staging", Status: "succeeded",
					PreDeployApprovals: []ReleaseApproval{
						{IsAutomated: false, Status: "approved", Approver: &IdentityRef{UniqueName: "approver@example.com"}, ApprovedBy: &IdentityRef{UniqueName: "approver@example.com"}},
					},
				},
				{ID: 11, Name: "Prod", Status: "inProgress"},
				{ID: 12, Name: "DR", Status: "notStarted"},
			},
		}}

		cutoff := releaseCreatedAt.Add(-time.Minute)
		config := v1.AzureDevops{Organization: "myorg"}
		result := buildReleaseResult(config, project, def, releases, cutoff)

		Expect(result.ID).To(Equal("MyProject/7"))
		Expect(result.Changes).To(HaveLen(2))

		for _, ch := range result.Changes {
			Expect(ch.ExternalID).To(Equal("MyProject/7"))
			Expect(ch.ConfigType).To(Equal(ReleaseType))
			Expect(ch.ExternalChangeID).ToNot(BeEmpty())
			Expect(ch.Source).To(Equal("Release-1"))
			Expect(ch.Details["url"]).To(Equal(webURL))
		}

		Expect(result.Changes[0].ChangeType).To(Equal(ChangeTypeSucceeded))
		Expect(result.Changes[1].ChangeType).To(Equal(ChangeTypeInProgress))

		pre, ok := result.Changes[0].Details["preDeployApprovals"].([]map[string]any)
		Expect(ok).To(BeTrue())
		Expect(pre).To(HaveLen(1))
		Expect(pre[0]["approver"]).To(Equal("approver@example.com"))

		_, hasPre := result.Changes[1].Details["preDeployApprovals"]
		Expect(hasPre).To(BeFalse())
	})
})
