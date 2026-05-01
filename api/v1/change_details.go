package v1

import "github.com/flanksource/duty/types"

// ChangeDetailsWithRaw marshals a typed ConfigChangeDetail (e.g. types.Backup,
// types.Promotion, types.Approval) into a ChangeResult.Details map and attaches
// the full source event under a sibling "raw" key so the canonical typed
// envelope and the lossless provider payload coexist in the same JSON blob.
func ChangeDetailsWithRaw(typed types.ConfigChangeDetail, raw any) JSON {
	details := NewJSON(typed)
	if raw != nil {
		// Store as plain map[string]any (not JSON) so consumers that type-assert
		// against map[string]any on nested fields see consistent types.
		details["raw"] = map[string]any(NewJSON(raw))
	}
	return details
}
