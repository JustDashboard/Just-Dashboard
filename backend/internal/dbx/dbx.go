// Package dbx provides database administration across Postgres, MySQL, SQLite
// and MongoDB: browsing schemas and tables, introspecting a table's full
// structure, editing rows through safe generated statements, running arbitrary
// queries, exporting data, generating ORM schemas, and dumping or restoring.
//
// Connection strings are secrets — they carry credentials — so they are held
// encrypted at rest and never returned to a client. Two rules keep the write
// paths safe: identifiers (schema, table and column names) are always validated
// and quoted, never bound, while values are always bound, never interpolated;
// and a query runner is inherently powerful, so every statement is classified
// before it runs and the handler demands the destructive capability for the
// destructive ones, while a statement classified as a read runs inside the
// engine's own read-only scope so a wrong verdict fails instead of writing.
// Row edits go one step further: they run in a transaction that is rolled back
// unless every UPDATE and DELETE touched exactly the one row intended.
package dbx

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "github.com/ClickHouse/clickhouse-go/v2"
	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "github.com/microsoft/go-mssqldb"
	_ "github.com/sijms/go-ora/v2"
	_ "modernc.org/sqlite"
)

type Driver string

const (
	DriverPostgres Driver = "postgres"
	DriverMySQL    Driver = "mysql"
	DriverMongo    Driver = "mongodb"
	// DriverSQLite treats a single database file as the connection. It uses the
	// same pure-Go driver (modernc.org/sqlite) the dashboard already stores its
	// own state in, so it adds no build dependency and needs no CGO.
	DriverSQLite     Driver = "sqlite"
	DriverMSSQL      Driver = "sqlserver"
	DriverClickHouse Driver = "clickhouse"
	DriverOracle     Driver = "oracle"
	// DriverRedis is the one key/value engine. It has no tables, no SQL and no
	// rows, so it shares none of the query path below and is driven entirely
	// through redis.go.
	DriverRedis Driver = "redis"
)

var ErrUnsupported = errors.New("unsupported database driver")

// Drivers lists every engine the dashboard can connect to, in the order the UI
// offers them. Deriving the list here rather than repeating it in the handler
// and again in the frontend is what stops a newly registered dialect being
// invisible in the connection form.
func Drivers() []Driver {
	return []Driver{
		DriverPostgres, DriverMySQL, DriverSQLite, DriverMSSQL,
		DriverClickHouse, DriverOracle, DriverMongo, DriverRedis,
	}
}

func (d Driver) Valid() bool {
	if d == DriverMongo || d == DriverRedis {
		return true
	}
	_, ok := dialects[d]
	return ok
}

// IsSQL reports whether the driver goes through database/sql and the dialect
// layer. Mongo and Redis are the two that do not, so callers branch on this
// rather than listing engines — a list that was wrong the moment a seventh
// engine arrived.
func (d Driver) IsSQL() bool {
	_, ok := dialects[d]
	return ok
}

// itoa is strconv.Itoa without the import, used by the placeholder renderers on
// a hot enough path that the dialects call it constantly.
func itoa(n int) string { return strconv.Itoa(n) }

// underscoreToWords turns SQL Server's NO_ACTION into NO ACTION, so referential
// actions read the same whichever engine reported them.
func underscoreToWords(s string) string { return strings.ReplaceAll(s, "_", " ") }

// Manager owns the connection pools. Pools are opened lazily and reused: a
// query runner that dialled a fresh connection per request would exhaust the
// server's connection limit under any real use.
type Manager struct {
	mu      sync.Mutex
	pools   map[int64]*sql.DB
	opening map[int64]*poolOpening
}

type poolOpening struct {
	done        chan struct{}
	cancel      context.CancelFunc
	db          *sql.DB
	err         error
	invalidated bool
}

func NewManager() *Manager {
	return &Manager{pools: map[int64]*sql.DB{}, opening: map[int64]*poolOpening{}}
}

func (m *Manager) Pool(ctx context.Context, id int64, driver Driver, dsn string) (*sql.DB, error) {
	m.mu.Lock()
	if db, ok := m.pools[id]; ok {
		m.mu.Unlock()
		return db, nil
	}
	if pending, ok := m.opening[id]; ok {
		m.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-pending.done:
			if !pending.invalidated && ctx.Err() == nil &&
				(errors.Is(pending.err, context.Canceled) || errors.Is(pending.err, context.DeadlineExceeded)) {
				return m.Pool(ctx, id, driver, dsn)
			}
			return pending.db, pending.err
		}
	}
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	pending := &poolOpening{done: make(chan struct{}), cancel: cancel}
	m.opening[id] = pending
	m.mu.Unlock()

	// Dial only this connection outside the map lock. A refused or slow
	// handshake must not queue requests for unrelated, already healthy pools.
	db, err := openPool(pingCtx, driver, dsn)
	cancel()
	m.mu.Lock()
	if m.opening[id] != pending {
		// Close (including a saved-connection edit) invalidated this attempt.
		// Never publish a pool for the old credentials after that boundary.
		err = context.Canceled
	} else {
		delete(m.opening, id)
		if err == nil {
			m.pools[id] = db
		}
	}
	if err == nil {
		pending.db = db
	}
	pending.err = err
	close(pending.done)
	m.mu.Unlock()
	if err != nil && db != nil {
		_ = db.Close()
	}
	return pending.db, err
}

func openPool(ctx context.Context, driver Driver, dsn string) (*sql.DB, error) {
	d, err := DialectFor(driver)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open(d.SQLDriverName(), d.NormaliseDSN(dsn))
	if err != nil {
		return nil, err
	}
	// A dashboard is not the application: a handful of connections is plenty,
	// and a large pool here would compete with the real workload.
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(30 * time.Minute)
	// And a much shorter idle life on top of it. A dashboard's pool sits unused
	// between page loads, which is exactly when the server on the other end
	// gets restarted; a connection idle for longer than this is cheaper to
	// re-dial than to discover is dead in the middle of somebody's query.
	db.SetConnMaxIdleTime(2 * time.Minute)
	// Then whatever the engine needs on top — SQLite's single writer, say.
	d.TunePool(db)

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// Probe opens a throwaway connection to verify a DSN before it is saved, and
// returns the server version so the operator gets confirmation they reached the
// engine they meant to. It never touches the manager's pool cache: a connection
// being tested may be wrong, and a failed test must not leave a broken pool
// cached under some id.
func Probe(ctx context.Context, driver Driver, dsn string) (string, error) {
	d, err := DialectFor(driver)
	if err != nil {
		return "", err
	}
	db, err := sql.Open(d.SQLDriverName(), d.NormaliseDSN(dsn))
	if err != nil {
		return "", err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		return "", err
	}
	// A version string is a courtesy, not the test — the ping already proved the
	// connection. An engine that refuses the query still reports as reachable.
	var version string
	_ = db.QueryRowContext(pingCtx, d.VersionQuery()).Scan(&version)
	return strings.TrimSpace(version), nil
}

func (m *Manager) Close(id int64) {
	m.mu.Lock()
	db := m.pools[id]
	delete(m.pools, id)
	if pending := m.opening[id]; pending != nil {
		delete(m.opening, id)
		pending.invalidated = true
		pending.cancel()
	}
	m.mu.Unlock()
	if db != nil {
		_ = db.Close()
	}
}

func (m *Manager) Shutdown() {
	m.mu.Lock()
	pools := m.pools
	m.pools = map[int64]*sql.DB{}
	for id, pending := range m.opening {
		delete(m.opening, id)
		pending.invalidated = true
		pending.cancel()
	}
	m.mu.Unlock()
	for _, db := range pools {
		_ = db.Close()
	}
}

type PoolStats struct {
	Open            int    `json:"open"`
	InUse           int    `json:"inUse"`
	Idle            int    `json:"idle"`
	WaitCount       int64  `json:"waitCount"`
	WaitDuration    string `json:"waitDuration"`
	MaxOpen         int    `json:"maxOpen"`
	MaxIdleClosed   int64  `json:"maxIdleClosed"`
	MaxLifetimeGone int64  `json:"maxLifetimeClosed"`
}

func (m *Manager) Stats(id int64) *PoolStats {
	m.mu.Lock()
	db, ok := m.pools[id]
	m.mu.Unlock()
	if !ok {
		return nil
	}
	s := db.Stats()
	return &PoolStats{
		Open: s.OpenConnections, InUse: s.InUse, Idle: s.Idle,
		WaitCount: s.WaitCount, WaitDuration: s.WaitDuration.String(),
		MaxOpen: s.MaxOpenConnections, MaxIdleClosed: s.MaxIdleClosed,
		MaxLifetimeGone: s.MaxLifetimeClosed,
	}
}

type Database struct {
	Name     string `json:"name"`
	Size     int64  `json:"size,omitempty"`
	Owner    string `json:"owner,omitempty"`
	Encoding string `json:"encoding,omitempty"`
}

func ListDatabases(ctx context.Context, db *sql.DB, driver Driver) ([]Database, error) {
	d, err := DialectFor(driver)
	if err != nil {
		return nil, err
	}
	return d.Databases(ctx, db)
}

type Table struct {
	Schema string `json:"schema"`
	Name   string `json:"name"`
	Type   string `json:"type"`
	// The planner's estimate, and -1 where the engine has none: a table the
	// planner has never analysed, a view, an engine that does not count. It
	// used to be floored to 0, and a catalogue that cannot say how many rows a
	// table has was indistinguishable from one saying the table is empty.
	Rows    int64  `json:"estimatedRows"`
	Size    int64  `json:"size,omitempty"`
	Comment string `json:"comment,omitempty"`
}

func ListTables(ctx context.Context, db *sql.DB, driver Driver, schema string) ([]Table, error) {
	d, err := DialectFor(driver)
	if err != nil {
		return nil, err
	}
	return d.Tables(ctx, db, schema)
}

type Column struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Nullable bool   `json:"nullable"`
	Default  string `json:"default,omitempty"`
	Key      string `json:"key,omitempty"`
	Position int    `json:"position"`
}

func ListColumns(ctx context.Context, db *sql.DB, driver Driver, schema, table string) ([]Column, error) {
	d, err := DialectFor(driver)
	if err != nil {
		return nil, err
	}
	return d.Columns(ctx, db, schema, table)
}

// identifierRe is the strict form, used where a name becomes a path segment or
// a shell argument rather than a quoted SQL identifier — a database name headed
// for a dump filename, say. SQL identifiers use validateIdent plus the
// dialect's own quoting, which is both safer and permissive enough for the
// hyphens and non-ASCII letters real schemas contain.
var identifierRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_$]{0,62}$`)

// qualify renders a validated, quoted schema.table reference for a dialect.
// An empty schema — the normal case for SQLite, and for an engine where the
// operator has not narrowed it — yields a bare table name rather than a
// dangling dot.
func qualify(d Dialect, schema, table string) (string, error) {
	qTable, err := d.QuoteIdent(table)
	if err != nil {
		return "", err
	}
	if schema == "" || d.Driver() == DriverSQLite {
		return qTable, nil
	}
	qSchema, err := d.QuoteIdent(schema)
	if err != nil {
		return "", err
	}
	return qSchema + "." + qTable, nil
}

type QueryResult struct {
	Columns []string `json:"columns"`
	Types   []string `json:"types"`
	// Kinds says what each column holds in the dashboard's own vocabulary
	// (ValueKind), so the grid does not need a table of every engine's type
	// names to know a number from a date.
	Kinds     []string `json:"kinds,omitempty"`
	Rows      [][]any  `json:"rows"`
	RowCount  int      `json:"rowCount"`
	Affected  int64    `json:"rowsAffected"`
	Duration  string   `json:"duration"`
	Truncated bool     `json:"truncated"`
	Statement string   `json:"statement"`
	// Clipped lists the cells that hold a preview rather than the value: a
	// binary value past the hex preview, or text past the page's cell limit.
	// A preview must never be written back as if it were the value.
	Clipped []ClippedCell `json:"clipped,omitempty"`
}

// ClippedCell names one cell whose value was cut for display, and how large
// the whole value is in bytes.
type ClippedCell struct {
	Row    int   `json:"row"`
	Column int   `json:"column"`
	Size   int64 `json:"size"`
}
