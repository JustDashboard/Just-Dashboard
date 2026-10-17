package dbx

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// A connection pooler for a PostgreSQL server: PgBouncer, run by the dashboard
// beside the database it fronts.
//
// This file is the part that needs no Docker and no server: the settings a
// pooler is given and what is held to them, the two files PgBouncer reads
// (pgbouncer.ini and userlist.txt), the statements that let it look an
// account's password up in the database, and the reading of its admin
// console. The container, its network and its lifecycle are the API's.

const (
	// PoolerImage is pinned by version: a pooler that changes under the
	// operator when a tag moves is a surprise in the one process every
	// application connection goes through. The image's own entrypoint is
	// bypassed (it writes a configuration from environment variables, which
	// would put the password where `docker inspect` shows it), so only the
	// binary and its user are relied on.
	PoolerImage = "edoburu/pgbouncer:v1.25.2-p0"
	// PoolerListenPort is where PgBouncer listens inside its container. The
	// host port that publishes it is chosen at creation.
	PoolerListenPort = 6432

	PoolModeTransaction = "transaction"
	PoolModeSession     = "session"

	// PoolerAuthUserlist authenticates clients from a file the dashboard
	// writes from the connection's own account; PoolerAuthQuery lets PgBouncer
	// ask the database for the password of whichever account connects.
	PoolerAuthUserlist = "userlist"
	PoolerAuthQuery    = "auth_query"

	// PoolerAuthRole and PoolerAuthSchema are what auth_query mode leaves in
	// the database: one account that can call one function. PoolerStatsUser is
	// PgBouncer's own console account, which reads and cannot change anything.
	PoolerAuthRole   = "jd_pgbouncer"
	PoolerAuthSchema = "jd_pgbouncer"
	PoolerStatsUser  = "jd_pooler_stats"

	// PoolerConfigPath and PoolerUserlistPath are where the two files live in
	// the container.
	PoolerConfigPath   = "/etc/pgbouncer/pgbouncer.ini"
	PoolerUserlistPath = "/etc/pgbouncer/userlist.txt"
)

// Bounds on what an operator may ask for. A pool larger than a Postgres
// server's max_connections only queues the difference, so these are limits on
// sanity and not on what the server will take.
const (
	PoolerMaxPoolSize      = 500
	PoolerMaxClientConn    = 10000
	poolerDefaultPoolSize  = 20
	poolerDefaultMaxClient = 200
)

// PoolerSettings is everything about a pooler an operator chooses.
type PoolerSettings struct {
	// PoolMode is how long a client holds a server connection: for one
	// transaction, or for the whole session.
	PoolMode string `json:"poolMode"`
	// PoolSize is the server connections kept for each database and account
	// pair (PgBouncer's default_pool_size).
	PoolSize int `json:"poolSize"`
	// MaxClientConn is how many clients may be connected to the pooler at once.
	MaxClientConn int    `json:"maxClientConn"`
	AuthMode      string `json:"authMode"`
}

// DefaultPoolerSettings are what the form starts from: transaction pooling,
// the mode the pooler exists for, with the connection's own account in a
// userlist, which asks nothing of the database.
func DefaultPoolerSettings() PoolerSettings {
	return PoolerSettings{
		PoolMode: PoolModeTransaction, PoolSize: poolerDefaultPoolSize,
		MaxClientConn: poolerDefaultMaxClient, AuthMode: PoolerAuthUserlist,
	}
}

// Validate says what is wrong with the settings, in a sentence an operator can
// act on.
func (s PoolerSettings) Validate() error {
	switch s.PoolMode {
	case PoolModeTransaction, PoolModeSession:
	default:
		return fmt.Errorf("pool mode must be transaction or session")
	}
	if s.PoolSize < 1 || s.PoolSize > PoolerMaxPoolSize {
		return fmt.Errorf("pool size must be between 1 and %d", PoolerMaxPoolSize)
	}
	if s.MaxClientConn < 1 || s.MaxClientConn > PoolerMaxClientConn {
		return fmt.Errorf("max client connections must be between 1 and %d", PoolerMaxClientConn)
	}
	switch s.AuthMode {
	case PoolerAuthUserlist, PoolerAuthQuery:
	default:
		return fmt.Errorf("authentication must be userlist or auth_query")
	}
	return nil
}

// PoolerConfig is what the two files are written from.
type PoolerConfig struct {
	Settings PoolerSettings
	// Server is the name the pooler reaches the database by on their shared
	// network, and ServerPort the port the engine listens on inside it.
	Server     string
	ServerPort int
	// Database is the one auth_query looks passwords up in.
	Database string
	// User and Password are the connection's account, for the userlist.
	User, Password string
	// AuthPassword is the password of PoolerAuthRole, and StatsPassword of
	// PoolerStatsUser.
	AuthPassword, StatsPassword string
}

var (
	poolerServerRE   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
	poolerDatabaseRE = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.$-]{0,62}$`)
)

// INI is pgbouncer.ini. It carries no password: those are in the userlist, and
// the file is the one an operator is most likely to be shown.
func (c PoolerConfig) INI() (string, error) {
	if err := c.Settings.Validate(); err != nil {
		return "", err
	}
	if !poolerServerRE.MatchString(c.Server) {
		return "", fmt.Errorf("%q is not a name the pooler can reach the database by", c.Server)
	}
	if c.ServerPort < 1 || c.ServerPort > 65535 {
		return "", fmt.Errorf("the database's port is unknown")
	}
	var b strings.Builder
	line := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }
	line("; Written by Just Dashboard. Change these settings in the dashboard: this file is replaced.")
	line("[databases]")
	line("* = host=%s port=%d", c.Server, c.ServerPort)
	line("")
	line("[pgbouncer]")
	// The container's own interface: what reaches it from outside is the
	// published binding and the network it shares with the database.
	line("listen_addr = *")
	line("listen_port = %d", PoolerListenPort)
	line("unix_socket_dir =")
	line("auth_file = %s", PoolerUserlistPath)
	if c.Settings.AuthMode == PoolerAuthQuery {
		if !poolerDatabaseRE.MatchString(c.Database) {
			return "", fmt.Errorf("auth_query needs a database name it can look passwords up in")
		}
		// A client with an MD5 verifier on the server is asked for MD5 and one
		// with a SCRAM secret for SCRAM, which a single method cannot do.
		line("auth_type = md5")
		line("auth_user = %s", PoolerAuthRole)
		line("auth_dbname = %s", c.Database)
		line("auth_query = SELECT usename, passwd FROM %s.get_auth($1)", PoolerAuthSchema)
	} else {
		line("auth_type = scram-sha-256")
	}
	line("pool_mode = %s", c.Settings.PoolMode)
	line("default_pool_size = %d", c.Settings.PoolSize)
	line("max_client_conn = %d", c.Settings.MaxClientConn)
	// Transaction pooling breaks protocol-level prepared statements unless
	// PgBouncer tracks them, which most drivers use without being asked.
	line("max_prepared_statements = 100")
	// Sent by JDBC at startup, and refused by PgBouncer as an unsupported
	// startup parameter otherwise.
	line("ignore_startup_parameters = extra_float_digits")
	line("stats_users = %s", PoolerStatsUser)
	// Every connection of a busy application would otherwise be a line in a
	// log nobody reads.
	line("log_connections = 0")
	line("log_disconnections = 0")
	return b.String(), nil
}

// Userlist is userlist.txt: the accounts PgBouncer authenticates itself.
//
// Passwords are written as they are. PgBouncer needs the plain password to log
// in to the database as the account (a verifier would only let it check the
// client), and the file is as readable as the container's environment already
// is to whoever reaches the Docker socket.
func (c PoolerConfig) Userlist() (string, error) {
	entries := [][2]string{}
	if c.Settings.AuthMode == PoolerAuthQuery {
		entries = append(entries, [2]string{PoolerAuthRole, c.AuthPassword})
	} else {
		if c.User == "" || c.Password == "" {
			return "", fmt.Errorf("the saved connection has no account and password to put in the userlist")
		}
		entries = append(entries, [2]string{c.User, c.Password})
	}
	entries = append(entries, [2]string{PoolerStatsUser, c.StatsPassword})
	var b strings.Builder
	for _, entry := range entries {
		for _, part := range entry {
			if part == "" || strings.ContainsAny(part, "\x00\r\n") {
				return "", fmt.Errorf("an account name or password cannot be written to the userlist")
			}
		}
		fmt.Fprintf(&b, "\"%s\" \"%s\"\n", strings.ReplaceAll(entry[0], `"`, `""`), strings.ReplaceAll(entry[1], `"`, `""`))
	}
	return b.String(), nil
}

// PoolerAuthStatements is what auth_query mode leaves in the database, in the
// order it is run (the names are constants of this package, so none is quoted): an account that can log in and nothing else, and a
// function, owned by the connection's own (super)user, that returns one
// account's stored password hash to that account alone.
//
// The function exists because PgBouncer cannot be allowed to read pg_shadow
// itself without being a superuser, and a superuser the pooler signs in as is
// exactly what this avoids. exists says whether the login role is already
// there, which decides between creating and altering it.
func PoolerAuthStatements(exists bool, password string) []string {
	verb := "CREATE"
	if exists {
		verb = "ALTER"
	}
	role, schema := PoolerAuthRole, PoolerAuthSchema
	fn := schema + ".get_auth(text)"
	return []string{
		verb + " ROLE " + role + " WITH LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION PASSWORD " + passwordLiteral(DriverPostgres, password),
		"CREATE SCHEMA IF NOT EXISTS " + schema,
		"CREATE OR REPLACE FUNCTION " + schema + ".get_auth(p_usename text) RETURNS TABLE(usename name, passwd text) " +
			"LANGUAGE sql SECURITY DEFINER SET search_path = pg_catalog AS " +
			"$$ SELECT s.usename, s.passwd::text FROM pg_catalog.pg_shadow s WHERE s.usename = p_usename $$",
		"REVOKE ALL ON FUNCTION " + fn + " FROM PUBLIC",
		"GRANT USAGE ON SCHEMA " + schema + " TO " + role,
		"GRANT EXECUTE ON FUNCTION " + fn + " TO " + role,
	}
}

// PoolerAuthRemoval takes auth_query mode's objects out again.
func PoolerAuthRemoval() []string {
	return []string{
		"DROP FUNCTION IF EXISTS " + PoolerAuthSchema + ".get_auth(text)",
		"DROP SCHEMA IF EXISTS " + PoolerAuthSchema,
		"DROP ROLE IF EXISTS " + PoolerAuthRole,
	}
}

// --- the admin console --------------------------------------------------------

// PoolerPool is one row of SHOW POOLS: the clients and server connections of
// one database and account.
type PoolerPool struct {
	Database   string `json:"database"`
	User       string `json:"user"`
	PoolMode   string `json:"poolMode"`
	ClActive   int64  `json:"clActive"`
	ClWaiting  int64  `json:"clWaiting"`
	SvActive   int64  `json:"svActive"`
	SvIdle     int64  `json:"svIdle"`
	SvUsed     int64  `json:"svUsed"`
	SvTested   int64  `json:"svTested"`
	SvLogin    int64  `json:"svLogin"`
	MaxWaitSec int64  `json:"maxWaitSec"`
}

// PoolerStat is one row of SHOW STATS: what a database has been asked since
// PgBouncer started (the totals) and over the last statistics period (the
// averages). Times are microseconds and sizes bytes, as PgBouncer states them.
type PoolerStat struct {
	Database       string `json:"database"`
	TotalXactCount int64  `json:"totalXactCount"`
	TotalQueryCnt  int64  `json:"totalQueryCount"`
	TotalReceived  int64  `json:"totalReceived"`
	TotalSent      int64  `json:"totalSent"`
	TotalXactTime  int64  `json:"totalXactTime"`
	TotalQueryTime int64  `json:"totalQueryTime"`
	TotalWaitTime  int64  `json:"totalWaitTime"`
	AvgXactCount   int64  `json:"avgXactCount"`
	AvgQueryCount  int64  `json:"avgQueryCount"`
	AvgRecv        int64  `json:"avgRecv"`
	AvgSent        int64  `json:"avgSent"`
	AvgXactTime    int64  `json:"avgXactTime"`
	AvgQueryTime   int64  `json:"avgQueryTime"`
	AvgWaitTime    int64  `json:"avgWaitTime"`
}

// PoolerReading is one look at the admin console.
type PoolerReading struct {
	Version string       `json:"version"`
	Pools   []PoolerPool `json:"pools"`
	Stats   []PoolerStat `json:"stats"`
}

// poolerAdminDatabase is PgBouncer's pseudo-database for its console.
const poolerAdminDatabase = "pgbouncer"

// PoolerAdminDSN is how the console is reached: on the host port the pooler is
// published on, as its read-only account. The console speaks the simple query
// protocol only.
func PoolerAdminDSN(host string, port int, password string) string {
	u := url.URL{
		Scheme: "postgres", User: url.UserPassword(PoolerStatsUser, password),
		Host: host + ":" + strconv.Itoa(port), Path: "/" + poolerAdminDatabase,
		RawQuery: url.Values{
			"sslmode": {"disable"}, "default_query_exec_mode": {"simple_protocol"},
			"application_name": {ApplicationName},
		}.Encode(),
	}
	return u.String()
}

// ReadPooler asks the console for the pools, the statistics and the version.
// A connection is opened for it and closed again: the figures are read when a
// page is open, which is not often enough to keep one.
func ReadPooler(ctx context.Context, dsn string) (*PoolerReading, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	version, err := showPooler(ctx, db, "SHOW VERSION")
	if err != nil {
		return nil, err
	}
	pools, err := showPooler(ctx, db, "SHOW POOLS")
	if err != nil {
		return nil, err
	}
	stats, err := showPooler(ctx, db, "SHOW STATS")
	if err != nil {
		return nil, err
	}
	out := &PoolerReading{Pools: []PoolerPool{}, Stats: []PoolerStat{}}
	if len(version) > 0 {
		out.Version = poolerVersion(version[0]["version"])
	}
	for _, row := range pools {
		if row["database"] != poolerAdminDatabase {
			out.Pools = append(out.Pools, poolerPoolOf(row))
		}
	}
	for _, row := range stats {
		if row["database"] != poolerAdminDatabase {
			out.Stats = append(out.Stats, poolerStatOf(row))
		}
	}
	return out, nil
}

// showPooler runs one SHOW command and returns its rows by column name. The
// columns differ between PgBouncer releases, so they are read by name and a
// missing one reads as empty.
func showPooler(ctx context.Context, db *sql.DB, command string) ([]map[string]string, error) {
	rows, err := db.QueryContext(ctx, command)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var out []map[string]string
	for rows.Next() {
		values := make([]sql.NullString, len(columns))
		targets := make([]any, len(columns))
		for i := range values {
			targets[i] = &values[i]
		}
		if err := rows.Scan(targets...); err != nil {
			return nil, err
		}
		row := make(map[string]string, len(columns))
		for i, name := range columns {
			row[name] = values[i].String
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// poolerVersion is the number in "PgBouncer 1.25.2".
func poolerVersion(text string) string {
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(text), "PgBouncer"))
}

func poolerInt(row map[string]string, name string) int64 {
	n, _ := strconv.ParseInt(strings.TrimSpace(row[name]), 10, 64)
	return n
}

func poolerPoolOf(row map[string]string) PoolerPool {
	return PoolerPool{
		Database: row["database"], User: row["user"], PoolMode: row["pool_mode"],
		ClActive: poolerInt(row, "cl_active"), ClWaiting: poolerInt(row, "cl_waiting"),
		SvActive: poolerInt(row, "sv_active"), SvIdle: poolerInt(row, "sv_idle"),
		SvUsed: poolerInt(row, "sv_used"), SvTested: poolerInt(row, "sv_tested"),
		SvLogin: poolerInt(row, "sv_login"), MaxWaitSec: poolerInt(row, "maxwait"),
	}
}

func poolerStatOf(row map[string]string) PoolerStat {
	return PoolerStat{
		Database:       row["database"],
		TotalXactCount: poolerInt(row, "total_xact_count"), TotalQueryCnt: poolerInt(row, "total_query_count"),
		TotalReceived: poolerInt(row, "total_received"), TotalSent: poolerInt(row, "total_sent"),
		TotalXactTime: poolerInt(row, "total_xact_time"), TotalQueryTime: poolerInt(row, "total_query_time"),
		TotalWaitTime: poolerInt(row, "total_wait_time"),
		AvgXactCount:  poolerInt(row, "avg_xact_count"), AvgQueryCount: poolerInt(row, "avg_query_count"),
		AvgRecv: poolerInt(row, "avg_recv"), AvgSent: poolerInt(row, "avg_sent"),
		AvgXactTime: poolerInt(row, "avg_xact_time"), AvgQueryTime: poolerInt(row, "avg_query_time"),
		AvgWaitTime: poolerInt(row, "avg_wait_time"),
	}
}
