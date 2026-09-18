// Package dbx provides database administration across Postgres, MySQL, SQLite
// and MongoDB: browsing schemas and tables, introspecting a table's full
// structure, editing rows through safe generated statements, running arbitrary
// queries, exporting data, generating ORM schemas, and dumping or restoring.
//
// Connection strings are secrets — they carry credentials — so they are held
// encrypted at rest and never returned to a client. Two rules keep the write
// paths safe: identifiers (schema, table and column names) are always validated
// and quoted, never bound, while values are always bound, never interpolated;
// and a query runner is inherently powerful, so destructive statements are
// classified before they run and the handler demands the destructive capability
// for them, plus a typed confirmation once a statement is critical. Row edits go
// one step further and refuse to run without a primary key,
// so an UPDATE or DELETE cannot silently touch more than the one row intended.
package dbx

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"math"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

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
	mu    sync.Mutex
	pools map[int64]*sql.DB
}

func NewManager() *Manager {
	return &Manager{pools: map[int64]*sql.DB{}}
}

func (m *Manager) Pool(ctx context.Context, id int64, driver Driver, dsn string) (*sql.DB, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if db, ok := m.pools[id]; ok {
		return db, nil
	}
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

	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		db.Close()
		return nil, err
	}
	m.pools[id] = db
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
	defer m.mu.Unlock()
	if db, ok := m.pools[id]; ok {
		db.Close()
		delete(m.pools, id)
	}
}

func (m *Manager) Shutdown() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, db := range m.pools {
		db.Close()
		delete(m.pools, id)
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
	Schema  string `json:"schema"`
	Name    string `json:"name"`
	Type    string `json:"type"`
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
	Columns   []string `json:"columns"`
	Types     []string `json:"types"`
	Rows      [][]any  `json:"rows"`
	RowCount  int      `json:"rowCount"`
	Affected  int64    `json:"rowsAffected"`
	Duration  string   `json:"duration"`
	Truncated bool     `json:"truncated"`
	Statement string   `json:"statement"`
}

// RunQuery executes a statement and materialises the result set. Rows beyond
// maxRows are dropped and flagged rather than streamed: a browser table is not
// where a million-row result belongs.
func RunQuery(ctx context.Context, db *sql.DB, query string, maxRows int, args ...any) (*QueryResult, error) {
	if maxRows <= 0 || maxRows > 5000 {
		maxRows = 500
	}
	args, err := sqlArguments(args)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	res := &QueryResult{Columns: []string{}, Types: []string{}, Rows: [][]any{}, Statement: query}

	if !returnsRows(query) {
		exec, err := db.ExecContext(ctx, query, args...)
		if err != nil {
			return nil, err
		}
		res.Affected, _ = exec.RowsAffected()
		res.Duration = time.Since(start).Round(time.Microsecond).String()
		return res, nil
	}

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	res, err = collectRows(rows, maxRows, query)
	if err != nil {
		return nil, err
	}
	res.Duration = time.Since(start).Round(time.Microsecond).String()
	return res, nil
}

// collectRows materialises a result set. It is separate from RunQuery because
// the plan commands on SQL Server and Oracle have to run on a connection they
// hold themselves, and would otherwise duplicate this loop.
func collectRows(rows *sql.Rows, maxRows int, statement string) (*QueryResult, error) {
	res := &QueryResult{Columns: []string{}, Types: []string{}, Rows: [][]any{}, Statement: statement}
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	res.Columns = cols
	if types, err := rows.ColumnTypes(); err == nil {
		for _, t := range types {
			res.Types = append(res.Types, t.DatabaseTypeName())
		}
	}
	for rows.Next() {
		if len(res.Rows) >= maxRows {
			res.Truncated = true
			break
		}
		holders := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range holders {
			ptrs[i] = &holders[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make([]any, len(cols))
		for i, v := range holders {
			row[i] = normaliseValue(v)
		}
		res.Rows = append(res.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	res.RowCount = len(res.Rows)
	return res, nil
}

// normaliseValue converts driver values into something JSON can carry.
//
// []byte is the interesting one, and it arrives for two completely different
// reasons. MySQL hands back ordinary text columns as bytes, so encoding every
// []byte would turn most of a MySQL database into base64; but a bytea, a BLOB
// or a varbinary really is binary, and `string(t)` on it is both unreadable
// and *lossy* — invalid UTF-8 becomes U+FFFD, which put mojibake and raw
// control characters into the grid, into CSV and JSON exports, and into the
// INSERT statement the row menu copies to the clipboard, where the bytes that
// came back were no longer the bytes that went in.
//
// So the decision is made on the content: bytes that are valid UTF-8 text are
// the text they are, and anything else becomes the hex form every one of these
// engines also accepts and prints. Long values are cut off rather than turning
// a megabyte blob into two megabytes of hex in a row nobody can read anyway.
func normaliseValue(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case []byte:
		if isPrintableText(t) {
			return string(t)
		}
		return hexPreview(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case uint64:
		return strconv.FormatUint(t, 10)
	case int:
		return strconv.FormatInt(int64(t), 10)
	case uint:
		return strconv.FormatUint(uint64(t), 10)
	case float64:
		if math.IsNaN(t) || math.IsInf(t, 0) {
			return strconv.FormatFloat(t, 'g', -1, 64)
		}
		return t
	case float32:
		if math.IsNaN(float64(t)) || math.IsInf(float64(t), 0) {
			return strconv.FormatFloat(float64(t), 'g', -1, 32)
		}
		return t
	case time.Time:
		return t.UTC().Format(time.RFC3339Nano)
	default:
		return normaliseNumericContainer(v)
	}
}

// maxHexPreview bounds the rendered form of a binary value. A row is meant to
// be looked at; an operator who needs the whole blob wants the export, not a
// cell.
const maxHexPreview = 256

func isPrintableText(b []byte) bool {
	if !utf8.Valid(b) {
		return false
	}
	for _, r := range string(b) {
		// Tab, newline and carriage return are ordinary in a text column. The
		// rest of C0, and the NUL in particular, mean this is not text.
		if r == '\t' || r == '\n' || r == '\r' {
			continue
		}
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func hexPreview(b []byte) string {
	if len(b) <= maxHexPreview {
		return "\\x" + hex.EncodeToString(b)
	}
	return "\\x" + hex.EncodeToString(b[:maxHexPreview]) +
		"… (" + itoa(len(b)) + " bytes)"
}

func returnsRows(query string) bool {
	trimmed := strings.ToLower(normaliseSQL(query))
	for _, prefix := range []string{"select", "with", "show", "describe", "desc", "explain", "table", "values", "pragma"} {
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}
	// A statement with RETURNING produces rows even though it mutates.
	return strings.Contains(trimmed, " returning ")
}

// normaliseSQL strips comments and collapses whitespace so that a statement
// cannot hide its verb between the words the patterns below look for. The
// classification is what decides whether the caller needs the destructive
// capability, so a gap here is an authorisation gap: "DELETE/**/FROM users"
// used to classify as a read and ran with no capability check and no typed
// confirmation.
//
// String literals are copied through verbatim rather than removed. Removing
// them would let `SELECT 'x--'` swallow the statement that follows it, and
// keeping them can only over-report — a SELECT whose text contains the word
// "delete" costs the operator one extra confirmation, which is the direction
// this function is allowed to be wrong in.
func normaliseSQL(q string) string {
	var b strings.Builder
	b.Grow(len(q))
	for i := 0; i < len(q); i++ {
		c := q[i]
		switch {
		case c == '\'' || c == '"' || c == '`':
			quote := c
			b.WriteByte(c)
			for i++; i < len(q); i++ {
				b.WriteByte(q[i])
				if q[i] == '\\' && quote != '`' && i+1 < len(q) {
					i++
					b.WriteByte(q[i])
					continue
				}
				if q[i] == quote {
					break
				}
			}
		case c == '#', c == '-' && i+1 < len(q) && q[i+1] == '-':
			for i < len(q) && q[i] != '\n' {
				i++
			}
			b.WriteByte(' ')
		case c == '/' && i+1 < len(q) && q[i+1] == '*':
			i += 2
			for i+1 < len(q) && !(q[i] == '*' && q[i+1] == '/') {
				i++
			}
			i++
			b.WriteByte(' ')
		default:
			b.WriteByte(c)
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// Risk classifies a statement so the UI can warn before it runs.
type Risk struct {
	Destructive bool     `json:"destructive"`
	Level       string   `json:"level"`
	Reasons     []string `json:"reasons"`
}

// Go's regexp package is RE2, which has no negative lookahead, so "mutates
// every row" is expressed as a positive match plus an absence check rather
// than one pattern.
var (
	deleteRe    = regexp.MustCompile(`(?is)\bdelete\b`)
	updateSetRe = regexp.MustCompile(`(?is)\bupdate\s+\S+\s+set\b`)
	whereRe     = regexp.MustCompile(`(?is)\bwhere\b`)
)

func matches(re *regexp.Regexp) func(string) bool {
	return re.MatchString
}

// unscoped reports a statement that mutates without a WHERE clause — the
// difference between "deletes rows" and "empties the table".
func unscoped(re *regexp.Regexp) func(string) bool {
	return func(q string) bool { return re.MatchString(q) && !whereRe.MatchString(q) }
}

var riskPatterns = []struct {
	match  func(string) bool
	level  string
	reason string
}{
	// Deliberately "any DROP" rather than a list of object types: the list
	// omitted ROLE, OWNED, FUNCTION and everything a future dialect adds, and
	// each omission was a statement that ran without confirmation.
	{matches(regexp.MustCompile(`(?is)\bdrop\b`)), "critical", "drops a database object"},
	{matches(regexp.MustCompile(`(?is)\btruncate\b`)), "critical", "truncates a table"},
	{matches(regexp.MustCompile(`(?is)\bcopy\b.*\bfrom\s+program\b`)), "critical", "runs a shell command on the database host"},
	{unscoped(deleteRe), "critical", "deletes every row (no WHERE clause)"},
	{unscoped(updateSetRe), "critical", "updates every row (no WHERE clause)"},
	{matches(deleteRe), "high", "deletes rows"},
	{matches(regexp.MustCompile(`(?is)\bupdate\b`)), "high", "updates rows"},
	{matches(regexp.MustCompile(`(?is)\balter\b`)), "high", "alters a database object"},
	{matches(regexp.MustCompile(`(?is)\bgrant\b|\brevoke\b`)), "high", "changes permissions"},
	{matches(regexp.MustCompile(`(?is)\binsert\s+into\b|\breplace\s+into\b`)), "medium", "inserts rows"},
	{matches(regexp.MustCompile(`(?is)\bcreate\b`)), "medium", "creates a database object"},
}

// readOnlyLeaders are the statement forms that cannot change anything. PRAGMA
// is absent on purpose — `PRAGMA journal_mode=WAL` writes.
var readOnlyLeaders = map[string]bool{
	"select": true, "with": true, "show": true, "describe": true,
	"desc": true, "explain": true, "table": true, "values": true,
}

func Classify(query string) Risk {
	statements, err := sqlStatements(query)
	if err != nil {
		return Risk{Level: "high", Destructive: true, Reasons: []string{err.Error()}}
	}
	result := Risk{Level: "read", Reasons: []string{}}
	for _, statement := range statements {
		risk := classifyStatement(statement)
		if rank(risk.Level) > rank(result.Level) {
			result.Level = risk.Level
		}
		result.Reasons = append(result.Reasons, risk.Reasons...)
	}
	result.Destructive = result.Level == "high" || result.Level == "critical"
	return result
}

func classifyStatement(query string) Risk {
	q := normaliseSQL(query)
	risk := Risk{Level: "read", Reasons: []string{}}
	for _, p := range riskPatterns {
		if p.match(q) {
			risk.Reasons = append(risk.Reasons, p.reason)
			if rank(p.level) > rank(risk.Level) {
				risk.Level = p.level
			}
		}
	}
	// Fail closed. An unrecognised statement — DO, CALL, VACUUM, whatever the
	// next dialect adds — used to be indistinguishable from a SELECT, and the
	// query runner derives its capability check from this verdict. Costing the
	// operator a confirmation for a statement nobody enumerated is the right
	// side to be wrong on.
	if risk.Level == "read" && !readOnly(q) {
		risk.Level = "high"
		risk.Reasons = append(risk.Reasons, "statement is not a recognised read")
	}
	risk.Destructive = risk.Level == "critical" || risk.Level == "high"
	return risk
}

// readOnly reports whether every statement in q leads with a read-only verb.
func readOnly(q string) bool {
	// Classify already split the input with the quote-aware boundary parser.
	word := strings.ToLower(strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(q), "(")))
	if i := strings.IndexAny(word, " \t\r\n(\""); i >= 0 {
		word = word[:i]
	}
	return readOnlyLeaders[word]
}

func rank(level string) int {
	switch level {
	case "critical":
		return 3
	case "high":
		return 2
	case "medium":
		return 1
	default:
		return 0
	}
}
