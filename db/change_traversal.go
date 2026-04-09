package db

import (
	"fmt"

	"github.com/flanksource/commons/logger"
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/db/models"
	"github.com/flanksource/duty"
	dutyModels "github.com/flanksource/duty/models"
	"github.com/samber/lo"
)

const maxTraversalDepth = 50

func resolveChange(change *v1.ChangeResult, action string, targetConfigID string) {
	change.Resolved = &dutyModels.ConfigChange{
		ConfigID:          targetConfigID,
		ChangeType:        change.ChangeType,
		Severity:          dutyModels.Severity(change.Severity),
		Source:            change.Source,
		Summary:           change.Summary,
		Patches:           change.Patches,
		ExternalCreatedBy: change.CreatedBy,
		CreatedAt:         change.CreatedAt,
	}
	change.Resolved.Action = action
	if change.Diff != nil {
		change.Resolved.Diff = *change.Diff
	}
}

// findAncestor walks the parent_id chain from ci and returns the first
// ancestor whose Type matches ancestorType. If ancestorType is empty,
// returns the immediate parent. Returns nil if no match is found.
func findAncestor(ctx api.ScrapeContext, ci *models.ConfigItem, ancestorType string) (*models.ConfigItem, error) {
	if ci == nil || ci.ParentID == nil {
		return nil, nil
	}

	if ancestorType == "" {
		return ctx.TempCache().Get(ctx, lo.FromPtr(ci.ParentID))
	}

	currentID := lo.FromPtr(ci.ParentID)
	for depth := 0; depth < maxTraversalDepth && currentID != ""; depth++ {
		parent, err := ctx.TempCache().Get(ctx, currentID)
		if err != nil {
			return nil, fmt.Errorf("failed to get parent %s: %w", currentID, err)
		}
		if parent == nil {
			return nil, nil
		}
		if parent.Type == ancestorType {
			return parent, nil
		}
		currentID = lo.FromPtr(parent.ParentID)
	}

	return nil, nil
}

// processMoveUpCopyUp handles move-up and copy-up actions on changes.
// It resolves the target ancestor config item and either redirects the
// change (move-up) or creates a duplicate (copy-up).
//
// Returns additional changes to create (for copy-up).
func processMoveUpCopyUp(ctx api.ScrapeContext, result *v1.ScrapeResult, ci *models.ConfigItem) []v1.ChangeResult {
	var additional []v1.ChangeResult

	for i := len(result.Changes) - 1; i >= 0; i-- {
		change := &result.Changes[i]
		if change.Action != v1.MoveUp && change.Action != v1.CopyUp {
			continue
		}

		if ci == nil {
			logger.Warnf("move-up/copy-up: source config item is nil for change %s", change.ExternalChangeID)
			change.Action = ""
			continue
		}

		ancestor, err := findAncestor(ctx, ci, change.AncestorType)
		if err != nil {
			logger.Warnf("move-up/copy-up: error finding ancestor (type=%s) for config %s: %v",
				change.AncestorType, ci.ID, err)
			change.Action = ""
			continue
		}

		if ancestor == nil {
			logger.Warnf("move-up/copy-up: no ancestor found (type=%s) for config %s/%s",
				change.AncestorType, ci.Type, lo.FromPtr(ci.Name))
			change.Action = ""
			continue
		}

		if change.Action == v1.MoveUp {
			resolveChange(change, string(v1.MoveUp), ancestor.ID)
			change.ConfigID = ancestor.ID
			change.Action = ""
		} else {
			copied := *change
			copied.ConfigID = ancestor.ID
			copied.Action = ""
			if change.ExternalChangeID != "" {
				copied.ExternalChangeID = change.ExternalChangeID + ":copy-up:" + ancestor.ID
			}
			if change.Details != nil {
				copiedDetails := make(map[string]any, len(change.Details))
				for k, v := range change.Details {
					copiedDetails[k] = v
				}
				copied.Details = copiedDetails
			}
			resolveChange(&copied, string(v1.CopyUp), ancestor.ID)
			additional = append(additional, copied)
			resolveChange(change, string(v1.CopyUp), ci.ID)
			change.Action = ""
		}
	}

	return additional
}

// processCopyMove handles copy and move actions on changes by resolving
// target config items via RelationshipSelectorTemplate lookups.
func processCopyMove(ctx api.ScrapeContext, result *v1.ScrapeResult, ci *models.ConfigItem) []v1.ChangeResult {
	var additional []v1.ChangeResult

	for i := len(result.Changes) - 1; i >= 0; i-- {
		change := &result.Changes[i]
		if (change.Action != v1.Copy && change.Action != v1.Move) || change.Target == nil {
			continue
		}

		env := change.AsMap()
		change.FlushMap()
		env["change"] = change.AsMap()
		if ci != nil {
			env["config"] = map[string]any{
				"name":        lo.FromPtr(ci.Name),
				"type":        ci.Type,
				"external_id": ci.ExternalID,
				"labels":      ci.Labels,
				"tags":        ci.Tags,
			}
		}

		target := *change.Target
		if target.Scope.IsEmpty() && ctx.ScraperID() != "" {
			target.Scope = duty.Lookup{Value: ctx.ScraperID()}
		}

		targetIDs, err := duty.LookupConfigs(ctx.DutyContext(), target, result.Labels, env)
		if err != nil {
			logger.Warnf("copy/move: error looking up targets for change %s: %v", change.ExternalChangeID, err)
			change.Action = ""
			change.Target = nil
			continue
		}

		if len(targetIDs) == 0 {
			logger.Warnf("copy/move: no targets found for change %s", change.ExternalChangeID)
			change.Action = ""
			change.Target = nil
			continue
		}

		ids := make([]string, len(targetIDs))
		for j, id := range targetIDs {
			ids[j] = id.String()
		}

		additional = append(additional, applyCopyMove(change, ids, change.Action)...)
		change.Target = nil
	}

	return additional
}

// applyCopyMove applies copy or move fan-out logic to a change given resolved target IDs.
// For move: first target gets the original change, rest get copies.
// For copy: all targets get copies, original is left on its parent.
func applyCopyMove(change *v1.ChangeResult, targetIDs []string, action v1.ChangeAction) []v1.ChangeResult {
	if len(targetIDs) == 0 {
		change.Action = ""
		return nil
	}

	var additional []v1.ChangeResult

	if action == v1.Move {
		resolveChange(change, string(v1.Move), targetIDs[0])
		change.ConfigID = targetIDs[0]
		change.Action = ""
		for _, id := range targetIDs[1:] {
			copied := *change
			copied.ConfigID = id
			if change.ExternalChangeID != "" {
				copied.ExternalChangeID = change.ExternalChangeID + ":copy:" + id
			}
			if change.Details != nil {
				copiedDetails := make(map[string]any, len(change.Details))
				for k, v := range change.Details {
					copiedDetails[k] = v
				}
				copied.Details = copiedDetails
			}
			resolveChange(&copied, string(v1.Move), id)
			copied.FlushMap()
			additional = append(additional, copied)
		}
	} else {
		resolveChange(change, string(v1.Copy), change.ConfigID)
		change.Action = ""
		for _, id := range targetIDs {
			copied := *change
			copied.ConfigID = id
			if change.ExternalChangeID != "" {
				copied.ExternalChangeID = change.ExternalChangeID + ":copy:" + id
			}
			if change.Details != nil {
				copiedDetails := make(map[string]any, len(change.Details))
				for k, v := range change.Details {
					copiedDetails[k] = v
				}
				copied.Details = copiedDetails
			}
			resolveChange(&copied, string(v1.Copy), id)
			copied.FlushMap()
			additional = append(additional, copied)
		}
	}

	return additional
}
