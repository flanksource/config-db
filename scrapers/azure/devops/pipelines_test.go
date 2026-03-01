package devops

import (
	"testing"
	"time"

	v1 "github.com/flanksource/config-db/api/v1"
)

// --- maxAge: resolveMaxAge, effectiveSince, run filter ---

func TestEffectiveSince(t *testing.T) {
	maxAge := 7 * 24 * time.Hour

	// Case 1: first scrape (lastRun is zero) — since = now-maxAge
	since := effectiveSince(maxAge, time.Time{})
	if time.Since(since) < maxAge-time.Second || time.Since(since) > maxAge+time.Second {
		t.Errorf("first scrape: expected since ≈ now-%v, got %v ago", maxAge, time.Since(since))
	}

	// Case 2: lastRun is recent (after cutoff) — since = lastRun
	recentLastRun := time.Now().Add(-24 * time.Hour) // 1d ago, within 7d window
	since = effectiveSince(maxAge, recentLastRun)
	if since.Before(recentLastRun.Add(-time.Second)) || since.After(recentLastRun.Add(time.Second)) {
		t.Errorf("recent lastRun: expected since ≈ lastRun, got %v", since)
	}

	// Case 3: lastRun is old (before cutoff) — since = cutoff (now-maxAge)
	oldLastRun := time.Now().Add(-30 * 24 * time.Hour) // 30d ago, outside 7d window
	since = effectiveSince(maxAge, oldLastRun)
	if time.Since(since) < maxAge-time.Second || time.Since(since) > maxAge+time.Second {
		t.Errorf("old lastRun: expected since ≈ now-%v, got %v ago", maxAge, time.Since(since))
	}
}

func TestMaxAgeRunFilter(t *testing.T) {
	maxAge := 7 * 24 * time.Hour
	cutoff := time.Now().Add(-maxAge)

	runs := []Run{
		{ID: 1, CreatedDate: time.Now().Add(-1 * 24 * time.Hour)},  // 1d ago — recent
		{ID: 2, CreatedDate: time.Now().Add(-5 * 24 * time.Hour)},  // 5d ago — recent
		{ID: 3, CreatedDate: time.Now().Add(-8 * 24 * time.Hour)},  // 8d ago — old
		{ID: 4, CreatedDate: time.Now().Add(-30 * 24 * time.Hour)}, // 30d ago — old
	}

	var passedIDs []int
	for _, run := range runs {
		if !run.CreatedDate.Before(cutoff) {
			passedIDs = append(passedIDs, run.ID)
		}
	}

	if len(passedIDs) != 2 {
		t.Errorf("expected 2 runs to pass filter, got %d: %v", len(passedIDs), passedIDs)
	}
	for _, id := range passedIDs {
		if id != 1 && id != 2 {
			t.Errorf("unexpected run ID %d passed filter", id)
		}
	}
}

// --- FR-1: Run state machine ---

func TestRunChangeType(t *testing.T) {
	tests := []struct {
		state              string
		result             string
		hasPendingApproval bool
		want               string
	}{
		{RunStateInProgress, "", false, ChangeTypeInProgress},
		{RunStateInProgress, "", true, ChangeTypePendingApproval},
		{RunStateCancelling, "", false, ChangeTypeCancelling},
		{RunStateCompleted, RunResultSucceeded, false, ChangeTypeSucceeded},
		{RunStateCompleted, RunResultFailed, false, ChangeTypeFailed},
		{RunStateCompleted, RunResultCanceled, false, ChangeTypeCancelled},
		{RunStateCompleted, RunResultTimedOut, false, ChangeTypeTimedOut},
		// unknown result still maps via completed branch — falls through to InProgress default
		{RunStateCompleted, "unknown", false, ChangeTypeInProgress},
	}
	for _, tc := range tests {
		run := Run{State: tc.state, Result: tc.result}
		got := runChangeType(run, tc.hasPendingApproval)
		if got != tc.want {
			t.Errorf("runChangeType(state=%q result=%q pending=%v) = %q, want %q",
				tc.state, tc.result, tc.hasPendingApproval, got, tc.want)
		}
	}
}

func TestIsTerminalRun(t *testing.T) {
	terminalCases := []Run{
		{State: RunStateCompleted, Result: RunResultSucceeded},
		{State: RunStateCompleted, Result: RunResultFailed},
		{State: RunStateCompleted, Result: RunResultCanceled},
		{State: RunStateCompleted, Result: RunResultTimedOut},
	}
	nonTerminalCases := []Run{
		{State: RunStateInProgress},
		{State: RunStateCancelling},
		{State: RunStateCompleted, Result: "unknown"},
		{State: RunStateCompleted, Result: ""},
	}
	for _, run := range terminalCases {
		if !isTerminalRun(run) {
			t.Errorf("expected terminal: state=%q result=%q", run.State, run.Result)
		}
	}
	for _, run := range nonTerminalCases {
		if isTerminalRun(run) {
			t.Errorf("expected non-terminal: state=%q result=%q", run.State, run.Result)
		}
	}
}

// --- FR-2: Terminal-run cache ---

func TestRunCacheAddHas(t *testing.T) {
	c := &runCache{}
	const id = "MyProject/42/100"

	if c.has(id) {
		t.Fatal("empty cache should not contain any ID")
	}
	c.add(id)
	if !c.has(id) {
		t.Fatalf("cache should contain %q after add", id)
	}
}

func TestRunCacheTTLExpiry(t *testing.T) {
	// Cache loaded 2h ago with TTL of 1h — should be stale
	staleCache := &runCache{lastLoaded: time.Now().Add(-2 * time.Hour)}
	staleCache.ids = map[string]struct{}{"stale/1/1": {}}
	if !staleCache.has("stale/1/1") {
		t.Fatal("pre-seeded ID must be present")
	}
	staleCache.RLock()
	isStale := time.Since(staleCache.lastLoaded) >= time.Hour
	staleCache.RUnlock()
	if !isStale {
		t.Fatal("cache loaded 2h ago should be stale with TTL=1h")
	}

	// Cache loaded 30min ago with TTL of 1h — should still be fresh
	freshCache := &runCache{lastLoaded: time.Now().Add(-30 * time.Minute)}
	freshCache.ids = map[string]struct{}{"fresh/1/1": {}}
	if !freshCache.has("fresh/1/1") {
		t.Fatal("pre-seeded ID must be present")
	}
	freshCache.RLock()
	isFresh := time.Since(freshCache.lastLoaded) < time.Hour
	freshCache.RUnlock()
	if !isFresh {
		t.Fatal("cache loaded 30min ago should be fresh with TTL=1h")
	}
}

// --- FR-4: Pipeline definition cache ---

func TestPipelineDefCacheHitMiss(t *testing.T) {
	c := &defCache{}
	pipelineID := 7
	revision := 3

	// Miss on empty cache
	if got, ok := c.get(pipelineID, revision); ok || got != nil {
		t.Fatal("empty cache should return miss")
	}

	def := &PipelineDefinition{YamlPath: "build.yaml"}
	c.set(pipelineID, revision, def)

	// Hit with matching revision
	got, ok := c.get(pipelineID, revision)
	if !ok || got != def {
		t.Fatal("expected cache hit with matching revision")
	}

	// Miss with different revision
	if got, ok := c.get(pipelineID, revision+1); ok || got != nil {
		t.Fatal("expected cache miss for different revision")
	}
}

func TestPipelineDefCacheRevisionUpdate(t *testing.T) {
	c := &defCache{}
	pipelineID := 5

	def1 := &PipelineDefinition{YamlPath: "v1.yaml"}
	def2 := &PipelineDefinition{YamlPath: "v2.yaml"}

	c.set(pipelineID, 1, def1)
	c.set(pipelineID, 2, def2)

	// Only revision 2 should be present
	got, ok := c.get(pipelineID, 2)
	if !ok || got != def2 {
		t.Fatal("expected updated definition after revision bump")
	}
	if _, ok := c.get(pipelineID, 1); ok {
		t.Fatal("old revision should no longer be cached")
	}
}

// --- Pipeline.GetID — revision-stable ---

func TestPipelineGetIDStable(t *testing.T) {
	webHref := "https://dev.azure.com/myorg/myproject/_build/definition?definitionId=42"
	apiURL := "https://dev.azure.com/myorg/myproject/_apis/pipelines/42?revision=7"

	// Web link present — prefer it (always stable)
	p := Pipeline{URL: apiURL, Links: map[string]Link{"web": {Href: webHref}}}
	if got := p.GetID(); got != webHref {
		t.Errorf("expected web href %q, got %q", webHref, got)
	}

	// No web link — strip query string from URL
	p2 := Pipeline{URL: apiURL}
	want := "https://dev.azure.com/myorg/myproject/_apis/pipelines/42"
	if got := p2.GetID(); got != want {
		t.Errorf("expected stripped URL %q, got %q", want, got)
	}

	// Different revisions with same pipeline ID must produce the same ID
	p3 := Pipeline{URL: "https://dev.azure.com/myorg/myproject/_apis/pipelines/42?revision=8"}
	if p2.GetID() != p3.GetID() {
		t.Errorf("different revisions should yield same ID: %q vs %q", p2.GetID(), p3.GetID())
	}
}

// --- Project approval map grouping ---

func TestProjectApprovalLookup(t *testing.T) {
	approvals := []PipelineApproval{
		{ID: "a1", Pipeline: &ApprovalPipelineRef{ID: 10}, Steps: []ApprovalStep{{Status: "pending", AssignedApprover: IdentityRef{UniqueName: "u@example.com"}}}},
		{ID: "a2", Pipeline: &ApprovalPipelineRef{ID: 10}, Steps: []ApprovalStep{{Status: "approved", AssignedApprover: IdentityRef{UniqueName: "v@example.com"}}}},
		{ID: "a3", Pipeline: &ApprovalPipelineRef{ID: 20}, Steps: []ApprovalStep{{Status: "pending", AssignedApprover: IdentityRef{UniqueName: "w@example.com"}}}},
		{ID: "a4", Pipeline: nil}, // no run reference — must be skipped
	}

	byRunID := make(map[int][]PipelineApproval)
	for _, a := range approvals {
		if a.Pipeline != nil {
			byRunID[a.Pipeline.ID] = append(byRunID[a.Pipeline.ID], a)
		}
	}

	if len(byRunID[10]) != 2 {
		t.Errorf("run 10: expected 2 approvals, got %d", len(byRunID[10]))
	}
	if len(byRunID[20]) != 1 {
		t.Errorf("run 20: expected 1 approval, got %d", len(byRunID[20]))
	}
	if len(byRunID[99]) != 0 {
		t.Errorf("run 99: expected 0 approvals, got %d", len(byRunID[99]))
	}
	// nil-pipeline approval must not appear in any bucket
	total := 0
	for _, v := range byRunID {
		total += len(v)
	}
	if total != 3 {
		t.Errorf("expected 3 total approvals in map (nil-pipeline excluded), got %d", total)
	}
}

// --- FR-1: hasPendingApprovals helper ---

func TestHasPendingApprovals(t *testing.T) {
	noApprovals := []PipelineApproval{}
	if hasPendingApprovals(noApprovals) {
		t.Error("empty approvals should not be pending")
	}

	allApproved := []PipelineApproval{{
		Steps: []ApprovalStep{
			{AssignedApprover: IdentityRef{UniqueName: "user@example.com"}, Status: "approved"},
		},
	}}
	if hasPendingApprovals(allApproved) {
		t.Error("all-approved steps should not be pending")
	}

	withPending := []PipelineApproval{{
		Steps: []ApprovalStep{
			{AssignedApprover: IdentityRef{UniqueName: "user@example.com"}, Status: "pending"},
		},
	}}
	if !hasPendingApprovals(withPending) {
		t.Error("pending step should be detected as pending approval")
	}
}

// --- Classic release: display name ---

func TestReleaseDisplayName(t *testing.T) {
	tests := []struct {
		path string
		name string
		want string
	}{
		{`\`, "Deploy", "Deploy"},
		{`\Production`, "Deploy", "Production / Deploy"},
		{`\Production\EU`, "Deploy", `Production\EU / Deploy`},
		{"", "Deploy", "Deploy"},
	}
	for _, tc := range tests {
		got := releaseDisplayName(ReleaseDefinition{Path: tc.path, Name: tc.name})
		if got != tc.want {
			t.Errorf("path=%q name=%q: got %q, want %q", tc.path, tc.name, got, tc.want)
		}
	}
}

// --- Classic release: environment status mapping ---

func TestReleaseEnvStatusMapping(t *testing.T) {
	covered := []struct {
		status string
		want   string
	}{
		{"succeeded", ChangeTypeSucceeded},
		{"partiallySucceeded", ChangeTypeFailed},
		{"canceled", ChangeTypeCancelled},
		{"rejected", ChangeTypeFailed},
		{"inProgress", ChangeTypeInProgress},
		{"queued", ChangeTypeInProgress},
		{"scheduled", ChangeTypeInProgress},
	}
	for _, tc := range covered {
		got, ok := releaseEnvStatusToChangeType[tc.status]
		if !ok {
			t.Errorf("status %q not in map", tc.status)
			continue
		}
		if got != tc.want {
			t.Errorf("status %q: got %q, want %q", tc.status, got, tc.want)
		}
	}

	// Statuses that should produce no change (skipped entirely)
	for _, skip := range []string{"notStarted", "failed", "notDeployed"} {
		if _, ok := releaseEnvStatusToChangeType[skip]; ok {
			t.Errorf("status %q should not be in map (environments with this status are skipped)", skip)
		}
	}
}

func TestApprovalSummary(t *testing.T) {
	approvals := []ReleaseApproval{
		{IsAutomated: true, Status: "approved", Approver: &IdentityRef{UniqueName: "system"}},
		{IsAutomated: false, Status: "approved", Approver: &IdentityRef{UniqueName: "alice@example.com"}, ApprovedBy: &IdentityRef{UniqueName: "alice@example.com"}},
		{IsAutomated: false, Status: "skipped", Approver: &IdentityRef{UniqueName: "bob@example.com"}, Comments: "not needed"},
	}
	got := approvalSummary(approvals)
	if len(got) != 1 {
		t.Fatalf("expected 1 entry (automated and skipped excluded), got %d", len(got))
	}
	if got[0]["approver"] != "alice@example.com" || got[0]["status"] != "approved" {
		t.Errorf("entry 0 unexpected: %v", got[0])
	}
}

// TestBuildReleaseResultChanges verifies that buildReleaseResult produces changes
// with ExternalID and ConfigType set, so they can be resolved against the parent
// config item when the TempCache is pre-populated.
func TestBuildReleaseResultChanges(t *testing.T) {
	project := Project{Name: "MyProject"}
	def := ReleaseDefinition{ID: 7, Name: "Deploy", Path: `\`}

	const webURL = "https://dev.azure.com/myorg/myproject/_release?releaseId=1"
	releaseCreatedAt := time.Now().Add(-1 * time.Hour)
	releases := []Release{
		{
			ID:        1,
			Name:      "Release-1",
			CreatedOn: releaseCreatedAt,
			CreatedBy: &IdentityRef{UniqueName: "user@example.com"},
			Links:     map[string]Link{"web": {Href: webURL}},
			// The ADO list-releases API returns zero for env.createdOn/modifiedOn;
			// filtering is done on release.CreatedOn instead.
			Environments: []ReleaseEnvironment{
				{
					ID: 10, Name: "Staging", Status: "succeeded",
					PreDeployApprovals: []ReleaseApproval{
						{IsAutomated: false, Status: "approved", Approver: &IdentityRef{UniqueName: "approver@example.com"}, ApprovedBy: &IdentityRef{UniqueName: "approver@example.com"}},
					},
				},
				{ID: 11, Name: "Prod", Status: "inProgress"},
				{ID: 12, Name: "DR", Status: "notStarted"}, // must be excluded
			},
		},
	}

	cutoff := releaseCreatedAt.Add(-time.Minute) // release.CreatedOn is after cutoff
	config := v1.AzureDevops{Organization: "myorg"}

	result := buildReleaseResult(config, project, def, releases, cutoff)

	if result.ID != "MyProject/7" {
		t.Errorf("result.ID = %q, want %q", result.ID, "MyProject/7")
	}

	if len(result.Changes) != 2 {
		t.Fatalf("expected 2 changes, got %d", len(result.Changes))
	}

	for _, ch := range result.Changes {
		if ch.ExternalID != "MyProject/7" {
			t.Errorf("change ExternalID = %q, want %q", ch.ExternalID, "MyProject/7")
		}
		if ch.ConfigType != ReleaseType {
			t.Errorf("change ConfigType = %q, want %q", ch.ConfigType, ReleaseType)
		}
		if ch.ExternalChangeID == "" {
			t.Error("change ExternalChangeID must not be empty")
		}
		if ch.Source != webURL {
			t.Errorf("change Source = %q, want %q", ch.Source, webURL)
		}
		if got, ok := ch.Details["webUrl"]; !ok || got != webURL {
			t.Errorf("change Details[webUrl] = %v, want %q", got, webURL)
		}
	}

	if result.Changes[0].ChangeType != ChangeTypeSucceeded {
		t.Errorf("Staging change type = %q, want %q", result.Changes[0].ChangeType, ChangeTypeSucceeded)
	}
	if result.Changes[1].ChangeType != ChangeTypeInProgress {
		t.Errorf("Prod change type = %q, want %q", result.Changes[1].ChangeType, ChangeTypeInProgress)
	}

	// Staging should carry the pre-deploy approval
	pre, ok := result.Changes[0].Details["preDeployApprovals"].([]map[string]any)
	if !ok || len(pre) != 1 {
		t.Fatalf("Staging: expected 1 preDeployApproval entry, got %v", result.Changes[0].Details["preDeployApprovals"])
	}
	if pre[0]["approver"] != "approver@example.com" {
		t.Errorf("preDeployApprovals[0].approver = %v, want %q", pre[0]["approver"], "approver@example.com")
	}

	// Prod has no approvals — key must be absent
	if _, ok := result.Changes[1].Details["preDeployApprovals"]; ok {
		t.Error("Prod: unexpected preDeployApprovals in details")
	}
}
