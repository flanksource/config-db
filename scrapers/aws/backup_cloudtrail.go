package aws

import (
	"encoding/json"

	"github.com/flanksource/duty/types"

	v1 "github.com/flanksource/config-db/api/v1"
)

// backupSnapshotEventNames are CloudTrail EventNames that represent a
// successful backup / snapshot creation across RDS, EBS, and the AWS Backup
// service. Failures surface via the AWS Backup service path, not CloudTrail,
// so these all map to BackupCompleted.
var backupSnapshotEventNames = map[string]struct{}{
	"CreateDBSnapshot":        {},
	"CreateDBClusterSnapshot": {},
	"CopyDBSnapshot":          {},
	"CopyDBClusterSnapshot":   {},
	"CreateSnapshot":          {}, // EBS
	"CreateSnapshots":         {}, // EBS (multi-volume)
	"CopySnapshot":            {}, // EBS
	"StartBackupJob":          {}, // AWS Backup service
}

// backupRestoreEventNames are CloudTrail EventNames that represent a
// restore operation across RDS, EBS, and the AWS Backup service.
var backupRestoreEventNames = map[string]struct{}{
	"RestoreDBInstanceFromDBSnapshot":    {},
	"RestoreDBInstanceToPointInTime":     {},
	"RestoreDBClusterFromSnapshot":       {},
	"RestoreDBClusterToPointInTime":      {},
	"CreateVolumeFromSnapshot":           {}, // EBS
	"CreateVolume":                       {}, // EBS (when SnapshotId is set)
	"StartRestoreJob":                    {}, // AWS Backup service
}

// classifyBackupEvent inspects a CloudTrail event name and — if it is a
// backup or restore lifecycle event — returns the canonical v1 change_type
// and a typed Details payload carrying the full raw cloudtrail event under
// details.raw. Returns ok=false for unrelated events, leaving the caller to
// keep the existing passthrough behaviour.
func classifyBackupEvent(eventName string, cloudTrailEventJSON string) (changeType string, details v1.JSON, ok bool) {
	if _, isBackup := backupSnapshotEventNames[eventName]; isBackup {
		return types.ChangeTypeBackupCompleted,
			v1.ChangeDetailsWithRaw(types.Backup{
				BackupType: types.BackupTypeSnapshot,
				Status:     types.StatusCompleted,
			}, parseCloudTrailEvent(cloudTrailEventJSON)),
			true
	}

	if _, isRestore := backupRestoreEventNames[eventName]; isRestore {
		// CreateVolume is only a restore when it references a snapshot; without
		// SnapshotId in requestParameters it is a plain volume creation.
		if eventName == "CreateVolume" && !cloudTrailEventHasSnapshotID(cloudTrailEventJSON) {
			return "", nil, false
		}
		return types.ChangeTypeBackupRestored,
			v1.ChangeDetailsWithRaw(types.Restore{
				Status: types.StatusCompleted,
			}, parseCloudTrailEvent(cloudTrailEventJSON)),
			true
	}

	return "", nil, false
}

// parseCloudTrailEvent decodes the raw event JSON into a structured object so
// details.raw is a queryable JSON object in Postgres rather than a string.
func parseCloudTrailEvent(s string) any {
	if s == "" {
		return nil
	}
	var out any
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return s // fall back to string if unparseable
	}
	return out
}

// cloudTrailEventHasSnapshotID reports whether the given CloudTrail event's
// requestParameters include a SnapshotId field (any case) — used to detect
// CreateVolume events that are in fact snapshot restores.
func cloudTrailEventHasSnapshotID(s string) bool {
	var evt struct {
		RequestParameters map[string]any `json:"requestParameters"`
	}
	if err := json.Unmarshal([]byte(s), &evt); err != nil {
		return false
	}
	for k, v := range evt.RequestParameters {
		if (k == "snapshotId" || k == "SnapshotId") && v != nil && v != "" {
			return true
		}
	}
	return false
}
