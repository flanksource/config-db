package aws

import (
	"github.com/flanksource/duty/types"

	v1 "github.com/flanksource/config-db/api/v1"
)

// withRaw marshals a typed ConfigChangeDetail (e.g. types.Backup, types.Restore)
// into a ChangeResult.Details map and attaches the full source event under a
// sibling "raw" key so the canonical typed envelope and the lossless AWS
// payload coexist in the same JSON blob.
func withRaw(typed types.ConfigChangeDetail, raw any) v1.JSON {
	details := v1.NewJSON(typed)
	if raw != nil {
		details["raw"] = v1.NewJSON(raw)
	}
	return details
}
