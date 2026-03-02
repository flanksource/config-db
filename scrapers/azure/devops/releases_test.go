package devops

import (
	"testing"
	"time"

	v1 "github.com/flanksource/config-db/api/v1"
	dutyModels "github.com/flanksource/duty/models"
)

// helpers for building test identities

func identityRef(uniqueName, displayName, id string) *IdentityRef {
	return &IdentityRef{UniqueName: uniqueName, DisplayName: displayName, ID: id}
}

func makeUsers(identities ...*IdentityRef) map[string]dutyModels.ExternalUser {
	m := make(map[string]dutyModels.ExternalUser)
	for _, id := range identities {
		ensureExternalUser(id, "test-org", m)
	}
	return m
}

func externalID(configType, id string) v1.ExternalID {
	return v1.ExternalID{ConfigType: configType, ExternalID: id}
}

// --- ensureExternalUser ---

func TestEnsureExternalUser_AddsNewUser(t *testing.T) {
	users := make(map[string]dutyModels.ExternalUser)
	identity := identityRef("alice@org.com", "Alice", "alice-id")

	ensureExternalUser(identity, "my-org", users)

	u, ok := users["alice@org.com"]
	if !ok {
		t.Fatal("expected user to be added")
	}
	if u.Name != "Alice" {
		t.Errorf("expected Name=Alice, got %q", u.Name)
	}
	if u.AccountID != "my-org" {
		t.Errorf("expected AccountID=my-org, got %q", u.AccountID)
	}
}

func TestEnsureExternalUser_SkipsNilOrEmpty(t *testing.T) {
	users := make(map[string]dutyModels.ExternalUser)

	ensureExternalUser(nil, "org", users)
	ensureExternalUser(&IdentityRef{UniqueName: ""}, "org", users)

	if len(users) != 0 {
		t.Errorf("expected no users added, got %d", len(users))
	}
}

func TestEnsureExternalUser_IdempotentOnDuplicate(t *testing.T) {
	users := make(map[string]dutyModels.ExternalUser)
	identity := identityRef("alice@org.com", "Alice", "alice-id")

	ensureExternalUser(identity, "org", users)
	ensureExternalUser(identity, "org", users)

	if len(users) != 1 {
		t.Errorf("expected 1 user, got %d", len(users))
	}
}

// --- deploymentAccessLog ---

func TestDeploymentAccessLog_WithKnownIdentity(t *testing.T) {
	identity := identityRef("alice@org.com", "Alice", "alice-id")
	users := makeUsers(identity)
	eid := externalID(ReleaseType, "proj/42")
	createdAt := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)

	log := deploymentAccessLog(identity, eid, createdAt, "Production", users)

	if log == nil {
		t.Fatal("expected non-nil access log")
	}
	if log.ConfigExternalID.ExternalID != eid.ExternalID || log.ConfigExternalID.ConfigType != eid.ConfigType {
		t.Errorf("unexpected ConfigExternalID: %+v", log.ConfigExternalID)
	}
	if log.ConfigAccessLog.CreatedAt != createdAt {
		t.Errorf("unexpected CreatedAt: %v", log.ConfigAccessLog.CreatedAt)
	}
	if log.ConfigAccessLog.Properties["role"] != "Deployment" {
		t.Errorf("expected role=Deployment, got %v", log.ConfigAccessLog.Properties["role"])
	}
	if log.ConfigAccessLog.Properties["environment"] != "Production" {
		t.Errorf("expected environment=Production, got %v", log.ConfigAccessLog.Properties["environment"])
	}
}

func TestDeploymentAccessLog_NilIdentityReturnsNil(t *testing.T) {
	users := make(map[string]dutyModels.ExternalUser)
	log := deploymentAccessLog(nil, externalID(ReleaseType, "proj/1"), time.Now(), "Staging", users)
	if log != nil {
		t.Fatal("expected nil for nil identity")
	}
}

func TestDeploymentAccessLog_UnknownIdentityReturnsNil(t *testing.T) {
	// identity not added to users map
	identity := identityRef("unknown@org.com", "Unknown", "uid")
	log := deploymentAccessLog(identity, externalID(ReleaseType, "proj/1"), time.Now(), "Staging", map[string]dutyModels.ExternalUser{})
	if log != nil {
		t.Fatal("expected nil when identity not in users map")
	}
}

// --- approvalAccessLog ---

func TestApprovalAccessLog_ApprovedIncludesComment(t *testing.T) {
	approver := identityRef("bob@org.com", "Bob", "bob-id")
	users := makeUsers(approver)
	eid := externalID(ReleaseType, "proj/42")
	createdAt := time.Date(2024, 2, 1, 9, 0, 0, 0, time.UTC)

	a := ReleaseApproval{
		Status:     "approved",
		ApprovedBy: approver,
		Comments:   "LGTM",
	}

	log := approvalAccessLog(a, eid, "Prod", createdAt, users)

	if log == nil {
		t.Fatal("expected non-nil log for approved approval")
	}
	if log.ConfigAccessLog.Properties["role"] != "DeploymentApproval" {
		t.Errorf("expected role=DeploymentApproval, got %v", log.ConfigAccessLog.Properties["role"])
	}
	if log.ConfigAccessLog.Properties["status"] != "approved" {
		t.Errorf("expected status=approved, got %v", log.ConfigAccessLog.Properties["status"])
	}
	if log.ConfigAccessLog.Properties["comments"] != "LGTM" {
		t.Errorf("expected comments=LGTM, got %v", log.ConfigAccessLog.Properties["comments"])
	}
	if log.ConfigAccessLog.Properties["environment"] != "Prod" {
		t.Errorf("expected environment=Prod, got %v", log.ConfigAccessLog.Properties["environment"])
	}
}

func TestApprovalAccessLog_RejectedNoComment(t *testing.T) {
	approver := identityRef("carol@org.com", "Carol", "carol-id")
	users := makeUsers(approver)

	a := ReleaseApproval{
		Status:     "rejected",
		ApprovedBy: approver,
	}

	log := approvalAccessLog(a, externalID(ReleaseType, "proj/1"), "Staging", time.Now(), users)

	if log == nil {
		t.Fatal("expected non-nil log for rejected approval")
	}
	if _, hasComment := log.ConfigAccessLog.Properties["comments"]; hasComment {
		t.Error("expected no comments key for empty comment")
	}
}

func TestApprovalAccessLog_PendingReturnsNil(t *testing.T) {
	approver := identityRef("dave@org.com", "Dave", "dave-id")
	users := makeUsers(approver)

	a := ReleaseApproval{Status: "pending", Approver: approver}
	log := approvalAccessLog(a, externalID(ReleaseType, "proj/1"), "Staging", time.Now(), users)
	if log != nil {
		t.Fatal("expected nil for pending approval")
	}
}

func TestApprovalAccessLog_AutomatedReturnsNil(t *testing.T) {
	approver := identityRef("auto@org.com", "Auto", "auto-id")
	users := makeUsers(approver)

	a := ReleaseApproval{Status: "approved", IsAutomated: true, ApprovedBy: approver}
	log := approvalAccessLog(a, externalID(ReleaseType, "proj/1"), "Staging", time.Now(), users)
	if log != nil {
		t.Fatal("expected nil for automated approval")
	}
}

func TestApprovalAccessLog_SkippedReturnsNil(t *testing.T) {
	approver := identityRef("eve@org.com", "Eve", "eve-id")
	users := makeUsers(approver)

	a := ReleaseApproval{Status: "skipped", ApprovedBy: approver}
	log := approvalAccessLog(a, externalID(ReleaseType, "proj/1"), "Staging", time.Now(), users)
	if log != nil {
		t.Fatal("expected nil for skipped approval")
	}
}

func TestApprovalAccessLog_FallsBackToApprover(t *testing.T) {
	approver := identityRef("fallback@org.com", "Fallback", "fb-id")
	users := makeUsers(approver)

	// ApprovedBy is nil, Approver is set
	a := ReleaseApproval{
		Status:   "approved",
		Approver: approver,
	}

	log := approvalAccessLog(a, externalID(ReleaseType, "proj/1"), "Staging", time.Now(), users)
	if log == nil {
		t.Fatal("expected non-nil log when falling back to Approver")
	}
}

// --- buildReleaseResult access log integration ---

func makeDef(id int, name, path string) ReleaseDefinition {
	return ReleaseDefinition{ID: id, Name: name, Path: path}
}

func makeRelease(id int, createdBy *IdentityRef, envs []ReleaseEnvironment, createdOn time.Time) Release {
	return Release{
		ID:           id,
		Name:         "Release-1",
		CreatedOn:    createdOn,
		CreatedBy:    createdBy,
		Environments: envs,
	}
}

func TestBuildReleaseResult_EmitsDeploymentAccessLog(t *testing.T) {
	org := "test-org"
	trigger := identityRef("alice@org.com", "Alice", "alice-id")
	createdOn := time.Now().Add(-1 * time.Hour)
	cutoff := createdOn.Add(-1 * time.Hour)

	def := makeDef(1, "Deploy", `\`)
	env := ReleaseEnvironment{ID: 10, Name: "Production", Status: "succeeded"}
	release := makeRelease(100, trigger, []ReleaseEnvironment{env}, createdOn)

	result := buildReleaseResult(
		v1.AzureDevops{Organization: org},
		Project{Name: "MyProject"},
		def,
		[]Release{release},
		cutoff,
	)

	if len(result.ConfigAccessLogs) == 0 {
		t.Fatal("expected at least one access log entry")
	}
	found := false
	for _, log := range result.ConfigAccessLogs {
		if log.ConfigAccessLog.Properties["role"] == "Deployment" &&
			log.ConfigAccessLog.Properties["environment"] == "Production" {
			found = true
		}
	}
	if !found {
		t.Errorf("no Deployment access log for Production env; logs: %+v", result.ConfigAccessLogs)
	}
}

func TestBuildReleaseResult_EmitsApprovalAccessLog(t *testing.T) {
	org := "test-org"
	trigger := identityRef("alice@org.com", "Alice", "alice-id")
	approver := identityRef("bob@org.com", "Bob", "bob-id")
	createdOn := time.Now().Add(-1 * time.Hour)
	cutoff := createdOn.Add(-1 * time.Hour)

	def := makeDef(1, "Deploy", `\`)
	env := ReleaseEnvironment{
		ID:     10,
		Name:   "Staging",
		Status: "succeeded",
		PreDeployApprovals: []ReleaseApproval{
			{Status: "approved", ApprovedBy: approver, Comments: "OK"},
		},
	}
	release := makeRelease(100, trigger, []ReleaseEnvironment{env}, createdOn)

	result := buildReleaseResult(
		v1.AzureDevops{Organization: org},
		Project{Name: "MyProject"},
		def,
		[]Release{release},
		cutoff,
	)

	found := false
	for _, log := range result.ConfigAccessLogs {
		if log.ConfigAccessLog.Properties["role"] == "DeploymentApproval" &&
			log.ConfigAccessLog.Properties["status"] == "approved" {
			found = true
		}
	}
	if !found {
		t.Errorf("no DeploymentApproval access log; logs: %+v", result.ConfigAccessLogs)
	}
}

func TestBuildReleaseResult_SkipsPendingApprovalEnv(t *testing.T) {
	org := "test-org"
	trigger := identityRef("alice@org.com", "Alice", "alice-id")
	createdOn := time.Now().Add(-1 * time.Hour)
	cutoff := createdOn.Add(-1 * time.Hour)

	def := makeDef(1, "Deploy", `\`)
	env := ReleaseEnvironment{
		ID:     10,
		Name:   "Prod",
		Status: "inProgress",
		PreDeployApprovals: []ReleaseApproval{
			{Status: "pending", IsAutomated: false},
		},
	}
	release := makeRelease(100, trigger, []ReleaseEnvironment{env}, createdOn)

	result := buildReleaseResult(
		v1.AzureDevops{Organization: org},
		Project{Name: "MyProject"},
		def,
		[]Release{release},
		cutoff,
	)

	if len(result.Changes) != 0 {
		t.Errorf("expected no changes for pending-approval env, got %d", len(result.Changes))
	}
	for _, log := range result.ConfigAccessLogs {
		if log.ConfigAccessLog.Properties["role"] == "DeploymentApproval" {
			t.Errorf("unexpected approval access log for pending approval: %+v", log)
		}
	}
}

func TestBuildReleaseResult_ExcludesStaleReleases(t *testing.T) {
	cutoff := time.Now()
	createdOn := cutoff.Add(-1 * time.Hour) // before cutoff — stale

	def := makeDef(1, "Deploy", `\`)
	env := ReleaseEnvironment{ID: 10, Name: "Prod", Status: "succeeded"}
	release := makeRelease(100, nil, []ReleaseEnvironment{env}, createdOn)

	result := buildReleaseResult(
		v1.AzureDevops{Organization: "org"},
		Project{Name: "MyProject"},
		def,
		[]Release{release},
		cutoff,
	)

	if len(result.Changes) != 0 || len(result.ConfigAccessLogs) != 0 {
		t.Errorf("expected no output for stale release, got changes=%d logs=%d",
			len(result.Changes), len(result.ConfigAccessLogs))
	}
}
