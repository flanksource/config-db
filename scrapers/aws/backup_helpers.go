package aws

import (
	"strconv"

	backupTypes "github.com/aws/aws-sdk-go-v2/service/backup/types"
	rdsTypes "github.com/aws/aws-sdk-go-v2/service/rds/types"
	"github.com/flanksource/duty/types"
	"github.com/samber/lo"
)

// awsEnvironment builds the canonical Environment envelope for AWS-emitted
// changes. Identifier is the region (queryable, indexed), Name is the
// account ID so UIs can render a short human label, and Tags carry
// region+account_id for filtering. Type is always Cloud.
func awsEnvironment(ctx *AWSContext) types.Environment {
	env := types.Environment{
		EnvironmentType: types.EnvironmentTypeCloud,
		Identifier:      ctx.Session.Region,
		Tags:            map[string]string{"region": ctx.Session.Region},
	}
	if ctx.Caller != nil {
		account := lo.FromPtr(ctx.Caller.Account)
		if account != "" {
			env.Name = account
			env.Tags["account_id"] = account
		}
	}
	return env
}

// rdsTagsToMap converts an RDS TagList into the map[string]string shape
// expected by Event.Tags. Skips entries with nil keys to avoid panics.
func rdsTagsToMap(tags []rdsTypes.Tag) map[string]string {
	if len(tags) == 0 {
		return nil
	}
	out := make(map[string]string, len(tags))
	for _, t := range tags {
		k := lo.FromPtr(t.Key)
		if k == "" {
			continue
		}
		out[k] = lo.FromPtr(t.Value)
	}
	return out
}

// rdsSnapshotCreatedBy maps an RDS snapshot to a canonical Identity. Automated
// snapshots are emitted by the RDS service itself; manual snapshots are taken
// by an IAM principal but DescribeDBSnapshots does not return who, so we fall
// back to the instance master username as a best-effort label.
func rdsSnapshotCreatedBy(s rdsTypes.DBSnapshot) types.Identity {
	switch lo.FromPtr(s.SnapshotType) {
	case "automated":
		return types.Identity{
			ID:   "rds.amazonaws.com",
			Type: types.IdentityTypeAuto,
			Name: "RDS automated backup",
		}
	case "awsbackup":
		return types.Identity{
			ID:   "backup.amazonaws.com",
			Type: types.IdentityTypeAuto,
			Name: "AWS Backup",
		}
	case "manual":
		return types.Identity{
			ID:   lo.FromPtr(s.MasterUsername),
			Type: types.IdentityTypeUser,
			Name: lo.FromPtr(s.MasterUsername),
		}
	}
	return types.Identity{}
}

// rdsSnapshotProperties exposes the RDS-specific snapshot flavour and the
// instance-allocated capacity (in GiB) that DescribeDBSnapshots reports.
// These do not have a canonical Backup envelope field — RDS does not return
// per-snapshot byte size — so they live in Event.Properties for queryability.
func rdsSnapshotProperties(s rdsTypes.DBSnapshot) map[string]string {
	props := map[string]string{}
	if v := lo.FromPtr(s.SnapshotType); v != "" {
		props["snapshot_type"] = v
	}
	if v := lo.FromPtr(s.Engine); v != "" {
		props["engine"] = v
	}
	if s.AllocatedStorage != nil {
		props["allocated_storage_gib"] = strconv.FormatInt(int64(*s.AllocatedStorage), 10)
	}
	if s.PercentProgress != nil {
		props["percent_progress"] = strconv.FormatInt(int64(*s.PercentProgress), 10)
	}
	return props
}

// backupJobProperties surfaces AWS Backup job metadata that does not have a
// canonical envelope field but is useful for filtering in Postgres.
func backupJobProperties(job backupTypes.BackupJob) map[string]string {
	props := map[string]string{}
	if v := lo.FromPtr(job.ResourceName); v != "" {
		props["resource_name"] = v
	}
	if v := lo.FromPtr(job.ResourceType); v != "" {
		props["resource_type"] = v
	}
	if v := lo.FromPtr(job.BackupVaultName); v != "" {
		props["vault_name"] = v
	}
	if v := lo.FromPtr(job.BackupType); v != "" {
		props["backup_flavour"] = v
	}
	if v := lo.FromPtr(job.AccountId); v != "" {
		props["account_id"] = v
	}
	if v := lo.FromPtr(job.PercentDone); v != "" {
		props["percent_done"] = v
	}
	if job.IsEncrypted {
		props["encrypted"] = "true"
	}
	return props
}

// recoveryPointProperties surfaces AWS Backup recovery point metadata that
// does not fit the canonical Backup envelope.
func recoveryPointProperties(rp backupTypes.RecoveryPointByBackupVault, vault backupTypes.BackupVaultListMember) map[string]string {
	props := map[string]string{}
	if v := lo.FromPtr(rp.ResourceName); v != "" {
		props["resource_name"] = v
	}
	if v := lo.FromPtr(rp.ResourceType); v != "" {
		props["resource_type"] = v
	}
	if v := lo.FromPtr(vault.BackupVaultName); v != "" {
		props["vault_name"] = v
	}
	if rp.IsEncrypted {
		props["encrypted"] = "true"
	}
	if rp.CreatedBy != nil {
		if v := lo.FromPtr(rp.CreatedBy.BackupRuleId); v != "" {
			props["backup_rule_id"] = v
		}
	}
	return props
}

// recoveryPointCreatedBy maps an AWS Backup RecoveryPointCreator (backup-plan
// metadata) to a canonical Identity. Falls back to the IAM role ARN when no
// plan is associated (ad-hoc / on-demand backups).
func recoveryPointCreatedBy(creator *backupTypes.RecoveryPointCreator, iamRoleArn string) types.Identity {
	if creator != nil && creator.BackupPlanArn != nil {
		return types.Identity{
			ID:   lo.FromPtr(creator.BackupPlanArn),
			Type: types.IdentityTypeAuto,
			Name: lo.CoalesceOrEmpty(lo.FromPtr(creator.BackupPlanName), lo.FromPtr(creator.BackupPlanId), "AWS Backup plan"),
		}
	}
	if iamRoleArn != "" {
		return types.Identity{
			ID:   iamRoleArn,
			Type: types.IdentityTypeRole,
			Name: iamRoleArn,
		}
	}
	return types.Identity{}
}
