package dbx

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
)

// What an account may do, and changing it.
//
// The three-level grant in admin.go — read, write, all — answers "make an
// account for this application" in one step and nothing finer. This is the
// rest: what a role holds right now, object by object, and granting or
// revoking one privilege on one database, schema or table.
//
// The privileges are a closed set per engine and per level, published so the
// page builds its form from what the server accepts rather than from a list
// of its own. A request names privileges from that set; the keyword that
// reaches the statement is the set's, never the request's. Identifiers are
// quoted by the dialect, as everywhere.

// The levels a privilege can be held at. Not every engine has every one:
// MySQL and ClickHouse have no schema inside a database, and only PostgreSQL
// grants on sequences or makes one role a member of another.
const (
	GrantOnDatabase = "database"
	GrantOnSchema   = "schema"
	GrantOnTable    = "table"
	GrantOnSequence = "sequence"
	GrantOnRole     = "role"
)

// PrivilegeLevel is one level an engine grants at and what can be granted
// there.
type PrivilegeLevel struct {
	Level      string   `json:"level"`
	Privileges []string `json:"privileges"`
	// Needs names the request fields that identify the object at this level,
	// in order: "database", "schema", "table", "memberOf".
	Needs []string `json:"needs"`
	// AllObjects is true when the last of Needs may be left empty to mean
	// every object of that kind in the schema.
	AllObjects bool `json:"allObjects,omitempty"`
	// Future is true when the grant can also cover objects created later.
	Future bool `json:"future,omitempty"`
	// GrantOption is true when the privilege can be granted with the right
	// to grant it on.
	GrantOption bool `json:"grantOption,omitempty"`
}

// PrivilegeChange is one grant or revoke.
type PrivilegeChange struct {
	Role string
	Host string
	// Level is one of the GrantOn* levels.
	Level      string
	Database   string
	Schema     string
	Table      string
	Privileges []string
	// MemberOf is the role being granted, at the role level.
	MemberOf string
	// GrantOption adds WITH GRANT OPTION to a grant, and narrows a revoke to
	// taking only the right to grant on.
	GrantOption bool
	// Future extends the change to objects created later, where the engine
	// has default privileges.
	Future bool
	Revoke bool
}

// Grant is one thing a role holds: a set of privileges on one object.
type Grant struct {
	Grantee string `json:"grantee"`
	// Host is the second half of a MySQL account.
	Host  string `json:"host,omitempty"`
	Level string `json:"level"`
	// Database, Schema and Table name the object, as far down as Level goes.
	Database   string   `json:"database,omitempty"`
	Schema     string   `json:"schema,omitempty"`
	Table      string   `json:"table,omitempty"`
	Privileges []string `json:"privileges"`
	// Grantable is true when the grantee may grant these on.
	Grantable bool   `json:"grantable,omitempty"`
	Grantor   string `json:"grantor,omitempty"`
	// Owner marks privileges held by owning the object, which no revoke
	// takes away.
	Owner bool `json:"owner,omitempty"`
	// Future marks a default privilege: it applies to objects that Grantor
	// creates from now on, not to one that exists.
	Future bool `json:"future,omitempty"`
}

// GrantFilter narrows a listing to one role, one schema or one object. Empty
// fields do not filter.
type GrantFilter struct {
	Role   string
	Host   string
	Schema string
	Table  string
}

// maxGrants bounds a listing. GRANT … ON ALL TABLES writes a row per table,
// so a role granted a schema of two thousand tables holds two thousand grants.
const maxGrants = 1000

// RoleDetail is one account with everything the engine knows about it.
type RoleDetail struct {
	Role
	// Attributes are the engine's own flags beyond the ones Role carries, by
	// the name the alter request uses: inherit, replication, bypassRls.
	Attributes map[string]bool `json:"attributes"`
	// Members are the roles that are members of this one.
	Members []string `json:"members"`
	// Config is what the role's sessions start with (ALTER ROLE … SET).
	Config []string `json:"config"`
	// AuthPlugin is how the account authenticates, where the engine says.
	AuthPlugin string  `json:"authPlugin,omitempty"`
	Grants     []Grant `json:"grants"`
	// GrantsTruncated is true when the role holds more than Grants lists.
	GrantsTruncated bool `json:"grantsTruncated"`
	// Editable names the attributes an alter may change on this engine.
	Editable []string `json:"editable"`
	Notes    []string `json:"notes,omitempty"`
}

// PrivilegeAdmin is the optional dialect half: an engine that can list and
// change privileges at a level finer than a whole database.
type PrivilegeAdmin interface {
	PrivilegeLevels() []PrivilegeLevel
	// privilegeStatements renders a change already checked against
	// PrivilegeLevels into the statements that make it.
	privilegeStatements(c PrivilegeChange, privileges []string) ([]string, error)
	// PrivilegesNeedDatabase reports whether a change at this level has to
	// run connected to the database it names.
	PrivilegesNeedDatabase(level string) bool
	Grants(ctx context.Context, db *sql.DB, f GrantFilter) ([]Grant, bool, error)
	// roleExtras fills what the role list does not carry.
	roleExtras(ctx context.Context, db *sql.DB, detail *RoleDetail) error
}

// ErrPrivilegeRequest marks a refusal that is about the request: a level the
// engine does not have, a privilege outside the closed set, a missing name.
type ErrPrivilegeRequest struct{ msg string }

func (e ErrPrivilegeRequest) Error() string { return e.msg }

func privilegeRefused(format string, args ...any) error {
	return ErrPrivilegeRequest{msg: fmt.Sprintf(format, args...)}
}

func privilegeAdmin(driver Driver) (PrivilegeAdmin, error) {
	d, err := DialectFor(driver)
	if err != nil {
		return nil, err
	}
	p, ok := d.(PrivilegeAdmin)
	if !ok {
		return nil, ErrUnsupported
	}
	return p, nil
}

// PrivilegeLevelsFor is the closed set of levels and privileges an engine
// accepts; empty for an engine with no fine-grained grants from here.
func PrivilegeLevelsFor(driver Driver) []PrivilegeLevel {
	p, err := privilegeAdmin(driver)
	if err != nil {
		return []PrivilegeLevel{}
	}
	return p.PrivilegeLevels()
}

// PrivilegesNeedDatabase reports whether a change at a level must run
// connected to the database it names.
func PrivilegesNeedDatabase(driver Driver, level string) bool {
	p, err := privilegeAdmin(driver)
	return err == nil && p.PrivilegesNeedDatabase(level)
}

// checkPrivileges validates a change against the engine's closed set and
// returns the privileges in the set's own spelling. ALL stands alone: mixed
// with others it would be a grant whose meaning depends on the order.
func checkPrivileges(levels []PrivilegeLevel, c PrivilegeChange) ([]string, error) {
	var level *PrivilegeLevel
	names := []string{}
	for i := range levels {
		names = append(names, levels[i].Level)
		if levels[i].Level == c.Level {
			level = &levels[i]
		}
	}
	if level == nil {
		return nil, privilegeRefused("level must be one of %s", strings.Join(names, ", "))
	}
	if strings.TrimSpace(c.Role) == "" {
		return nil, privilegeRefused("a role is required")
	}
	for i, need := range level.Needs {
		value := map[string]string{"database": c.Database, "schema": c.Schema, "table": c.Table, "memberOf": c.MemberOf}[need]
		last := i == len(level.Needs)-1
		if strings.TrimSpace(value) == "" && !(last && level.AllObjects) {
			return nil, privilegeRefused("a %s is required at the %s level", need, c.Level)
		}
	}
	if c.Future && !level.Future {
		return nil, privilegeRefused("this engine has no default privileges at the %s level", c.Level)
	}
	if c.GrantOption && !level.GrantOption {
		return nil, privilegeRefused("the %s level has no grant option", c.Level)
	}
	if c.Level == GrantOnRole {
		return nil, nil
	}
	if len(c.Privileges) == 0 {
		return nil, privilegeRefused("at least one privilege is required")
	}
	out := []string{}
	for _, asked := range c.Privileges {
		found := ""
		for _, known := range level.Privileges {
			if strings.EqualFold(strings.TrimSpace(asked), known) {
				found = known
			}
		}
		if found == "" {
			return nil, privilegeRefused("%q is not a privilege at the %s level; it takes %s",
				asked, c.Level, strings.Join(level.Privileges, ", "))
		}
		if !containsString(out, found) {
			out = append(out, found)
		}
	}
	if containsString(out, "ALL") && len(out) > 1 {
		return nil, privilegeRefused("ALL already includes every other privilege; send it alone")
	}
	return out, nil
}

// PrivilegeStatements renders a change without running it, so the page can
// show the server's own SQL before anything is sent.
func PrivilegeStatements(driver Driver, c PrivilegeChange) ([]string, error) {
	p, err := privilegeAdmin(driver)
	if err != nil {
		return nil, err
	}
	privileges, err := checkPrivileges(p.PrivilegeLevels(), c)
	if err != nil {
		return nil, err
	}
	return p.privilegeStatements(c, privileges)
}

// ChangePrivileges grants or revokes and returns the statements it ran. They
// run in one transaction where the engine has transactional grants, so a
// change of three statements is never left a third done.
func ChangePrivileges(ctx context.Context, db *sql.DB, driver Driver, c PrivilegeChange) ([]string, error) {
	stmts, err := PrivilegeStatements(driver, c)
	if err != nil {
		return nil, err
	}
	if driver == DriverPostgres || driver == DriverMSSQL {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return nil, err
		}
		defer tx.Rollback()
		for _, stmt := range stmts {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				return nil, err
			}
		}
		return stmts, tx.Commit()
	}
	for i, stmt := range stmts {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			// MySQL stores the database half of a grant as the pattern it
			// was written with. A revoke has to name that same string, and
			// a grant made elsewhere was usually written without escaping
			// its underscores — so the literal spelling is tried second.
			if alt, ok := mysqlRevokeFallback(driver, c, stmt); ok {
				if _, altErr := db.ExecContext(ctx, alt); altErr == nil {
					stmts[i] = alt
					continue
				}
			}
			return nil, err
		}
	}
	return stmts, nil
}

// ListGrants lists what roles hold, narrowed by the filter.
func ListGrants(ctx context.Context, db *sql.DB, driver Driver, f GrantFilter) ([]Grant, bool, error) {
	p, err := privilegeAdmin(driver)
	if err != nil {
		return nil, false, err
	}
	grants, truncated, err := p.Grants(ctx, db, f)
	if err != nil {
		return nil, false, err
	}
	if grants == nil {
		grants = []Grant{}
	}
	return grants, truncated, nil
}

// ErrNoSuchRole is returned when the named account is not on the server.
var ErrNoSuchRole = fmt.Errorf("no such account")

// EditableRoleAttributes names what an alter may change on each engine, in
// the request's own field names.
func EditableRoleAttributes(driver Driver) []string {
	switch driver {
	case DriverPostgres:
		return []string{"password", "login", "superuser", "createDb", "createRole", "connectionLimit",
			"validUntil", "inherit", "replication", "bypassRls"}
	case DriverMySQL:
		return []string{"password", "login", "locked", "superuser", "createDb", "createRole", "connectionLimit"}
	case DriverMSSQL:
		return []string{"password", "login", "locked", "superuser", "createDb", "createRole"}
	case DriverClickHouse, DriverMongo, DriverRedis:
		return []string{"password", "superuser"}
	}
	return []string{}
}

// ReadRoleDetail describes one account: the row the role list shows, the
// engine's own attributes, its memberships and what it holds.
func ReadRoleDetail(ctx context.Context, db *sql.DB, driver Driver, name, host string) (*RoleDetail, error) {
	admin, err := AdminFor(driver)
	if err != nil {
		return nil, err
	}
	roles, err := admin.Roles(ctx, db)
	if err != nil {
		return nil, err
	}
	var found *Role
	for i := range roles {
		if roles[i].Name == name && (host == "" || roles[i].Host == host) {
			found = &roles[i]
			break
		}
	}
	if found == nil {
		return nil, ErrNoSuchRole
	}
	out := &RoleDetail{Role: *found, Attributes: map[string]bool{}, Members: []string{}, Config: []string{},
		Grants: []Grant{}, Editable: EditableRoleAttributes(driver)}
	p, err := privilegeAdmin(driver)
	if err != nil {
		out.Notes = append(out.Notes, "This engine's grants are not listed from here.")
		return out, nil
	}
	if err := p.roleExtras(ctx, db, out); err != nil {
		out.Notes = append(out.Notes, "Some attributes could not be read: "+err.Error())
	}
	grants, truncated, err := p.Grants(ctx, db, GrantFilter{Role: found.Name, Host: found.Host})
	if err != nil {
		out.Notes = append(out.Notes, "Grants could not be read: "+err.Error())
		return out, nil
	}
	if grants != nil {
		out.Grants = grants
	}
	out.GrantsTruncated = truncated
	return out, nil
}

// privilegeList renders a privilege set for a statement. ALL is spelled the
// way each engine wants it by the caller; here it is only joined.
func privilegeList(privileges []string, all string) string {
	if len(privileges) == 1 && privileges[0] == "ALL" {
		return all
	}
	return strings.Join(privileges, ", ")
}

// --- PostgreSQL --------------------------------------------------------------

func (postgresDialect) PrivilegeLevels() []PrivilegeLevel {
	return []PrivilegeLevel{
		{Level: GrantOnDatabase, Needs: []string{"database"}, GrantOption: true,
			Privileges: []string{"CONNECT", "CREATE", "TEMPORARY", "ALL"}},
		{Level: GrantOnSchema, Needs: []string{"schema"}, GrantOption: true,
			Privileges: []string{"USAGE", "CREATE", "ALL"}},
		{Level: GrantOnTable, Needs: []string{"schema", "table"}, AllObjects: true, Future: true, GrantOption: true,
			Privileges: []string{"SELECT", "INSERT", "UPDATE", "DELETE", "TRUNCATE", "REFERENCES", "TRIGGER", "ALL"}},
		{Level: GrantOnSequence, Needs: []string{"schema", "table"}, AllObjects: true, Future: true, GrantOption: true,
			Privileges: []string{"USAGE", "SELECT", "UPDATE", "ALL"}},
		{Level: GrantOnRole, Needs: []string{"memberOf"}, Privileges: []string{}},
	}
}

// Schemas, tables and sequences live in one database's catalogue, so a grant
// on them runs there. A database's own privileges and role membership are
// server-wide and run from anywhere.
func (postgresDialect) PrivilegesNeedDatabase(level string) bool {
	return level == GrantOnSchema || level == GrantOnTable || level == GrantOnSequence
}

func (d postgresDialect) privilegeStatements(c PrivilegeChange, privileges []string) ([]string, error) {
	role, err := d.QuoteIdent(c.Role)
	if err != nil {
		return nil, err
	}
	// The two verbs differ in more than the keyword: a grant ends in TO and
	// may carry the grant option after it, a revoke ends in FROM and names
	// the grant option before the privileges.
	render := func(on string) string {
		list := privilegeList(privileges, "ALL PRIVILEGES")
		if c.Revoke {
			prefix := "REVOKE "
			if c.GrantOption {
				prefix += "GRANT OPTION FOR "
			}
			return prefix + list + " ON " + on + " FROM " + role
		}
		stmt := "GRANT " + list + " ON " + on + " TO " + role
		if c.GrantOption {
			stmt += " WITH GRANT OPTION"
		}
		return stmt
	}
	switch c.Level {
	case GrantOnRole:
		group, err := d.QuoteIdent(c.MemberOf)
		if err != nil {
			return nil, err
		}
		if c.Revoke {
			return []string{"REVOKE " + group + " FROM " + role}, nil
		}
		return []string{"GRANT " + group + " TO " + role}, nil
	case GrantOnDatabase:
		name, err := d.QuoteIdent(c.Database)
		if err != nil {
			return nil, err
		}
		return []string{render("DATABASE " + name)}, nil
	case GrantOnSchema:
		name, err := d.QuoteIdent(c.Schema)
		if err != nil {
			return nil, err
		}
		return []string{render("SCHEMA " + name)}, nil
	case GrantOnTable, GrantOnSequence:
		schema, err := d.QuoteIdent(c.Schema)
		if err != nil {
			return nil, err
		}
		kind, kinds := "TABLE", "TABLES"
		if c.Level == GrantOnSequence {
			kind, kinds = "SEQUENCE", "SEQUENCES"
		}
		if c.Table != "" {
			if c.Future {
				return nil, privilegeRefused("future objects are covered by a grant on the whole schema, not on one %s", strings.ToLower(kind))
			}
			rel, err := qualify(d, c.Schema, c.Table)
			if err != nil {
				return nil, err
			}
			return []string{render(kind + " " + rel)}, nil
		}
		stmts := []string{render("ALL " + kinds + " IN SCHEMA " + schema)}
		if c.Future {
			// The same clause, inside ALTER DEFAULT PRIVILEGES, without the
			// schema on the object: the schema is the scope of the default.
			stmts = append(stmts, "ALTER DEFAULT PRIVILEGES IN SCHEMA "+schema+" "+render(kinds))
		}
		return stmts, nil
	}
	return nil, privilegeRefused("unknown level %q", c.Level)
}

// pgSystemSchemas keeps PostgreSQL's own schemas out of a listing.
const pgSystemSchemas = `n.nspname NOT IN ('pg_catalog', 'information_schema')
	      AND n.nspname NOT LIKE 'pg\_toast%' AND n.nspname NOT LIKE 'pg\_temp%'`

// Grants explodes the access-control lists of databases, schemas and
// relations, and the default privileges beside them. A database's list is
// visible from anywhere; a schema's and a table's only from inside their
// database, so those are this database's. The privileges of one grantee on
// one object from one grantor come back as one row.
func (postgresDialect) Grants(ctx context.Context, db *sql.DB, f GrantFilter) ([]Grant, bool, error) {
	rows, err := db.QueryContext(ctx, `
	  SELECT level, database, schema, object, grantee, grantor, grantable, owner, future,
	         string_agg(privilege, ',' ORDER BY privilege)
	  FROM (
	    SELECT 'database' AS level, d.datname::text AS database, ''::text AS schema, ''::text AS object,
	           COALESCE(ge.rolname, 'PUBLIC')::text AS grantee, COALESCE(gr.rolname, '')::text AS grantor,
	           a.privilege_type::text AS privilege, a.is_grantable AS grantable,
	           a.grantee = d.datdba AS owner, false AS future, 1 AS ord
	    FROM pg_database d
	    CROSS JOIN LATERAL aclexplode(d.datacl) a
	    LEFT JOIN pg_roles ge ON ge.oid = a.grantee
	    LEFT JOIN pg_roles gr ON gr.oid = a.grantor
	    WHERE NOT d.datistemplate AND $2 = '' AND $3 = ''
	    UNION ALL
	    SELECT 'schema', current_database()::text, n.nspname::text, '',
	           COALESCE(ge.rolname, 'PUBLIC'), COALESCE(gr.rolname, ''),
	           a.privilege_type, a.is_grantable, a.grantee = n.nspowner, false, 2
	    FROM pg_namespace n
	    CROSS JOIN LATERAL aclexplode(n.nspacl) a
	    LEFT JOIN pg_roles ge ON ge.oid = a.grantee
	    LEFT JOIN pg_roles gr ON gr.oid = a.grantor
	    WHERE `+pgSystemSchemas+` AND $3 = ''
	    UNION ALL
	    SELECT CASE WHEN c.relkind = 'S' THEN 'sequence' ELSE 'table' END, current_database()::text,
	           n.nspname::text, c.relname::text,
	           COALESCE(ge.rolname, 'PUBLIC'), COALESCE(gr.rolname, ''),
	           a.privilege_type, a.is_grantable, a.grantee = c.relowner, false, 3
	    FROM pg_class c
	    JOIN pg_namespace n ON n.oid = c.relnamespace
	    CROSS JOIN LATERAL aclexplode(c.relacl) a
	    LEFT JOIN pg_roles ge ON ge.oid = a.grantee
	    LEFT JOIN pg_roles gr ON gr.oid = a.grantor
	    WHERE c.relkind IN ('r', 'v', 'm', 'p', 'f', 'S') AND `+pgSystemSchemas+`
	    UNION ALL
	    SELECT CASE d.defaclobjtype WHEN 'S' THEN 'sequence' WHEN 'n' THEN 'schema' ELSE 'table' END,
	           current_database()::text, COALESCE(n.nspname, '')::text, '',
	           COALESCE(ge.rolname, 'PUBLIC'), pg_get_userbyid(d.defaclrole)::text,
	           a.privilege_type, a.is_grantable, false, true, 4
	    FROM pg_default_acl d
	    LEFT JOIN pg_namespace n ON n.oid = d.defaclnamespace
	    CROSS JOIN LATERAL aclexplode(d.defaclacl) a
	    LEFT JOIN pg_roles ge ON ge.oid = a.grantee
	    WHERE d.defaclobjtype IN ('r', 'S', 'n') AND $3 = ''
	  ) g
	  WHERE ($1 = '' OR grantee = $1) AND ($2 = '' OR schema = $2) AND ($3 = '' OR object = $3)
	  GROUP BY ord, level, database, schema, object, grantee, grantor, grantable, owner, future
	  ORDER BY ord, database, schema, object, grantee
	  LIMIT $4`, f.Role, f.Schema, f.Table, maxGrants+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	out := []Grant{}
	for rows.Next() {
		var g Grant
		var privileges string
		if err := rows.Scan(&g.Level, &g.Database, &g.Schema, &g.Table, &g.Grantee, &g.Grantor,
			&g.Grantable, &g.Owner, &g.Future, &privileges); err != nil {
			return nil, false, err
		}
		g.Privileges = strings.Split(privileges, ",")
		if len(out) == maxGrants {
			return out, true, rows.Err()
		}
		out = append(out, g)
	}
	return out, false, rows.Err()
}

func (postgresDialect) roleExtras(ctx context.Context, db *sql.DB, detail *RoleDetail) error {
	var inherit, replication, bypass bool
	var members, config string
	err := db.QueryRowContext(ctx, `
	  SELECT r.rolinherit, r.rolreplication, r.rolbypassrls,
	         COALESCE((SELECT string_agg(m.rolname, chr(31) ORDER BY m.rolname)
	                   FROM pg_auth_members am JOIN pg_roles m ON m.oid = am.member
	                   WHERE am.roleid = r.oid), ''),
	         COALESCE(array_to_string(r.rolconfig, chr(31)), '')
	  FROM pg_roles r WHERE r.rolname = $1`, detail.Name).Scan(&inherit, &replication, &bypass, &members, &config)
	if err != nil {
		return err
	}
	detail.Attributes["inherit"] = inherit
	detail.Attributes["replication"] = replication
	detail.Attributes["bypassRls"] = bypass
	detail.Members = splitUnit(members)
	detail.Config = splitUnit(config)
	return nil
}

// --- MySQL / MariaDB ---------------------------------------------------------

func (mysqlDialect) PrivilegeLevels() []PrivilegeLevel {
	return []PrivilegeLevel{
		{Level: GrantOnDatabase, Needs: []string{"database"}, GrantOption: true,
			Privileges: []string{"SELECT", "INSERT", "UPDATE", "DELETE", "CREATE", "DROP", "ALTER", "INDEX",
				"REFERENCES", "CREATE VIEW", "SHOW VIEW", "CREATE ROUTINE", "ALTER ROUTINE", "EXECUTE",
				"TRIGGER", "EVENT", "CREATE TEMPORARY TABLES", "LOCK TABLES", "ALL"}},
		{Level: GrantOnTable, Needs: []string{"database", "table"}, GrantOption: true,
			Privileges: []string{"SELECT", "INSERT", "UPDATE", "DELETE", "CREATE", "DROP", "ALTER", "INDEX",
				"REFERENCES", "CREATE VIEW", "SHOW VIEW", "TRIGGER", "ALL"}},
	}
}

// MySQL's grants are all in the mysql schema, whichever database the session
// has selected.
func (mysqlDialect) PrivilegesNeedDatabase(string) bool { return false }

func (d mysqlDialect) privilegeStatements(c PrivilegeChange, privileges []string) ([]string, error) {
	account, err := mysqlAccount(c.Role, c.Host)
	if err != nil {
		return nil, err
	}
	var on string
	switch c.Level {
	case GrantOnDatabase:
		name, err := mysqlGrantDatabase(c.Database)
		if err != nil {
			return nil, err
		}
		on = name + ".*"
	case GrantOnTable:
		// With a table named the database is an identifier, not a pattern.
		rel, err := qualify(d, c.Database, c.Table)
		if err != nil {
			return nil, err
		}
		on = rel
	default:
		return nil, privilegeRefused("unknown level %q", c.Level)
	}
	list := privilegeList(privileges, "ALL PRIVILEGES")
	if c.Revoke {
		if c.GrantOption {
			return []string{"REVOKE GRANT OPTION ON " + on + " FROM " + account}, nil
		}
		return []string{"REVOKE " + list + " ON " + on + " FROM " + account}, nil
	}
	stmt := "GRANT " + list + " ON " + on + " TO " + account
	if c.GrantOption {
		stmt += " WITH GRANT OPTION"
	}
	return []string{stmt}, nil
}

// mysqlRevokeFallback rewrites a database-level revoke to name the database
// without the escaping a grant from here would have used.
func mysqlRevokeFallback(driver Driver, c PrivilegeChange, stmt string) (string, bool) {
	if driver != DriverMySQL || !c.Revoke || c.Level != GrantOnDatabase {
		return "", false
	}
	escaped, err := mysqlGrantDatabase(c.Database)
	if err != nil {
		return "", false
	}
	plain, err := quoteBacktick(c.Database)
	if err != nil || plain == escaped {
		return "", false
	}
	return strings.Replace(stmt, " ON "+escaped+".* ", " ON "+plain+".* ", 1), true
}

// Grants reads one account's grants from SHOW GRANTS, or — with no account
// named — everybody's on a database or table from information_schema.
//
// SHOW GRANTS is the only source that lists every level for an account in
// one place, and on MariaDB its first line carries the account's password
// hash. So the lines are parsed into grants and never returned: the hash
// stops here.
func (mysqlDialect) Grants(ctx context.Context, db *sql.DB, f GrantFilter) ([]Grant, bool, error) {
	if f.Role == "" {
		return mysqlObjectGrants(ctx, db, f)
	}
	account, err := mysqlAccount(f.Role, f.Host)
	if err != nil {
		return nil, false, err
	}
	rows, err := db.QueryContext(ctx, "SHOW GRANTS FOR "+account)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	out := []Grant{}
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return nil, false, err
		}
		g, ok := parseMySQLGrant(line)
		if !ok {
			continue
		}
		g.Grantee, g.Host = f.Role, f.Host
		if f.Schema != "" && g.Database != f.Schema {
			continue
		}
		if f.Table != "" && g.Table != f.Table {
			continue
		}
		if len(out) == maxGrants {
			return out, true, rows.Err()
		}
		out = append(out, g)
	}
	return out, false, rows.Err()
}

// parseMySQLGrant reads one line of SHOW GRANTS into a grant. It takes what
// sits between GRANT and ON, and between ON and TO, and nothing after: the
// rest of the line is where MariaDB prints IDENTIFIED BY PASSWORD. A line that
// grants a role, a proxy or a routine has no table-shaped object and is
// skipped.
func parseMySQLGrant(line string) (Grant, bool) {
	rest, ok := strings.CutPrefix(line, "GRANT ")
	if !ok {
		return Grant{}, false
	}
	on := strings.Index(rest, " ON ")
	if on < 0 {
		return Grant{}, false
	}
	privileges, rest := rest[:on], rest[on+len(" ON "):]
	to := strings.Index(rest, " TO ")
	if to < 0 {
		return Grant{}, false
	}
	object, tail := rest[:to], rest[to:]
	if strings.HasPrefix(object, "PROCEDURE ") || strings.HasPrefix(object, "FUNCTION ") || strings.HasPrefix(privileges, "PROXY") {
		return Grant{}, false
	}
	object = strings.TrimPrefix(object, "TABLE ")
	dbPart, tablePart, ok := splitMySQLObject(object)
	if !ok {
		return Grant{}, false
	}
	g := Grant{Grantable: strings.Contains(tail, "WITH GRANT OPTION"), Privileges: splitMySQLPrivileges(privileges)}
	switch {
	case dbPart == "*" && tablePart == "*":
		g.Level = "server"
	case tablePart == "*":
		g.Level = GrantOnDatabase
		// The stored name is a pattern; undo the escaping a literal name
		// was written with so it reads as the database it is.
		g.Database = strings.NewReplacer(`\_`, "_", `\%`, "%", `\\`, `\`).Replace(dbPart)
	default:
		g.Level = GrantOnTable
		g.Database, g.Table = dbPart, tablePart
	}
	return g, true
}

// splitMySQLObject splits `db`.`table`, `db`.* or *.* at the dot between its
// halves, minding that a backtick-quoted name may itself contain a dot.
func splitMySQLObject(object string) (database, table string, ok bool) {
	object = strings.TrimSpace(object)
	read := func(s string) (name, rest string, ok bool) {
		if strings.HasPrefix(s, "*") {
			return "*", s[1:], true
		}
		if !strings.HasPrefix(s, "`") {
			return "", "", false
		}
		var b strings.Builder
		for i := 1; i < len(s); i++ {
			if s[i] != '`' {
				b.WriteByte(s[i])
				continue
			}
			if i+1 < len(s) && s[i+1] == '`' {
				b.WriteByte('`')
				i++
				continue
			}
			return b.String(), s[i+1:], true
		}
		return "", "", false
	}
	database, rest, ok := read(object)
	if !ok || !strings.HasPrefix(rest, ".") {
		return "", "", false
	}
	table, rest, ok = read(rest[1:])
	if !ok || strings.TrimSpace(rest) != "" {
		return "", "", false
	}
	return database, table, true
}

// splitMySQLPrivileges splits a privilege list at its commas, keeping a
// column list — SELECT (a, b) — with the privilege it belongs to.
func splitMySQLPrivileges(list string) []string {
	out := []string{}
	depth, start := 0, 0
	for i, r := range list {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, strings.TrimSpace(list[start:i]))
				start = i + 1
			}
		}
	}
	if last := strings.TrimSpace(list[start:]); last != "" {
		out = append(out, last)
	}
	for i, p := range out {
		if p == "ALL PRIVILEGES" {
			out[i] = "ALL"
		}
	}
	return out
}

// mysqlObjectGrants lists who holds what on a database or one of its tables.
// The grantee column is the quoted account, 'user'@'host', split back apart.
func mysqlObjectGrants(ctx context.Context, db *sql.DB, f GrantFilter) ([]Grant, bool, error) {
	query := `
	  SELECT GRANTEE, 'database', TABLE_SCHEMA, '', PRIVILEGE_TYPE, IS_GRANTABLE
	  FROM information_schema.SCHEMA_PRIVILEGES
	  WHERE (? = '' OR REPLACE(REPLACE(TABLE_SCHEMA, '\\_', '_'), '\\%', '%') = ?) AND ? = ''
	  UNION ALL
	  SELECT GRANTEE, 'table', TABLE_SCHEMA, TABLE_NAME, PRIVILEGE_TYPE, IS_GRANTABLE
	  FROM information_schema.TABLE_PRIVILEGES
	  WHERE (? = '' OR TABLE_SCHEMA = ?) AND (? = '' OR TABLE_NAME = ?)
	  ORDER BY 2, 3, 4, 1, 5
	  LIMIT ?`
	// A row per privilege comes back; several times the grant bound is read
	// so that folding them leaves the bound's worth of grants.
	rows, err := db.QueryContext(ctx, query, f.Schema, f.Schema, f.Table, f.Schema, f.Schema, f.Table, f.Table, maxGrants*20)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	index := map[string]int{}
	out := []Grant{}
	for rows.Next() {
		var grantee, level, schema, table, privilege, grantable string
		if err := rows.Scan(&grantee, &level, &schema, &table, &privilege, &grantable); err != nil {
			return nil, false, err
		}
		user, host := splitMySQLGrantee(grantee)
		key := strings.Join([]string{level, schema, table, user, host, grantable}, "\x00")
		i, ok := index[key]
		if !ok {
			if len(out) == maxGrants {
				return out, true, rows.Err()
			}
			out = append(out, Grant{Grantee: user, Host: host, Level: level, Table: table,
				Database:  strings.NewReplacer(`\_`, "_", `\%`, "%").Replace(schema),
				Grantable: strings.EqualFold(grantable, "YES"), Privileges: []string{}})
			i = len(out) - 1
			index[key] = i
		}
		out[i].Privileges = append(out[i].Privileges, privilege)
	}
	return out, false, rows.Err()
}

// splitMySQLGrantee turns 'user'@'host' into its halves.
func splitMySQLGrantee(grantee string) (user, host string) {
	at := strings.LastIndex(grantee, "'@'")
	if at < 0 {
		return strings.Trim(grantee, "'"), ""
	}
	return strings.ReplaceAll(strings.TrimPrefix(grantee[:at], "'"), "''", "'"), strings.TrimSuffix(grantee[at+3:], "'")
}

func (mysqlDialect) roleExtras(ctx context.Context, db *sql.DB, detail *RoleDetail) error {
	var plugin, expired sql.NullString
	err := db.QueryRowContext(ctx, `SELECT plugin, password_expired FROM mysql.user WHERE User = ? AND Host = ?`,
		detail.Name, detail.Host).Scan(&plugin, &expired)
	if err != nil {
		return err
	}
	detail.AuthPlugin = plugin.String
	detail.Attributes["locked"] = detail.Locked
	detail.Attributes["passwordExpired"] = strings.EqualFold(expired.String, "Y")
	return nil
}

// --- ClickHouse --------------------------------------------------------------

func (clickhouseDialect) PrivilegeLevels() []PrivilegeLevel {
	privileges := []string{"SELECT", "INSERT", "ALTER", "CREATE TABLE", "DROP TABLE", "TRUNCATE", "OPTIMIZE", "SHOW", "ALL"}
	return []PrivilegeLevel{
		{Level: GrantOnDatabase, Needs: []string{"database"}, GrantOption: true, Privileges: privileges},
		{Level: GrantOnTable, Needs: []string{"database", "table"}, GrantOption: true, Privileges: privileges},
	}
}

func (clickhouseDialect) PrivilegesNeedDatabase(string) bool { return false }

func (d clickhouseDialect) privilegeStatements(c PrivilegeChange, privileges []string) ([]string, error) {
	role, err := d.QuoteIdent(c.Role)
	if err != nil {
		return nil, err
	}
	database, err := d.QuoteIdent(c.Database)
	if err != nil {
		return nil, err
	}
	on := database + ".*"
	if c.Level == GrantOnTable {
		table, err := d.QuoteIdent(c.Table)
		if err != nil {
			return nil, err
		}
		on = database + "." + table
	}
	list := privilegeList(privileges, "ALL")
	if c.Revoke {
		prefix := "REVOKE "
		if c.GrantOption {
			prefix += "GRANT OPTION FOR "
		}
		return []string{prefix + list + " ON " + on + " FROM " + role}, nil
	}
	stmt := "GRANT " + list + " ON " + on + " TO " + role
	if c.GrantOption {
		stmt += " WITH GRANT OPTION"
	}
	return []string{stmt}, nil
}

// Grants reads system.grants, where a row with no database is a grant on
// everything and a partial revoke is a row of its own — listed as it is,
// with the privilege marked, because it takes away part of a wider grant
// and reading it as a grant would say the opposite.
func (clickhouseDialect) Grants(ctx context.Context, db *sql.DB, f GrantFilter) ([]Grant, bool, error) {
	rows, err := db.QueryContext(ctx, `
	  SELECT ifNull(user_name, ''), ifNull(role_name, ''), toString(access_type),
	         ifNull(database, ''), ifNull(table, ''), is_partial_revoke, grant_option
	  FROM system.grants
	  WHERE (? = '' OR user_name = ? OR role_name = ?)
	    AND (? = '' OR database = ?) AND (? = '' OR table = ?)
	  ORDER BY user_name, role_name, database, table, access_type
	  LIMIT ?`, f.Role, f.Role, f.Role, f.Schema, f.Schema, f.Table, f.Table, maxGrants*20)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	index := map[string]int{}
	out := []Grant{}
	for rows.Next() {
		var user, role, access, database, table string
		var revoke, grantable bool
		if err := rows.Scan(&user, &role, &access, &database, &table, &revoke, &grantable); err != nil {
			return nil, false, err
		}
		grantee := orText(user, role)
		if revoke {
			access = "NOT " + access
		}
		key := strings.Join([]string{grantee, database, table, fmt.Sprint(grantable)}, "\x00")
		i, ok := index[key]
		if !ok {
			if len(out) == maxGrants {
				return out, true, rows.Err()
			}
			g := Grant{Grantee: grantee, Database: database, Table: table, Grantable: grantable, Privileges: []string{}}
			switch {
			case database == "":
				g.Level = "server"
			case table == "":
				g.Level = GrantOnDatabase
			default:
				g.Level = GrantOnTable
			}
			out = append(out, g)
			i = len(out) - 1
			index[key] = i
		}
		out[i].Privileges = append(out[i].Privileges, access)
	}
	return out, false, rows.Err()
}

func (clickhouseDialect) roleExtras(ctx context.Context, db *sql.DB, detail *RoleDetail) error {
	var auth, storage sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT toString(auth_type), storage FROM system.users WHERE name = ?`, detail.Name).Scan(&auth, &storage); err != nil {
		return err
	}
	detail.AuthPlugin = strings.Trim(auth.String, "[]'")
	if storage.String != "" && storage.String != "local_directory" && storage.String != "local directory" {
		// An account defined in users.xml cannot be altered by SQL at all.
		detail.Editable = []string{}
		detail.Notes = append(detail.Notes, "This account is defined in the server's configuration ("+storage.String+"), so it is changed there rather than from here.")
	}
	rows, err := db.QueryContext(ctx, `SELECT granted_role_name FROM system.role_grants WHERE user_name = ? ORDER BY 1`, detail.Name)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		detail.MemberOf = append(detail.MemberOf, name)
	}
	return rows.Err()
}

// --- SQL Server --------------------------------------------------------------

func (mssqlDialect) PrivilegeLevels() []PrivilegeLevel {
	return []PrivilegeLevel{
		{Level: GrantOnDatabase, Needs: []string{"database"}, GrantOption: true,
			Privileges: []string{"CONNECT", "CREATE TABLE", "CREATE VIEW", "CREATE PROCEDURE", "SELECT", "INSERT",
				"UPDATE", "DELETE", "EXECUTE", "ALTER", "CONTROL"}},
		{Level: GrantOnSchema, Needs: []string{"schema"}, GrantOption: true,
			Privileges: []string{"SELECT", "INSERT", "UPDATE", "DELETE", "EXECUTE", "REFERENCES", "ALTER", "CONTROL"}},
		{Level: GrantOnTable, Needs: []string{"schema", "table"}, GrantOption: true,
			Privileges: []string{"SELECT", "INSERT", "UPDATE", "DELETE", "REFERENCES", "ALTER", "CONTROL"}},
	}
}

// Every SQL Server permission below the server is recorded in the database it
// applies to, against a user that exists only there.
func (mssqlDialect) PrivilegesNeedDatabase(string) bool { return true }

func (d mssqlDialect) privilegeStatements(c PrivilegeChange, privileges []string) ([]string, error) {
	role, err := d.QuoteIdent(c.Role)
	if err != nil {
		return nil, err
	}
	on := ""
	switch c.Level {
	case GrantOnDatabase:
	case GrantOnSchema:
		schema, err := d.QuoteIdent(c.Schema)
		if err != nil {
			return nil, err
		}
		on = " ON SCHEMA::" + schema
	case GrantOnTable:
		rel, err := qualify(d, c.Schema, c.Table)
		if err != nil {
			return nil, err
		}
		on = " ON OBJECT::" + rel
	default:
		return nil, privilegeRefused("unknown level %q", c.Level)
	}
	list := strings.Join(privileges, ", ")
	if c.Revoke {
		prefix := "REVOKE "
		if c.GrantOption {
			prefix += "GRANT OPTION FOR "
		}
		// CASCADE takes the permission from whoever this user granted it on
		// to as well; without it the revoke of a grantable permission fails.
		return []string{prefix + list + on + " FROM " + role + " CASCADE"}, nil
	}
	stmt := "GRANT " + list + on + " TO " + role
	if c.GrantOption {
		stmt += " WITH GRANT OPTION"
	}
	// The login needs a user in this database to hold a permission in it.
	// Made only where there is none, so the batch does not fail on one that
	// was already there.
	ensure := "IF NOT EXISTS (SELECT 1 FROM sys.database_principals WHERE name = " + dumpString(DriverMSSQL, c.Role) + ") CREATE USER " + role + " FOR LOGIN " + role
	return []string{ensure, stmt}, nil
}

// Grants reads the current database's permissions: class 0 is the database
// itself, 1 an object, 3 a schema.
func (mssqlDialect) Grants(ctx context.Context, db *sql.DB, f GrantFilter) ([]Grant, bool, error) {
	rows, err := db.QueryContext(ctx, `
	  SELECT TOP (@p4) pr.name,
	         CASE p.class WHEN 0 THEN 'database' WHEN 3 THEN 'schema' ELSE 'table' END,
	         DB_NAME(),
	         CASE p.class WHEN 3 THEN SCHEMA_NAME(p.major_id) WHEN 1 THEN OBJECT_SCHEMA_NAME(p.major_id) ELSE '' END,
	         CASE p.class WHEN 1 THEN OBJECT_NAME(p.major_id) ELSE '' END,
	         p.permission_name, p.state_desc, ISNULL(g.name, '')
	  FROM sys.database_permissions p
	  JOIN sys.database_principals pr ON pr.principal_id = p.grantee_principal_id
	  LEFT JOIN sys.database_principals g ON g.principal_id = p.grantor_principal_id
	  WHERE p.class IN (0, 1, 3) AND p.minor_id = 0
	    AND (@p1 = '' OR pr.name = @p1)
	    AND (@p2 = '' OR CASE p.class WHEN 3 THEN SCHEMA_NAME(p.major_id) WHEN 1 THEN OBJECT_SCHEMA_NAME(p.major_id) ELSE '' END = @p2)
	    AND (@p3 = '' OR (p.class = 1 AND OBJECT_NAME(p.major_id) = @p3))
	  ORDER BY 2, 4, 5, 1, 6`, f.Role, f.Schema, f.Table, maxGrants*20)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	index := map[string]int{}
	out := []Grant{}
	for rows.Next() {
		var grantee, level, database, permission, state, grantor string
		var schema, table sql.NullString
		if err := rows.Scan(&grantee, &level, &database, &schema, &table, &permission, &state, &grantor); err != nil {
			return nil, false, err
		}
		if state == "DENY" {
			permission = "DENY " + permission
		}
		grantable := state == "GRANT_WITH_GRANT_OPTION"
		key := strings.Join([]string{grantee, level, schema.String, table.String, grantor, fmt.Sprint(grantable)}, "\x00")
		i, ok := index[key]
		if !ok {
			if len(out) == maxGrants {
				return out, true, rows.Err()
			}
			out = append(out, Grant{Grantee: grantee, Level: level, Database: database, Schema: schema.String,
				Table: table.String, Grantor: grantor, Grantable: grantable, Privileges: []string{}})
			i = len(out) - 1
			index[key] = i
		}
		out[i].Privileges = append(out[i].Privileges, permission)
	}
	return out, false, rows.Err()
}

// roleExtras lists the database roles the login's user belongs to here —
// db_datareader and its siblings, which is how the three-level grant gives
// access on this engine.
func (mssqlDialect) roleExtras(ctx context.Context, db *sql.DB, detail *RoleDetail) error {
	rows, err := db.QueryContext(ctx, `
	  SELECT r.name
	  FROM sys.database_role_members m
	  JOIN sys.database_principals r ON r.principal_id = m.role_principal_id
	  JOIN sys.database_principals u ON u.principal_id = m.member_principal_id
	  WHERE u.name = @p1
	  ORDER BY r.name`, detail.Name)
	if err != nil {
		return err
	}
	defer rows.Close()
	memberOf := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		memberOf = append(memberOf, name)
	}
	sort.Strings(memberOf)
	detail.MemberOf = memberOf
	detail.Attributes["locked"] = detail.Locked
	return rows.Err()
}
