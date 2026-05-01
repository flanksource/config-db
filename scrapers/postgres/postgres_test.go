package postgres

import "testing"

func TestClassifyAccessKeepsMostPermissiveDatabaseRole(t *testing.T) {
	server := serverInfo{HostRef: "localhost", Port: "5432"}
	user := roleInfo{OID: 1, Name: "app_user", CanLogin: true}
	databaseID := databaseExternalID(server, "app")

	access := classifyAccess(
		server,
		[]roleInfo{user},
		[]privilegeSet{
			{
				ResourceType: databaseType,
				ResourceID:   databaseID,
				ResourceName: "app",
				Database:     "app",
				RoleOID:      user.OID,
				Privileges:   map[string]bool{"database:connect": true},
			},
			{
				ResourceType: databaseType,
				ResourceID:   databaseID,
				ResourceName: "app",
				Database:     "app",
				RoleOID:      user.OID,
				Privileges:   map[string]bool{"table:insert": true},
			},
		},
		nil,
		nil,
	)

	if len(access) != 1 {
		t.Fatalf("expected one access row, got %d", len(access))
	}
	if access[0].ConfigExternalID.ConfigType != databaseType {
		t.Fatalf("expected database access, got %s", access[0].ConfigExternalID.ConfigType)
	}
	expectedRole := permissionRoleAlias(server, writerRole)
	if len(access[0].ExternalRoleAliases) != 1 || access[0].ExternalRoleAliases[0] != expectedRole {
		t.Fatalf("expected writer role %q, got %#v", expectedRole, access[0].ExternalRoleAliases)
	}
}

func TestClassifyAccessIncludesSpecificDatabaseRoles(t *testing.T) {
	server := serverInfo{HostRef: "localhost", Port: "5432"}
	user := roleInfo{OID: 1, Name: "app_user", CanLogin: true}
	group := roleInfo{OID: 2, Name: "app_reader"}
	databaseID := databaseExternalID(server, "app")

	access := classifyAccess(
		server,
		[]roleInfo{user, group},
		[]privilegeSet{
			{
				ResourceType: databaseType,
				ResourceID:   databaseID,
				ResourceName: "app",
				Database:     "app",
				RoleOID:      user.OID,
				Privileges:   map[string]bool{"database:connect": true},
			},
			{
				ResourceType: databaseType,
				ResourceID:   databaseID,
				ResourceName: "app",
				Database:     "app",
				RoleOID:      group.OID,
				Privileges:   map[string]bool{"database:connect": true},
			},
		},
		map[int64][]membership{
			user.OID: {{UserOID: user.OID, RoleOID: group.OID}},
		},
		nil,
	)

	var hasGrouped, hasSpecific bool
	for _, item := range access {
		if item.ConfigExternalID.ConfigType != databaseType {
			t.Fatalf("expected database access, got %s", item.ConfigExternalID.ConfigType)
		}
		if len(item.ExternalRoleAliases) != 1 {
			t.Fatalf("expected one role alias, got %#v", item.ExternalRoleAliases)
		}
		switch item.ExternalRoleAliases[0] {
		case permissionRoleAlias(server, readerRole):
			hasGrouped = true
		case dbRoleAlias(server, group.Name):
			hasSpecific = true
		}
	}
	if !hasGrouped || !hasSpecific {
		t.Fatalf("expected grouped reader and specific db role, got %#v", access)
	}
}
