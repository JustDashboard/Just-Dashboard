package dbx

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// The server behind a connection, as opposed to the database inside it.
//
// Everything else in this package looks at one database: its tables, its
// rows, its size. A server holds several, and the accounts that sign in to
// them, and the extensions and settings it was started with — and until this
// file the only way to any of that from the dashboard was the query console
// and a good memory for `CREATE ROLE` syntax on six engines. An operator who
// wants "a read-only user for the analytics team on this database" should not
// have to remember whether that is GRANT USAGE ON SCHEMA or GRANT SELECT ON
// db.* this week.
//
// Two rules hold here as everywhere in dbx. Identifiers go through the
// dialect's QuoteIdent and are never bound. Values are bound — with the one
// exception that has no alternative: no engine here accepts a bind marker in
// CREATE ROLE ... PASSWORD or CREATE USER ... IDENTIFIED BY, so a password is
// quoted as a literal by `passwordLiteral`, the same per-engine rule
// `dumpString` applies to a dumped row, after being checked for the
// characters no password ever legitimately carries.

// Role is one account on the server, in the terms every engine shares.
type Role struct {
	Name string `json:"name"`
	// Host is the second half of a MySQL account ('app'@'%'); empty elsewhere.
	Host       string `json:"host,omitempty"`
	Login      bool   `json:"login"`
	Superuser  bool   `json:"superuser"`
	CreateDB   bool   `json:"createDb"`
	CreateRole bool   `json:"createRole"`
	// ConnLimit is the account's connection cap; -1 is unlimited.
	ConnLimit  int      `json:"connectionLimit"`
	ValidUntil string   `json:"validUntil,omitempty"`
	MemberOf   []string `json:"memberOf,omitempty"`
	// Connections is how many sessions the account holds right now.
	Connections int  `json:"connections"`
	Locked      bool `json:"locked,omitempty"`
	// System marks an account the engine ships and manages itself, which the
	// page lists but does not offer to drop.
	System bool `json:"system,omitempty"`
}

// RoleSpec is what a create or alter carries.
//
// An alter changes only what the request sent. The four flags every engine
// shares are plain booleans, because Mongo and Redis read them as "grant this
// too" and a false there has always meant "leave it"; the Set* fields beside
// them say which ones an alter actually carried, and the SQL engines emit
// nothing for a flag whose Set* is false. Without that distinction a request
// carrying only a password read as "and clear every attribute", which is how
// changing a superuser's password used to demote it.
type RoleSpec struct {
	Name     string
	Host     string
	Password string
	// SetPassword distinguishes "change it to Password" from "leave it": an
	// alter that changes flags must not blank the account's password.
	SetPassword bool
	Login       bool
	Superuser   bool
	CreateDB    bool
	CreateRole  bool
	// ConnLimit of -1 is unlimited; 0 is "do not set".
	ConnLimit int

	SetLogin      bool
	SetSuperuser  bool
	SetCreateDB   bool
	SetCreateRole bool
	// The attributes only some engines have. Nil is "not sent".
	Inherit     *bool
	Replication *bool
	BypassRLS   *bool
	// Locked suspends the account without dropping it, where the engine can.
	Locked *bool
	// ValidUntil is when the password stops working: a timestamp, or
	// "infinity" (or the empty string) for never.
	ValidUntil *string
}

// Changes names what an alter would change, for the audit entry and for
// refusing a request that carries nothing. The password is named and never
// carried.
func (s RoleSpec) Changes() []string {
	out := []string{}
	add := func(set bool, name string) {
		if set {
			out = append(out, name)
		}
	}
	add(s.SetPassword, "password")
	add(s.SetLogin, "login")
	add(s.SetSuperuser, "superuser")
	add(s.SetCreateDB, "createDb")
	add(s.SetCreateRole, "createRole")
	add(s.ConnLimit != 0, "connectionLimit")
	add(s.Inherit != nil, "inherit")
	add(s.Replication != nil, "replication")
	add(s.BypassRLS != nil, "bypassRls")
	add(s.Locked != nil, "locked")
	add(s.ValidUntil != nil, "validUntil")
	return out
}

// asked names what a create asks for beyond a plain account that signs in
// with a password. A form may send every field it has; only the ones that ask
// for something are a request the engine has to be able to honour.
func (s RoleSpec) asked() []string {
	out := []string{}
	add := func(set bool, name string) {
		if set {
			out = append(out, name)
		}
	}
	on := func(v *bool, want bool) bool { return v != nil && *v == want }
	add(s.SetLogin && !s.Login, "login")
	add(s.Superuser, "superuser")
	add(s.CreateDB, "createDb")
	add(s.CreateRole, "createRole")
	add(s.ConnLimit > 0, "connectionLimit")
	add(on(s.Inherit, false), "inherit")
	add(on(s.Replication, true), "replication")
	add(on(s.BypassRLS, true), "bypassRls")
	add(on(s.Locked, true), "locked")
	add(s.ValidUntil != nil && strings.TrimSpace(*s.ValidUntil) != "" && !strings.EqualFold(strings.TrimSpace(*s.ValidUntil), "infinity"), "validUntil")
	return out
}

// ErrRoleAttribute marks an attribute the request set that this engine's
// accounts do not have. It is the request that is wrong, not the server.
type ErrRoleAttribute struct{ msg string }

func (e ErrRoleAttribute) Error() string { return e.msg }

// roleEngineNames is how each engine is named in a refusal.
var roleEngineNames = map[Driver]string{
	DriverPostgres: "PostgreSQL", DriverMySQL: "MySQL", DriverMSSQL: "SQL Server",
	DriverClickHouse: "ClickHouse", DriverMongo: "MongoDB", DriverRedis: "Redis",
}

// roleGrantOnly are the engines where "administrator" is something an account
// is granted and this surface has no single act that takes it back, with what
// to do instead.
var roleGrantOnly = map[Driver]string{
	DriverClickHouse: "revoke the grants the account should lose",
	DriverMongo:      "revoke the roles the account should lose",
	DriverRedis:      "edit the account's ACL rule",
}

// CheckRoleRequest refuses a create or an alter that asks an engine's accounts
// for something they cannot be given.
//
// It is one check in front of every engine because the alternative is what
// each engine's own code does with a field it does not read: nothing. Redis
// has no way to lock an account from here, and a request to lock one used to
// be answered "done" — to an operator who then believed a compromised account
// was shut. A request is honoured whole or refused naming the part that
// cannot be.
func CheckRoleRequest(driver Driver, spec RoleSpec, create bool) error {
	names := spec.Changes()
	if create {
		names = spec.asked()
	} else if len(names) == 0 {
		return ErrRoleAttribute{msg: "nothing to change"}
	}
	editable := EditableRoleAttributes(driver)
	for _, name := range names {
		if containsString(editable, name) {
			continue
		}
		if driver == DriverPostgres && name == "locked" {
			return ErrRoleAttribute{msg: "a PostgreSQL role is suspended by taking away its login, not by locking it"}
		}
		return ErrRoleAttribute{msg: fmt.Sprintf("%s accounts have no %s attribute", roleEngineNames[driver], name)}
	}
	if instead, only := roleGrantOnly[driver]; only && !create && spec.SetSuperuser && !spec.Superuser {
		return ErrRoleAttribute{msg: fmt.Sprintf("%s has no administrator flag to clear; %s", roleEngineNames[driver], instead)}
	}
	return nil
}

// GrantLevel is how much of a database a role is handed.
type GrantLevel string

const (
	// GrantRead is SELECT on everything, present and future.
	GrantRead GrantLevel = "read"
	// GrantWrite is read plus INSERT, UPDATE and DELETE.
	GrantWrite GrantLevel = "write"
	// GrantAll is everything the engine has on that database.
	GrantAll GrantLevel = "all"
)

func (g GrantLevel) Valid() bool {
	return g == GrantRead || g == GrantWrite || g == GrantAll
}

// DatabaseGrant is the one-step "give this account this database": a level
// rather than a list of privileges, expanded by each engine into whatever an
// application at that level needs.
type DatabaseGrant struct {
	Role     string
	Host     string
	Database string
	// Schema narrows a PostgreSQL grant to one schema. Empty means every
	// schema in the database that is not the engine's own, which is what
	// "this database" means to the person asking.
	Schema string
	Level  GrantLevel
	// Preview renders the statements, and works out what they would cover,
	// without running them.
	Preview bool
}

// GrantResult is what a database grant did, or with Preview would do.
type GrantResult struct {
	// Statements is the SQL, in the order it ran.
	Statements []string `json:"statements"`
	// Schemas are the schemas the grant reached, on the engines that have
	// them inside a database.
	Schemas []string `json:"schemas,omitempty"`
	// SkippedSchemas are the ones a grant on "the database" left alone, each
	// with why. Naming one in the request grants on it regardless.
	SkippedSchemas []SkippedSchema `json:"skippedSchemas,omitempty"`
	// Notes say what the grant could not cover.
	Notes []string `json:"notes,omitempty"`
}

// SkippedSchema is a schema a whole-database grant did not touch.
type SkippedSchema struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// Extension is one optional module the server offers, installed or not.
type Extension struct {
	Name             string `json:"name"`
	Version          string `json:"version,omitempty"`
	AvailableVersion string `json:"availableVersion,omitempty"`
	Installed        bool   `json:"installed"`
	Schema           string `json:"schema,omitempty"`
	Comment          string `json:"comment,omitempty"`
}

// Setting is one server parameter, with what the engine says about it: what
// kind of value it takes, what it started as, and whether changing it takes
// effect without a restart.
type Setting struct {
	Name        string `json:"name"`
	Value       string `json:"value"`
	Unit        string `json:"unit,omitempty"`
	Category    string `json:"category,omitempty"`
	Description string `json:"description,omitempty"`
	Source      string `json:"source,omitempty"`
	// RestartRequired marks a value that only a server restart changes.
	RestartRequired bool `json:"restartRequired,omitempty"`
	// Type is bool, integer, real, string or enum, where the engine says.
	Type string `json:"type,omitempty"`
	// Default is the value the server would have with no configuration.
	Default string `json:"default,omitempty"`
	// Min and Max bound a numeric value, in the setting's own unit.
	Min string `json:"min,omitempty"`
	Max string `json:"max,omitempty"`
	// Enum lists the values an enum accepts.
	Enum []string `json:"enum,omitempty"`
	// Context is the engine's own word for when a change applies:
	// PostgreSQL's postmaster, sighup, superuser, user; a pragma's file or
	// connection.
	Context string `json:"context,omitempty"`
	// PendingRestart is true when the configured value differs from the one
	// in effect and only a restart will apply it.
	PendingRestart bool `json:"pendingRestart,omitempty"`
	// Changed is true when the value is not the default.
	Changed bool `json:"changed,omitempty"`
	// Editable is true when this setting can be changed from here by the
	// account the connection signs in with.
	Editable bool `json:"editable"`
	// Redacted is true when the value was withheld from this viewer because
	// a parameter of this name can hold a credential.
	Redacted bool `json:"redacted,omitempty"`
}

// Admin is the server-level surface an engine offers. It is a separate
// interface from Dialect and optional, because SQLite has no server and
// Oracle's account model is far enough from the rest that pretending it fits
// would produce statements that fail on contact.
type Admin interface {
	Roles(ctx context.Context, db *sql.DB) ([]Role, error)
	CreateRole(ctx context.Context, db *sql.DB, spec RoleSpec) error
	AlterRole(ctx context.Context, db *sql.DB, spec RoleSpec) error
	DropRole(ctx context.Context, db *sql.DB, name, host string) error
	// Grant hands a role a level of access to a database and returns the
	// statements it ran and what they covered. The pool it is given is
	// connected to that database where the engine needs it to be (Postgres
	// grants on tables from inside the database that holds them); the caller
	// arranges that through GrantNeedsDatabase.
	Grant(ctx context.Context, db *sql.DB, grant DatabaseGrant) (*GrantResult, error)
	// GrantNeedsDatabase reports whether Grant has to run connected to the
	// target database rather than to whichever one the connection opens.
	GrantNeedsDatabase() bool
	CreateDatabase(ctx context.Context, db *sql.DB, name, owner string) error
	Extensions(ctx context.Context, db *sql.DB) ([]Extension, error)
	CreateExtension(ctx context.Context, db *sql.DB, name string) error
	DropExtension(ctx context.Context, db *sql.DB, name string) error
	Settings(ctx context.Context, db *sql.DB) ([]Setting, error)
}

// AdminFor returns the server surface for a driver, or ErrUnsupported where
// the engine has none.
func AdminFor(driver Driver) (Admin, error) {
	d, err := DialectFor(driver)
	if err != nil {
		return nil, err
	}
	a, ok := d.(Admin)
	if !ok {
		return nil, ErrUnsupported
	}
	return a, nil
}

// validatePassword refuses the characters no password legitimately carries
// and that would be the only way to break out of the quoting below.
func validatePassword(p string) error {
	if p == "" {
		return fmt.Errorf("a password is required")
	}
	if len(p) > 256 {
		return fmt.Errorf("password is too long")
	}
	for _, r := range p {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("password contains a control character")
		}
	}
	return nil
}

// passwordLiteral quotes a password for the one statement family that cannot
// take it as a bind argument. Same rule as dumpString: doubled quotes
// everywhere, backslashes doubled on the engines where a backslash escapes.
func passwordLiteral(driver Driver, p string) string {
	return dumpString(driver, p)
}

// --- PostgreSQL --------------------------------------------------------------

func (postgresDialect) Roles(ctx context.Context, db *sql.DB) ([]Role, error) {
	rows, err := db.QueryContext(ctx, `
	  SELECT r.rolname, r.rolcanlogin, r.rolsuper, r.rolcreatedb, r.rolcreaterole,
	         r.rolconnlimit, COALESCE(r.rolvaliduntil::text, ''),
	         COALESCE((SELECT string_agg(b.rolname, ',' ORDER BY b.rolname)
	                   FROM pg_auth_members m JOIN pg_roles b ON b.oid = m.roleid
	                   WHERE m.member = r.oid), ''),
	         (SELECT count(*) FROM pg_stat_activity a WHERE a.usename = r.rolname)
	  FROM pg_roles r
	  WHERE r.rolname NOT LIKE 'pg\_%'
	  ORDER BY r.rolcanlogin DESC, r.rolname`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Role{}
	for rows.Next() {
		var r Role
		var members string
		if err := rows.Scan(&r.Name, &r.Login, &r.Superuser, &r.CreateDB, &r.CreateRole,
			&r.ConnLimit, &r.ValidUntil, &members, &r.Connections); err != nil {
			return nil, err
		}
		if members != "" {
			r.MemberOf = strings.Split(members, ",")
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// pgRoleOptions renders the WITH clause of CREATE ROLE or ALTER ROLE.
//
// A create states all four shared flags, because a role is made with a
// definite shape. An alter states only the ones the request carried: ALTER
// ROLE leaves an attribute it does not mention exactly as it was, and that is
// the behaviour wanted from a form that changes one thing.
func pgRoleOptions(spec RoleSpec, create bool) (string, error) {
	parts := []string{}
	flag := func(set, on bool, word string) {
		if !set {
			return
		}
		if on {
			parts = append(parts, word)
		} else {
			parts = append(parts, "NO"+word)
		}
	}
	optional := func(v *bool, word string) {
		if v != nil {
			flag(true, *v, word)
		}
	}
	flag(create || spec.SetLogin, spec.Login, "LOGIN")
	flag(create || spec.SetSuperuser, spec.Superuser, "SUPERUSER")
	flag(create || spec.SetCreateDB, spec.CreateDB, "CREATEDB")
	flag(create || spec.SetCreateRole, spec.CreateRole, "CREATEROLE")
	optional(spec.Inherit, "INHERIT")
	optional(spec.Replication, "REPLICATION")
	optional(spec.BypassRLS, "BYPASSRLS")
	// A create may carry "not locked" from a form that sends every field; an
	// alter that names the attribute at all is asking for a change.
	if spec.Locked != nil && (!create || *spec.Locked) {
		return "", ErrRoleAttribute{msg: "a PostgreSQL role is suspended by taking away its login, not by locking it"}
	}
	if spec.ConnLimit != 0 {
		if spec.ConnLimit < -1 {
			return "", ErrRoleAttribute{msg: "a connection limit is -1 for unlimited or a positive number"}
		}
		parts = append(parts, "CONNECTION LIMIT "+itoa(spec.ConnLimit))
	}
	if spec.ValidUntil != nil {
		until, err := pgValidUntil(*spec.ValidUntil)
		if err != nil {
			return "", err
		}
		parts = append(parts, "VALID UNTIL "+until)
	}
	if spec.SetPassword || create {
		if spec.Password != "" {
			parts = append(parts, "PASSWORD "+passwordLiteral(DriverPostgres, spec.Password))
		} else if spec.SetPassword && !create {
			parts = append(parts, "PASSWORD NULL")
		}
	}
	if len(parts) == 0 {
		return "", ErrRoleAttribute{msg: "nothing to change"}
	}
	return strings.Join(parts, " "), nil
}

// pgValidUntil renders the literal VALID UNTIL takes. The value is parsed
// and written back out rather than quoted as it came: the clause accepts any
// timestamp text the server can read, which is more than a form should send.
func pgValidUntil(v string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" || strings.EqualFold(v, "infinity") {
		return "'infinity'", nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, v); err == nil {
			return "'" + t.UTC().Format("2006-01-02 15:04:05") + "+00'", nil
		}
	}
	return "", ErrRoleAttribute{msg: "validUntil is a date, a timestamp or \"infinity\""}
}

func (d postgresDialect) CreateRole(ctx context.Context, db *sql.DB, spec RoleSpec) error {
	name, err := d.QuoteIdent(spec.Name)
	if err != nil {
		return err
	}
	if spec.Password != "" {
		if err := validatePassword(spec.Password); err != nil {
			return err
		}
	}
	options, err := pgRoleOptions(spec, true)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, "CREATE ROLE "+name+" WITH "+options)
	return err
}

func (d postgresDialect) AlterRole(ctx context.Context, db *sql.DB, spec RoleSpec) error {
	name, err := d.QuoteIdent(spec.Name)
	if err != nil {
		return err
	}
	if spec.SetPassword && spec.Password != "" {
		if err := validatePassword(spec.Password); err != nil {
			return err
		}
	}
	options, err := pgRoleOptions(spec, false)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, "ALTER ROLE "+name+" WITH "+options)
	return err
}

func (d postgresDialect) DropRole(ctx context.Context, db *sql.DB, name, _ string) error {
	q, err := d.QuoteIdent(name)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, "DROP ROLE "+q)
	return err
}

func (postgresDialect) GrantNeedsDatabase() bool { return true }

// pgQuerier is the half of a pool or a transaction the catalogue reads need.
type pgQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// pgSupabaseSchemas are the schemas Supabase keeps its own services' state
// in. They are ordinary schemas owned by ordinary roles, so nothing in the
// catalogue marks them; they are recognised by name, and only on a server
// that has Supabase's administrator role — on any other, a schema called auth
// is the application's own.
var pgSupabaseSchemas = map[string]bool{
	"auth": true, "storage": true, "realtime": true, "_realtime": true, "vault": true,
	"graphql": true, "graphql_public": true, "extensions": true, "pgbouncer": true, "net": true,
	"supabase_functions": true, "supabase_migrations": true, "_analytics": true, "_supavisor": true,
}

// pgGrantSchemas decides which schemas "this database" means for a grant.
//
// Every schema that is not PostgreSQL's own was the first answer, and it is
// too wide: pg_cron keeps its job list in a schema, TimescaleDB its
// catalogue, Supabase its users and their password hashes, and a role granted
// "read on this database" for reporting has no business in any of them. A
// schema an extension created belongs to the extension — the catalogue says
// so, in pg_depend — and a platform's own are known by name. Those are left
// out and reported, so the operator sees what was not covered and can name
// one in the request if it was meant.
func pgGrantSchemas(ctx context.Context, q pgQuerier) (covered []string, skipped []SkippedSchema, err error) {
	var supabase bool
	if err := q.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'supabase_admin')`).Scan(&supabase); err != nil {
		return nil, nil, err
	}
	rows, err := q.QueryContext(ctx, `
	  SELECT n.nspname,
	         COALESCE((SELECT e.extname FROM pg_depend d
	                   JOIN pg_extension e ON e.oid = d.refobjid
	                   WHERE d.classid = 'pg_namespace'::regclass AND d.objid = n.oid
	                     AND d.refclassid = 'pg_extension'::regclass AND d.deptype = 'e'
	                   LIMIT 1), '')
	  FROM pg_namespace n
	  WHERE n.nspname NOT IN ('pg_catalog', 'information_schema')
	    AND n.nspname NOT LIKE 'pg\_toast%' AND n.nspname NOT LIKE 'pg\_temp%'
	  ORDER BY n.nspname`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	covered, skipped = []string{}, []SkippedSchema{}
	for rows.Next() {
		var name, extension string
		if err := rows.Scan(&name, &extension); err != nil {
			return nil, nil, err
		}
		if reason := pgSchemaSkipReason(name, extension, supabase); reason != "" {
			skipped = append(skipped, SkippedSchema{Name: name, Reason: reason})
			continue
		}
		covered = append(covered, name)
	}
	return covered, skipped, rows.Err()
}

// pgSchemaSkipReason says why a whole-database grant leaves a schema alone,
// or nothing when it does not. extension is the extension the schema is a
// member of, if any; supabase is whether the server is one of Supabase's.
func pgSchemaSkipReason(name, extension string, supabase bool) string {
	switch {
	case extension != "":
		return "it belongs to the " + extension + " extension"
	case name == "hdb_catalog":
		return "it is Hasura's own catalogue"
	case supabase && pgSupabaseSchemas[name]:
		return "it is one of Supabase's own schemas"
	}
	return ""
}

// pgFutureOwners finds, per schema, the roles whose future objects a default
// privilege has to be written for, beside the connection's own.
//
// ALTER DEFAULT PRIVILEGES applies to objects one role creates, and without
// FOR ROLE that role is whoever runs the statement — the dashboard's own
// account, which is rarely the one a migration runs as. The roles a migration
// does run as are the ones that already own something in the schema, and the
// one that owns the schema itself. Speaking for a role takes being a member of
// it, and asking for one that is not would abort the whole transaction, so
// those are returned apart: they cannot be covered, and the caller says so.
func pgFutureOwners(ctx context.Context, q pgQuerier, schemas []string) (owners, uncovered map[string][]string, err error) {
	rows, err := q.QueryContext(ctx, `
	  SELECT DISTINCT n.nspname, r.rolname, pg_has_role(current_user, r.oid, 'MEMBER')
	  FROM pg_namespace n
	  JOIN LATERAL (
	    SELECT c.relowner AS owner FROM pg_class c
	    WHERE c.relnamespace = n.oid AND c.relkind IN ('r', 'p', 'S')
	    UNION
	    SELECT n.nspowner
	  ) o ON true
	  JOIN pg_roles r ON r.oid = o.owner
	  WHERE n.nspname = ANY($1) AND r.rolname <> current_user AND r.rolname NOT LIKE 'pg\_%'
	  ORDER BY 1, 2`, schemas)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	owners, uncovered = map[string][]string{}, map[string][]string{}
	for rows.Next() {
		var schema, owner string
		var member bool
		if err := rows.Scan(&schema, &owner, &member); err != nil {
			return nil, nil, err
		}
		if member {
			owners[schema] = append(owners[schema], owner)
		} else {
			uncovered[schema] = append(uncovered[schema], owner)
		}
	}
	return owners, uncovered, rows.Err()
}

// futureOwnerNotes says which roles' future objects a default privilege could
// not be written for.
func futureOwnerNotes(schemas []string, uncovered map[string][]string) []string {
	notes := []string{}
	for _, schema := range schemas {
		if roles := uncovered[schema]; len(roles) > 0 {
			notes = append(notes, fmt.Sprintf("Objects that %s creates in %s later are not covered: this connection's account is not a member of that role.",
				strings.Join(roles, ", "), schema))
		}
	}
	return notes
}

// pgGrantStatements expands a level into the statements that give it, for
// the given schemas. owners names, per schema, the roles whose future tables
// should carry the same privileges, as pgFutureOwners found them.
func pgGrantStatements(d postgresDialect, g DatabaseGrant, schemas []string, owners map[string][]string) ([]string, error) {
	r, err := d.QuoteIdent(g.Role)
	if err != nil {
		return nil, err
	}
	dbName, err := d.QuoteIdent(g.Database)
	if err != nil {
		return nil, err
	}
	var onDatabase, onSchema, onTables, onSequences, onFunctions string
	switch g.Level {
	case GrantAll:
		onDatabase, onSchema = "ALL PRIVILEGES", "ALL"
		onTables, onSequences, onFunctions = "ALL PRIVILEGES", "ALL PRIVILEGES", "ALL PRIVILEGES"
	case GrantWrite:
		onDatabase, onSchema = "CONNECT", "USAGE"
		onTables, onSequences = "SELECT, INSERT, UPDATE, DELETE", "USAGE, SELECT"
	case GrantRead:
		onDatabase, onSchema = "CONNECT", "USAGE"
		onTables, onSequences = "SELECT", "SELECT"
	default:
		return nil, fmt.Errorf("unknown grant level %q", g.Level)
	}
	stmts := []string{"GRANT " + onDatabase + " ON DATABASE " + dbName + " TO " + r}
	for _, schema := range schemas {
		s, err := d.QuoteIdent(schema)
		if err != nil {
			return nil, err
		}
		stmts = append(stmts,
			"GRANT "+onSchema+" ON SCHEMA "+s+" TO "+r,
			"GRANT "+onTables+" ON ALL TABLES IN SCHEMA "+s+" TO "+r,
			"GRANT "+onSequences+" ON ALL SEQUENCES IN SCHEMA "+s+" TO "+r,
		)
		if onFunctions != "" {
			stmts = append(stmts, "GRANT "+onFunctions+" ON ALL FUNCTIONS IN SCHEMA "+s+" TO "+r)
		}
		// A table the migration creates tomorrow should be readable too.
		// The plain form covers what this account creates; one FOR ROLE per
		// other owner covers what they do.
		for _, owner := range append([]string{""}, owners[schema]...) {
			prefix := "ALTER DEFAULT PRIVILEGES"
			if owner != "" {
				o, err := d.QuoteIdent(owner)
				if err != nil {
					return nil, err
				}
				prefix += " FOR ROLE " + o
			}
			prefix += " IN SCHEMA " + s
			stmts = append(stmts,
				prefix+" GRANT "+onTables+" ON TABLES TO "+r,
				prefix+" GRANT "+onSequences+" ON SEQUENCES TO "+r,
			)
		}
	}
	return stmts, nil
}

// Grant runs inside the target database. The database-level GRANT only
// covers connecting; what an application needs is the schemas and the tables
// in them, present and future — which is why ALTER DEFAULT PRIVILEGES is part
// of every level. All of it runs in one transaction: a grant that stopped
// halfway used to leave an account that could connect and read nothing, and
// nothing on the page to say which half had run.
func (d postgresDialect) Grant(ctx context.Context, db *sql.DB, g DatabaseGrant) (*GrantResult, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	out := &GrantResult{Schemas: []string{g.Schema}}
	if g.Schema == "" {
		if out.Schemas, out.SkippedSchemas, err = pgGrantSchemas(ctx, tx); err != nil {
			return nil, err
		}
	}
	owners, uncovered, err := pgFutureOwners(ctx, tx, out.Schemas)
	if err != nil {
		return nil, err
	}
	out.Notes = futureOwnerNotes(out.Schemas, uncovered)
	if out.Statements, err = pgGrantStatements(d, g, out.Schemas, owners); err != nil {
		return nil, err
	}
	if g.Preview {
		return out, nil
	}
	for _, stmt := range out.Statements {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return nil, err
		}
	}
	return out, tx.Commit()
}

func (d postgresDialect) CreateDatabase(ctx context.Context, db *sql.DB, name, owner string) error {
	q, err := d.QuoteIdent(name)
	if err != nil {
		return err
	}
	stmt := "CREATE DATABASE " + q
	if owner != "" {
		o, err := d.QuoteIdent(owner)
		if err != nil {
			return err
		}
		stmt += " OWNER " + o
	}
	_, err = db.ExecContext(ctx, stmt)
	return err
}

func (postgresDialect) Extensions(ctx context.Context, db *sql.DB) ([]Extension, error) {
	rows, err := db.QueryContext(ctx, `
	  SELECT a.name, COALESCE(e.extversion, ''), COALESCE(a.default_version, ''),
	         e.oid IS NOT NULL, COALESCE(n.nspname, ''), COALESCE(a.comment, '')
	  FROM pg_available_extensions a
	  LEFT JOIN pg_extension e ON e.extname = a.name
	  LEFT JOIN pg_namespace n ON n.oid = e.extnamespace
	  ORDER BY (e.oid IS NULL), a.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Extension{}
	for rows.Next() {
		var e Extension
		if err := rows.Scan(&e.Name, &e.Version, &e.AvailableVersion, &e.Installed, &e.Schema, &e.Comment); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (d postgresDialect) CreateExtension(ctx context.Context, db *sql.DB, name string) error {
	q, err := d.QuoteIdent(name)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, "CREATE EXTENSION IF NOT EXISTS "+q)
	return err
}

func (d postgresDialect) DropExtension(ctx context.Context, db *sql.DB, name string) error {
	q, err := d.QuoteIdent(name)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, "DROP EXTENSION "+q)
	return err
}

// The parameters an operator actually looks for, rather than all 350. Each is
// the answer to a question that gets asked: how many connections may I have,
// how much memory is it using, is it listening beyond loopback, is SSL on,
// where are its files, does it log slow statements.
var postgresSettingNames = []string{
	"server_version", "data_directory", "config_file", "hba_file", "listen_addresses", "port",
	"max_connections", "superuser_reserved_connections", "shared_buffers", "work_mem",
	"maintenance_work_mem", "effective_cache_size", "wal_level", "max_wal_size",
	"checkpoint_timeout", "synchronous_commit", "ssl", "password_encryption",
	"timezone", "log_destination", "log_min_duration_statement", "statement_timeout",
	"idle_in_transaction_session_timeout", "lock_timeout", "autovacuum",
	"shared_preload_libraries", "max_worker_processes", "max_parallel_workers",
	"random_page_cost", "jit", "data_checksums", "track_io_timing",
}

func (d postgresDialect) Settings(ctx context.Context, db *sql.DB) ([]Setting, error) {
	all, err := d.AllSettings(ctx, db)
	if err != nil {
		return nil, err
	}
	return pickSettings(all, postgresSettingNames), nil
}

// pickSettings keeps the named settings, in the order they are named.
func pickSettings(all []Setting, names []string) []Setting {
	byName := make(map[string]Setting, len(all))
	for _, s := range all {
		byName[strings.ToLower(s.Name)] = s
	}
	out := []Setting{}
	for _, n := range names {
		if s, ok := byName[strings.ToLower(n)]; ok {
			out = append(out, s)
		}
	}
	return out
}

// --- MySQL / MariaDB ---------------------------------------------------------

// mysqlRolesSQL lists the accounts. Whether one is locked is a column of
// mysql.user on MySQL and a key of the account's JSON document on MariaDB,
// where mysql.user is a view over mysql.global_priv that leaves it out.
func mysqlRolesSQL(locked, from string) string {
	return `
	  SELECT u.User, u.Host, u.Super_priv = 'Y', u.Create_priv = 'Y', u.Create_user_priv = 'Y',
	         COALESCE(u.max_user_connections, 0),
	         (SELECT COUNT(*) FROM information_schema.PROCESSLIST p WHERE p.USER = u.User),
	         ` + locked + `
	  FROM ` + from + `
	  ORDER BY u.User, u.Host`
}

func (mysqlDialect) Roles(ctx context.Context, db *sql.DB) ([]Role, error) {
	rows, err := db.QueryContext(ctx, mysqlRolesSQL(`u.account_locked = 'Y'`, `mysql.user u`))
	if err != nil {
		rows, err = db.QueryContext(ctx, mysqlRolesSQL(
			`COALESCE(JSON_VALUE(g.Priv, '$.account_locked'), 'false') IN ('true', '1')`,
			`mysql.user u LEFT JOIN mysql.global_priv g ON g.User = u.User AND g.Host = u.Host`))
	}
	if err != nil {
		rows, err = db.QueryContext(ctx, mysqlRolesSQL(`0`, `mysql.user u`))
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Role{}
	for rows.Next() {
		var r Role
		if err := rows.Scan(&r.Name, &r.Host, &r.Superuser, &r.CreateDB, &r.CreateRole, &r.ConnLimit, &r.Connections, &r.Locked); err != nil {
			return nil, err
		}
		r.Login = !r.Locked
		if r.ConnLimit == 0 {
			r.ConnLimit = -1
		}
		// The accounts MySQL and MariaDB install for their own use.
		switch r.Name {
		case "mysql.sys", "mysql.session", "mysql.infoschema", "mariadb.sys":
			r.System = true
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// mysqlAccount renders 'user'@'host' with each half as a literal. Both halves
// are values in MySQL's grammar, not identifiers, so they take string quoting —
// the user half escaped, since validateIdent lets a quote or a backslash through.
func mysqlAccount(user, host string) (string, error) {
	if err := validateIdent(user); err != nil {
		return "", err
	}
	if host == "" {
		host = "%"
	}
	for _, r := range host {
		if !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') &&
			r != '.' && r != '%' && r != '_' && r != '-' && r != ':' {
			return "", fmt.Errorf("host %q may contain letters, digits, dots, dashes, colons, %% and _", host)
		}
	}
	return dumpString(DriverMySQL, user) + "@'" + host + "'", nil
}

// mysqlCreateRoleStatements renders the account and then what it was asked to
// have. An account that may not sign in is made locked: MySQL has no other
// way to say it.
func mysqlCreateRoleStatements(account string, spec RoleSpec) []string {
	create := "CREATE USER " + account + " IDENTIFIED BY " + passwordLiteral(DriverMySQL, spec.Password)
	if spec.ConnLimit > 0 {
		create += " WITH MAX_USER_CONNECTIONS " + itoa(spec.ConnLimit)
	}
	if (spec.SetLogin && !spec.Login) || (spec.Locked != nil && *spec.Locked) {
		create += " ACCOUNT LOCK"
	}
	stmts := []string{create}
	if spec.Superuser {
		// Every privilege already includes the two below.
		return append(stmts, "GRANT ALL PRIVILEGES ON *.* TO "+account+" WITH GRANT OPTION")
	}
	if spec.CreateDB {
		stmts = append(stmts, "GRANT CREATE ON *.* TO "+account)
	}
	if spec.CreateRole {
		stmts = append(stmts, "GRANT CREATE USER ON *.* TO "+account)
	}
	return stmts
}

func (mysqlDialect) CreateRole(ctx context.Context, db *sql.DB, spec RoleSpec) error {
	account, err := mysqlAccount(spec.Name, spec.Host)
	if err != nil {
		return err
	}
	if err := CheckRoleRequest(DriverMySQL, spec, true); err != nil {
		return err
	}
	if err := validatePassword(spec.Password); err != nil {
		return err
	}
	// The account and its privileges are separate statements and MySQL
	// commits each. If a later one fails the account is undone by hand: one
	// left behind without what it was asked to have cannot be created again,
	// and nothing on the page says it is there.
	for i, stmt := range mysqlCreateRoleStatements(account, spec) {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			if i > 0 {
				_, _ = db.ExecContext(context.WithoutCancel(ctx), "DROP USER "+account)
			}
			return err
		}
	}
	return nil
}

// AlterRole changes what the request carried, a statement per attribute:
// MySQL has no single statement that sets a password, a connection cap and a
// global privilege together. "Superuser" here is every privilege on *.* with
// the right to grant it, and taking it away revokes exactly that — the
// account's grants on particular databases are separate rows and stay.
func (mysqlDialect) AlterRole(ctx context.Context, db *sql.DB, spec RoleSpec) error {
	account, err := mysqlAccount(spec.Name, spec.Host)
	if err != nil {
		return err
	}
	if err := CheckRoleRequest(DriverMySQL, spec, false); err != nil {
		return err
	}
	stmts := []string{}
	if spec.SetPassword {
		if err := validatePassword(spec.Password); err != nil {
			return err
		}
		stmts = append(stmts, "ALTER USER "+account+" IDENTIFIED BY "+passwordLiteral(DriverMySQL, spec.Password))
	}
	if spec.ConnLimit != 0 {
		limit := spec.ConnLimit
		if limit < 0 {
			limit = 0
		}
		stmts = append(stmts, "ALTER USER "+account+" WITH MAX_USER_CONNECTIONS "+itoa(limit))
	}
	// An account that may not sign in is a locked one; MySQL has no other
	// way to say it, so the two attributes are the same switch.
	locked := spec.Locked
	if locked == nil && spec.SetLogin {
		off := !spec.Login
		locked = &off
	}
	if locked != nil {
		if *locked {
			stmts = append(stmts, "ALTER USER "+account+" ACCOUNT LOCK")
		} else {
			stmts = append(stmts, "ALTER USER "+account+" ACCOUNT UNLOCK")
		}
	}
	global := func(set, on bool, privilege string, withGrant bool) {
		if !set {
			return
		}
		if on {
			stmt := "GRANT " + privilege + " ON *.* TO " + account
			if withGrant {
				stmt += " WITH GRANT OPTION"
			}
			stmts = append(stmts, stmt)
			return
		}
		stmts = append(stmts, "REVOKE "+privilege+" ON *.* FROM "+account)
		if withGrant {
			stmts = append(stmts, "REVOKE GRANT OPTION ON *.* FROM "+account)
		}
	}
	global(spec.SetSuperuser, spec.Superuser, "ALL PRIVILEGES", true)
	// Revoking ALL has already taken CREATE and CREATE USER with it; stating
	// them again would only fail on a privilege that is no longer there.
	if !(spec.SetSuperuser && !spec.Superuser) {
		global(spec.SetCreateDB, spec.CreateDB, "CREATE", false)
		global(spec.SetCreateRole, spec.CreateRole, "CREATE USER", false)
	}
	for _, stmt := range stmts {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

func (mysqlDialect) DropRole(ctx context.Context, db *sql.DB, name, host string) error {
	account, err := mysqlAccount(name, host)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, "DROP USER "+account)
	return err
}

func (mysqlDialect) GrantNeedsDatabase() bool { return false }

// mysqlGrantDatabase quotes a database name for the left of ".*" in a GRANT.
//
// At that position MySQL reads the name as a pattern: "_" matches any one
// character and "%" any run of them, so a grant on shop_eu also covers
// shopXeu. A backslash makes each literal, which is what somebody naming a
// database means.
func mysqlGrantDatabase(name string) (string, error) {
	q, err := quoteBacktick(name)
	if err != nil {
		return "", err
	}
	q = strings.ReplaceAll(q, `\`, `\\`)
	q = strings.ReplaceAll(q, "_", `\_`)
	return strings.ReplaceAll(q, "%", `\%`), nil
}

func (d mysqlDialect) Grant(ctx context.Context, db *sql.DB, g DatabaseGrant) (*GrantResult, error) {
	account, err := mysqlAccount(g.Role, g.Host)
	if err != nil {
		return nil, err
	}
	dbName, err := mysqlGrantDatabase(g.Database)
	if err != nil {
		return nil, err
	}
	var privileges string
	switch g.Level {
	case GrantAll:
		privileges = "ALL PRIVILEGES"
	case GrantWrite:
		privileges = "SELECT, INSERT, UPDATE, DELETE, SHOW VIEW"
	case GrantRead:
		privileges = "SELECT, SHOW VIEW"
	default:
		return nil, fmt.Errorf("unknown grant level %q", g.Level)
	}
	out := &GrantResult{Statements: []string{"GRANT " + privileges + " ON " + dbName + ".* TO " + account}}
	if g.Preview {
		return out, nil
	}
	_, err = db.ExecContext(ctx, out.Statements[0])
	return out, err
}

func (d mysqlDialect) CreateDatabase(ctx context.Context, db *sql.DB, name, _ string) error {
	q, err := d.QuoteIdent(name)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, "CREATE DATABASE "+q)
	return err
}

// Extensions are MySQL's plugins: listed so the operator can see what is
// loaded, but not installed from here — INSTALL PLUGIN needs the shared
// object's file name, which is not something a page should guess at.
func (mysqlDialect) Extensions(ctx context.Context, db *sql.DB) ([]Extension, error) {
	rows, err := db.QueryContext(ctx, `
	  SELECT PLUGIN_NAME, COALESCE(PLUGIN_VERSION, ''), PLUGIN_STATUS, PLUGIN_TYPE, COALESCE(PLUGIN_DESCRIPTION, '')
	  FROM information_schema.PLUGINS
	  ORDER BY PLUGIN_TYPE, PLUGIN_NAME`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Extension{}
	for rows.Next() {
		var e Extension
		var status, typ string
		if err := rows.Scan(&e.Name, &e.Version, &status, &typ, &e.Comment); err != nil {
			return nil, err
		}
		e.Installed = strings.EqualFold(status, "ACTIVE")
		e.Schema = typ
		out = append(out, e)
	}
	return out, rows.Err()
}

func (mysqlDialect) CreateExtension(context.Context, *sql.DB, string) error { return ErrUnsupported }
func (mysqlDialect) DropExtension(context.Context, *sql.DB, string) error   { return ErrUnsupported }

var mysqlSettingNames = []string{
	"version", "version_comment", "datadir", "port", "bind_address", "skip_networking",
	"max_connections", "max_user_connections", "innodb_buffer_pool_size", "innodb_log_file_size",
	"tmp_table_size", "max_allowed_packet", "wait_timeout", "character_set_server",
	"collation_server", "default_storage_engine", "sql_mode", "time_zone",
	"log_bin", "slow_query_log", "long_query_time", "have_ssl", "require_secure_transport",
	"performance_schema",
}

func (d mysqlDialect) Settings(ctx context.Context, db *sql.DB) ([]Setting, error) {
	all, err := d.AllSettings(ctx, db)
	if err != nil {
		return nil, err
	}
	return pickSettings(all, mysqlSettingNames), nil
}

// --- ClickHouse --------------------------------------------------------------

func (clickhouseDialect) Roles(ctx context.Context, db *sql.DB) ([]Role, error) {
	rows, err := db.QueryContext(ctx, `SELECT name, auth_type::text FROM system.users ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Role{}
	for rows.Next() {
		var r Role
		var auth string
		if err := rows.Scan(&r.Name, &auth); err != nil {
			return nil, err
		}
		r.Login = true
		r.ConnLimit = -1
		r.System = r.Name == "default"
		out = append(out, r)
	}
	return out, rows.Err()
}

func (d clickhouseDialect) CreateRole(ctx context.Context, db *sql.DB, spec RoleSpec) error {
	name, err := d.QuoteIdent(spec.Name)
	if err != nil {
		return err
	}
	if err := CheckRoleRequest(DriverClickHouse, spec, true); err != nil {
		return err
	}
	if err := validatePassword(spec.Password); err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, "CREATE USER "+name+" IDENTIFIED WITH sha256_password BY "+passwordLiteral(DriverClickHouse, spec.Password))
	if err != nil {
		return err
	}
	if !spec.Superuser {
		return nil
	}
	if _, err := db.ExecContext(ctx, "GRANT ALL ON *.* TO "+name+" WITH GRANT OPTION"); err != nil {
		// Undone by hand, as on MySQL: the two statements are not one.
		_, _ = db.ExecContext(context.WithoutCancel(ctx), "DROP USER "+name)
		return err
	}
	return nil
}

func (d clickhouseDialect) AlterRole(ctx context.Context, db *sql.DB, spec RoleSpec) error {
	name, err := d.QuoteIdent(spec.Name)
	if err != nil {
		return err
	}
	// Among what this refuses is clearing the administrator flag: REVOKE ALL
	// ON *.* takes every grant the account has, at every level, which is far
	// more than unticking a box says.
	if err := CheckRoleRequest(DriverClickHouse, spec, false); err != nil {
		return err
	}
	if spec.SetPassword {
		if err := validatePassword(spec.Password); err != nil {
			return err
		}
		if _, err := db.ExecContext(ctx, "ALTER USER "+name+" IDENTIFIED WITH sha256_password BY "+passwordLiteral(DriverClickHouse, spec.Password)); err != nil {
			return err
		}
	}
	if spec.SetSuperuser {
		_, err = db.ExecContext(ctx, "GRANT ALL ON *.* TO "+name+" WITH GRANT OPTION")
	}
	return err
}

func (d clickhouseDialect) DropRole(ctx context.Context, db *sql.DB, name, _ string) error {
	q, err := d.QuoteIdent(name)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, "DROP USER "+q)
	return err
}

func (clickhouseDialect) GrantNeedsDatabase() bool { return false }

func (d clickhouseDialect) Grant(ctx context.Context, db *sql.DB, g DatabaseGrant) (*GrantResult, error) {
	r, err := d.QuoteIdent(g.Role)
	if err != nil {
		return nil, err
	}
	dbName, err := d.QuoteIdent(g.Database)
	if err != nil {
		return nil, err
	}
	var privileges string
	switch g.Level {
	case GrantAll:
		privileges = "ALL"
	case GrantWrite:
		privileges = "SELECT, INSERT, ALTER DELETE, ALTER UPDATE"
	case GrantRead:
		privileges = "SELECT"
	default:
		return nil, fmt.Errorf("unknown grant level %q", g.Level)
	}
	out := &GrantResult{Statements: []string{"GRANT " + privileges + " ON " + dbName + ".* TO " + r}}
	if g.Preview {
		return out, nil
	}
	_, err = db.ExecContext(ctx, out.Statements[0])
	return out, err
}

func (d clickhouseDialect) CreateDatabase(ctx context.Context, db *sql.DB, name, _ string) error {
	q, err := d.QuoteIdent(name)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, "CREATE DATABASE "+q)
	return err
}

func (clickhouseDialect) Extensions(context.Context, *sql.DB) ([]Extension, error) {
	return nil, ErrUnsupported
}
func (clickhouseDialect) CreateExtension(context.Context, *sql.DB, string) error {
	return ErrUnsupported
}
func (clickhouseDialect) DropExtension(context.Context, *sql.DB, string) error { return ErrUnsupported }

// The server settings an operator looks for first, out of the few hundred.
var clickhouseSettingNames = []string{
	"max_connections", "max_concurrent_queries", "max_server_memory_usage", "path", "tmp_path",
	"listen_host", "tcp_port", "http_port", "timezone", "mark_cache_size", "uncompressed_cache_size",
	"max_thread_pool_size",
}

func (d clickhouseDialect) Settings(ctx context.Context, db *sql.DB) ([]Setting, error) {
	all, err := d.AllSettings(ctx, db)
	if err != nil {
		return nil, err
	}
	return pickSettings(all, clickhouseSettingNames), nil
}

// --- SQL Server --------------------------------------------------------------

func (mssqlDialect) Roles(ctx context.Context, db *sql.DB) ([]Role, error) {
	rows, err := db.QueryContext(ctx, `
	  SELECT p.name, p.type_desc, p.is_disabled,
	         CASE WHEN IS_SRVROLEMEMBER('sysadmin', p.name) = 1 THEN 1 ELSE 0 END,
	         CASE WHEN IS_SRVROLEMEMBER('dbcreator', p.name) = 1 THEN 1 ELSE 0 END,
	         CASE WHEN IS_SRVROLEMEMBER('securityadmin', p.name) = 1 THEN 1 ELSE 0 END,
	         (SELECT COUNT(*) FROM sys.dm_exec_sessions s WHERE s.login_name = p.name AND s.is_user_process = 1)
	  FROM sys.server_principals p
	  WHERE p.type IN ('S', 'U') AND p.name NOT LIKE '##%'
	  ORDER BY p.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Role{}
	for rows.Next() {
		var r Role
		var typ string
		// is_disabled is a bit, which the driver hands over as a boolean; the
		// three memberships are CASE expressions and arrive as integers.
		var disabled bool
		var super, creator, security int
		if err := rows.Scan(&r.Name, &typ, &disabled, &super, &creator, &security, &r.Connections); err != nil {
			return nil, err
		}
		r.Login = !disabled
		r.Locked = disabled
		r.Superuser = super == 1
		r.CreateDB = creator == 1
		r.CreateRole = security == 1
		r.ConnLimit = -1
		r.System = strings.HasPrefix(r.Name, "NT ") || r.Name == "sa"
		out = append(out, r)
	}
	return out, rows.Err()
}

// mssqlCreateRoleStatements renders the login and then what it was asked to
// have: a membership of a fixed server role for each flag, and a disabled
// login for an account that may not sign in.
func mssqlCreateRoleStatements(name string, spec RoleSpec) []string {
	stmts := []string{"CREATE LOGIN " + name + " WITH PASSWORD = " + passwordLiteral(DriverMSSQL, spec.Password)}
	switch {
	case spec.Superuser:
		// sysadmin already holds what the other two give.
		stmts = append(stmts, "ALTER SERVER ROLE sysadmin ADD MEMBER "+name)
	default:
		if spec.CreateDB {
			stmts = append(stmts, "ALTER SERVER ROLE dbcreator ADD MEMBER "+name)
		}
		if spec.CreateRole {
			stmts = append(stmts, "ALTER SERVER ROLE securityadmin ADD MEMBER "+name)
		}
	}
	if (spec.SetLogin && !spec.Login) || (spec.Locked != nil && *spec.Locked) {
		stmts = append(stmts, "ALTER LOGIN "+name+" DISABLE")
	}
	return stmts
}

func (d mssqlDialect) CreateRole(ctx context.Context, db *sql.DB, spec RoleSpec) error {
	name, err := d.QuoteIdent(spec.Name)
	if err != nil {
		return err
	}
	if err := CheckRoleRequest(DriverMSSQL, spec, true); err != nil {
		return err
	}
	if err := validatePassword(spec.Password); err != nil {
		return err
	}
	for i, stmt := range mssqlCreateRoleStatements(name, spec) {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			if i > 0 {
				// Undone by hand, as on MySQL: the statements are not one.
				_, _ = db.ExecContext(context.WithoutCancel(ctx), "DROP LOGIN "+name)
			}
			return err
		}
	}
	return nil
}

// AlterRole changes what the request carried. The three shared flags are
// memberships of fixed server roles on SQL Server, so each is an ADD MEMBER
// or a DROP MEMBER, and a disabled login is the engine's locked account.
func (d mssqlDialect) AlterRole(ctx context.Context, db *sql.DB, spec RoleSpec) error {
	name, err := d.QuoteIdent(spec.Name)
	if err != nil {
		return err
	}
	if err := CheckRoleRequest(DriverMSSQL, spec, false); err != nil {
		return err
	}
	stmts := []string{}
	if spec.SetPassword {
		if err := validatePassword(spec.Password); err != nil {
			return err
		}
		stmts = append(stmts, "ALTER LOGIN "+name+" WITH PASSWORD = "+passwordLiteral(DriverMSSQL, spec.Password))
	}
	locked := spec.Locked
	if locked == nil && spec.SetLogin {
		off := !spec.Login
		locked = &off
	}
	if locked != nil {
		if *locked {
			stmts = append(stmts, "ALTER LOGIN "+name+" DISABLE")
		} else {
			stmts = append(stmts, "ALTER LOGIN "+name+" ENABLE")
		}
	}
	member := func(set, on bool, serverRole string) {
		if !set {
			return
		}
		verb := "DROP"
		if on {
			verb = "ADD"
		}
		stmts = append(stmts, "ALTER SERVER ROLE "+serverRole+" "+verb+" MEMBER "+name)
	}
	member(spec.SetSuperuser, spec.Superuser, "sysadmin")
	member(spec.SetCreateDB, spec.CreateDB, "dbcreator")
	member(spec.SetCreateRole, spec.CreateRole, "securityadmin")
	for _, stmt := range stmts {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

func (d mssqlDialect) DropRole(ctx context.Context, db *sql.DB, name, _ string) error {
	q, err := d.QuoteIdent(name)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, "DROP LOGIN "+q)
	return err
}

func (mssqlDialect) GrantNeedsDatabase() bool { return true }

func (d mssqlDialect) Grant(ctx context.Context, db *sql.DB, g DatabaseGrant) (*GrantResult, error) {
	r, err := d.QuoteIdent(g.Role)
	if err != nil {
		return nil, err
	}
	var dbRoles []string
	switch g.Level {
	case GrantAll:
		dbRoles = []string{"db_owner"}
	case GrantWrite:
		dbRoles = []string{"db_datareader", "db_datawriter"}
	case GrantRead:
		dbRoles = []string{"db_datareader"}
	default:
		return nil, fmt.Errorf("unknown grant level %q", g.Level)
	}
	out := &GrantResult{Statements: []string{}}
	for _, dbRole := range dbRoles {
		out.Statements = append(out.Statements, "ALTER ROLE "+dbRole+" ADD MEMBER "+r)
	}
	if g.Preview {
		return out, nil
	}
	// The login needs a user in the database before it can be a member of
	// anything; one that already exists is not an error worth failing over.
	_, _ = db.ExecContext(ctx, "CREATE USER "+r+" FOR LOGIN "+r)
	for i, stmt := range out.Statements {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			out.Statements = out.Statements[:i]
			return out, err
		}
	}
	return out, nil
}

func (d mssqlDialect) CreateDatabase(ctx context.Context, db *sql.DB, name, _ string) error {
	q, err := d.QuoteIdent(name)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, "CREATE DATABASE "+q)
	return err
}

func (mssqlDialect) Extensions(context.Context, *sql.DB) ([]Extension, error) {
	return nil, ErrUnsupported
}
func (mssqlDialect) CreateExtension(context.Context, *sql.DB, string) error { return ErrUnsupported }
func (mssqlDialect) DropExtension(context.Context, *sql.DB, string) error   { return ErrUnsupported }

var mssqlSettingNames = []string{
	"user connections", "max server memory (MB)", "min server memory (MB)", "max degree of parallelism",
	"cost threshold for parallelism", "remote access", "backup compression default", "default language",
}

func (d mssqlDialect) Settings(ctx context.Context, db *sql.DB) ([]Setting, error) {
	all, err := d.AllSettings(ctx, db)
	if err != nil {
		return nil, err
	}
	return pickSettings(all, mssqlSettingNames), nil
}
