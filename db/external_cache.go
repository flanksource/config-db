package db

import (
	stdcontext "context"
	"fmt"
	"time"

	gocache "github.com/eko/gocache/lib/v4/cache"
	"github.com/flanksource/commons/logger"
	"github.com/flanksource/commons/properties"
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	dutycache "github.com/flanksource/duty/cache"
	dutycontext "github.com/flanksource/duty/context"
	dutyModels "github.com/flanksource/duty/models"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/samber/lo"
	"gorm.io/gorm"
)

var CACHE_TIMEOUT = properties.Duration(time.Hour*24, "external.cache.timeout")

type typedCache[T any] struct {
	inner gocache.CacheInterface[T]
}

func newTypedCache[T any](name string) *typedCache[T] {
	return &typedCache[T]{inner: dutycache.NewCache[T](name, CACHE_TIMEOUT)}
}

func (c *typedCache[T]) Get(key string) (T, bool) {
	value, err := c.inner.Get(stdcontext.Background(), key)
	if err != nil {
		var zero T
		return zero, false
	}
	return value, true
}

func (c *typedCache[T]) Set(key string, value T) {
	_ = c.inner.Set(stdcontext.Background(), key, value)
}

func (c *typedCache[T]) Delete(key string) {
	_ = c.inner.Delete(stdcontext.Background(), key)
}

func (c *typedCache[T]) Flush() {
	_ = c.inner.Clear(stdcontext.Background())
}

var OrphanCache = newTypedCache[bool]("orphan")

// ExternalUserCache stores alias -> external_user_id mapping
var ExternalUserCache = newTypedCache[uuid.UUID]("external-users-alias")

// ExternalUserIDCache stores external_user_id -> winning external_user_id
// (the id under which the row currently lives after any merges).
var ExternalUserIDCache = newTypedCache[uuid.UUID]("external-users-id")

// ExternalRoleCache stores alias -> external_role_id mapping
var ExternalRoleCache = newTypedCache[uuid.UUID]("external-roles-alias")

// ExternalRoleIDCache stores external_role_id -> winning external_role_id.
var ExternalRoleIDCache = newTypedCache[uuid.UUID]("external-roles-id")

// ExternalGroupCache stores alias -> external_group_id mapping
var ExternalGroupCache = newTypedCache[uuid.UUID]("external-groups-alias")

// ExternalGroupIDCache stores external_group_id -> winning external_group_id.
var ExternalGroupIDCache = newTypedCache[uuid.UUID]("external-groups-id")

// externalEntityWithID is a constraint for external entity types that have an ID field
type externalEntityWithID interface {
	dutyModels.ExternalUser | dutyModels.ExternalRole | dutyModels.ExternalGroup
	TableName() string
}

// getEntityCache returns the appropriate cache for an external entity type
func getEntityCache[T externalEntityWithID]() *typedCache[uuid.UUID] {
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
func getEntityIDCache[T externalEntityWithID]() *typedCache[uuid.UUID] {
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

type externalEntityIDAliases struct {
	ID      uuid.UUID
	Aliases pq.StringArray `gorm:"type:text[]"`
}

type externalUserAliasMapping struct {
	ExternalUserID uuid.UUID
	Alias          string
}

func externalUserAliasTableExists(db *gorm.DB) (bool, error) {
	if db == nil {
		return false, nil
	}
	var exists bool
	if err := db.Raw("SELECT to_regclass('public.external_user_aliases') IS NOT NULL").Scan(&exists).Error; err != nil {
		return false, fmt.Errorf("check external_user_aliases table: %w", err)
	}
	return exists, nil
}

func warmExternalEntityCache(ctx dutycontext.Context, table string, aliasCache, idCache *typedCache[uuid.UUID]) int {
	var rows []externalEntityIDAliases
	if err := ctx.DB().Table(table).
		Select("id, aliases").
		Where("deleted_at IS NULL").
		Find(&rows).Error; err != nil {
		logger.Errorf("failed to warm %s cache: %v", table, err)
		return 0
	}
	for _, row := range rows {
		for _, alias := range row.Aliases {
			if norm := v1.NormalizeExternalID(alias); norm != "" {
				aliasCache.Set(norm, row.ID)
			}
		}
		idCache.Set(row.ID.String(), row.ID)
	}
	return len(rows)
}

// warmExternalUserAliasMappings loads the normalized lookup index, but only
// for aliases still present in the owning external_users.aliases source array.
// This guard keeps an inconsistent or stale index row from changing identity
// resolution.
func warmExternalUserAliasMappings(ctx dutycontext.Context) int {
	exists, err := externalUserAliasTableExists(ctx.DB())
	if err != nil {
		logger.Errorf("failed to inspect external_user_aliases: %v", err)
		return 0
	}
	if !exists {
		return 0
	}

	var mappings []externalUserAliasMapping
	if err := ctx.DB().Table("external_user_aliases eua").
		Select("eua.external_user_id, eua.alias").
		Joins(`JOIN external_users eu ON eu.id = eua.external_user_id
			AND eu.deleted_at IS NULL
			AND EXISTS (
				SELECT 1 FROM unnest(COALESCE(eu.aliases, '{}'::text[])) AS a
				WHERE lower(btrim(a)) = eua.alias
			)`).
		Where("eua.deleted_at IS NULL").
		Find(&mappings).Error; err != nil {
		logger.Errorf("failed to warm external_user_aliases cache: %v", err)
		return 0
	}

	for _, mapping := range mappings {
		norm := v1.NormalizeExternalID(mapping.Alias)
		if norm == "" {
			continue
		}
		ExternalUserCache.Set(norm, mapping.ExternalUserID)
		if historicalID, err := uuid.Parse(norm); err == nil && historicalID != uuid.Nil {
			ExternalUserIDCache.Set(historicalID.String(), mapping.ExternalUserID)
		}
	}
	return len(mappings)
}

// RefreshExternalUserCaches reloads user aliases and ID redirects after a
// manual alias or merge notification.
func RefreshExternalUserCaches(ctx dutycontext.Context) {
	ExternalUserCache.Flush()
	ExternalUserIDCache.Flush()
	users := warmExternalEntityCache(ctx, "external_users", ExternalUserCache, ExternalUserIDCache)
	mappings := warmExternalUserAliasMappings(ctx)
	logger.Infof("warmed external_users cache with %d entities and %d mappings", users, mappings)
}

// WarmExternalEntityCaches pre-fills the user/role/group alias caches from the database.
func WarmExternalEntityCaches(ctx dutycontext.Context) {
	RefreshExternalUserCaches(ctx)

	for _, table := range []struct {
		name       string
		aliasCache *typedCache[uuid.UUID]
		idCache    *typedCache[uuid.UUID]
	}{
		{"external_roles", ExternalRoleCache, ExternalRoleIDCache},
		{"external_groups", ExternalGroupCache, ExternalGroupIDCache},
	} {
		table.aliasCache.Flush()
		table.idCache.Flush()
		count := warmExternalEntityCache(ctx, table.name, table.aliasCache, table.idCache)
		logger.Infof("warmed %s cache with %d entities", table.name, count)
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
		if winner, ok := idCache.Get(id.String()); ok {
			return &winner, nil
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
			idCache.Set(id.String(), found)
		}
		return &found, nil
	}

	winner, err := findExternalEntityIDByAliases[T](ctx, []string{id.String()})
	if err != nil {
		return nil, err
	}
	if winner != nil && idCache != nil {
		idCache.Set(id.String(), *winner)
	}
	return winner, nil
}

// findExternalUserIDsInAliasMapping resolves normalized keys through the
// derived lookup index. external_users.aliases remains the source of truth, so
// index rows are accepted only while their alias is still present in that
// array. The separate lookup preserves rolling compatibility with Duty schemas
// that do not have the index table yet.
func findExternalUserIDsInAliasMapping(ctx api.ScrapeContext, aliases []string) ([]uuid.UUID, error) {
	exists, err := externalUserAliasTableExists(ctx.DB())
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, nil
	}

	normalized := normalizeExternalAliases(aliases)
	if len(normalized) == 0 {
		return nil, nil
	}

	var mappings []externalUserAliasMapping
	if err := ctx.DB().Table("external_user_aliases eua").
		Select("eua.external_user_id, eua.alias").
		Joins(`JOIN external_users eu ON eu.id = eua.external_user_id
			AND eu.deleted_at IS NULL
			AND EXISTS (
				SELECT 1 FROM unnest(COALESCE(eu.aliases, '{}'::text[])) AS a
				WHERE lower(btrim(a)) = eua.alias
			)`).
		Where("eua.deleted_at IS NULL AND eua.alias IN ?", normalized).
		Find(&mappings).Error; err != nil {
		return nil, fmt.Errorf("query external user alias mappings: %w", err)
	}

	seen := make(map[uuid.UUID]struct{}, len(mappings))
	ids := make([]uuid.UUID, 0, len(mappings))
	for _, mapping := range mappings {
		if _, ok := seen[mapping.ExternalUserID]; !ok {
			seen[mapping.ExternalUserID] = struct{}{}
			ids = append(ids, mapping.ExternalUserID)
		}
		norm := v1.NormalizeExternalID(mapping.Alias)
		ExternalUserCache.Set(norm, mapping.ExternalUserID)
		if historicalID, err := uuid.Parse(norm); err == nil && historicalID != uuid.Nil {
			ExternalUserIDCache.Set(historicalID.String(), mapping.ExternalUserID)
		}
	}
	return ids, nil
}

// findAllExternalEntityIDsByAliases returns all distinct entity IDs that share any alias with the given set.
func findAllExternalEntityIDsByAliases[T externalEntityWithID](ctx api.ScrapeContext, aliases []string) ([]uuid.UUID, error) {
	aliasCache := getEntityCache[T]()
	idCache := getEntityIDCache[T]()
	seen := make(map[uuid.UUID]bool)
	misses := make([]string, 0, len(aliases))
	checked := make(map[string]bool, len(aliases))

	for _, alias := range aliases {
		// Normalize so mixed-case input from scrapers matches the lowercased
		// rows stored by duty's config_triggers.sql alias trigger.
		norm := v1.NormalizeExternalID(alias)
		if norm == "" || checked[norm] {
			continue
		}
		checked[norm] = true

		if id, ok := aliasCache.Get(norm); ok {
			if idCache != nil {
				if winner, ok := idCache.Get(id.String()); ok {
					id = winner
				}
			}
			seen[id] = true
			continue
		}
		misses = append(misses, norm)
	}

	var zero T
	if len(misses) > 0 {
		if _, isUser := any(zero).(dutyModels.ExternalUser); isUser {
			mappedIDs, err := findExternalUserIDsInAliasMapping(ctx, misses)
			if err != nil {
				return nil, err
			}
			for _, id := range mappedIDs {
				seen[id] = true
			}

			// The mapping table is a normalized index of external_users.aliases.
			// Only keys absent from that index need the array-overlap fallback.
			unmapped := misses[:0]
			for _, alias := range misses {
				if _, ok := aliasCache.Get(alias); !ok {
					unmapped = append(unmapped, alias)
				}
			}
			misses = unmapped
		}
	}

	if len(misses) > 0 {
		var rows []struct {
			ID      uuid.UUID
			Aliases pq.StringArray `gorm:"type:text[]"`
		}
		if err := ctx.DB().Table(zero.TableName()).
			Select("id, aliases").
			Where("aliases && ?", pq.StringArray(misses)).
			Where("deleted_at IS NULL").
			Find(&rows).Error; err != nil {
			return nil, fmt.Errorf("failed to query %s by aliases: %w", zero.TableName(), err)
		}

		for _, row := range rows {
			seen[row.ID] = true
			if idCache != nil {
				idCache.Set(row.ID.String(), row.ID)
			}
			for _, alias := range row.Aliases {
				if norm := v1.NormalizeExternalID(alias); norm != "" {
					aliasCache.Set(norm, row.ID)
				}
			}
		}
	}

	result := make([]uuid.UUID, 0, len(seen))
	for id := range seen {
		result = append(result, id)
	}

	return result, nil
}
