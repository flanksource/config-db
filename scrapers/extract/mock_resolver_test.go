package extract

import (
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty/models"
	"github.com/google/uuid"
)

type MockResolver struct {
	Users  map[string]uuid.UUID // alias -> ID
	Roles  map[string]uuid.UUID
	Groups map[string]uuid.UUID

	Configs map[string]uuid.UUID // "config_type/external_id" -> ID
}

func NewMockResolver() *MockResolver {
	return &MockResolver{
		Users:   make(map[string]uuid.UUID),
		Roles:   make(map[string]uuid.UUID),
		Groups:  make(map[string]uuid.UUID),
		Configs: make(map[string]uuid.UUID),
	}
}

func (m *MockResolver) SyncExternalUsers(users []models.ExternalUser, _ *uuid.UUID) ([]models.ExternalUser, map[uuid.UUID]uuid.UUID, error) {
	idMap := make(map[uuid.UUID]uuid.UUID)
	for i := range users {
		originalID := users[i].ID
		if existingID := m.findExistingUser(users[i].Aliases); existingID != nil {
			users[i].ID = *existingID
		} else if users[i].ID == uuid.Nil {
			users[i].ID = uuid.New()
		}
		if originalID != uuid.Nil && originalID != users[i].ID {
			idMap[originalID] = users[i].ID
		}
		for _, alias := range users[i].Aliases {
			m.Users[alias] = users[i].ID
		}
	}
	return users, idMap, nil
}

func (m *MockResolver) findExistingUser(aliases []string) *uuid.UUID {
	for _, alias := range aliases {
		if id, ok := m.Users[alias]; ok {
			return &id
		}
	}
	return nil
}

func (m *MockResolver) SyncExternalGroups(groups []models.ExternalGroup, _ *uuid.UUID) ([]models.ExternalGroup, map[uuid.UUID]uuid.UUID, error) {
	idMap := make(map[uuid.UUID]uuid.UUID)
	for i := range groups {
		originalID := groups[i].ID
		if existingID := m.findExistingGroup(groups[i].Aliases); existingID != nil {
			groups[i].ID = *existingID
		} else if groups[i].ID == uuid.Nil {
			groups[i].ID = uuid.New()
		}
		if originalID != uuid.Nil && originalID != groups[i].ID {
			idMap[originalID] = groups[i].ID
		}
		for _, alias := range groups[i].Aliases {
			m.Groups[alias] = groups[i].ID
		}
	}
	return groups, idMap, nil
}

func (m *MockResolver) findExistingGroup(aliases []string) *uuid.UUID {
	for _, alias := range aliases {
		if id, ok := m.Groups[alias]; ok {
			return &id
		}
	}
	return nil
}

func (m *MockResolver) SyncExternalRoles(roles []models.ExternalRole, _ *uuid.UUID) ([]models.ExternalRole, error) {
	for i := range roles {
		if existingID := m.findExistingRole(roles[i].Aliases); existingID != nil {
			roles[i].ID = *existingID
		} else if roles[i].ID == uuid.Nil {
			roles[i].ID = uuid.New()
		}
		for _, alias := range roles[i].Aliases {
			m.Roles[alias] = roles[i].ID
		}
	}
	return roles, nil
}

func (m *MockResolver) findExistingRole(aliases []string) *uuid.UUID {
	for _, alias := range aliases {
		if id, ok := m.Roles[alias]; ok {
			return &id
		}
	}
	return nil
}

func (m *MockResolver) FindUserIDByAliases(aliases []string) (*uuid.UUID, error) {
	for _, alias := range aliases {
		if id, ok := m.Users[alias]; ok {
			return &id, nil
		}
	}
	return nil, nil
}

func (m *MockResolver) FindRoleIDByAliases(aliases []string) (*uuid.UUID, error) {
	for _, alias := range aliases {
		if id, ok := m.Roles[alias]; ok {
			return &id, nil
		}
	}
	return nil, nil
}

func (m *MockResolver) FindGroupIDByAliases(aliases []string) (*uuid.UUID, error) {
	for _, alias := range aliases {
		if id, ok := m.Groups[alias]; ok {
			return &id, nil
		}
	}
	return nil, nil
}

func (m *MockResolver) FindConfigIDByExternalID(ext v1.ExternalID) (uuid.UUID, error) {
	key := ext.ConfigType + "/" + ext.ExternalID
	if id, ok := m.Configs[key]; ok {
		return id, nil
	}
	return uuid.Nil, nil
}
