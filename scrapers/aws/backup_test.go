package aws

import (
	"encoding/json"
	"time"

	cloudtrailTypes "github.com/aws/aws-sdk-go-v2/service/cloudtrail/types"
	backupTypes "github.com/aws/aws-sdk-go-v2/service/backup/types"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/flanksource/duty/types"
	"github.com/samber/lo"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("classifyBackupEvent", func() {
	DescribeTable("maps AWS event names to canonical change types",
		func(eventName, rawEvent, wantChangeType, wantKind string) {
			ct, details, ok := classifyBackupEvent(eventName, rawEvent)

			if wantChangeType == "" {
				Expect(ok).To(BeFalse(), "expected passthrough for %q", eventName)
				return
			}

			Expect(ok).To(BeTrue())
			Expect(ct).To(Equal(wantChangeType))
			Expect(details).ToNot(BeNil())
			Expect(details["kind"]).To(Equal(wantKind), "details envelope should have canonical kind")
			Expect(details).To(HaveKey("raw"), "details must carry full raw event under .raw")
		},

		Entry("CreateDBSnapshot → BackupCompleted / Backup/v1",
			"CreateDBSnapshot",
			`{"eventName":"CreateDBSnapshot","requestParameters":{"dBSnapshotIdentifier":"snap-1"}}`,
			types.ChangeTypeBackupCompleted, "Backup/v1"),

		Entry("CreateSnapshot (EBS) → BackupCompleted",
			"CreateSnapshot",
			`{"eventName":"CreateSnapshot","requestParameters":{"volumeId":"vol-0abc"}}`,
			types.ChangeTypeBackupCompleted, "Backup/v1"),

		Entry("StartBackupJob → BackupCompleted",
			"StartBackupJob",
			`{"eventName":"StartBackupJob"}`,
			types.ChangeTypeBackupCompleted, "Backup/v1"),

		Entry("RestoreDBInstanceFromDBSnapshot → BackupRestored / Restore/v1",
			"RestoreDBInstanceFromDBSnapshot",
			`{"eventName":"RestoreDBInstanceFromDBSnapshot"}`,
			types.ChangeTypeBackupRestored, "Restore/v1"),

		Entry("CreateVolumeFromSnapshot → BackupRestored",
			"CreateVolumeFromSnapshot",
			`{"eventName":"CreateVolumeFromSnapshot","requestParameters":{"snapshotId":"snap-1"}}`,
			types.ChangeTypeBackupRestored, "Restore/v1"),

		Entry("CreateVolume with snapshotId → BackupRestored",
			"CreateVolume",
			`{"eventName":"CreateVolume","requestParameters":{"snapshotId":"snap-1","size":100}}`,
			types.ChangeTypeBackupRestored, "Restore/v1"),

		Entry("CreateVolume without snapshotId → passthrough",
			"CreateVolume",
			`{"eventName":"CreateVolume","requestParameters":{"size":100}}`,
			"", ""),

		Entry("DeleteDBSnapshot → passthrough (deletion not modelled)",
			"DeleteDBSnapshot",
			`{"eventName":"DeleteDBSnapshot"}`,
			"", ""),

		Entry("RunInstances (unrelated) → passthrough",
			"RunInstances",
			`{"eventName":"RunInstances"}`,
			"", ""),
	)

	It("preserves the full raw event as a structured JSON object (not a string)", func() {
		raw := `{"eventName":"CreateDBSnapshot","eventTime":"2026-04-01T00:00:00Z","requestParameters":{"dBSnapshotIdentifier":"snap-xyz"}}`

		_, details, ok := classifyBackupEvent("CreateDBSnapshot", raw)
		Expect(ok).To(BeTrue())

		rawField, hasRaw := details["raw"]
		Expect(hasRaw).To(BeTrue())

		// Must be a JSON object (queryable as details->'raw'->>'eventName'),
		// not a string-encoded blob. v1.JSON is itself map[string]any, so
		// reflect on the underlying map shape after round-tripping through
		// JSON to match how the value lands in Postgres.
		b, err := json.Marshal(rawField)
		Expect(err).ToNot(HaveOccurred())
		var asMap map[string]any
		Expect(json.Unmarshal(b, &asMap)).To(Succeed(), "details.raw must marshal as a JSON object, got %T", rawField)
		Expect(asMap["eventName"]).To(Equal("CreateDBSnapshot"))
	})
})

var _ = Describe("cloudtrailEventToChange classifier integration", func() {
	It("rewrites ChangeType and Details for CreateDBSnapshot", func() {
		raw := `{"userIdentity":{"type":"IAMUser","userName":"alice"},"eventName":"CreateDBSnapshot","eventSource":"rds.amazonaws.com"}`

		evt := cloudtrailTypes.Event{
			EventId:         lo.ToPtr("evt-1"),
			EventName:       lo.ToPtr("CreateDBSnapshot"),
			CloudTrailEvent: lo.ToPtr(raw),
		}

		change, err := cloudtrailEventToChange(evt, cloudtrailTypes.Resource{})
		Expect(err).ToNot(HaveOccurred())
		Expect(change.ChangeType).To(Equal(types.ChangeTypeBackupCompleted))
		Expect(change.Details["kind"]).To(Equal("Backup/v1"))
		Expect(change.Details).To(HaveKey("raw"))
	})

	It("leaves non-backup events untouched", func() {
		raw := `{"userIdentity":{"type":"IAMUser","userName":"bob"},"eventName":"RunInstances","eventSource":"ec2.amazonaws.com"}`

		evt := cloudtrailTypes.Event{
			EventId:         lo.ToPtr("evt-2"),
			EventName:       lo.ToPtr("RunInstances"),
			CloudTrailEvent: lo.ToPtr(raw),
		}

		change, err := cloudtrailEventToChange(evt, cloudtrailTypes.Resource{})
		Expect(err).ToNot(HaveOccurred())
		Expect(change.ChangeType).To(Equal("RunInstances"))
		// Old-shape passthrough: the JSON parse of the raw cloudtrail string.
		Expect(change.Details).ToNot(HaveKey("kind"))
	})
})

var _ = Describe("AWS Backup service job mappers", func() {
	DescribeTable("backupJobToChange canonicalises job states",
		func(state backupTypes.BackupJobState, wantType string, wantOK bool) {
			job := backupTypes.BackupJob{
				BackupJobId:  lo.ToPtr("job-1"),
				ResourceArn:  lo.ToPtr("arn:aws:rds:us-east-1:1:db:mydb"),
				ResourceType: lo.ToPtr("RDS"),
				ResourceName: lo.ToPtr("mydb"),
				State:        state,
				CreationDate: lo.ToPtr(time.Now()),
			}

			ctx := &AWSContext{Session: &aws.Config{Region: "us-east-1"}}
			cr, ok := backupJobToChange(ctx, job)
			Expect(ok).To(Equal(wantOK))
			if !wantOK {
				return
			}
			Expect(cr.ChangeType).To(Equal(wantType))
			Expect(cr.Source).To(Equal(SourceAWSBackup))
			Expect(cr.Details["kind"]).To(Equal("Backup/v1"))
			Expect(cr.Details).To(HaveKey("raw"))

			// Round-trip the details through JSON to match what lands in Postgres,
			// and confirm the typed payload stays valid.
			b, err := json.Marshal(cr.Details)
			Expect(err).ToNot(HaveOccurred())
			var decoded map[string]any
			Expect(json.Unmarshal(b, &decoded)).To(Succeed())
			Expect(decoded["kind"]).To(Equal("Backup/v1"))
			Expect(decoded["backup_type"]).To(Equal("Snapshot"))
		},

		Entry("Completed → BackupCompleted", backupTypes.BackupJobStateCompleted, types.ChangeTypeBackupCompleted, true),
		Entry("Failed → BackupFailed", backupTypes.BackupJobStateFailed, types.ChangeTypeBackupFailed, true),
		Entry("Aborted → BackupFailed", backupTypes.BackupJobStateAborted, types.ChangeTypeBackupFailed, true),
		Entry("Expired → BackupFailed", backupTypes.BackupJobStateExpired, types.ChangeTypeBackupFailed, true),
		Entry("Partial → BackupFailed", backupTypes.BackupJobStatePartial, types.ChangeTypeBackupFailed, true),

		// In-progress states are skipped entirely.
		Entry("Created → skip", backupTypes.BackupJobStateCreated, "", false),
		Entry("Pending → skip", backupTypes.BackupJobStatePending, "", false),
		Entry("Running → skip", backupTypes.BackupJobStateRunning, "", false),
		Entry("Aborting → skip", backupTypes.BackupJobStateAborting, "", false),
	)

	DescribeTable("restoreJobToChange canonicalises to BackupRestored",
		func(status backupTypes.RestoreJobStatus, wantOK bool) {
			job := backupTypes.RestoreJobsListMember{
				RestoreJobId:       lo.ToPtr("rj-1"),
				ResourceType:       lo.ToPtr("RDS"),
				CreatedResourceArn: lo.ToPtr("arn:aws:rds:us-east-1:1:db:restored"),
				Status:             status,
				CreationDate:       lo.ToPtr(time.Now()),
			}
			ctx := &AWSContext{Session: &aws.Config{Region: "us-east-1"}}

			cr, ok := restoreJobToChange(ctx, job)
			Expect(ok).To(Equal(wantOK))
			if !wantOK {
				return
			}
			Expect(cr.ChangeType).To(Equal(types.ChangeTypeBackupRestored))
			Expect(cr.Details["kind"]).To(Equal("Restore/v1"))
			Expect(cr.Details).To(HaveKey("raw"))
		},

		Entry("Completed → BackupRestored", backupTypes.RestoreJobStatusCompleted, true),
		Entry("Failed → BackupRestored (Failed status)", backupTypes.RestoreJobStatusFailed, true),
		Entry("Aborted → BackupRestored (Aborted status)", backupTypes.RestoreJobStatusAborted, true),
		Entry("Pending → skip", backupTypes.RestoreJobStatusPending, false),
		Entry("Running → skip", backupTypes.RestoreJobStatusRunning, false),
	)
})
