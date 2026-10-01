package dbx

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"io"
	"math"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// A dump that does not depend on a client binary.
//
// Four of the eight engines here have no dump tool this image could carry:
// ClickHouse's is a separate package, SQL Server's ships only inside Microsoft's
// own image, Oracle's runs on the *server* rather than the client, and Redis has
// none at all. The route taken until now was to report those four as
// "unsupported database driver", which is the dashboard telling the operator
// that the backup button on the connection they are looking at was never going
// to work — after they pressed it.
//
// So the dump is written here instead, over the connection the dashboard already
// has. Every engine gets a working backup, and the two that do have a native
// tool (Postgres, MySQL) still use it, because a custom-format pg_dump restores
// faster and more faithfully than any SQL text can. This is the floor, not the
// preference.
//
// The output is ordinary SQL — DDL followed by INSERTs — because that is what a
// dump means to the person holding the file. They can read it, grep it, and feed
// it to a client that is not this one.
//
// What goes into the file, and in what order, is decided by a plan read from
// the catalogue before a single row is (dump_sql_plan.go and the per-engine
// files beside it). This file is the part every engine shares: writing a plan
// out, replaying one, and rendering a value as a literal.

const (
	// Rows per INSERT. Large enough that a million-row table is not a million
	// statements, small enough that a failed restore names a bounded piece of
	// the data rather than "somewhere in this 200 MB statement".
	genericDumpRowsPerStatement = 200
	// And a byte ceiling on top, because 200 rows of a table holding documents
	// is a different size from 200 rows of integers. SQL Server's batch parser
	// and MySQL's max_allowed_packet both have limits an unbounded statement
	// walks straight into.
	genericDumpStatementBytes = 512 << 10
)

// dumpBuiltInSQL writes a SQL text dump of one database using nothing but the
// engine's own driver.
func dumpBuiltInSQL(ctx context.Context, driver Driver, dsn, outDir string, opts DumpOptions) (*DumpResult, error) {
	d, err := DialectFor(driver)
	if err != nil {
		return nil, err
	}
	database := opts.Database
	db, err := openForDump(ctx, d, dsnForDatabase(driver, dsn, database))
	if err != nil {
		return nil, err
	}
	defer db.Close()

	sel, err := newDumpSelection(opts.Tables, opts.ExcludeTables)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(outDir, 0o700); err != nil {
		return nil, err
	}
	start := time.Now()
	label := database
	if driver == DriverSQLite {
		if info, err := ParseDSN(driver, dsn); err == nil {
			label = strings.TrimSuffix(filepath.Base(info.Database), filepath.Ext(info.Database))
			database = filepath.Base(info.Database)
		}
	}
	ext := "sql"
	if opts.Compression == CompressionGzip {
		ext = "sql.gz"
	}
	path := freeDumpPath(outDir, dumpFilename(label, string(driver), ext, start))
	file, err := newDumpFile(path, opts.Compression == CompressionGzip)
	if err != nil {
		return nil, err
	}
	// A half-written dump is worse than none: it looks like a backup. Anything
	// that goes wrong past this point takes the file with it.
	ok := false
	defer func() {
		if !ok {
			file.Close()
			os.Remove(path)
		}
	}()

	// One snapshot for the catalogue and every table, where the engine has
	// one to give. Without it each table is read at a different moment, and a
	// dump taken while the application writes holds an order whose customer
	// was created after the customers table was read.
	q, release, err := dumpSnapshot(ctx, db, driver)
	if err != nil {
		return nil, err
	}
	defer release()

	plan, err := planDump(ctx, q, db, d, database, sel, opts)
	if err != nil {
		return nil, err
	}

	buffered := bufio.NewWriterSize(file, 256<<10)
	w := &countingWriter{w: buffered}
	summary, err := writeDumpPlan(ctx, w, q, d, database, plan, opts, start)
	if err != nil {
		return nil, err
	}
	if w.err != nil {
		return nil, w.err
	}
	if err := buffered.Flush(); err != nil {
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	ok = true

	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	return &DumpResult{
		Path: path, Size: st.Size(), Driver: driver, Database: database,
		Duration:  time.Since(start).Round(time.Millisecond).String(),
		StartedAt: start.UTC(), Summary: summary, Output: summary,
		Tool: BuiltInDumpTool,
	}, nil
}

// dumpQueryer is the part of a pool, a pinned connection and a transaction the
// dumper reads through, so the same code runs inside a snapshot where there is
// one and outside it where there is not.
type dumpQueryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// dumpSnapshot opens the read every part of a dump goes through.
//
// Postgres, MySQL and SQLite give a transaction that sees one moment. SQL
// Server only does with a database option most do not have set, Oracle's
// driver has no read-only transaction to ask for, and ClickHouse has no
// transactions; those read as they always did, table by table.
func dumpSnapshot(ctx context.Context, db *sql.DB, driver Driver) (dumpQueryer, func(), error) {
	switch driver {
	case DriverPostgres, DriverMySQL:
		tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
		if err != nil {
			return nil, nil, err
		}
		if driver == DriverPostgres {
			// With nothing on the search path the catalogue's own deparsers —
			// a default, a view body, a type name — qualify every name they
			// print, which is what makes the file mean the same thing whatever
			// the restoring session's path is.
			if _, err := tx.ExecContext(ctx, "SET LOCAL search_path TO ''"); err != nil {
				tx.Rollback()
				return nil, nil, err
			}
		} else {
			// A TIMESTAMP is rendered in the session's zone. Pinning it, and
			// writing the same pin into the file, is what makes the instant
			// survive a restore on a server set to another one.
			if _, err := tx.ExecContext(ctx, "SET time_zone = '+00:00'"); err != nil {
				tx.Rollback()
				return nil, nil, err
			}
		}
		return tx, func() { tx.Rollback() }, nil
	case DriverSQLite:
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return nil, nil, err
		}
		return tx, func() { tx.Rollback() }, nil
	}
	return db, func() {}, nil
}

// writeDumpPlan writes a plan to w in the order a restore has to replay it:
// what must go before the tables, the drops, the structure, the rows, what can
// only be added once the rows are in, and the views over all of it.
func writeDumpPlan(ctx context.Context, w *countingWriter, q dumpQueryer, d Dialect, database string, plan *dumpPlan, opts DumpOptions, start time.Time) (string, error) {
	driver := d.Driver()
	structure, data := !opts.DataOnly, !opts.SchemaOnly

	fmt.Fprintf(w, "%s\n-- engine: %s\n-- database: %s\n-- taken: %s\n",
		dumpHeader, driver, sqlComment(database), start.UTC().Format(time.RFC3339))
	switch {
	case opts.SchemaOnly:
		fmt.Fprintf(w, "-- contents: structure only\n")
	case opts.DataOnly:
		fmt.Fprintf(w, "-- contents: data only\n")
	}
	if len(opts.Tables) > 0 {
		fmt.Fprintf(w, "-- tables: %s\n", sqlComment(strings.Join(opts.Tables, ", ")))
	}
	if len(opts.ExcludeTables) > 0 {
		fmt.Fprintf(w, "-- excluded: %s\n", sqlComment(strings.Join(opts.ExcludeTables, ", ")))
	}
	for _, note := range plan.notes {
		fmt.Fprintf(w, "-- %s\n", sqlComment(note))
	}
	for _, s := range plan.skipped {
		fmt.Fprintf(w, "-- SKIPPED %s\n", sqlComment(s))
	}
	fmt.Fprintf(w, "\n")

	for _, stmt := range plan.session {
		writeStatement(w, stmt)
	}

	if structure {
		// Every DROP first, dependants before what they depend on, so nothing
		// is dropped while something else still points at it.
		for i := len(plan.views) - 1; i >= 0; i-- {
			writeStatement(w, plan.views[i].drop)
		}
		for i := len(plan.tables) - 1; i >= 0; i-- {
			writeStatement(w, plan.tables[i].drop)
		}
		for i := len(plan.sequences) - 1; i >= 0; i-- {
			writeStatement(w, plan.sequences[i].drop)
		}
		fmt.Fprintf(w, "\n")
		for _, stmt := range plan.before {
			writeStatement(w, stmt)
		}
		for _, seq := range plan.sequences {
			writeStatement(w, seq.create)
		}
	}

	var (
		dumped  int
		rowsAll int64
		skipped = append([]string(nil), plan.skipped...)
	)
	for _, pt := range plan.tables {
		fmt.Fprintf(w, "\n-- %s\n", sqlComment(pt.rel))
		if structure {
			writeStatement(w, pt.create)
		}
		if !data || pt.noData {
			dumped++
			continue
		}
		n, err := dumpTableRows(ctx, w, q, d, pt)
		if err != nil {
			if ctx.Err() != nil {
				return "", err
			}
			// One unreadable table must not cost the operator the other forty.
			// The failure is recorded in the file and in the result, so a dump
			// that is missing something says which something.
			fmt.Fprintf(w, "\n-- SKIPPED %s: %s\n\n", sqlComment(pt.rel), sqlComment(err.Error()))
			skipped = append(skipped, pt.table.Name)
			opts.progress("%s: skipped (%v)", pt.rel, err)
			continue
		}
		for _, stmt := range pt.afterData {
			writeStatement(w, stmt)
		}
		dumped++
		rowsAll += n
		opts.progress("%s: %d rows", pt.rel, n)
		if w.err != nil {
			return "", w.err
		}
	}

	if structure {
		fmt.Fprintf(w, "\n")
		for _, stmt := range plan.after {
			writeStatement(w, stmt)
		}
		for _, v := range plan.views {
			fmt.Fprintf(w, "\n-- %s\n", sqlComment(v.rel))
			writeStatement(w, v.create)
			for _, stmt := range v.after {
				writeStatement(w, stmt)
			}
		}
	}
	if data {
		for _, stmt := range plan.afterAll {
			writeStatement(w, stmt)
		}
	}
	for _, stmt := range plan.sessionEnd {
		writeStatement(w, stmt)
	}

	out := fmt.Sprintf("%d tables, %d rows", dumped, rowsAll)
	switch {
	case opts.SchemaOnly:
		out = fmt.Sprintf("%d tables, structure only", dumped)
	case opts.DataOnly:
		out += ", data only"
	}
	if n := len(plan.views); n > 0 && structure {
		out += fmt.Sprintf(", %d views", n)
	}
	if len(skipped) > 0 {
		out += fmt.Sprintf("; skipped %d (%s)", len(skipped), strings.Join(skipped, ", "))
	}
	fmt.Fprintf(w, "\n-- dump complete: %s\n", sqlComment(out))
	return out, nil
}

// The two lines that fence a statement the reader must take whole.
//
// A definition the catalogue hands back — a view body, a trigger, a CREATE
// TABLE with a comment in it — is the engine's text, in the engine's full
// grammar, and the splitter below only knows as much of that grammar as this
// package writes itself. Fencing such a statement means it is never lexed at
// all: a trigger body's semicolons and a function's dollar quotes pass through
// untouched. To any other client the fences are two comments.
const (
	rawStatementBegin = "-- jd:statement"
	rawStatementEnd   = "-- jd:end"
)

// dumpStatement is one statement of a dump. raw marks text that came from the
// catalogue rather than from this package.
type dumpStatement struct {
	sql string
	raw bool
}

func stmt(sql string) dumpStatement    { return dumpStatement{sql: sql} }
func rawStmt(sql string) dumpStatement { return dumpStatement{sql: sql, raw: true} }

func writeStatement(w io.Writer, s dumpStatement) {
	// Oracle's DBMS_METADATA and ClickHouse's SHOW CREATE both hand back text
	// that may already be terminated; a doubled semicolon is an empty statement
	// on some engines and a syntax error on others.
	text := strings.TrimRight(strings.TrimSpace(s.sql), ";\n\r\t ")
	if text == "" {
		return
	}
	if s.raw {
		fmt.Fprintf(w, "%s\n%s;\n%s\n", rawStatementBegin, text, rawStatementEnd)
		return
	}
	fmt.Fprintf(w, "%s;\n", text)
}

// dumpTableRows writes every row of one table and returns the row count. Rows
// are streamed: the whole point of not buffering the result is that a table
// larger than memory is exactly the table worth backing up.
func dumpTableRows(ctx context.Context, w *countingWriter, q dumpQueryer, d Dialect, pt dumpTable) (int64, error) {
	query := pt.selectSQL
	if query == "" {
		query = "SELECT * FROM " + pt.rel
	}
	rows, err := q.QueryContext(ctx, query)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return 0, err
	}
	quoted := make([]string, len(cols))
	for i, c := range cols {
		qc, err := d.QuoteIdent(c)
		if err != nil {
			return 0, err
		}
		quoted[i] = qc
	}
	// Which columns actually hold bytes.
	//
	// Several drivers hand back []byte for text: MySQL does it for every string
	// and for DECIMAL, so a dump that treated a byte slice as binary wrote every
	// email address as 0x75736572… — which reloads into a VARCHAR as the hex
	// digits, and into a DECIMAL not at all. The column's declared type is the
	// only thing that can tell the two apart.
	binaryCol := make([]bool, len(cols))
	typeName := make([]string, len(cols))
	if types, err := rows.ColumnTypes(); err == nil {
		for i, ct := range types {
			typeName[i] = ct.DatabaseTypeName()
			binaryCol[i] = isBinaryTypeName(typeName[i])
		}
	}
	batch := newInsertBatcher(w, d.Driver(), pt.rel, quoted, genericDumpRowsPerStatement)
	batch.overriding = pt.overriding

	var count int64
	for rows.Next() {
		vals, err := scanRawRow(rows, len(cols))
		if err != nil {
			return count, err
		}
		parts := make([]string, len(vals))
		for i, v := range vals {
			parts[i] = dumpColumnValue(d.Driver(), v, binaryCol[i], typeName[i])
		}
		if err := batch.add("(" + strings.Join(parts, ", ") + ")"); err != nil {
			return count, err
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return count, err
	}
	return count, batch.flush()
}

// insertBatcher gathers row tuples into INSERT statements of a bounded size.
// The dump and the SQL export both write through it, so the two cannot differ
// about how a row is spelled.
type insertBatcher struct {
	w      io.Writer
	driver Driver
	prefix string
	limit  int
	// overriding is Postgres's permission to write a column the table would
	// otherwise generate itself.
	overriding bool
	tuples     []string
	bytes      int
}

func newInsertBatcher(w io.Writer, driver Driver, rel string, quotedCols []string, rowsPerStatement int) *insertBatcher {
	return &insertBatcher{
		w: w, driver: driver, limit: rowsPerStatement,
		prefix: "INSERT INTO " + rel + " (" + strings.Join(quotedCols, ", ") + ") ",
	}
}

func (b *insertBatcher) add(tuple string) error {
	b.tuples = append(b.tuples, tuple)
	b.bytes += len(tuple)
	if len(b.tuples) >= b.limit || b.bytes >= genericDumpStatementBytes {
		return b.flush()
	}
	return nil
}

func (b *insertBatcher) flush() error {
	if len(b.tuples) == 0 {
		return nil
	}
	values := "VALUES "
	if b.overriding {
		values = "OVERRIDING SYSTEM VALUE VALUES "
	}
	var err error
	// Oracle has no multi-row VALUES list; every other engine here does,
	// and one statement per row would multiply a large restore by the
	// round-trip time.
	if b.driver == DriverOracle {
		for _, tuple := range b.tuples {
			if _, err = fmt.Fprintf(b.w, "%s%s%s;\n", b.prefix, values, tuple); err != nil {
				break
			}
		}
	} else {
		_, err = fmt.Fprintf(b.w, "%s%s%s;\n", b.prefix, values, strings.Join(b.tuples, ", "))
	}
	b.tuples, b.bytes = b.tuples[:0], 0
	return err
}

// dropTableStatement removes a table along with whatever still points at it,
// in whichever spelling this engine has for that.
//
// SQL Server and ClickHouse have no cascading form, which is the other half of
// why the dump is ordered: there, dropping in reverse dependency order is the
// only way a table with a child ever goes.
func dropTableStatement(driver Driver, rel string) string {
	switch driver {
	case DriverPostgres:
		return "DROP TABLE IF EXISTS " + rel + " CASCADE"
	case DriverOracle:
		// Oracle before 23c has no IF EXISTS; the restore treats a failing DROP
		// as the table not being there.
		return "DROP TABLE " + rel + " CASCADE CONSTRAINTS"
	default:
		return "DROP TABLE IF EXISTS " + rel
	}
}

// restoreGenericSQL replays a dump this package wrote.
func restoreGenericSQL(ctx context.Context, driver Driver, dsn, database, path string, opts RestoreOptions) (string, error) {
	d, err := DialectFor(driver)
	if err != nil {
		return "", err
	}
	text, closeDump, err := openDumpText(path)
	if err != nil {
		return "", err
	}
	defer closeDump()
	db, err := openForDump(ctx, d, dsnForDatabase(driver, dsn, database))
	if err != nil {
		return "", err
	}
	defer db.Close()

	// One connection for the whole restore rather than one from the pool per
	// statement: a dump is a sequence, and anything a statement sets for the
	// session — a search path, a constraint mode — has to still be set for the
	// next one.
	conn, err := db.Conn(ctx)
	if err != nil {
		return "", err
	}
	defer conn.Close()

	// Where the engine can roll structure back, the restore is one
	// transaction: a dump that fails on its fortieth statement leaves the
	// database as it was, rather than with its tables dropped and a third of
	// them back. MySQL, Oracle and ClickHouse commit at every DDL statement
	// whatever they are told, and SQL Server is left as it was for want of a
	// server to prove the change against.
	transactional := driver == DriverPostgres || driver == DriverSQLite
	if driver == DriverSQLite {
		// Foreign keys are switched off for the session, and it has to happen
		// before the transaction: inside one the pragma is silently ignored.
		// Left on, dropping a table deletes its rows first, and that delete
		// cascades into tables this dump does not hold.
		if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
			return "", err
		}
	}
	if transactional {
		if _, err := conn.ExecContext(ctx, "BEGIN"); err != nil {
			return "", err
		}
	}
	rollback := func() {
		if transactional {
			// The request may be what was cancelled; the rollback still has to
			// reach the server.
			_, _ = conn.ExecContext(context.WithoutCancel(ctx), "ROLLBACK")
		}
	}

	statements := newStatementReader(driver, text)
	var ran, dropped int
	lastReport := time.Now()
	for {
		statement, err := statements.next()
		if err == io.EOF {
			break
		}
		if err != nil {
			rollback()
			return "", fmt.Errorf("could not read the dump after statement %d: %w", ran, err)
		}
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			// A DROP that fails is the table not being there yet, which is the
			// ordinary case for a restore into an empty database. Everything
			// else stops: half a restore is not a restore, and continuing past
			// a failed CREATE would fill the *previous* table with these rows.
			if isDropStatement(statement) && !transactional {
				dropped++
				continue
			}
			rollback()
			if transactional {
				return "", fmt.Errorf("statement %d failed, and nothing was changed: %w\n%s", ran+1, err, truncateForMessage(statement))
			}
			return "", fmt.Errorf("statement %d failed: %w\n%s", ran+1, err, truncateForMessage(statement))
		}
		ran++
		if opts.Progress != nil && time.Since(lastReport) > 2*time.Second {
			opts.progress("%d statements applied", ran)
			lastReport = time.Now()
		}
	}
	if transactional {
		if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
			rollback()
			return "", fmt.Errorf("the restore could not be committed, and nothing was changed: %w", err)
		}
	}
	out := fmt.Sprintf("%d statements applied", ran)
	if dropped > 0 {
		out += fmt.Sprintf(", %d drops skipped (nothing to drop)", dropped)
	}
	opts.progress("%s", out)
	return out, nil
}

func isDropStatement(s string) bool {
	return strings.HasPrefix(strings.ToUpper(strings.TrimSpace(s)), "DROP ")
}

func truncateForMessage(s string) string {
	const max = 300
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

// openForDump dials a throwaway connection. It is not the request pool: a dump
// can run for half an hour, and holding one of the pool's five connections for
// that long would starve the pages the operator is still using.
func openForDump(ctx context.Context, d Dialect, dsn string) (*sql.DB, error) {
	db, err := sql.Open(d.SQLDriverName(), d.NormaliseDSN(dsn))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(2)
	d.TunePool(db)
	pingCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// dsnForDatabase re-points a connection string at another database on the same
// server, which is what makes "dump the database I picked" work when the saved
// connection names a different one.
func dsnForDatabase(driver Driver, dsn, database string) string {
	if database == "" {
		return dsn
	}
	switch driver {
	case DriverSQLite, DriverOracle, DriverClickHouse:
		// SQLite's database is the file. Oracle's is the service name, which is
		// a property of the server rather than something to switch. ClickHouse
		// takes a database in the path but qualifies every table name anyway,
		// and rewriting it would break a connection whose default database is
		// where the operator's grants are.
		return dsn
	case DriverMySQL:
		return mysqlDSNWithDatabase(dsn, database)
	case DriverMSSQL:
		return urlDSNWithQuery(dsn, "database", database)
	default:
		return urlDSNWithPath(dsn, database)
	}
}

// countingWriter is an io.Writer that swallows write errors and remembers the
// first, so the dump loop can stay readable. The error is not lost: the file is
// Sync'd and Stat'd at the end, and a dump that failed to write is a dump that
// did not land.
type countingWriter struct {
	w   interface{ Write([]byte) (int, error) }
	n   int64
	err error
}

func (c *countingWriter) Write(p []byte) (int, error) {
	if c.err != nil {
		return 0, c.err
	}
	n, err := c.w.Write(p)
	c.n += int64(n)
	if err != nil {
		c.err = err
	}
	return n, err
}

// sqlComment flattens text so it cannot escape a -- comment and change what the
// rest of the file means.
func sqlComment(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}

// dumpFilename builds a filename from a database name that may contain anything
// the engine allows — hyphens, dots, non-ASCII, a slash on the engines that
// permit one. Only the safe characters survive; the name is a label here, not
// an identifier, and the authoritative one is inside the file.
func dumpFilename(database, driver, ext string, at time.Time) string {
	base := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, database)
	base = strings.Trim(base, "_")
	if base == "" {
		base = driver
	}
	if len(base) > 64 {
		base = base[:64]
	}
	return fmt.Sprintf("%s-%s.%s", base, at.UTC().Format("20060102-150405"), ext)
}

// freeDumpPath is where a dump of this name goes: under the name itself, or,
// when a dump taken in the same second already has it, under the name with a
// number. The stamp in a name is to the second, and a dump taken to keep the
// current state follows the dump it is about to be replaced from by less than
// that — one of them would have overwritten the other.
func freeDumpPath(dir, name string) string {
	path := filepath.Join(dir, name)
	if _, err := os.Lstat(path); err != nil {
		return path
	}
	stem, ext := name, ""
	// The extension is everything from the first dot after the stamp, so
	// .sql.gz stays whole.
	if dot := strings.IndexByte(name, '.'); dot > 0 {
		stem, ext = name[:dot], name[dot:]
	}
	for n := 2; ; n++ {
		path = filepath.Join(dir, fmt.Sprintf("%s-%d%s", stem, n, ext))
		if _, err := os.Lstat(path); err != nil {
			return path
		}
	}
}

// --- literals -------------------------------------------------------------

// isBinaryTypeName reports whether a driver's declared column type holds bytes
// rather than text. Names differ per engine — BYTEA, BLOB, VARBINARY, RAW,
// IMAGE — and every one of them contains one of these words.
func isBinaryTypeName(name string) bool {
	upper := strings.ToUpper(name)
	for _, marker := range []string{"BINARY", "BLOB", "BYTEA", "RAW", "IMAGE"} {
		if strings.Contains(upper, marker) {
			return true
		}
	}
	return false
}

// dumpValue renders one column of one row, knowing whether the column is
// binary. dumpLiteral is the same thing for a value with no column behind it.
func dumpValue(driver Driver, v any, binary bool) string {
	if b, ok := v.([]byte); ok && !binary {
		return dumpString(driver, string(b))
	}
	return dumpLiteral(driver, v)
}

// numberText is what a number looks like when a driver hands it back as text.
var numberText = regexp.MustCompile(`^-?[0-9]+(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)

// dumpColumnValue is dumpValue for a value whose column type is known, which
// is what the cases a bare value cannot settle need: an instant has to be
// written in the form the column's type reads, and a number a driver returned
// as text has to stay a number.
func dumpColumnValue(driver Driver, v any, binary bool, typeName string) string {
	if v == nil {
		return "NULL"
	}
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return "NULL"
		}
		if _, isBig := v.(*big.Int); isBig {
			break
		}
		rv = rv.Elem()
		v = rv.Interface()
	}
	switch x := v.(type) {
	case time.Time:
		return dumpTimeFor(driver, x, typeName)
	case float64:
		if lit, ok := postgresNonFinite(driver, x); ok {
			return lit
		}
	case float32:
		if lit, ok := postgresNonFinite(driver, float64(x)); ok {
			return lit
		}
	}
	if driver == DriverClickHouse && clickhouseNumeric(typeName) {
		// The driver wraps a Decimal in a type of its own, and a quoted number
		// is a String to ClickHouse's VALUES parser.
		if s, ok := v.(fmt.Stringer); ok && numberText.MatchString(s.String()) {
			return s.String()
		}
	}
	return dumpValue(driver, v, binary)
}

// postgresNonFinite spells the three values no other engine here will take in
// a VALUES list, and Postgres will, so there they are kept rather than nulled.
func postgresNonFinite(driver Driver, f float64) (string, bool) {
	if driver != DriverPostgres {
		return "", false
	}
	switch {
	case math.IsNaN(f):
		return "'NaN'", true
	case math.IsInf(f, 1):
		return "'Infinity'", true
	case math.IsInf(f, -1):
		return "'-Infinity'", true
	}
	return "", false
}

// clickhouseNumeric reports a ClickHouse column type that holds a number,
// through whatever Nullable or LowCardinality it is wrapped in.
func clickhouseNumeric(typeName string) bool {
	t := clickhouseInnerType(typeName)
	for _, prefix := range []string{"Int", "UInt", "Float", "Decimal"} {
		if strings.HasPrefix(t, prefix) {
			return true
		}
	}
	return false
}

func clickhouseInnerType(typeName string) string {
	t := strings.TrimSpace(typeName)
	for {
		switch {
		case strings.HasPrefix(t, "Nullable(") && strings.HasSuffix(t, ")"):
			t = t[len("Nullable(") : len(t)-1]
		case strings.HasPrefix(t, "LowCardinality(") && strings.HasSuffix(t, ")"):
			t = t[len("LowCardinality(") : len(t)-1]
		default:
			return t
		}
	}
}

// dumpLiteral renders one scanned value as a literal for this engine.
//
// This is the one place in the package that puts a value into a statement
// rather than binding it, and it exists because a dump file *is* text — there
// is nothing to bind to. Every branch is therefore written for the engine's
// actual lexer rather than for SQL in general: MySQL and ClickHouse treat a
// backslash inside a string literal as an escape and the others do not, so a
// Windows path dumped with the standard doubling rule would come back one
// backslash short on two engines and correct on four.
//
// Anything that cannot be represented as text at all — a byte string that is
// not valid UTF-8, a string carrying a NUL — goes out as the engine's binary
// literal instead of being mangled into something that parses.
func dumpLiteral(driver Driver, v any) string {
	if v == nil {
		return "NULL"
	}
	// A wide integer arrives as a pointer whose methods are on the pointer.
	// Dereferencing it first, as every other pointer is, left a struct with no
	// String method and the dump wrote its fields.
	switch x := v.(type) {
	case *big.Int:
		if x == nil {
			return "NULL"
		}
		return x.String()
	case big.Int:
		return x.String()
	}
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return "NULL"
		}
		rv = rv.Elem()
	}
	v = rv.Interface()

	switch x := v.(type) {
	case bool:
		return dumpBool(driver, x)
	case []byte:
		return dumpBytes(driver, x)
	case string:
		return dumpString(driver, x)
	case time.Time:
		return dumpTime(driver, x)
	case big.Int:
		return x.String()
	}

	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(rv.Int(), 10)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(rv.Uint(), 10)
	case reflect.Float32, reflect.Float64:
		f := rv.Float()
		if math.IsNaN(f) || math.IsInf(f, 0) {
			// No engine here accepts a bare NaN in a VALUES list, and the ones
			// with a spelling for it disagree on what it is. NULL is the only
			// answer that reloads everywhere.
			return "NULL"
		}
		return strconv.FormatFloat(f, 'g', -1, 64)
	case reflect.Slice, reflect.Array:
		return dumpSequence(driver, rv)
	case reflect.Map:
		return dumpMap(driver, rv)
	}
	if s, ok := v.(fmt.Stringer); ok {
		return dumpString(driver, s.String())
	}
	return dumpString(driver, fmt.Sprint(v))
}

func dumpBool(driver Driver, b bool) string {
	switch driver {
	case DriverPostgres, DriverClickHouse:
		if b {
			return "TRUE"
		}
		return "FALSE"
	default:
		// SQL Server has no boolean type at all and Oracle gained one only in
		// 23c; both store these as 1/0, which every engine here also accepts.
		if b {
			return "1"
		}
		return "0"
	}
}

// backslashEscapes reports whether a backslash inside a single-quoted string is
// an escape character for this engine.
func backslashEscapes(driver Driver) bool {
	return driver == DriverMySQL || driver == DriverClickHouse
}

// oracleLiteralChars is how much of a string goes into one Oracle literal. The
// engine refuses a literal past 4000 bytes, and a character can be four.
const oracleLiteralChars = 1000

func dumpString(driver Driver, s string) string {
	if strings.ContainsRune(s, 0) || !utf8.ValidString(s) {
		return dumpBytes(driver, []byte(s))
	}
	escaped := strings.ReplaceAll(s, "'", "''")
	if backslashEscapes(driver) {
		escaped = strings.ReplaceAll(s, `\`, `\\`)
		escaped = strings.ReplaceAll(escaped, "'", "''")
	}
	switch driver {
	case DriverMSSQL:
		// N'' is what keeps a non-ASCII character intact on the way in: an
		// unprefixed literal is read in the database's code page first and only
		// then widened, so anything outside it is already gone.
		return "N'" + escaped + "'"
	case DriverOracle:
		if utf8.RuneCountInString(s) > oracleLiteralChars {
			// A long value is written as pieces joined on the server, each
			// short enough to be a literal and the whole of it a CLOB.
			return oracleLongString(s)
		}
	}
	return "'" + escaped + "'"
}

func oracleLongString(s string) string {
	runes := []rune(s)
	parts := make([]string, 0, len(runes)/oracleLiteralChars+1)
	for len(runes) > 0 {
		n := min(oracleLiteralChars, len(runes))
		parts = append(parts, "TO_CLOB('"+strings.ReplaceAll(string(runes[:n]), "'", "''")+"')")
		runes = runes[n:]
	}
	return strings.Join(parts, " || ")
}

func dumpBytes(driver Driver, b []byte) string {
	hex := strings.ToUpper(bytesToHex(b))
	switch driver {
	case DriverPostgres:
		// standard_conforming_strings has been on by default since 9.1, so the
		// backslash here is literal and this is the hex bytea input format.
		return `'\x` + strings.ToLower(hex) + `'::bytea`
	case DriverSQLite:
		return "X'" + hex + "'"
	case DriverMySQL, DriverMSSQL:
		if len(b) == 0 {
			// 0x with no digits is a syntax error on both.
			return "''"
		}
		return "0x" + hex
	case DriverClickHouse:
		return "unhex('" + hex + "')"
	case DriverOracle:
		if len(b) == 0 {
			return "NULL"
		}
		return "HEXTORAW('" + hex + "')"
	default:
		return "X'" + hex + "'"
	}
}

const hexDigits = "0123456789abcdef"

func bytesToHex(b []byte) string {
	out := make([]byte, 0, len(b)*2)
	for _, c := range b {
		out = append(out, hexDigits[c>>4], hexDigits[c&0x0f])
	}
	return string(out)
}

func dumpTime(driver Driver, t time.Time) string {
	return dumpTimeFor(driver, t, "")
}

// dumpTimeFor writes an instant as the column it is going back into reads one.
//
// The value is always written in UTC. What differs is whether the literal says
// so: a form with no zone is read in the restoring session's, which is the
// zone of whoever configured that server and not a property of the data.
func dumpTimeFor(driver Driver, t time.Time, typeName string) string {
	const plain = "2006-01-02 15:04:05.999999"
	switch driver {
	case DriverPostgres:
		// The offset is ignored by a column without a zone and honoured by one
		// with, which is right for both.
		return "'" + t.UTC().Format(plain) + "+00'"
	case DriverOracle:
		// Oracle will not read an ISO string as a date without being told the
		// format, and its NLS settings are per session — so the format travels
		// with the value rather than being assumed.
		return "TO_TIMESTAMP('" + t.UTC().Format(plain) + "', 'YYYY-MM-DD HH24:MI:SS.FF')"
	case DriverClickHouse:
		inner := clickhouseInnerType(typeName)
		switch {
		case strings.HasPrefix(inner, "DateTime64"):
			precision := 3
			if open := strings.IndexByte(inner, '('); open >= 0 {
				digits := strings.TrimSpace(inner[open+1:])
				if end := strings.IndexAny(digits, ",)"); end >= 0 {
					digits = digits[:end]
				}
				if n, err := strconv.Atoi(strings.TrimSpace(digits)); err == nil && n >= 0 && n <= 9 {
					precision = n
				}
			}
			return fmt.Sprintf("toDateTime64('%s', %d, 'UTC')",
				t.UTC().Format("2006-01-02 15:04:05.000000000"), precision)
		case strings.HasPrefix(inner, "DateTime"):
			return "toDateTime('" + t.UTC().Format("2006-01-02 15:04:05") + "', 'UTC')"
		case strings.HasPrefix(inner, "Date"):
			return "'" + t.UTC().Format("2006-01-02") + "'"
		}
		// ClickHouse's DateTime has second resolution and rejects a fractional
		// part; DateTime64 accepts this form too.
		return "'" + t.UTC().Format("2006-01-02 15:04:05") + "'"
	default:
		return "'" + t.UTC().Format(plain) + "'"
	}
}

// sqlSequence renders an array. Only ClickHouse has a column type that scans
// into a Go slice, so that is the syntax to produce; elsewhere a slice arriving
// here is something the driver chose to represent that way and the text form is
// the honest fallback.
func dumpSequence(driver Driver, rv reflect.Value) string {
	parts := make([]string, rv.Len())
	for i := range parts {
		parts[i] = dumpLiteral(driver, rv.Index(i).Interface())
	}
	if driver == DriverClickHouse {
		return "[" + strings.Join(parts, ", ") + "]"
	}
	return dumpString(driver, "["+strings.Join(parts, ",")+"]")
}

func dumpMap(driver Driver, rv reflect.Value) string {
	// Sorted by rendered key so the same table dumps byte-identically twice —
	// Go randomises map iteration, and a dump that differs from itself is one
	// nobody can diff to see what actually changed.
	keys := rv.MapKeys()
	rendered := make([]string, 0, len(keys))
	for _, k := range keys {
		rendered = append(rendered, dumpLiteral(driver, k.Interface())+"\x00"+
			dumpLiteral(driver, rv.MapIndex(k).Interface()))
	}
	sortStrings(rendered)
	pairs := make([]string, 0, len(rendered)*2)
	joined := make([]string, 0, len(rendered))
	for _, r := range rendered {
		k, v, _ := strings.Cut(r, "\x00")
		pairs = append(pairs, k, v)
		joined = append(joined, k+":"+v)
	}
	if driver == DriverClickHouse {
		return "map(" + strings.Join(pairs, ", ") + ")"
	}
	return dumpString(driver, "{"+strings.Join(joined, ",")+"}")
}

// scanRawRow reads one row without the normalisation the browse path applies.
// That turns a byte string into a printable "\x…" preview for a grid cell,
// which is the right answer on screen and the wrong one in a backup: the dump
// has to carry the bytes, not a description of them.
func scanRawRow(rows *sql.Rows, n int) ([]any, error) {
	holders := make([]any, n)
	ptrs := make([]any, n)
	for i := range holders {
		ptrs[i] = &holders[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		return nil, err
	}
	return holders, nil
}

// --- connection strings ---------------------------------------------------

// urlDSNWithPath replaces the database in a URL-shaped connection string.
func urlDSNWithPath(dsn, database string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return dsn
	}
	u.Path = "/" + database
	return u.String()
}

// urlDSNWithQuery sets one query parameter, which is where SQL Server keeps the
// database rather than in the path.
func urlDSNWithQuery(dsn, key, value string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return dsn
	}
	q := u.Query()
	q.Set(key, value)
	u.RawQuery = q.Encode()
	return u.String()
}

// mysqlDSNWithDatabase rewrites the go-sql-driver form, user:pass@tcp(host)/db.
// The database is whatever sits between the last slash outside the address and
// the parameters, and the address itself may contain slashes for a unix socket
// — which is why the search starts after the closing bracket.
func mysqlDSNWithDatabase(dsn, database string) string {
	if !strings.Contains(dsn, "://") {
		start := 0
		if close := strings.LastIndex(dsn, ")"); close >= 0 {
			start = close + 1
		}
		rest := dsn[start:]
		params := ""
		if q := strings.Index(rest, "?"); q >= 0 {
			params = rest[q:]
		}
		return dsn[:start] + "/" + database + params
	}
	return urlDSNWithPath(dsn, database)
}

// --- splitting ------------------------------------------------------------

// splitSQLStatements cuts a dump into statements at the semicolons that are
// actually statement terminators.
//
// A naive split on ";" is wrong the moment a row contains one, which for any
// table holding text is immediately. So this tracks the three places a
// semicolon means nothing: inside a string literal, inside a quoted identifier,
// and inside a comment. The escape rules are the engine's own — the same
// backslash question dumpLiteral answers — because a splitter that disagrees
// with the writer about where a string ends is worse than no splitter.
func splitSQLStatements(driver Driver, text string) []string {
	r := newStatementReader(driver, strings.NewReader(text))
	var out []string
	for {
		s, err := r.next()
		if err != nil {
			return out
		}
		out = append(out, s)
	}
}

// statementReader is splitSQLStatements over a stream. A dump is replayed a
// statement at a time, and reading the whole file to find where they end meant
// a restore needed the dump's size in memory before it had run a line of it.
type statementReader struct {
	r    *bufio.Reader
	bs   bool
	idOK map[byte]byte
	cur  strings.Builder
	// queued is a fenced statement read while another was still open.
	queued string
	done   bool
}

func newStatementReader(driver Driver, r io.Reader) *statementReader {
	return &statementReader{
		r:    bufio.NewReaderSize(r, 256<<10),
		bs:   backslashEscapes(driver),
		idOK: identifierQuotes(driver),
	}
}

// next returns the next statement, or io.EOF after the last.
func (s *statementReader) next() (string, error) {
	if s.queued != "" {
		out := s.queued
		s.queued = ""
		return out, nil
	}
	for !s.done {
		c, err := s.r.ReadByte()
		if err == io.EOF {
			s.done = true
			break
		}
		if err != nil {
			return "", err
		}
		switch {
		case c == '-' && s.peekIs('-'):
			line, err := s.restOfLine()
			if err != nil {
				return "", err
			}
			// The comment stood between two tokens, and so must something.
			s.cur.WriteByte('\n')
			if strings.TrimSpace("-"+line) != rawStatementBegin {
				continue
			}
			raw, err := s.rawStatement()
			if err != nil {
				return "", err
			}
			if pending := s.take(); pending != "" {
				s.queued = raw
				return pending, nil
			}
			if raw != "" {
				return raw, nil
			}
		case c == '/' && s.peekIs('*'):
			if err := s.skipBlockComment(); err != nil {
				return "", err
			}
		case c == '\'':
			s.cur.WriteByte(c)
			if err := s.copyString(); err != nil {
				return "", err
			}
		case s.idOK[c] != 0:
			s.cur.WriteByte(c)
			if err := s.copyQuoted(s.idOK[c]); err != nil {
				return "", err
			}
		case c == ';':
			if out := s.take(); out != "" {
				return out, nil
			}
		default:
			s.cur.WriteByte(c)
		}
	}
	if out := s.take(); out != "" {
		return out, nil
	}
	return "", io.EOF
}

func (s *statementReader) take() string {
	out := strings.TrimSpace(s.cur.String())
	s.cur.Reset()
	return out
}

func (s *statementReader) peekIs(c byte) bool {
	b, err := s.r.Peek(1)
	return err == nil && b[0] == c
}

// restOfLine consumes up to and including the newline and returns what it
// read, without the newline.
func (s *statementReader) restOfLine() (string, error) {
	var line strings.Builder
	for {
		c, err := s.r.ReadByte()
		if err == io.EOF {
			return line.String(), nil
		}
		if err != nil {
			return "", err
		}
		if c == '\n' {
			return line.String(), nil
		}
		line.WriteByte(c)
	}
}

// rawStatement reads the lines up to the closing fence as one statement.
func (s *statementReader) rawStatement() (string, error) {
	var body strings.Builder
	for {
		line, err := s.r.ReadString('\n')
		if strings.TrimSpace(line) == rawStatementEnd {
			break
		}
		body.WriteString(line)
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
	}
	return strings.TrimRight(strings.TrimSpace(body.String()), ";\n\r\t "), nil
}

func (s *statementReader) skipBlockComment() error {
	// The opening star is still unread.
	if _, err := s.r.ReadByte(); err != nil {
		return err
	}
	prev := byte(0)
	for {
		c, err := s.r.ReadByte()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if prev == '*' && c == '/' {
			return nil
		}
		prev = c
	}
}

func (s *statementReader) copyString() error {
	for {
		c, err := s.r.ReadByte()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if s.bs && c == '\\' {
			next, err := s.r.ReadByte()
			if err == io.EOF {
				s.cur.WriteByte(c)
				return nil
			}
			if err != nil {
				return err
			}
			s.cur.WriteByte(c)
			s.cur.WriteByte(next)
			continue
		}
		s.cur.WriteByte(c)
		if c == '\'' {
			if s.peekIs('\'') {
				s.r.ReadByte()
				s.cur.WriteByte('\'')
				continue
			}
			return nil
		}
	}
}

func (s *statementReader) copyQuoted(closer byte) error {
	for {
		c, err := s.r.ReadByte()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		s.cur.WriteByte(c)
		if c == closer {
			if s.peekIs(closer) {
				s.r.ReadByte()
				s.cur.WriteByte(closer)
				continue
			}
			return nil
		}
	}
}

// identifierQuotes maps an opening quote character to its closer. SQL Server's
// bracket form is the only one where they differ, which is exactly why this is
// a map rather than a set.
func identifierQuotes(driver Driver) map[byte]byte {
	switch driver {
	case DriverMySQL, DriverClickHouse:
		return map[byte]byte{'`': '`', '"': '"'}
	case DriverMSSQL:
		return map[byte]byte{'[': ']', '"': '"'}
	default:
		return map[byte]byte{'"': '"'}
	}
}

// DSNForDatabase is dsnForDatabase for callers outside the package: the same
// connection pointed at another database on the same server, or the string
// unchanged on the engines whose connection cannot name one.
func DSNForDatabase(driver Driver, dsn, database string) string {
	return dsnForDatabase(driver, dsn, database)
}

// OpenDatabase opens a short-lived pool to another database on the same
// server, for the one statement that has to run from inside it. The caller
// closes it.
func OpenDatabase(ctx context.Context, driver Driver, dsn, database string) (*sql.DB, error) {
	d, err := DialectFor(driver)
	if err != nil {
		return nil, err
	}
	return openForDump(ctx, d, dsnForDatabase(driver, dsn, database))
}
