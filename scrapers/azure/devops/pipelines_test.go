package devops

import (
	"time"

	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty/types"
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
		Entry("nested folders", `\Production\EU`, "Deploy", "Production/EU / Deploy"),
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
		result := buildReleaseResult(testCtx(), config, project, def, nil, releases, cutoff)

		Expect(result.ID).To(Equal("azuredevops://myorg/MyProject/release/7"))
		Expect(result.Changes).To(HaveLen(3))

		Expect(result.Changes[0].ExternalID).To(Equal("azuredevops://myorg/MyProject/release/7"))
		Expect(result.Changes[0].ConfigType).To(Equal(ReleaseType))
		Expect(result.Changes[0].Source).To(Equal("AzureDevops/release/azuredevops://myorg/MyProject/release/7"))

		Expect(result.Changes[0].ChangeType).To(Equal(types.ChangeTypeDeployment))
		Expect(result.Changes[1].ChangeType).To(Equal(types.ChangeTypeApproved))
		Expect(result.Changes[2].ChangeType).To(Equal(types.ChangeTypeDeployment))

		stagingTo := result.Changes[0].Details["to"].(map[string]any)
		Expect(stagingTo["name"]).To(Equal("Staging"))
		prodTo := result.Changes[2].Details["to"].(map[string]any)
		Expect(prodTo["name"]).To(Equal("Prod"))

		stagingApprovals := result.Changes[0].Details["approvals"].([]any)
		Expect(stagingApprovals).To(HaveLen(1))
		approver := stagingApprovals[0].(map[string]any)["approver"].(map[string]any)
		Expect(approver["id"]).To(Equal("approver@example.com"))

		_, hasApprovals := result.Changes[2].Details["approvals"]
		Expect(hasApprovals).To(BeFalse())
	})
})

var _ = Describe("pipelineApprovalChanges", func() {
	It("returns no changes for empty approvals", func() {
		Expect(pipelineApprovalChanges(nil, "id", "src", "base")).To(BeEmpty())
	})

	It("skips pending steps", func() {
		approvals := []PipelineApproval{{
			ID:    "a1",
			Steps: []ApprovalStep{{Status: "pending", AssignedApprover: IdentityRef{UniqueName: "u@example.com"}}},
		}}
		Expect(pipelineApprovalChanges(approvals, "id", "src", "base")).To(BeEmpty())
	})

	It("emits Approved change with correct fields", func() {
		ts := time.Date(2024, 6, 15, 10, 0, 0, 0, time.UTC)
		approvals := []PipelineApproval{{
			ID: "a1",
			Steps: []ApprovalStep{{
				Status:           "approved",
				AssignedApprover: IdentityRef{UniqueName: "assigned@example.com"},
				ActualApprover:   &IdentityRef{UniqueName: "actual@example.com"},
				Comment:          "LGTM",
				LastModifiedOn:   ts,
			}},
		}}

		changes := pipelineApprovalChanges(approvals, "ext-id", "my-source", "org/proj/1/100")
		Expect(changes).To(HaveLen(1))

		ch := changes[0]
		Expect(ch.ChangeType).To(Equal(ChangeTypeApproved))
		Expect(*ch.CreatedBy).To(Equal("actual@example.com"))
		Expect(*ch.CreatedAt).To(Equal(ts))
		Expect(ch.ExternalID).To(Equal("ext-id"))
		Expect(ch.ConfigType).To(Equal(PipelineType))
		Expect(ch.Source).To(Equal("my-source"))
		Expect(ch.Severity).To(Equal("info"))
		Expect(ch.Summary).To(ContainSubstring("actual@example.com"))
		Expect(ch.Summary).To(ContainSubstring("LGTM"))
		Expect(ch.ExternalChangeID).To(Equal("org/proj/1/100/approval/a1/0"))
	})

	It("emits Rejected change with high severity", func() {
		approvals := []PipelineApproval{{
			ID: "a2",
			Steps: []ApprovalStep{{
				Status:           "rejected",
				AssignedApprover: IdentityRef{UniqueName: "user@example.com"},
				LastModifiedOn:   time.Now(),
			}},
		}}

		changes := pipelineApprovalChanges(approvals, "id", "src", "base")
		Expect(changes).To(HaveLen(1))
		Expect(changes[0].ChangeType).To(Equal(ChangeTypeRejected))
		Expect(changes[0].Severity).To(Equal("high"))
	})

	It("falls back to AssignedApprover when ActualApprover is nil", func() {
		approvals := []PipelineApproval{{
			ID: "a3",
			Steps: []ApprovalStep{{
				Status:           "approved",
				AssignedApprover: IdentityRef{UniqueName: "assigned@example.com"},
				LastModifiedOn:   time.Now(),
			}},
		}}

		changes := pipelineApprovalChanges(approvals, "id", "src", "base")
		Expect(changes).To(HaveLen(1))
		Expect(*changes[0].CreatedBy).To(Equal("assigned@example.com"))
	})

	It("handles mixed steps correctly", func() {
		approvals := []PipelineApproval{{
			ID: "a4",
			Steps: []ApprovalStep{
				{Status: "approved", AssignedApprover: IdentityRef{UniqueName: "a@x.com"}, LastModifiedOn: time.Now()},
				{Status: "pending", AssignedApprover: IdentityRef{UniqueName: "b@x.com"}},
				{Status: "rejected", AssignedApprover: IdentityRef{UniqueName: "c@x.com"}, LastModifiedOn: time.Now()},
			},
		}}

		changes := pipelineApprovalChanges(approvals, "id", "src", "base")
		Expect(changes).To(HaveLen(2))
		Expect(changes[0].ChangeType).To(Equal(ChangeTypeApproved))
		Expect(changes[1].ChangeType).To(Equal(ChangeTypeRejected))
	})

	It("emits typed Approval envelope with Manual stage and step raw", func() {
		ts := time.Date(2024, 6, 15, 10, 0, 0, 0, time.UTC)
		approvals := []PipelineApproval{{
			ID: "a1",
			Steps: []ApprovalStep{{
				Status:           "approved",
				AssignedApprover: IdentityRef{UniqueName: "assigned@example.com"},
				ActualApprover:   &IdentityRef{UniqueName: "actual@example.com", DisplayName: "Actual"},
				Comment:          "LGTM",
				LastModifiedOn:   ts,
			}},
		}}

		changes := pipelineApprovalChanges(approvals, "ext-id", "my-source", "org/proj/1/100")
		Expect(changes).To(HaveLen(1))

		details := changes[0].Details
		Expect(details["kind"]).To(Equal("Approval/v1"))
		Expect(details["stage"]).To(Equal(string(types.ApprovalStageManual)))
		Expect(details["status"]).To(Equal(string(types.ApprovalStatusApproved)))

		approver := details["approver"].(map[string]any)
		Expect(approver["id"]).To(Equal("actual@example.com"))
		Expect(approver["comment"]).To(Equal("LGTM"))

		raw := details["raw"].(map[string]any)
		Expect(raw["comment"]).To(Equal("LGTM"))
	})
})

var _ = Describe("runStateToStatus", func() {
	DescribeTable("maps Run.State/Run.Result to canonical types.Status",
		func(state, result string, expected types.Status) {
			Expect(runStateToStatus(state, result)).To(Equal(expected))
		},
		Entry("inProgress -> Running", RunStateInProgress, "", types.StatusRunning),
		Entry("cancelling -> Running", RunStateCancelling, "", types.StatusRunning),
		Entry("completed+succeeded -> Completed", RunStateCompleted, RunResultSucceeded, types.StatusCompleted),
		Entry("completed+failed -> Failed", RunStateCompleted, RunResultFailed, types.StatusFailed),
		Entry("completed+canceled -> Failed", RunStateCompleted, RunResultCanceled, types.StatusFailed),
		Entry("completed+timedOut -> Timeout", RunStateCompleted, RunResultTimedOut, types.StatusTimeout),
		Entry("unknown -> Pending", "unknown", "", types.StatusPending),
	)
})

var _ = Describe("buildPipelineRunDetail", func() {
	It("populates Event URL/timestamp/properties and maps Status", func() {
		ts := time.Date(2024, 6, 15, 10, 0, 0, 0, time.UTC)
		run := Run{
			ID:          42,
			Name:        "build-42",
			State:       RunStateCompleted,
			Result:      RunResultSucceeded,
			CreatedDate: ts,
			Links:       map[string]Link{"web": {Href: "https://dev.azure.com/org/proj/_build/42"}},
		}
		detail := buildPipelineRunDetail(run, "ext-42")
		Expect(detail.Kind()).To(Equal("PipelineRun/v1"))
		Expect(detail.Event.ID).To(Equal("ext-42"))
		Expect(detail.Event.URL).To(Equal("https://dev.azure.com/org/proj/_build/42"))
		Expect(detail.Event.Timestamp).To(Equal(ts.Format(time.RFC3339)))
		Expect(detail.Event.Properties).To(HaveKeyWithValue("state", RunStateCompleted))
		Expect(detail.Event.Properties).To(HaveKeyWithValue("result", RunResultSucceeded))
		Expect(detail.Status).To(Equal(types.StatusCompleted))
	})
})

var _ = Describe("GetLabels", func() {
	It("returns empty labels regardless of pipeline fields", func() {
		p := Pipeline{
			TemplateParameters: map[string]any{"env": "prod"},
			Variables:          map[string]Variable{"region": {Value: "us-east-1"}},
		}
		Expect(p.GetLabels()).To(BeEmpty())
	})
})

var _ = Describe("buildPipelineConfig", func() {
	It("includes configuration and repository metadata", func() {
		p := Pipeline{
			ID:       42,
			Name:     "Build",
			URL:      "https://dev.azure.com/myorg/myproject/_apis/pipelines/42",
			Folder:   `\`,
			Revision: 3,
			Configuration: &PipelineConfig{
				Type: "yaml",
				Path: "azure-pipelines.yml",
				Repository: &Repository{
					Name:          "myrepo",
					URL:           "https://dev.azure.com/myorg/myproject/_git/myrepo",
					DefaultBranch: "refs/heads/main",
				},
			},
			Links: map[string]Link{"web": {Href: "https://dev.azure.com/myorg/myproject/_build/definition?definitionId=42"}},
		}

		cfg := buildPipelineConfig(p)
		Expect(cfg["configuration"]).To(Equal(map[string]any{"type": "yaml", "yamlPath": "azure-pipelines.yml"}))
		Expect(cfg["repository"]).To(Equal(map[string]any{
			"name":          "myrepo",
			"url":           "https://dev.azure.com/myorg/myproject/_git/myrepo",
			"defaultBranch": "refs/heads/main",
		}))
		Expect(cfg["webUrl"]).To(Equal("https://dev.azure.com/myorg/myproject/_build/definition?definitionId=42"))
	})

	It("works with minimal pipeline", func() {
		p := Pipeline{ID: 1, Name: "Test"}
		cfg := buildPipelineConfig(p)
		Expect(cfg["name"]).To(Equal("Test"))
		Expect(cfg).NotTo(HaveKey("configuration"))
	})
})
