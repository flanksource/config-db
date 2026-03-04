package db

import (
	"errors"
	"fmt"
	"time"

	"github.com/flanksource/commons/logger"
	"github.com/flanksource/commons/properties"
	"github.com/flanksource/config-db/api"
	"github.com/lib/pq"
	"github.com/flanksource/duty/context"
	dutyModels "github.com/flanksource/duty/models"
	"github.com/google/uuid"
	"github.com/patrickmn/go-cache"
	"github.com/samber/lo"
	"gorm.io/gorm"
)

var CACHE_TIMEOUT = properties.Duration(time.Hour*24, "external.cache.timeout")

var OrphanCache = cache.New(CACHE_TIMEOUT, CACHE_TIMEOUT)

// ExternalUserCache stores alias -> external_user_id mapping
var ExternalUserCache = cache.New(CACHE_TIMEOUT, CACHE_TIMEOUT)

// ExternalRoleCache stores alias -> external_role_id mapping
var ExternalRoleCache = cache.New(CACHE_TIMEOUT, CACHE_TIMEOUT)

// ExternalGroupCache stores alias -> external_group_id mapping
var ExternalGroupCache = cache.New(CACHE_TIMEOUT, CACHE_TIMEOUT)

// externalEntityWithID is a constraint for external entity types that have an ID field
type externalEntityWithID interface {
	dutyModels.ExternalUser | dutyModels.ExternalRole | dutyModels.ExternalGroup
	TableName() string
}

// getEntityID extracts the ID from an external entity
func getEntityID[T externalEntityWithID](entity *T) uuid.UUID {
	switch e := any(entity).(type) {
	case *dutyModels.ExternalUser:
		return e.ID
	case *dutyModels.ExternalRole:
		return e.ID
	case *dutyModels.ExternalGroup:
		return e.ID
	default:
		return uuid.Nil
	}
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

// WarmExternalEntityCaches pre-fills the user/role/group alias caches from the database.
func WarmExternalEntityCaches(ctx context.Context) {
	type idAliases struct {
		ID      uuid.UUID
		Aliases pq.StringArray `gorm:"type:text[]"`
	}

	for _, table := range []struct {
		name  string
		cache *cache.Cache
	}{
		{"external_users", ExternalUserCache},
		{"external_roles", ExternalRoleCache},
		{"external_groups", ExternalGroupCache},
	} {
		var rows []idAliases
		if err := ctx.DB().Table(table.name).
			Select("id, aliases").
			Where("deleted_at IS NULL").
			Where("aliases IS NOT NULL AND array_length(aliases, 1) > 0").
			Find(&rows).Error; err != nil {
			logger.Errorf("failed to warm %s cache: %v", table.name, err)
			continue
		}
		for _, row := range rows {
			for _, alias := range row.Aliases {
				table.cache.Set(alias, row.ID, cache.DefaultExpiration)
			}
		}
		logger.Infof("warmed %s cache with %d entities", table.name, len(rows))
	}
}

// findExternalEntityIDByAliases looks up an external entity ID by aliases.
// It first checks the cache, then queries the DB. Returns the ID if found, nil otherwise.
func findExternalEntityIDByAliases[T externalEntityWithID](ctx api.ScrapeContext, aliases []string) (*uuid.UUID, error) {
	aliasCache := getEntityCache[T]()

	for _, alias := range aliases {
		if cachedID, ok := aliasCache.Get(alias); ok {
			id, valid := cachedID.(uuid.UUID)
			if !valid {
				continue
			}
			return lo.ToPtr(id), nil
		}
	}

	// Query DB for any matching alias
	for _, alias := range aliases {
		var entity T
		err := ctx.DB().
			Select("id").
			Where("? = ANY(aliases)", alias).
			Where("deleted_at IS NULL").
			First(&entity).Error

		if err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, fmt.Errorf("failed to query %s by alias: %w", entity.TableName(), err)
			}
			continue
		}

		// Found in DB, populate cache for all aliases and return
		id := getEntityID(&entity)
		for _, a := range aliases {
			aliasCache.Set(a, id, cache.DefaultExpiration)
		}
		return lo.ToPtr(id), nil
	}

	return nil, nil
}
