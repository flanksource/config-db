package aws

import (
	rdsTypes "github.com/aws/aws-sdk-go-v2/service/rds/types"
	"github.com/flanksource/duty/types"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("rdsChangeType", func() {
	DescribeTable("narrowed to failures and restoration; backup lifecycle is owned by snapshot poller",
		func(sourceType rdsTypes.SourceType, category, message, wantType string, wantOK bool) {
			ct, _, _, skip := rdsChangeType(sourceType, category, message)
			ok := !skip
			Expect(ok).To(Equal(wantOK))
			if !wantOK {
				return
			}
			Expect(ct).To(Equal(wantType))
		},

		// Backup lifecycle is now emitted by rds_backups.go from
		// DescribeDBSnapshots. These messages must be skipped here to
		// avoid duplicating rows that the snapshot poller owns.
		Entry("db-instance Backing up → skip (snapshot poller owns Started)",
			rdsTypes.SourceTypeDbInstance, "backup", "Backing up DB instance", "", false),
		Entry("db-instance Finished → skip (snapshot poller owns Completed)",
			rdsTypes.SourceTypeDbInstance, "backup", "Finished DB Instance backup", "", false),
		Entry("db-snapshot Creating → skip (snapshot poller owns Started)",
			rdsTypes.SourceTypeDbSnapshot, "creation", "Creating automated snapshot", "", false),

		// Restoration is emitted here — restored databases do not surface
		// via DescribeDBSnapshots.
		Entry("db-instance restoration → BackupRestored",
			rdsTypes.SourceTypeDbInstance, "restoration", "Restored from snapshot", types.ChangeTypeBackupRestored, true),
		Entry("db-snapshot restoration → BackupRestored",
			rdsTypes.SourceTypeDbSnapshot, "restoration", "Restored from snapshot", types.ChangeTypeBackupRestored, true),

		// Failures emit from RDS events because a failed backup produces
		// no snapshot to poll. The allowlist triggers on failure verbs
		// across any category.
		Entry("Failed to create snapshot → BackupFailed",
			rdsTypes.SourceTypeDbSnapshot, "creation", "Failed to create automated backup", types.ChangeTypeBackupFailed, true),
		Entry("Error taking snapshot → BackupFailed",
			rdsTypes.SourceTypeDbInstance, "backup", "Error during backup", types.ChangeTypeBackupFailed, true),
		Entry("Incompatible option → BackupFailed",
			rdsTypes.SourceTypeDbInstance, "backup", "Incompatible parameter group", types.ChangeTypeBackupFailed, true),

		// Unrelated categories/messages are skipped.
		Entry("notification → skip",
			rdsTypes.SourceTypeDbInstance, "notification", "Storage threshold approaching", "", false),
	)
})
