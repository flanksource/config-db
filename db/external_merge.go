package db

import (
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/flanksource/config-db/api"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type unionFind struct {
	parent map[int]int
	rank   map[int]int
}

func newUnionFind() *unionFind {
	return &unionFind{parent: make(map[int]int), rank: make(map[int]int)}
}

func (uf *unionFind) find(x int) int {
	if _, ok := uf.parent[x]; !ok {
		uf.parent[x] = x
	}
	if uf.parent[x] != x {
		uf.parent[x] = uf.find(uf.parent[x])
	}
	return uf.parent[x]
}

func (uf *unionFind) union(a, b int) {
	ra, rb := uf.find(a), uf.find(b)
	if ra == rb {
		return
	}
	if uf.rank[ra] < uf.rank[rb] {
		ra, rb = rb, ra
	}
	uf.parent[rb] = ra
	if uf.rank[ra] == uf.rank[rb] {
		uf.rank[ra]++
	}
}

type entityAccessors[T any] struct {
	GetID        func(T) uuid.UUID
	SetID        func(*T, uuid.UUID)
	GetAliases   func(T) []string
	SetAliases   func(*T, []string)
	GetUpdatedAt func(T) *time.Time
	MergeScalar  func(winner *T, loser T)
}

// mergeByOverlappingAliases performs batch-internal transitive merge of entities
// that share any alias. Returns merged entities and a map of loser→winner IDs for DB merges.
func mergeByOverlappingAliases[T any](
	ctx api.ScrapeContext,
	items []T,
	acc entityAccessors[T],
	findDBIDs func(aliases []string) ([]uuid.UUID, error),
) ([]T, map[uuid.UUID]uuid.UUID, error) {
	if len(items) == 0 {
		return nil, nil, nil
	}

	// Phase A: batch-internal transitive merge via union-find
	uf := newUnionFind()
	aliasToIdx := make(map[string]int)
	for i, item := range items {
		for _, alias := range acc.GetAliases(item) {
			if prev, ok := aliasToIdx[alias]; ok {
				uf.union(prev, i)
			} else {
				aliasToIdx[alias] = i
			}
		}
	}

	// Group items by root
	groups := make(map[int][]int)
	for i := range items {
		root := uf.find(i)
		groups[root] = append(groups[root], i)
	}

	var merged []T
	dbMerges := make(map[uuid.UUID]uuid.UUID) // loser → winner

	for _, indices := range groups {
		winner := items[indices[0]]
		for _, idx := range indices[1:] {
			loser := items[idx]
			for _, alias := range acc.GetAliases(loser) {
				if !slices.Contains(acc.GetAliases(winner), alias) {
					acc.SetAliases(&winner, append(acc.GetAliases(winner), alias))
				}
			}
			if lt, wt := acc.GetUpdatedAt(loser), acc.GetUpdatedAt(winner); lt != nil && (wt == nil || lt.After(*wt)) {
				acc.MergeScalar(&winner, loser)
			}
		}

		// Phase B: DB overlap detection
		allAliases := acc.GetAliases(winner)
		id := acc.GetID(winner)
		if id != uuid.Nil {
			allAliases = appendUnique(allAliases, id.String())
		}

		existingIDs, err := findDBIDs(allAliases)
		if err != nil {
			return nil, nil, err
		}

		switch len(existingIDs) {
		case 0:
			// new entity, keep as-is
		case 1:
			if id != uuid.Nil && id != existingIDs[0] {
				acc.SetAliases(&winner, appendUnique(acc.GetAliases(winner), id.String()))
				dbMerges[id] = existingIDs[0]
			}
			for _, idx := range indices {
				origID := acc.GetID(items[idx])
				if origID != uuid.Nil && origID != existingIDs[0] {
					dbMerges[origID] = existingIDs[0]
				}
			}
			acc.SetID(&winner, existingIDs[0])
		default:
			// Multiple DB entities overlap — pick the first as winner, merge others
			winnerDBID := existingIDs[0]
			for _, loserID := range existingIDs[1:] {
				dbMerges[loserID] = winnerDBID
			}
			if id != uuid.Nil && id != winnerDBID {
				acc.SetAliases(&winner, appendUnique(acc.GetAliases(winner), id.String()))
				dbMerges[id] = winnerDBID
			}
			for _, idx := range indices {
				origID := acc.GetID(items[idx])
				if origID != uuid.Nil && origID != winnerDBID {
					dbMerges[origID] = winnerDBID
				}
			}
			acc.SetID(&winner, winnerDBID)
		}

		sort.Strings(acc.GetAliases(winner))
		merged = append(merged, winner)
	}

	if len(dbMerges) > 0 {
		ctx.Logger.Infof("external entity merge: %d entities merged into %d", len(dbMerges), len(merged))
	}

	return merged, dbMerges, nil
}

func appendUnique(slice []string, item string) []string {
	if !slices.Contains(slice, item) {
		return append(slice, item)
	}
	return slice
}

// mergeAwayExternalUsers remaps FKs from loser user IDs to the winner and soft-deletes losers.
func mergeAwayExternalUsers(tx *gorm.DB, merges map[uuid.UUID]uuid.UUID) error {
	for loser, winner := range merges {
		for _, stmt := range []string{
			`UPDATE config_access SET external_user_id = ? WHERE external_user_id = ?`,
			`UPDATE access_reviews SET external_user_id = ? WHERE external_user_id = ?`,
			`UPDATE config_access_logs SET external_user_id = ? WHERE external_user_id = ?`,
		} {
			if err := tx.Exec(stmt, winner, loser).Error; err != nil {
				return fmt.Errorf("failed to remap user FK: %w", err)
			}
		}
		// external_user_groups has composite PK — insert-on-conflict then delete
		if err := tx.Exec(`
			INSERT INTO external_user_groups (external_user_id, external_group_id, created_at)
			SELECT ?, external_group_id, created_at FROM external_user_groups
			WHERE external_user_id = ? AND deleted_at IS NULL
			ON CONFLICT (external_user_id, external_group_id) DO NOTHING
		`, winner, loser).Error; err != nil {
			return fmt.Errorf("failed to remap user_groups for user merge: %w", err)
		}
		if err := tx.Exec(`DELETE FROM external_user_groups WHERE external_user_id = ?`, loser).Error; err != nil {
			return fmt.Errorf("failed to delete loser user_groups: %w", err)
		}
		// Merge aliases from loser into winner, then soft-delete
		if err := tx.Exec(`
			UPDATE external_users SET
				aliases = NULLIF(ARRAY(SELECT DISTINCT unnest FROM unnest(aliases || (SELECT COALESCE(aliases, '{}') FROM external_users WHERE id = ?)) ORDER BY 1), '{}'::text[])
			WHERE id = ?
		`, loser, winner).Error; err != nil {
			return fmt.Errorf("failed to merge aliases for user: %w", err)
		}
		if err := tx.Exec(`UPDATE external_users SET deleted_at = NOW() WHERE id = ?`, loser).Error; err != nil {
			return fmt.Errorf("failed to soft-delete loser user: %w", err)
		}
	}
	return nil
}

// mergeAwayExternalGroups remaps FKs from loser group IDs to the winner and soft-deletes losers.
func mergeAwayExternalGroups(tx *gorm.DB, merges map[uuid.UUID]uuid.UUID) error {
	for loser, winner := range merges {
		if err := tx.Exec(`UPDATE config_access SET external_group_id = ? WHERE external_group_id = ?`, winner, loser).Error; err != nil {
			return fmt.Errorf("failed to remap group FK in config_access: %w", err)
		}
		// external_user_groups has composite PK
		if err := tx.Exec(`
			INSERT INTO external_user_groups (external_user_id, external_group_id, created_at)
			SELECT external_user_id, ?, created_at FROM external_user_groups
			WHERE external_group_id = ? AND deleted_at IS NULL
			ON CONFLICT (external_user_id, external_group_id) DO NOTHING
		`, winner, loser).Error; err != nil {
			return fmt.Errorf("failed to remap user_groups for group merge: %w", err)
		}
		if err := tx.Exec(`DELETE FROM external_user_groups WHERE external_group_id = ?`, loser).Error; err != nil {
			return fmt.Errorf("failed to delete loser group memberships: %w", err)
		}
		if err := tx.Exec(`
			UPDATE external_groups SET
				aliases = NULLIF(ARRAY(SELECT DISTINCT unnest FROM unnest(aliases || (SELECT COALESCE(aliases, '{}') FROM external_groups WHERE id = ?)) ORDER BY 1), '{}'::text[])
			WHERE id = ?
		`, loser, winner).Error; err != nil {
			return fmt.Errorf("failed to merge aliases for group: %w", err)
		}
		if err := tx.Exec(`UPDATE external_groups SET deleted_at = NOW() WHERE id = ?`, loser).Error; err != nil {
			return fmt.Errorf("failed to soft-delete loser group: %w", err)
		}
	}
	return nil
}

// mergeAwayExternalRoles remaps FKs from loser role IDs to the winner and soft-deletes losers.
func mergeAwayExternalRoles(tx *gorm.DB, merges map[uuid.UUID]uuid.UUID) error {
	for loser, winner := range merges {
		for _, stmt := range []string{
			`UPDATE config_access SET external_role_id = ? WHERE external_role_id = ?`,
			`UPDATE access_reviews SET external_role_id = ? WHERE external_role_id = ?`,
		} {
			if err := tx.Exec(stmt, winner, loser).Error; err != nil {
				return fmt.Errorf("failed to remap role FK: %w", err)
			}
		}
		if err := tx.Exec(`
			UPDATE external_roles SET
				aliases = NULLIF(ARRAY(SELECT DISTINCT unnest FROM unnest(aliases || (SELECT COALESCE(aliases, '{}') FROM external_roles WHERE id = ?)) ORDER BY 1), '{}'::text[])
			WHERE id = ?
		`, loser, winner).Error; err != nil {
			return fmt.Errorf("failed to merge aliases for role: %w", err)
		}
		if err := tx.Exec(`UPDATE external_roles SET deleted_at = NOW() WHERE id = ?`, loser).Error; err != nil {
			return fmt.Errorf("failed to soft-delete loser role: %w", err)
		}
	}
	return nil
}
