package db

import (
	stdcontext "context"
	"fmt"
	"sync"
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
)

var CACHE_TIMEOUT = properties.Duration(time.Hour*24, "external.cache.timeout")

type typedCache[T any] struct {
	mu    sync.RWMutex
	name  string
	inner gocache.CacheInterface[T]
}

func newTypedCache[T any](name string) *typedCache[T] {
	return &typedCache[T]{name: name, inner: dutycache.NewCache[T](name, CACHE_TIMEOUT)}
}

func (c *typedCache[T]) Get(key string) (T, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	value, err := c.inner.Get(stdcontext.Background(), key)
	if err != nil {
		var zero T
		return zero, false
	}
	return value, true
}

func (c *typedCache[T]) Set(key string, value T) {
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.inner.Set(stdcontext.Background(), key, value)
}

func (c *typedCache[T]) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.inner.Delete(stdcontext.Background(), key)
}

func (c *typedCache[T]) Flush() {
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.inner.Clear(stdcontext.Background())
}

func (c *typedCache[T]) buildReplacement(values map[string]T) (gocache.CacheInterface[T], error) {
	replacement := dutycache.NewCache[T](c.name, CACHE_TIMEOUT)
	for key, value := range values {
		if err := replacement.Set(stdcontext.Background(), key, value); err != nil {
			return nil, fmt.Errorf("populate %s cache: %w", c.name, err)
		}
	}
	return replacement, nil
}

func (c *typedCache[T]) swap(replacement gocache.CacheInterface[T]) {
	c.mu.Lock()
	c.inner = replacement
	c.mu.Unlock()
}

var OrphanCache = newTypedCache[bool]("orphan")

// ExternalUserCache stores alias -> external_user_id mapping
var ExternalUserCache = newTypedCache[uuid.UUID]("external-users-alias")

// ExternalUserIDCache stores external_user_id -> winning external_user_id
// (the id under which the row currently lives after any merges).
var ExternalUserIDCache = newTypedCache[uuid.UUID]("external-users-id")

// externalUserCachesMu makes the alias and ID cache generation switch atomic
// for lookups that consult both caches.
var externalUserCachesMu sync.RWMutex

// externalUserCacheRefreshMu serializes notification, periodic, and startup
// refreshes so an older snapshot cannot replace a newer generation.
var externalUserCacheRefreshMu sync.Mutex

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

func warmExternalEntityCache(ctx dutycontext.Context, table string, aliasCache, idCache *typedCache[uuid.UUID]) (int, error) {
	var rows []externalEntityIDAliases
	if err := ctx.DB().Table(table).
		Select("id, aliases").
		Where("deleted_at IS NULL").
		Find(&rows).Error; err != nil {
		return 0, fmt.Errorf("load %s cache: %w", table, err)
	}

	aliases := make(map[string]uuid.UUID)
	ids := make(map[string]uuid.UUID)
	for _, row := range rows {
		for _, alias := range row.Aliases {
			if norm := v1.NormalizeExternalID(alias); norm != "" {
				aliases[norm] = row.ID
			}
		}
		ids[row.ID.String()] = row.ID
	}
	aliasReplacement, err := aliasCache.buildReplacement(aliases)
	if err != nil {
		return 0, err
	}
	idReplacement, err := idCache.buildReplacement(ids)
	if err != nil {
		return 0, err
	}
	aliasCache.swap(aliasReplacement)
	idCache.swap(idReplacement)
	return len(rows), nil
}

// RefreshExternalUserCaches loads a complete cache generation before swapping
// it into use. external_user_aliases is a required Duty schema dependency; a
// missing table or failed query is returned to the caller and leaves the
// previous cache generation untouched.
func RefreshExternalUserCaches(ctx dutycontext.Context) error {
	externalUserCacheRefreshMu.Lock()
	defer externalUserCacheRefreshMu.Unlock()

	var users []externalEntityIDAliases
	if err := ctx.DB().Table("external_users").
		Select("id, aliases").
		Where("deleted_at IS NULL").
		Find(&users).Error; err != nil {
		return fmt.Errorf("load external_users cache: %w", err)
	}

	// Only accept derived-index rows that still exist in the authoritative
	// external_users.aliases array.
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
		return fmt.Errorf("load external_user_aliases cache: %w", err)
	}

	aliases := make(map[string]uuid.UUID)
	ids := make(map[string]uuid.UUID)
	for _, user := range users {
		ids[user.ID.String()] = user.ID
		for _, alias := range user.Aliases {
			if norm := v1.NormalizeExternalID(alias); norm != "" {
				aliases[norm] = user.ID
			}
		}
	}
	for _, mapping := range mappings {
		norm := v1.NormalizeExternalID(mapping.Alias)
		if norm == "" {
			continue
		}
		aliases[norm] = mapping.ExternalUserID
		if historicalID, err := uuid.Parse(norm); err == nil && historicalID != uuid.Nil {
			ids[historicalID.String()] = mapping.ExternalUserID
		}
	}

	aliasReplacement, err := ExternalUserCache.buildReplacement(aliases)
	if err != nil {
		return err
	}
	idReplacement, err := ExternalUserIDCache.buildReplacement(ids)
	if err != nil {
		return err
	}

	externalUserCachesMu.Lock()
	ExternalUserCache.swap(aliasReplacement)
	ExternalUserIDCache.swap(idReplacement)
	externalUserCachesMu.Unlock()

	logger.Infof("warmed external_users cache with %d entities and %d mappings", len(users), len(mappings))
	return nil
}

// WarmExternalEntityCaches pre-fills the user/role/group alias caches from the database.
func WarmExternalEntityCaches(ctx dutycontext.Context) error {
	if err := RefreshExternalUserCaches(ctx); err != nil {
		return err
	}

	for _, table := range []struct {
		name       string
		aliasCache *typedCache[uuid.UUID]
		idCache    *typedCache[uuid.UUID]
	}{
		{"external_roles", ExternalRoleCache, ExternalRoleIDCache},
		{"external_groups", ExternalGroupCache, ExternalGroupIDCache},
	} {
		count, err := warmExternalEntityCache(ctx, table.name, table.aliasCache, table.idCache)
		if err != nil {
			return err
		}
		logger.Infof("warmed %s cache with %d entities", table.name, count)
	}
	return nil
}

// findExternalEntityIDByAliases looks up an external entity ID by aliases.
// External-user misses remain cache-only; role and group misses retain their
// existing database fallback.
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

// findExternalEntityByID resolves an external entity by canonical ID. External
// users are cache-only because refreshes preload canonical and historical IDs;
// role and group misses retain their existing database fallback.
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
	if _, isUser := any(zero).(dutyModels.ExternalUser); isUser {
		return nil, nil
	}
	var foundIDs []uuid.UUID
	err := ctx.DB().Table(zero.TableName()).
		Select("id").
		Where("id = ? AND deleted_at IS NULL", id).
		Limit(1).
		Pluck("id", &foundIDs).Error
	if err != nil {
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

// findAllExternalEntityIDsByAliases returns all distinct entity IDs that share any alias with the given set.
func findAllExternalEntityIDsByAliases[T externalEntityWithID](ctx api.ScrapeContext, aliases []string) ([]uuid.UUID, error) {
	aliasCache := getEntityCache[T]()
	idCache := getEntityIDCache[T]()
	var zero T
	_, isUser := any(zero).(dutyModels.ExternalUser)
	if isUser {
		externalUserCachesMu.RLock()
		defer externalUserCachesMu.RUnlock()
	}

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

	if isUser {
		result := make([]uuid.UUID, 0, len(seen))
		for id := range seen {
			result = append(result, id)
		}
		return result, nil
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
