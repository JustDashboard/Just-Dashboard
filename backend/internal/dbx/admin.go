package dbx

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
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

// Extension is one optional module the server offers, installed or not.
type Extension struct {
	Name             string `json:"name"`
	Version          string `json:"version,omitempty"`
	AvailableVersion string `json:"availableVersion,omitempty"`
	Installed        bool   `json:"installed"`
	Schema           string `json:"schema,omitempty"`
	Comment          string `json:"comment,omitempty"`
}

// Setting is one server parameter the operator is likely to ask about.
type Setting struct {
	Name        string `json:"name"`
	Value       string `json:"value"`
	Unit        string `json:"unit,omitempty"`
	Category    string `json:"category,omitempty"`
	Description string `json:"description,omitempty"`
	Source      string `json:"source,omitempty"`
	// RestartRequired marks a value that only a server restart changes.
	RestartRequired bool `json:"restartRequired,omitempty"`
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
	// Grant hands a role a level of access to a database. The pool it is
	// given is connected to that database where the engine needs it to be
	// (Postgres grants on tables from inside the database that holds them);
	// the caller arranges that through GrantNeedsDatabase.
	Grant(ctx context.Context, db *sql.DB, role, host, database string, level GrantLevel) error
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

func pgRoleOptions(spec RoleSpec, create bool) string {
	parts := []string{}
	flag := func(on bool, word string) {
		if on {
			parts = append(parts, word)
		} else {
			parts = append(parts, "NO"+word)
		}
	}
	flag(spec.Login, "LOGIN")
	flag(spec.Superuser, "SUPERUSER")
	flag(spec.CreateDB, "CREATEDB")
	flag(spec.CreateRole, "CREATEROLE")
	if spec.ConnLimit != 0 {
		parts = append(parts, "CONNECTION LIMIT "+itoa(spec.ConnLimit))
	}
	if spec.SetPassword || create {
		if spec.Password != "" {
			parts = append(parts, "PASSWORD "+passwordLiteral(DriverPostgres, spec.Password))
		} else if spec.SetPassword {
			parts = append(parts, "PASSWORD NULL")
		}
	}
	return strings.Join(parts, " ")
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
	_, err = db.ExecContext(ctx, "CREATE ROLE "+name+" WITH "+pgRoleOptions(spec, true))
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
	_, err = db.ExecContext(ctx, "ALTER ROLE "+name+" WITH "+pgRoleOptions(spec, false))
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

// Grant runs inside the target database. The database-level GRANT only
// covers connecting; what an application needs is the schema and the tables
// in it, present and future — which is why ALTER DEFAULT PRIVILEGES is part
// of every level, so a table the migration creates tomorrow is readable too.
func (d postgresDialect) Grant(ctx context.Context, db *sql.DB, role, _, database string, level GrantLevel) error {
	r, err := d.QuoteIdent(role)
	if err != nil {
		return err
	}
	dbName, err := d.QuoteIdent(database)
	if err != nil {
		return err
	}
	var stmts []string
	switch level {
	case GrantAll:
		stmts = []string{
			"GRANT ALL PRIVILEGES ON DATABASE " + dbName + " TO " + r,
			"GRANT ALL ON SCHEMA public TO " + r,
			"GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA public TO " + r,
			"GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA public TO " + r,
			"GRANT ALL PRIVILEGES ON ALL FUNCTIONS IN SCHEMA public TO " + r,
			"ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT ALL PRIVILEGES ON TABLES TO " + r,
			"ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT ALL PRIVILEGES ON SEQUENCES TO " + r,
		}
	case GrantWrite:
		stmts = []string{
			"GRANT CONNECT ON DATABASE " + dbName + " TO " + r,
			"GRANT USAGE ON SCHEMA public TO " + r,
			"GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO " + r,
			"GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO " + r,
			"ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO " + r,
			"ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT USAGE, SELECT ON SEQUENCES TO " + r,
		}
	case GrantRead:
		stmts = []string{
			"GRANT CONNECT ON DATABASE " + dbName + " TO " + r,
			"GRANT USAGE ON SCHEMA public TO " + r,
			"GRANT SELECT ON ALL TABLES IN SCHEMA public TO " + r,
			"GRANT SELECT ON ALL SEQUENCES IN SCHEMA public TO " + r,
			"ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT ON TABLES TO " + r,
		}
	default:
		return fmt.Errorf("unknown grant level %q", level)
	}
	for _, stmt := range stmts {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
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

func (postgresDialect) Settings(ctx context.Context, db *sql.DB) ([]Setting, error) {
	rows, err := db.QueryContext(ctx, `
	  SELECT name, setting, COALESCE(unit, ''), category, COALESCE(short_desc, ''),
	         source, context IN ('postmaster', 'internal')
	  FROM pg_settings WHERE name = ANY($1)
	  ORDER BY array_position($1, name)`, postgresSettingNames)
	if err != nil {
		return nil, err
	}
	return scanSettings(rows)
}

func scanSettings(rows *sql.Rows) ([]Setting, error) {
	defer rows.Close()
	out := []Setting{}
	for rows.Next() {
		var s Setting
		if err := rows.Scan(&s.Name, &s.Value, &s.Unit, &s.Category, &s.Description, &s.Source, &s.RestartRequired); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// --- MySQL / MariaDB ---------------------------------------------------------

func (mysqlDialect) Roles(ctx context.Context, db *sql.DB) ([]Role, error) {
	rows, err := db.QueryContext(ctx, `
	  SELECT u.User, u.Host, u.Super_priv = 'Y', u.Create_priv = 'Y', u.Create_user_priv = 'Y',
	         COALESCE(u.max_user_connections, 0),
	         (SELECT COUNT(*) FROM information_schema.PROCESSLIST p WHERE p.USER = u.User)
	  FROM mysql.user u
	  ORDER BY u.User, u.Host`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Role{}
	for rows.Next() {
		var r Role
		if err := rows.Scan(&r.Name, &r.Host, &r.Superuser, &r.CreateDB, &r.CreateRole, &r.ConnLimit, &r.Connections); err != nil {
			return nil, err
		}
		r.Login = true
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
// are values in MySQL's grammar, not identifiers, so they take string quoting.
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
	return "'" + user + "'@'" + host + "'", nil
}

func (mysqlDialect) CreateRole(ctx context.Context, db *sql.DB, spec RoleSpec) error {
	account, err := mysqlAccount(spec.Name, spec.Host)
	if err != nil {
		return err
	}
	if err := validatePassword(spec.Password); err != nil {
		return err
	}
	stmt := "CREATE USER " + account + " IDENTIFIED BY " + passwordLiteral(DriverMySQL, spec.Password)
	if spec.ConnLimit > 0 {
		stmt += " WITH MAX_USER_CONNECTIONS " + itoa(spec.ConnLimit)
	}
	if _, err := db.ExecContext(ctx, stmt); err != nil {
		return err
	}
	if spec.Superuser {
		_, err = db.ExecContext(ctx, "GRANT ALL PRIVILEGES ON *.* TO "+account+" WITH GRANT OPTION")
		return err
	}
	if spec.CreateDB {
		_, err = db.ExecContext(ctx, "GRANT CREATE ON *.* TO "+account)
		return err
	}
	return nil
}

func (mysqlDialect) AlterRole(ctx context.Context, db *sql.DB, spec RoleSpec) error {
	account, err := mysqlAccount(spec.Name, spec.Host)
	if err != nil {
		return err
	}
	if spec.SetPassword {
		if err := validatePassword(spec.Password); err != nil {
			return err
		}
		if _, err := db.ExecContext(ctx, "ALTER USER "+account+" IDENTIFIED BY "+passwordLiteral(DriverMySQL, spec.Password)); err != nil {
			return err
		}
	}
	if spec.ConnLimit != 0 {
		limit := spec.ConnLimit
		if limit < 0 {
			limit = 0
		}
		if _, err := db.ExecContext(ctx, "ALTER USER "+account+" WITH MAX_USER_CONNECTIONS "+itoa(limit)); err != nil {
			return err
		}
	}
	if spec.Superuser {
		_, err = db.ExecContext(ctx, "GRANT ALL PRIVILEGES ON *.* TO "+account+" WITH GRANT OPTION")
		return err
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

func (d mysqlDialect) Grant(ctx context.Context, db *sql.DB, role, host, database string, level GrantLevel) error {
	account, err := mysqlAccount(role, host)
	if err != nil {
		return err
	}
	dbName, err := d.QuoteIdent(database)
	if err != nil {
		return err
	}
	var privileges string
	switch level {
	case GrantAll:
		privileges = "ALL PRIVILEGES"
	case GrantWrite:
		privileges = "SELECT, INSERT, UPDATE, DELETE, SHOW VIEW"
	case GrantRead:
		privileges = "SELECT, SHOW VIEW"
	default:
		return fmt.Errorf("unknown grant level %q", level)
	}
	_, err = db.ExecContext(ctx, "GRANT "+privileges+" ON "+dbName+".* TO "+account)
	return err
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

func (mysqlDialect) Settings(ctx context.Context, db *sql.DB) ([]Setting, error) {
	placeholders := make([]string, len(mysqlSettingNames))
	args := make([]any, len(mysqlSettingNames))
	for i, n := range mysqlSettingNames {
		placeholders[i] = "?"
		args[i] = n
	}
	rows, err := db.QueryContext(ctx, `
	  SELECT VARIABLE_NAME, VARIABLE_VALUE FROM performance_schema.global_variables
	  WHERE VARIABLE_NAME IN (`+strings.Join(placeholders, ",")+`)`, args...)
	if err != nil {
		// MariaDB keeps the same view under information_schema.
		rows, err = db.QueryContext(ctx, `
		  SELECT VARIABLE_NAME, VARIABLE_VALUE FROM information_schema.GLOBAL_VARIABLES
		  WHERE VARIABLE_NAME IN (`+strings.Join(placeholders, ",")+`)`, args...)
		if err != nil {
			return nil, err
		}
	}
	defer rows.Close()
	byName := map[string]string{}
	for rows.Next() {
		var name, value string
		if err := rows.Scan(&name, &value); err != nil {
			return nil, err
		}
		byName[strings.ToLower(name)] = value
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := []Setting{}
	for _, n := range mysqlSettingNames {
		if v, ok := byName[n]; ok {
			out = append(out, Setting{Name: n, Value: v})
		}
	}
	return out, nil
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
	if err := validatePassword(spec.Password); err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, "CREATE USER "+name+" IDENTIFIED WITH sha256_password BY "+passwordLiteral(DriverClickHouse, spec.Password))
	if err != nil {
		return err
	}
	if spec.Superuser {
		_, err = db.ExecContext(ctx, "GRANT ALL ON *.* TO "+name+" WITH GRANT OPTION")
	}
	return err
}

func (d clickhouseDialect) AlterRole(ctx context.Context, db *sql.DB, spec RoleSpec) error {
	name, err := d.QuoteIdent(spec.Name)
	if err != nil {
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
	if spec.Superuser {
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

func (d clickhouseDialect) Grant(ctx context.Context, db *sql.DB, role, _, database string, level GrantLevel) error {
	r, err := d.QuoteIdent(role)
	if err != nil {
		return err
	}
	dbName, err := d.QuoteIdent(database)
	if err != nil {
		return err
	}
	var privileges string
	switch level {
	case GrantAll:
		privileges = "ALL"
	case GrantWrite:
		privileges = "SELECT, INSERT, ALTER DELETE, ALTER UPDATE"
	case GrantRead:
		privileges = "SELECT"
	default:
		return fmt.Errorf("unknown grant level %q", level)
	}
	_, err = db.ExecContext(ctx, "GRANT "+privileges+" ON "+dbName+".* TO "+r)
	return err
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

func (clickhouseDialect) Settings(ctx context.Context, db *sql.DB) ([]Setting, error) {
	rows, err := db.QueryContext(ctx, `
	  SELECT name, value, '', '', description, if(changed, 'changed', 'default'), 0
	  FROM system.server_settings
	  WHERE name IN ('max_connections', 'max_concurrent_queries', 'max_server_memory_usage',
	                 'path', 'tmp_path', 'listen_host', 'tcp_port', 'http_port', 'timezone',
	                 'mark_cache_size', 'uncompressed_cache_size', 'max_thread_pool_size')
	  ORDER BY name`)
	if err != nil {
		return nil, err
	}
	return scanSettings(rows)
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
		var disabled, super, creator, security int
		if err := rows.Scan(&r.Name, &typ, &disabled, &super, &creator, &security, &r.Connections); err != nil {
			return nil, err
		}
		r.Login = true
		r.Locked = disabled == 1
		r.Superuser = super == 1
		r.CreateDB = creator == 1
		r.CreateRole = security == 1
		r.ConnLimit = -1
		r.System = strings.HasPrefix(r.Name, "NT ") || r.Name == "sa"
		out = append(out, r)
	}
	return out, rows.Err()
}

func (d mssqlDialect) CreateRole(ctx context.Context, db *sql.DB, spec RoleSpec) error {
	name, err := d.QuoteIdent(spec.Name)
	if err != nil {
		return err
	}
	if err := validatePassword(spec.Password); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, "CREATE LOGIN "+name+" WITH PASSWORD = "+passwordLiteral(DriverMSSQL, spec.Password)); err != nil {
		return err
	}
	if spec.Superuser {
		_, err = db.ExecContext(ctx, "ALTER SERVER ROLE sysadmin ADD MEMBER "+name)
		return err
	}
	if spec.CreateDB {
		_, err = db.ExecContext(ctx, "ALTER SERVER ROLE dbcreator ADD MEMBER "+name)
	}
	return err
}

func (d mssqlDialect) AlterRole(ctx context.Context, db *sql.DB, spec RoleSpec) error {
	name, err := d.QuoteIdent(spec.Name)
	if err != nil {
		return err
	}
	if spec.SetPassword {
		if err := validatePassword(spec.Password); err != nil {
			return err
		}
		if _, err := db.ExecContext(ctx, "ALTER LOGIN "+name+" WITH PASSWORD = "+passwordLiteral(DriverMSSQL, spec.Password)); err != nil {
			return err
		}
	}
	if spec.Superuser {
		_, err = db.ExecContext(ctx, "ALTER SERVER ROLE sysadmin ADD MEMBER "+name)
	}
	return err
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

func (d mssqlDialect) Grant(ctx context.Context, db *sql.DB, role, _, _ string, level GrantLevel) error {
	r, err := d.QuoteIdent(role)
	if err != nil {
		return err
	}
	var dbRoles []string
	switch level {
	case GrantAll:
		dbRoles = []string{"db_owner"}
	case GrantWrite:
		dbRoles = []string{"db_datareader", "db_datawriter"}
	case GrantRead:
		dbRoles = []string{"db_datareader"}
	default:
		return fmt.Errorf("unknown grant level %q", level)
	}
	// The login needs a user in the database before it can be a member of
	// anything; one that already exists is not an error worth failing over.
	_, _ = db.ExecContext(ctx, "CREATE USER "+r+" FOR LOGIN "+r)
	for _, dbRole := range dbRoles {
		if _, err := db.ExecContext(ctx, "ALTER ROLE "+dbRole+" ADD MEMBER "+r); err != nil {
			return err
		}
	}
	return nil
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

func (mssqlDialect) Settings(ctx context.Context, db *sql.DB) ([]Setting, error) {
	rows, err := db.QueryContext(ctx, `
	  SELECT name, CAST(value_in_use AS NVARCHAR(64)), '', '', CAST(description AS NVARCHAR(256)),
	         CASE WHEN is_dynamic = 1 THEN 'dynamic' ELSE 'startup' END,
	         CASE WHEN is_dynamic = 1 THEN 0 ELSE 1 END
	  FROM sys.configurations
	  WHERE name IN ('user connections', 'max server memory (MB)', 'min server memory (MB)',
	                 'max degree of parallelism', 'cost threshold for parallelism',
	                 'remote access', 'backup compression default', 'default language')
	  ORDER BY name`)
	if err != nil {
		return nil, err
	}
	return scanSettings(rows)
}
