package aws

import (
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	rdsTypes "github.com/aws/aws-sdk-go-v2/service/rds/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/flanksource/duty/types"
	"github.com/samber/lo"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("rdsSnapshotToChange", func() {
	const (
		dbID            = "oma-shared-insurance-nonprod-001"
		snapshotARN     = "arn:aws:rds:eu-west-1:1:snapshot:rds:oma-shared-insurance-nonprod-001-2026-04-12-22-08"
		snapshotName    = "rds:oma-shared-insurance-nonprod-001-2026-04-12-22-08"
		snapshotSizeGiB = int32(100)
		accountID       = "507476443755"
		region          = "eu-west-1"
	)

	snapshotAt := time.Date(2026, 4, 12, 22, 9, 14, 0, time.UTC)

	makeSnapshot := func(status string) rdsTypes.DBSnapshot {
		return rdsTypes.DBSnapshot{
			DBSnapshotArn:        lo.ToPtr(snapshotARN),
			DBSnapshotIdentifier: lo.ToPtr(snapshotName),
			DBInstanceIdentifier: lo.ToPtr(dbID),
			SnapshotType:         lo.ToPtr("automated"),
			SnapshotCreateTime:   lo.ToPtr(snapshotAt),
			AllocatedStorage:     lo.ToPtr(snapshotSizeGiB),
			Status:               lo.ToPtr(status),
			Engine:               lo.ToPtr("postgres"),
			MasterUsername:       lo.ToPtr("admin"),
			PercentProgress:      lo.ToPtr(int32(100)),
			TagList: []rdsTypes.Tag{
				{Key: lo.ToPtr("Environment"), Value: lo.ToPtr("nonprod")},
				{Key: lo.ToPtr("Owner"), Value: lo.ToPtr("data-team")},
			},
		}
	}

	ctx := &AWSContext{
		Session: &aws.Config{Region: region},
		Caller:  &sts.GetCallerIdentityOutput{Account: lo.ToPtr(accountID)},
	}

	DescribeTable("maps snapshot Status to canonical change lifecycle",
		func(status, wantChangeType string, wantTypedStatus, wantKey string, wantOK bool) {
			cr, ok := rdsSnapshotToChange(ctx, makeSnapshot(status))
			Expect(ok).To(Equal(wantOK))
			if !wantOK {
				return
			}
			Expect(cr.ChangeType).To(Equal(wantChangeType))
			Expect(cr.Source).To(Equal(SourceRDSBackups))
			Expect(cr.ExternalChangeID).To(Equal(wantKey))
			Expect(cr.ExternalID).To(Equal(dbID))
			Expect(cr.Details["kind"]).To(Equal("Backup/v1"))
			Expect(cr.Details).To(HaveKey("raw"))
			Expect(cr.Details["status"]).To(Equal(wantTypedStatus))
		},

		// Lifecycle transitions share the ARN as the key so ON CONFLICT
		// upserts them onto a single row.
		Entry("creating → BackupStarted on ARN key",
			"creating", types.ChangeTypeBackupStarted, string(types.StatusRunning), snapshotARN, true),
		Entry("available → BackupCompleted on ARN key",
			"available", types.ChangeTypeBackupCompleted, string(types.StatusCompleted), snapshotARN, true),
		Entry("failed → BackupFailed on ARN key",
			"failed", types.ChangeTypeBackupFailed, string(types.StatusFailed), snapshotARN, true),

		// Deletion gets its own row so the historical BackupCompleted record survives.
		Entry("deleting → BackupDeleted on distinct key",
			"deleting", types.ChangeTypeBackupDeleted, "Deleted", snapshotARN+"-deleted", true),
		Entry("deleted → BackupDeleted on distinct key",
			"deleted", types.ChangeTypeBackupDeleted, "Deleted", snapshotARN+"-deleted", true),

		// Unknown states are skipped rather than emitting garbage.
		Entry("modifying → skip", "modifying", "", "", "", false),
	)

	It("only populates EndTimestamp once the snapshot is available", func() {
		creating, _ := rdsSnapshotToChange(ctx, makeSnapshot("creating"))
		available, _ := rdsSnapshotToChange(ctx, makeSnapshot("available"))

		Expect(creating.Details["end"]).To(BeNil(), "creating snapshot must not carry an end timestamp")
		Expect(available.Details["end"]).To(Equal(snapshotAt.Format(time.RFC3339)))
	})

	It("produces identical ExternalChangeID on repeated invocations", func() {
		s := makeSnapshot("available")
		a, _ := rdsSnapshotToChange(ctx, s)
		b, _ := rdsSnapshotToChange(ctx, s)
		Expect(a.ExternalChangeID).To(Equal(b.ExternalChangeID))
		Expect(a.ExternalChangeID).To(Equal(snapshotARN))
	})

	It("keys distinct snapshots independently", func() {
		a := makeSnapshot("available")
		b := makeSnapshot("available")
		b.DBSnapshotArn = lo.ToPtr(snapshotARN + "-other")

		ca, _ := rdsSnapshotToChange(ctx, a)
		cb, _ := rdsSnapshotToChange(ctx, b)
		Expect(ca.ExternalChangeID).ToNot(Equal(cb.ExternalChangeID))
	})

	It("populates the Environment envelope with region+account", func() {
		cr, _ := rdsSnapshotToChange(ctx, makeSnapshot("available"))
		env, ok := cr.Details["environment"].(map[string]any)
		Expect(ok).To(BeTrue(), "environment must be a JSON object")
		Expect(env["identifier"]).To(Equal(region))
		Expect(env["name"]).To(Equal(accountID))
		Expect(env["type"]).To(Equal(string(types.EnvironmentTypeCloud)))
		Expect(env["tags"]).To(HaveKeyWithValue("region", region))
		Expect(env["tags"]).To(HaveKeyWithValue("account_id", accountID))
	})

	It("does not synthesize a fake size from AllocatedStorage", func() {
		// RDS DescribeDBSnapshots does not expose per-snapshot byte size.
		// AllocatedStorage is the instance capacity, not the backup size,
		// so the Backup envelope's size field must be empty rather than
		// reporting a misleading number.
		cr, _ := rdsSnapshotToChange(ctx, makeSnapshot("available"))
		Expect(cr.Details).ToNot(HaveKey("size"))
	})

	It("lifts snapshot TagList into Event.Tags", func() {
		cr, _ := rdsSnapshotToChange(ctx, makeSnapshot("available"))
		tags, ok := cr.Details["tags"].(map[string]any)
		Expect(ok).To(BeTrue(), "tags must be a JSON object")
		Expect(tags).To(HaveKeyWithValue("Environment", "nonprod"))
		Expect(tags).To(HaveKeyWithValue("Owner", "data-team"))
	})

	It("exposes RDS snapshot flavour and capacity in Event.Properties", func() {
		cr, _ := rdsSnapshotToChange(ctx, makeSnapshot("available"))
		props, ok := cr.Details["properties"].(map[string]any)
		Expect(ok).To(BeTrue(), "properties must be a JSON object")
		Expect(props).To(HaveKeyWithValue("snapshot_type", "automated"))
		Expect(props).To(HaveKeyWithValue("engine", "postgres"))
		Expect(props).To(HaveKeyWithValue("allocated_storage_gib", "100"))
		Expect(props).To(HaveKeyWithValue("percent_progress", "100"))
	})

	DescribeTable("CreatedBy reflects who initiated the snapshot",
		func(snapshotType, wantID string, wantType types.IdentityType) {
			s := makeSnapshot("available")
			s.SnapshotType = lo.ToPtr(snapshotType)
			cr, _ := rdsSnapshotToChange(ctx, s)
			createdBy, ok := cr.Details["created_by"].(map[string]any)
			Expect(ok).To(BeTrue(), "created_by must be present and a JSON object")
			Expect(createdBy["id"]).To(Equal(wantID))
			Expect(createdBy["type"]).To(Equal(string(wantType)))
		},
		Entry("automated → System:Auto / rds.amazonaws.com",
			"automated", "rds.amazonaws.com", types.IdentityTypeAuto),
		Entry("awsbackup → System:Auto / backup.amazonaws.com",
			"awsbackup", "backup.amazonaws.com", types.IdentityTypeAuto),
		Entry("manual → User / MasterUsername",
			"manual", "admin", types.IdentityTypeUser),
	)
})
