package postgres

import (
	"database/sql"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/flanksource/commons/hash"
	"github.com/flanksource/config-db/api"
	v1 "github.com/flanksource/config-db/api/v1"
	"github.com/flanksource/duty/models"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/xo/dburl"

	_ "github.com/lib/pq"
)

const (
	serverType   = "Postgres::Server"
	databaseType = "Postgres::Database"

	readerRole     = "reader"
	writerRole     = "writer"
	ddlAdminRole   = "ddladmin"
	superAdminRole = "superadmin"
)

type Scraper struct{}

type roleInfo struct {
	OID        int64
	Name       string
	CanLogin   bool
	Super      bool
	CreateDB   bool
	CreateRole bool
}

type databaseInfo struct {
	Name        string
	Owner       string
	Encoding    string
	Collation   string
	CType       string
	AllowConn   bool
	ConnLimit   int
	IsTemplate  bool
	ConfigID    string
	ConfigName  string
	ConfigClass string
}

type serverInfo struct {
	Host    string
	HostRef string
	Port    string
	Version string
}

type membership struct {
	UserOID     int64
	RoleOID     int64
	AdminOption bool
}

type privilegeSet struct {
	ResourceType string
	ResourceID   string
	ResourceName string
	Database     string
	Schema       string
	RoleOID      int64
	Privileges   map[string]bool
	Owner        bool
	GrantOption  bool
}

type schemaInfo struct {
	OID          int64
	Name         string
	ResourceID   string
	ResourceName string
	Database     string
}

func (s Scraper) CanScrape(configs v1.ScraperSpec) bool {
	return len(configs.Postgres) > 0
}

func (s Scraper) Scrape(ctx api.ScrapeContext) v1.ScrapeResults {
	var results v1.ScrapeResults
	for _, config := range ctx.ScrapeConfig().Spec.Postgres {
		results = append(results, scrapeOne(ctx, config)...)
	}
	return results
}

func scrapeOne(ctx api.ScrapeContext, config v1.Postgres) v1.ScrapeResults {
	var results v1.ScrapeResults

	connection := config.Connection.GetModel()
	var err error
	if strings.HasPrefix(config.Connection.Connection, "connection://") {
		connection, err = ctx.DutyContext().HydrateConnectionByURL(config.Connection.Connection)
		if err != nil {
			return results.Errorf(err, "failed to hydrate postgres connection %s", config.Connection.Connection)
		}
		if connection == nil {
			return results.Errorf(fmt.Errorf("connection not found"), "failed to find postgres connection %s", config.Connection.Connection)
		}
	} else {
		connection, err = ctx.HydrateConnection(connection)
		if err != nil {
			return results.Errorf(err, "failed to hydrate postgres connection %s", config.Connection.GetEndpoint())
		}
	}

	db, err := dburl.Open(connection.URL)
	if err != nil {
		return results.Errorf(err, "failed to open postgres connection %s", config.Connection.GetEndpoint())
	}
	defer db.Close() // nolint:errcheck

	server, err := fetchServerInfo(db)
	if err != nil {
		return results.Errorf(err, "failed to query postgres server")
	}

	databases, err := fetchDatabases(db, server)
	if err != nil {
		return results.Errorf(err, "failed to query postgres databases")
	}

	serverID := serverExternalID(server)
	results = append(results, v1.ScrapeResult{
		BaseScraper: config.BaseScraper,
		ID:          serverID,
		Name:        server.HostRef + ":" + server.Port,
		Type:        serverType,
		ConfigClass: serverType,
		Config: map[string]any{
			"host":    server.Host,
			"port":    server.Port,
			"version": server.Version,
		},
	})

	for _, database := range databases {
		results = append(results, v1.ScrapeResult{
			BaseScraper: config.BaseScraper,
			ID:          database.ConfigID,
			Name:        database.Name,
			Type:        databaseType,
			ConfigClass: databaseType,
			Config: map[string]any{
				"server_id":         serverID,
				"database":          database.Name,
				"owner":             database.Owner,
				"encoding":          database.Encoding,
				"collation":         database.Collation,
				"ctype":             database.CType,
				"allow_connections": database.AllowConn,
				"connection_limit":  database.ConnLimit,
				"is_template":       database.IsTemplate,
			},
		})
	}

	if !config.Permissions {
		return results
	}

	permissionResults := buildPermissionResults(ctx, config, connection.URL, server, databases)
	results = append(results, permissionResults...)
	return results
}

func buildPermissionResults(ctx api.ScrapeContext, config v1.Postgres, connectionURL string, server serverInfo, databases []databaseInfo) v1.ScrapeResults {
	var results v1.ScrapeResults

	db, err := dburl.Open(connectionURL)
	if err != nil {
		return results.Errorf(err, "failed to open postgres permission connection")
	}
	defer db.Close() // nolint:errcheck

	roles, err := fetchRoles(db)
	if err != nil {
		return results.Errorf(err, "failed to query postgres roles")
	}
	roleByOID := map[int64]roleInfo{}
	for _, role := range roles {
		roleByOID[role.OID] = role
	}

	memberships, err := fetchMemberships(db)
	if err != nil {
		return results.Errorf(err, "failed to query postgres role memberships")
	}
	memberRoles := map[int64][]membership{}
	adminMember := map[string]bool{}
	for _, membership := range memberships {
		memberRoles[membership.UserOID] = append(memberRoles[membership.UserOID], membership)
		if membership.AdminOption {
			adminMember[fmt.Sprintf("%d/%d", membership.UserOID, membership.RoleOID)] = true
		}
	}

	externalUsers := map[string]models.ExternalUser{}
	externalGroups := map[string]models.ExternalGroup{}
	externalRoles := map[string]models.ExternalRole{}
	var userGroups []v1.ExternalUserGroup

	for _, role := range roles {
		if role.CanLogin {
			alias := userAlias(server, role.Name)
			externalUsers[alias] = models.ExternalUser{
				Name:     role.Name,
				Tenant:   serverExternalID(server),
				UserType: "PostgresRole",
				Aliases:  pq.StringArray{alias},
			}
		} else {
			alias := dbRoleAlias(server, role.Name)
			externalGroups[alias] = models.ExternalGroup{
				Name:      role.Name,
				Tenant:    serverExternalID(server),
				GroupType: "PostgresRole",
				Aliases:   pq.StringArray{alias},
			}
			externalRoles[alias] = models.ExternalRole{
				Name:     role.Name,
				Tenant:   serverExternalID(server),
				RoleType: "PostgresRole",
				Aliases:  pq.StringArray{alias},
			}
		}
	}

	for _, membership := range memberships {
		userRole, userOK := roleByOID[membership.UserOID]
		groupRole, groupOK := roleByOID[membership.RoleOID]
		if !userOK || !groupOK || !userRole.CanLogin || groupRole.CanLogin {
			continue
		}
		userGroups = append(userGroups, v1.ExternalUserGroup{
			ExternalUserAliases:  []string{userAlias(server, userRole.Name)},
			ExternalGroupAliases: []string{dbRoleAlias(server, groupRole.Name)},
		})
	}

	for _, roleName := range []string{readerRole, writerRole, ddlAdminRole, superAdminRole} {
		alias := permissionRoleAlias(server, roleName)
		externalRoles[alias] = models.ExternalRole{
			Name:        roleName,
			Tenant:      serverExternalID(server),
			RoleType:    "PostgresPermission",
			Description: "Grouped PostgreSQL permission role",
			Aliases:     pq.StringArray{alias},
		}
	}

	dbPrivs, err := fetchDatabasePrivileges(db, roles, databases, server)
	if err != nil {
		return results.Errorf(err, "failed to query postgres database privileges")
	}
	allPrivs := dbPrivs

	for _, database := range databases {
		if !database.AllowConn || database.IsTemplate {
			continue
		}
		databaseURL, err := connectionURLForDatabase(connectionURL, database.Name)
		if err != nil {
			ctx.Warnf("postgres: failed to build database URL for %s: %v", database.Name, err)
			continue
		}
		databaseDB, err := dburl.Open(databaseURL)
		if err != nil {
			ctx.Warnf("postgres: failed to connect to database %s for permissions: %v", database.Name, err)
			continue
		}

		schemaPrivs, err := scrapeDatabasePermissions(databaseDB, server, database, roles)
		_ = databaseDB.Close()
		if err != nil {
			ctx.Warnf("postgres: failed to scrape database %s permissions: %v", database.Name, err)
			continue
		}
		allPrivs = mergePrivilegeSets(allPrivs, schemaPrivs)
	}

	results = append(results, v1.ScrapeResult{
		BaseScraper:        config.BaseScraper,
		ExternalUsers:      mapValues(externalUsers),
		ExternalGroups:     mapValues(externalGroups),
		ExternalRoles:      mapValues(externalRoles),
		ExternalUserGroups: userGroups,
		ConfigAccess:       dedupeAccess(classifyAccess(server, roles, allPrivs, memberRoles, adminMember)),
	})
	return results
}

func fetchServerInfo(db *sql.DB) (serverInfo, error) {
	var server serverInfo
	err := db.QueryRow(`
		SELECT
			COALESCE(host(inet_server_addr()), 'localhost'),
			current_setting('port'),
			version()
	`).Scan(&server.Host, &server.Port, &server.Version)
	if err != nil {
		return server, err
	}
	server.HostRef = server.Host
	if strings.Contains(server.HostRef, ":") {
		server.HostRef = "[" + server.HostRef + "]"
	}
	return server, nil
}

func fetchDatabases(db *sql.DB, server serverInfo) ([]databaseInfo, error) {
	rows, err := db.Query(`
		SELECT
			d.datname,
			pg_catalog.pg_get_userbyid(d.datdba),
			pg_encoding_to_char(d.encoding),
			d.datcollate,
			d.datctype,
			d.datallowconn,
			d.datconnlimit,
			d.datistemplate
		FROM pg_database d
		ORDER BY d.datname
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() // nolint:errcheck

	var databases []databaseInfo
	for rows.Next() {
		var database databaseInfo
		if err := rows.Scan(&database.Name, &database.Owner, &database.Encoding, &database.Collation, &database.CType, &database.AllowConn, &database.ConnLimit, &database.IsTemplate); err != nil {
			return nil, err
		}
		database.ConfigID = databaseExternalID(server, database.Name)
		database.ConfigName = database.Name
		database.ConfigClass = databaseType
		databases = append(databases, database)
	}
	return databases, rows.Err()
}

func fetchRoles(db *sql.DB) ([]roleInfo, error) {
	rows, err := db.Query(`
		SELECT oid, rolname, rolcanlogin, rolsuper, rolcreatedb, rolcreaterole
		FROM pg_roles
		WHERE rolname !~ '^pg_'
		ORDER BY rolname
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() // nolint:errcheck

	var roles []roleInfo
	for rows.Next() {
		var role roleInfo
		if err := rows.Scan(&role.OID, &role.Name, &role.CanLogin, &role.Super, &role.CreateDB, &role.CreateRole); err != nil {
			return nil, err
		}
		roles = append(roles, role)
	}
	return roles, rows.Err()
}

func fetchMemberships(db *sql.DB) ([]membership, error) {
	rows, err := db.Query(`
		SELECT
			u.oid::bigint AS member,
			g.oid::bigint AS roleid,
			COALESCE(bool_or(m.admin_option), false) AS admin_option
		FROM pg_roles u
		JOIN pg_roles g ON u.oid <> g.oid
		LEFT JOIN pg_auth_members m ON m.member = u.oid AND m.roleid = g.oid
		WHERE u.rolcanlogin
			AND NOT g.rolcanlogin
			AND u.rolname !~ '^pg_'
			AND g.rolname !~ '^pg_'
			AND pg_has_role(u.oid, g.oid, 'USAGE')
		GROUP BY u.oid, g.oid
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() // nolint:errcheck

	var memberships []membership
	for rows.Next() {
		var m membership
		if err := rows.Scan(&m.UserOID, &m.RoleOID, &m.AdminOption); err != nil {
			return nil, err
		}
		memberships = append(memberships, m)
	}
	return memberships, rows.Err()
}

func fetchDatabasePrivileges(db *sql.DB, roles []roleInfo, databases []databaseInfo, server serverInfo) ([]privilegeSet, error) {
	var out []privilegeSet
	for _, role := range roles {
		for _, database := range databases {
			privileges := map[string]bool{}
			for _, privilege := range []string{"CONNECT", "CREATE", "TEMPORARY"} {
				var hasPrivilege bool
				if err := db.QueryRow(`SELECT has_database_privilege($1::oid, $2, $3)`, role.OID, database.Name, privilege).Scan(&hasPrivilege); err != nil {
					return nil, err
				}
				if hasPrivilege {
					privileges["database:"+strings.ToLower(privilege)] = true
				}
			}
			if role.Super {
				privileges["role:superuser"] = true
			}
			if role.CreateDB {
				privileges["role:createdb"] = true
			}
			if role.CreateRole {
				privileges["role:createrole"] = true
			}
			if len(privileges) == 0 {
				continue
			}
			out = append(out, privilegeSet{
				ResourceType: databaseType,
				ResourceID:   databaseExternalID(server, database.Name),
				ResourceName: database.Name,
				Database:     database.Name,
				RoleOID:      role.OID,
				Privileges:   privileges,
			})
		}
	}
	return out, nil
}

func scrapeDatabasePermissions(db *sql.DB, server serverInfo, database databaseInfo, roles []roleInfo) ([]privilegeSet, error) {
	schemas, err := fetchSchemas(db, server, database)
	if err != nil {
		return nil, err
	}

	privs, err := fetchSchemaPrivileges(db, roles, schemas)
	if err != nil {
		return nil, err
	}

	tablePrivs, err := fetchTablePrivileges(db, roles, schemas)
	if err != nil {
		return nil, err
	}
	privs = mergePrivilegeSets(privs, tablePrivs)

	sequencePrivs, err := fetchSequencePrivileges(db, roles, schemas)
	if err != nil {
		return nil, err
	}
	privs = mergePrivilegeSets(privs, sequencePrivs)

	functionPrivs, err := fetchFunctionPrivileges(db, roles, schemas)
	if err != nil {
		return nil, err
	}
	privs = mergePrivilegeSets(privs, functionPrivs)

	return privs, nil
}

func fetchSchemas(db *sql.DB, server serverInfo, database databaseInfo) ([]schemaInfo, error) {
	rows, err := db.Query(`
		SELECT n.oid::bigint, n.nspname
		FROM pg_namespace n
		WHERE n.nspname <> 'information_schema'
			AND n.nspname !~ '^pg_'
		ORDER BY n.nspname
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() // nolint:errcheck

	var schemas []schemaInfo
	for rows.Next() {
		var schema schemaInfo
		if err := rows.Scan(&schema.OID, &schema.Name); err != nil {
			return nil, err
		}
		schema.Database = database.Name
		schema.ResourceID = database.ConfigID
		schema.ResourceName = database.Name
		schemas = append(schemas, schema)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return schemas, nil
}

func fetchSchemaPrivileges(db *sql.DB, roles []roleInfo, schemas []schemaInfo) ([]privilegeSet, error) {
	var out []privilegeSet
	for _, schema := range schemas {
		for _, role := range roles {
			privileges := map[string]bool{}
			for _, privilege := range []string{"USAGE", "CREATE"} {
				var hasPrivilege bool
				if err := db.QueryRow(`SELECT has_schema_privilege($1::oid, $2::oid, $3)`, role.OID, schema.OID, privilege).Scan(&hasPrivilege); err != nil {
					return nil, err
				}
				if hasPrivilege {
					privileges["schema:"+strings.ToLower(privilege)] = true
				}
			}
			if len(privileges) == 0 {
				continue
			}
			out = append(out, privilegeSet{
				ResourceType: databaseType,
				ResourceID:   schema.ResourceID,
				ResourceName: schema.ResourceName,
				Database:     schema.Database,
				Schema:       schema.Name,
				RoleOID:      role.OID,
				Privileges:   privileges,
			})
		}
	}
	return out, nil
}

func fetchTablePrivileges(db *sql.DB, roles []roleInfo, schemas []schemaInfo) ([]privilegeSet, error) {
	rows, err := db.Query(`
		SELECT c.oid::bigint, n.nspname, c.relname
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE c.relkind IN ('r','p','v','m','f')
			AND n.nspname <> 'information_schema'
			AND n.nspname !~ '^pg_'
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() // nolint:errcheck
	type objectInfo struct {
		OID    int64
		Schema string
		Name   string
	}
	var objects []objectInfo
	for rows.Next() {
		var object objectInfo
		if err := rows.Scan(&object.OID, &object.Schema, &object.Name); err != nil {
			return nil, err
		}
		objects = append(objects, object)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	schemaByName := privilegeSchemaMap(schemas)
	var out []privilegeSet
	for _, object := range objects {
		schema, ok := schemaByName[object.Schema]
		if !ok {
			continue
		}
		for _, role := range roles {
			privileges := map[string]bool{}
			for _, privilege := range []string{"SELECT", "INSERT", "UPDATE", "DELETE", "TRUNCATE", "REFERENCES", "TRIGGER"} {
				var hasPrivilege bool
				if err := db.QueryRow(`SELECT has_table_privilege($1::oid, $2::oid, $3)`, role.OID, object.OID, privilege).Scan(&hasPrivilege); err != nil {
					return nil, err
				}
				if hasPrivilege {
					privileges["table:"+strings.ToLower(privilege)] = true
				}
			}
			if len(privileges) == 0 {
				continue
			}
			out = append(out, privilegeSet{
				ResourceType: databaseType,
				ResourceID:   schema.ResourceID,
				ResourceName: schema.ResourceName,
				Database:     schema.Database,
				Schema:       schema.Name,
				RoleOID:      role.OID,
				Privileges:   privileges,
			})
		}
	}
	return out, nil
}

func fetchSequencePrivileges(db *sql.DB, roles []roleInfo, schemas []schemaInfo) ([]privilegeSet, error) {
	rows, err := db.Query(`
		SELECT c.oid::bigint, n.nspname, c.relname
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE c.relkind = 'S'
			AND n.nspname <> 'information_schema'
			AND n.nspname !~ '^pg_'
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() // nolint:errcheck
	type objectInfo struct {
		OID    int64
		Schema string
		Name   string
	}
	var objects []objectInfo
	for rows.Next() {
		var object objectInfo
		if err := rows.Scan(&object.OID, &object.Schema, &object.Name); err != nil {
			return nil, err
		}
		objects = append(objects, object)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	schemaByName := privilegeSchemaMap(schemas)
	var out []privilegeSet
	for _, object := range objects {
		schema, ok := schemaByName[object.Schema]
		if !ok {
			continue
		}
		for _, role := range roles {
			privileges := map[string]bool{}
			for _, privilege := range []string{"USAGE", "SELECT", "UPDATE"} {
				var hasPrivilege bool
				if err := db.QueryRow(`SELECT has_sequence_privilege($1::oid, $2::oid, $3)`, role.OID, object.OID, privilege).Scan(&hasPrivilege); err != nil {
					return nil, err
				}
				if hasPrivilege {
					privileges["sequence:"+strings.ToLower(privilege)] = true
				}
			}
			if len(privileges) == 0 {
				continue
			}
			out = append(out, privilegeSet{
				ResourceType: databaseType,
				ResourceID:   schema.ResourceID,
				ResourceName: schema.ResourceName,
				Database:     schema.Database,
				Schema:       schema.Name,
				RoleOID:      role.OID,
				Privileges:   privileges,
			})
		}
	}
	return out, nil
}

func fetchFunctionPrivileges(db *sql.DB, roles []roleInfo, schemas []schemaInfo) ([]privilegeSet, error) {
	rows, err := db.Query(`
		SELECT p.oid::bigint, n.nspname, p.proname
		FROM pg_proc p
		JOIN pg_namespace n ON n.oid = p.pronamespace
		WHERE n.nspname <> 'information_schema'
			AND n.nspname !~ '^pg_'
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() // nolint:errcheck
	type objectInfo struct {
		OID    int64
		Schema string
		Name   string
	}
	var objects []objectInfo
	for rows.Next() {
		var object objectInfo
		if err := rows.Scan(&object.OID, &object.Schema, &object.Name); err != nil {
			return nil, err
		}
		objects = append(objects, object)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	schemaByName := privilegeSchemaMap(schemas)
	var out []privilegeSet
	for _, object := range objects {
		schema, ok := schemaByName[object.Schema]
		if !ok {
			continue
		}
		for _, role := range roles {
			var hasPrivilege bool
			if err := db.QueryRow(`SELECT has_function_privilege($1::oid, $2::oid, 'EXECUTE')`, role.OID, object.OID).Scan(&hasPrivilege); err != nil {
				return nil, err
			}
			if !hasPrivilege {
				continue
			}
			out = append(out, privilegeSet{
				ResourceType: databaseType,
				ResourceID:   schema.ResourceID,
				ResourceName: schema.ResourceName,
				Database:     schema.Database,
				Schema:       schema.Name,
				RoleOID:      role.OID,
				Privileges:   map[string]bool{"function:execute": true},
			})
		}
	}
	return out, nil
}

func classifyAccess(server serverInfo, roles []roleInfo, privs []privilegeSet, memberRoles map[int64][]membership, adminMember map[string]bool) []v1.ExternalConfigAccess {
	roleByOID := map[int64]roleInfo{}
	for _, role := range roles {
		roleByOID[role.OID] = role
	}

	type groupedAccess struct {
		priv           privilegeSet
		principalAlias string
		roleName       string
	}
	grouped := map[string]groupedAccess{}
	var dbRoleAccess []v1.ExternalConfigAccess

	for _, priv := range mergePrivilegeSets(nil, privs) {
		role, ok := roleByOID[priv.RoleOID]
		if !ok || !role.CanLogin {
			continue
		}
		groupedRole := classifyPrivilegeSet(role, priv, false)
		if groupedRole != "" {
			principalAlias := userAlias(server, role.Name)
			key := priv.ResourceID + "/" + principalAlias
			existing, ok := grouped[key]
			if !ok || roleRank(groupedRole) > roleRank(existing.roleName) {
				grouped[key] = groupedAccess{priv: priv, principalAlias: principalAlias, roleName: groupedRole}
			}
		}

		for _, membership := range memberRoles[role.OID] {
			memberRole, ok := roleByOID[membership.RoleOID]
			if !ok || memberRole.CanLogin {
				continue
			}
			inherited := findPrivilegeSet(privs, priv.ResourceID, membership.RoleOID)
			if inherited == nil {
				continue
			}
			specificAlias := dbRoleAlias(server, memberRole.Name)
			principalAlias := userAlias(server, role.Name)
			dbRoleAccess = append(dbRoleAccess, accessRow(server, priv, principalAlias, specificAlias, "dbrole-"+memberRole.Name))
			if adminMember[fmt.Sprintf("%d/%d", role.OID, membership.RoleOID)] {
				key := priv.ResourceID + "/" + principalAlias
				grouped[key] = groupedAccess{priv: priv, principalAlias: principalAlias, roleName: superAdminRole}
			}
		}
	}

	var access []v1.ExternalConfigAccess
	for _, item := range grouped {
		access = append(access, accessRow(server, item.priv, item.principalAlias, permissionRoleAlias(server, item.roleName), item.roleName))
	}
	access = append(access, dbRoleAccess...)
	return access
}

func classifyPrivilegeSet(role roleInfo, priv privilegeSet, adminOption bool) string {
	if role.Super || role.CreateRole || adminOption || hasAny(priv.Privileges, "role:superuser", "role:createrole") {
		return superAdminRole
	}
	if role.CreateDB || priv.Owner || hasAny(priv.Privileges,
		"role:createdb",
		"database:create",
		"schema:create",
		"table:references",
		"table:trigger",
	) {
		return ddlAdminRole
	}
	if hasAny(priv.Privileges,
		"table:insert",
		"table:update",
		"table:delete",
		"table:truncate",
		"sequence:update",
		"function:execute",
	) {
		return writerRole
	}
	if len(priv.Privileges) > 0 {
		return readerRole
	}
	return ""
}

func accessRow(server serverInfo, priv privilegeSet, principalAlias, roleAlias, roleName string) v1.ExternalConfigAccess {
	return v1.ExternalConfigAccess{
		ID: deterministicID("postgres-access", serverExternalID(server), priv.ResourceID, principalAlias, roleAlias, roleName),
		ConfigExternalID: v1.ExternalID{
			ConfigType: priv.ResourceType,
			ExternalID: priv.ResourceID,
		},
		ExternalUserAliases: []string{principalAlias},
		ExternalRoleAliases: []string{roleAlias},
	}
}

func serverExternalID(server serverInfo) string {
	return fmt.Sprintf("postgres://server/%s:%s", server.HostRef, server.Port)
}

func databaseExternalID(server serverInfo, database string) string {
	return fmt.Sprintf("postgres://database/%s:%s/%s", server.HostRef, server.Port, database)
}

func userAlias(server serverInfo, role string) string {
	return fmt.Sprintf("postgres://server/%s:%s/user/%s", server.HostRef, server.Port, role)
}

func dbRoleAlias(server serverInfo, role string) string {
	return fmt.Sprintf("postgres://server/%s:%s/db-role/%s", server.HostRef, server.Port, role)
}

func permissionRoleAlias(server serverInfo, role string) string {
	return fmt.Sprintf("postgres://server/%s:%s/permission/%s", server.HostRef, server.Port, role)
}

func connectionURLForDatabase(rawURL, database string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	u.Path = "/" + url.PathEscape(database)
	u.RawPath = ""
	return u.String(), nil
}

func deterministicID(parts ...string) string {
	id, err := hash.DeterministicUUID(parts)
	if err != nil {
		return uuid.New().String()
	}
	return id.String()
}

func hasAny(values map[string]bool, keys ...string) bool {
	for _, key := range keys {
		if values[key] {
			return true
		}
	}
	return false
}

func roleRank(role string) int {
	switch role {
	case superAdminRole:
		return 4
	case ddlAdminRole:
		return 3
	case writerRole:
		return 2
	case readerRole:
		return 1
	default:
		return 0
	}
}

func mapValues[T any](m map[string]T) []T {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]T, 0, len(keys))
	for _, key := range keys {
		out = append(out, m[key])
	}
	return out
}

func privilegeSchemaMap(schemas []schemaInfo) map[string]schemaInfo {
	out := map[string]schemaInfo{}
	for _, schema := range schemas {
		if schema.Name != "" {
			out[schema.Name] = schema
		}
	}
	return out
}

func mergePrivilegeSets(base []privilegeSet, items []privilegeSet) []privilegeSet {
	merged := map[string]privilegeSet{}
	for _, item := range append(base, items...) {
		key := fmt.Sprintf("%s/%d", item.ResourceID, item.RoleOID)
		existing, ok := merged[key]
		if !ok {
			if item.Privileges == nil {
				item.Privileges = map[string]bool{}
			}
			merged[key] = item
			continue
		}
		for privilege := range item.Privileges {
			existing.Privileges[privilege] = true
		}
		existing.Owner = existing.Owner || item.Owner
		existing.GrantOption = existing.GrantOption || item.GrantOption
		merged[key] = existing
	}

	keys := make([]string, 0, len(merged))
	for key := range merged {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]privilegeSet, 0, len(keys))
	for _, key := range keys {
		out = append(out, merged[key])
	}
	return out
}

func findPrivilegeSet(items []privilegeSet, resourceID string, roleOID int64) *privilegeSet {
	for _, item := range items {
		if item.ResourceID == resourceID && item.RoleOID == roleOID {
			return &item
		}
	}
	return nil
}

func dedupeAccess(items []v1.ExternalConfigAccess) []v1.ExternalConfigAccess {
	seen := map[string]v1.ExternalConfigAccess{}
	for _, item := range items {
		seen[item.ID] = item
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]v1.ExternalConfigAccess, 0, len(keys))
	for _, key := range keys {
		out = append(out, seen[key])
	}
	return out
}
