package db

import (
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/config-db/scrapers/extract"
	dutyModels "github.com/flanksource/duty/models"
	"github.com/google/uuid"
	"github.com/patrickmn/go-cache"

	"github.com/flanksource/config-db/api"
	"time"
)

var _ extract.Resolver = &DBResolver{}

type DBResolver struct {
	ctx api.ScrapeContext
}

func NewDBResolver(ctx api.ScrapeContext) *DBResolver {
	return &DBResolver{ctx: ctx}
}

func (d *DBResolver) SyncExternalUsers(users []dutyModels.ExternalUser, scraperID *uuid.UUID) ([]dutyModels.ExternalUser, map[uuid.UUID]uuid.UUID, error) {
	now := time.Now()
	resolved, _, err := resolveExternalUsers(d.ctx, users, scraperID, now)
	if err != nil {
		return nil, nil, err
	}
	for _, u := range resolved {
		for _, alias := range u.Aliases {
			ExternalUserCache.Set(alias, u.ID, cache.DefaultExpiration)
		}
	}
	return resolved, nil, nil
}

func (d *DBResolver) SyncExternalGroups(groups []dutyModels.ExternalGroup, scraperID *uuid.UUID) ([]dutyModels.ExternalGroup, map[uuid.UUID]uuid.UUID, error) {
	now := time.Now()
	resolved, _, err := resolveExternalGroups(d.ctx, groups, scraperID, now)
	if err != nil {
		return nil, nil, err
	}
	for _, g := range resolved {
		for _, alias := range g.Aliases {
			ExternalGroupCache.Set(alias, g.ID, cache.DefaultExpiration)
		}
	}
	return resolved, nil, nil
}

func (d *DBResolver) SyncExternalRoles(roles []dutyModels.ExternalRole, scraperID *uuid.UUID) ([]dutyModels.ExternalRole, error) {
	now := time.Now()
	resolved, _, err := resolveExternalRoles(d.ctx, roles, scraperID, now)
	if err != nil {
		return nil, err
	}
	for _, r := range resolved {
		for _, alias := range r.Aliases {
			ExternalRoleCache.Set(alias, r.ID, cache.DefaultExpiration)
		}
	}
	return resolved, nil
}

func (d *DBResolver) FindUserIDByAliases(aliases []string) (*uuid.UUID, error) {
	return findExternalEntityIDByAliases[dutyModels.ExternalUser](d.ctx, aliases)
}

func (d *DBResolver) FindRoleIDByAliases(aliases []string) (*uuid.UUID, error) {
	return findExternalEntityIDByAliases[dutyModels.ExternalRole](d.ctx, aliases)
}

func (d *DBResolver) FindGroupIDByAliases(aliases []string) (*uuid.UUID, error) {
	return findExternalEntityIDByAliases[dutyModels.ExternalGroup](d.ctx, aliases)
}

func (d *DBResolver) FindConfigIDByExternalID(ext v1.ExternalID) (uuid.UUID, error) {
	config, err := d.ctx.TempCache().FindExternalID(d.ctx, ext)
	if err != nil {
		return uuid.Nil, err
	}
	if config == "" {
		return uuid.Nil, nil
	}
	return uuid.Parse(config)
}
