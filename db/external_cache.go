package db

import (
	"fmt"
	"time"

	"github.com/flanksource/commons/logger"
	"github.com/flanksource/commons/properties"
	"github.com/flanksource/config-db/api"
	"github.com/flanksource/duty/context"
	dutyModels "github.com/flanksource/duty/models"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/patrickmn/go-cache"
	"github.com/samber/lo"
	"gorm.io/gorm"
)

var CACHE_TIMEOUT = properties.Duration(time.Hour*24, "external.cache.timeout")

var OrphanCache = cache.New(CACHE_TIMEOUT, CACHE_TIMEOUT)

// ExternalUserCache stores alias -> external_user_id mapping
var ExternalUserCache = cache.New(CACHE_TIMEOUT, CACHE_TIMEOUT)

// ExternalUserIDCache stores external_user_id -> winning external_user_id
// (the id under which the row currently lives after any merges).
var ExternalUserIDCache = cache.New(CACHE_TIMEOUT, CACHE_TIMEOUT)

// ExternalRoleCache stores alias -> external_role_id mapping
var ExternalRoleCache = cache.New(CACHE_TIMEOUT, CACHE_TIMEOUT)

// ExternalRoleIDCache stores external_role_id -> winning external_role_id.
var ExternalRoleIDCache = cache.New(CACHE_TIMEOUT, CACHE_TIMEOUT)

// ExternalGroupCache stores alias -> external_group_id mapping
var ExternalGroupCache = cache.New(CACHE_TIMEOUT, CACHE_TIMEOUT)

// ExternalGroupIDCache stores external_group_id -> winning external_group_id.
var ExternalGroupIDCache = cache.New(CACHE_TIMEOUT, CACHE_TIMEOUT)

// externalEntityWithID is a constraint for external entity types that have an ID field
type externalEntityWithID interface {
	dutyModels.ExternalUser | dutyModels.ExternalRole | dutyModels.ExternalGroup
	TableName() string
}

// getEntityCache returns the appropriate cache for an external entity type
func getEntityCache[T externalEntityWithID]() *cache.Cache {
	var zero T
	switch any(zero).(type) {
	case dutyModels.ExternalUser:
		return ExternalUserCache
	case dutyModels.ExternalRole:
		return ExternalRoleCache
	case dutyModels.ExternalGroup:
		return ExternalGroupCache
	default:
		return nil
	}
}

// getEntityIDCache returns the id-keyed cache for an external entity type.
func getEntityIDCache[T externalEntityWithID]() *cache.Cache {
	var zero T
	switch any(zero).(type) {
	case dutyModels.ExternalUser:
		return ExternalUserIDCache
	case dutyModels.ExternalRole:
		return ExternalRoleIDCache
	case dutyModels.ExternalGroup:
		return ExternalGroupIDCache
	default:
		return nil
	}
}

// WarmExternalEntityCaches pre-fills the user/role/group alias caches from the database.
func WarmExternalEntityCaches(ctx context.Context) {
	type idAliases struct {
		ID      uuid.UUID
		Aliases pq.StringArray `gorm:"type:text[]"`
	}

	for _, table := range []struct {
		name        string
		aliasCache  *cache.Cache
		idCache     *cache.Cache
	}{
		{"external_users", ExternalUserCache, ExternalUserIDCache},
		{"external_roles", ExternalRoleCache, ExternalRoleIDCache},
		{"external_groups", ExternalGroupCache, ExternalGroupIDCache},
	} {
		var rows []idAliases
		if err := ctx.DB().Table(table.name).
			Select("id, aliases").
			Where("deleted_at IS NULL").
			Find(&rows).Error; err != nil {
			logger.Errorf("failed to warm %s cache: %v", table.name, err)
			continue
		}
		for _, row := range rows {
			for _, alias := range row.Aliases {
				table.aliasCache.Set(alias, row.ID, cache.DefaultExpiration)
			}
			table.idCache.Set(row.ID.String(), row.ID, cache.DefaultExpiration)
		}
		logger.Infof("warmed %s cache with %d entities", table.name, len(rows))
	}
}

// findExternalEntityIDByAliases looks up an external entity ID by aliases.
// It first checks the cache, then queries the DB. Returns the ID if found, nil otherwise.
func findExternalEntityIDByAliases[T externalEntityWithID](ctx api.ScrapeContext, aliases []string) (*uuid.UUID, error) {
	ids, err := findAllExternalEntityIDsByAliases[T](ctx, aliases)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, nil
	}
	return lo.ToPtr(ids[0]), nil
}

// findExternalEntityByID resolves an external entity by canonical id. It checks
// the id-cache first, then queries `id =` on the live table. If the row is
// not found by id, it falls back to alias overlap — covering the case where
// the entity was previously merged into a winner whose `aliases` array now
// contains the original (loser) id.
//
// `entity.aliases` is invariant-free of `entity.id` for live entities, so the
// alias fallback only fires for historical/loser ids — never for the entity's
// current canonical id.
func findExternalEntityByID[T externalEntityWithID](ctx api.ScrapeContext, id uuid.UUID) (*uuid.UUID, error) {
	if id == uuid.Nil {
		return nil, nil
	}

	idCache := getEntityIDCache[T]()
	if idCache != nil {
		if cached, ok := idCache.Get(id.String()); ok {
			if winner, valid := cached.(uuid.UUID); valid {
				return &winner, nil
			}
		}
	}

	var zero T
	var foundIDs []uuid.UUID
	err := ctx.DB().Table(zero.TableName()).
		Select("id").
		Where("id = ? AND deleted_at IS NULL", id).
		Limit(1).
		Pluck("id", &foundIDs).Error
	if err != nil && err != gorm.ErrRecordNotFound {
		return nil, fmt.Errorf("failed to query %s by id: %w", zero.TableName(), err)
	}
	var found uuid.UUID
	if len(foundIDs) > 0 {
		found = foundIDs[0]
	}
	if found != uuid.Nil {
		if idCache != nil {
			idCache.Set(id.String(), found, cache.DefaultExpiration)
		}
		return &found, nil
	}

	winner, err := findExternalEntityIDByAliases[T](ctx, []string{id.String()})
	if err != nil {
		return nil, err
	}
	if winner != nil && idCache != nil {
		idCache.Set(id.String(), *winner, cache.DefaultExpiration)
	}
	return winner, nil
}

// findAllExternalEntityIDsByAliases returns all distinct entity IDs that share any alias with the given set.
func findAllExternalEntityIDsByAliases[T externalEntityWithID](ctx api.ScrapeContext, aliases []string) ([]uuid.UUID, error) {
	aliasCache := getEntityCache[T]()
	seen := make(map[uuid.UUID]bool)

	for _, alias := range aliases {
		if cachedID, ok := aliasCache.Get(alias); ok {
			if id, valid := cachedID.(uuid.UUID); valid {
				seen[id] = true
			}
		}
	}

	var zero T
	var dbIDs []uuid.UUID
	if err := ctx.DB().Table(zero.TableName()).
		Select("DISTINCT id").
		Where("aliases && ?", pq.StringArray(aliases)).
		Where("deleted_at IS NULL").
		Pluck("id", &dbIDs).Error; err != nil {
		return nil, fmt.Errorf("failed to query %s by aliases: %w", zero.TableName(), err)
	}

	for _, id := range dbIDs {
		seen[id] = true
	}

	result := make([]uuid.UUID, 0, len(seen))
	for id := range seen {
		result = append(result, id)
	}

	// Populate cache
	for _, id := range result {
		for _, a := range aliases {
			aliasCache.Set(a, id, cache.DefaultExpiration)
		}
	}

	return result, nil
}
