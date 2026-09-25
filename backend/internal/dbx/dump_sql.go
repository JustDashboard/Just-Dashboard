package dbx

import (
	"context"
	"database/sql"
	"fmt"
	"math"
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

// dumpGenericSQL writes a SQL text dump of one database using nothing but the
// engine's own driver.
func dumpGenericSQL(ctx context.Context, driver Driver, dsn, database, outDir string) (*DumpResult, error) {
	d, err := DialectFor(driver)
	if err != nil {
		return nil, err
	}
	db, err := openForDump(ctx, d, dsnForDatabase(driver, dsn, database))
	if err != nil {
		return nil, err
	}
	defer db.Close()

	if err := os.MkdirAll(outDir, 0o700); err != nil {
		return nil, err
	}
	start := time.Now()
	path := filepath.Join(outDir, dumpFilename(database, string(driver), "sql", start))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, err
	}
	// A half-written dump is worse than none: it looks like a backup. Anything
	// that goes wrong past this point takes the file with it.
	ok := false
	defer func() {
		f.Close()
		if !ok {
			os.Remove(path)
		}
	}()

	schema := resolveDumpSchema(ctx, db, driver, database)
	tables, err := d.Tables(ctx, db, schema)
	if err != nil {
		return nil, fmt.Errorf("cannot list tables: %w", err)
	}

	w := &countingWriter{w: f}
	fmt.Fprintf(w, "-- Just Dashboard dump\n-- engine: %s\n-- database: %s\n-- taken: %s\n\n",
		driver, database, start.UTC().Format(time.RFC3339))

	// Read every table's structure first, then order the whole dump by what
	// references what.
	//
	// Alphabetical order is wrong twice over on any schema with a foreign key:
	// a CREATE naming a table that has not been created yet fails, and so does
	// an INSERT of a child row before its parent exists. `jd_posts` sorts before
	// `jd_users`, which is exactly the case, and the restore failed on the
	// second statement with a constraint error that described the symptom and
	// not the cause.
	plan := []dumpTable{}
	var skipped []string
	for _, t := range tables {
		if strings.EqualFold(t.Type, "view") {
			// A view has no rows of its own and INSERTing into one fails on
			// most engines. Its definition is not lost — it is derived from
			// tables that are in this file.
			continue
		}
		detail, err := Detail(ctx, db, driver, t.Schema, t.Name)
		if err != nil {
			fmt.Fprintf(w, "-- SKIPPED %s.%s: %s\n", t.Schema, t.Name, sqlComment(err.Error()))
			skipped = append(skipped, t.Name)
			continue
		}
		rel, err := qualify(d, t.Schema, t.Name)
		if err != nil {
			fmt.Fprintf(w, "-- SKIPPED %s.%s: %s\n", t.Schema, t.Name, sqlComment(err.Error()))
			skipped = append(skipped, t.Name)
			continue
		}
		plan = append(plan, dumpTable{table: t, detail: detail, rel: rel})
	}
	plan = orderByDependency(plan)

	// Every DROP first, parents last, so a table is never dropped while a child
	// still points at it.
	fmt.Fprintf(w, "\n")
	for i := len(plan) - 1; i >= 0; i-- {
		fmt.Fprintf(w, "%s;\n", dropTableStatement(driver, plan[i].rel))
	}

	var (
		dumped  int
		rowsAll int64
	)
	for _, pt := range plan {
		n, err := dumpOneTable(ctx, w, db, d, pt)
		if err != nil {
			// One unreadable table must not cost the operator the other forty.
			// The failure is recorded in the file and in the result, so a dump
			// that is missing something says which something.
			fmt.Fprintf(w, "\n-- SKIPPED %s: %s\n\n", pt.rel, sqlComment(err.Error()))
			skipped = append(skipped, pt.table.Name)
			continue
		}
		dumped++
		rowsAll += n
	}
	if w.err != nil {
		return nil, w.err
	}
	if err := f.Sync(); err != nil {
		return nil, err
	}
	ok = true

	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	out := fmt.Sprintf("%d tables, %d rows", dumped, rowsAll)
	if len(skipped) > 0 {
		out += fmt.Sprintf("; skipped %d (%s)", len(skipped), strings.Join(skipped, ", "))
	}
	return &DumpResult{
		Path: path, Size: st.Size(), Driver: driver, Database: database,
		Duration:  time.Since(start).Round(time.Millisecond).String(),
		StartedAt: start.UTC(), Summary: out, Output: out,
	}, nil
}

// dumpTable is one table with everything the dump needs about it read once.
type dumpTable struct {
	table  Table
	detail *TableDetail
	rel    string
}

// orderByDependency sorts tables so a referenced table comes before the tables
// referencing it. A cycle — two tables pointing at each other, which several
// engines allow — cannot be ordered, so those tables keep their original
// position rather than being dropped from the dump; the restore of a cyclic
// schema needs the constraints relaxed, and losing the data would be worse than
// needing a hand with the constraints.
func orderByDependency(tables []dumpTable) []dumpTable {
	byName := map[string]int{}
	for i, t := range tables {
		byName[strings.ToLower(t.table.Name)] = i
	}
	const (
		unvisited = 0
		visiting  = 1
		done      = 2
	)
	state := make([]int, len(tables))
	out := make([]dumpTable, 0, len(tables))
	var visit func(i int)
	visit = func(i int) {
		if state[i] != unvisited {
			return
		}
		state[i] = visiting
		for _, fk := range tables[i].detail.ForeignKeys {
			j, ok := byName[strings.ToLower(fk.RefTable)]
			if !ok || j == i {
				// A self-reference orders fine on its own, and a reference out
				// of this schema is not this dump's to satisfy.
				continue
			}
			if state[j] == visiting {
				continue // cycle
			}
			visit(j)
		}
		state[i] = done
		out = append(out, tables[i])
	}
	for i := range tables {
		visit(i)
	}
	return out
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

// dumpOneTable writes the DDL and every row of one table, and returns the row
// count. Rows are streamed: the whole point of not buffering the result is that
// a table larger than memory is exactly the table worth backing up.
func dumpOneTable(ctx context.Context, w *countingWriter, db *sql.DB, d Dialect, pt dumpTable) (int64, error) {
	rel := pt.rel
	ddl := strings.TrimSpace(pt.detail.CreateSQL)
	if ddl == "" {
		ddl = synthCreateTable(d, pt.table.Schema, pt.table.Name, pt.detail)
	}
	// Oracle's DBMS_METADATA and ClickHouse's SHOW CREATE both hand back text
	// that may already be terminated; a doubled semicolon is an empty statement
	// on some engines and a syntax error on others.
	ddl = strings.TrimRight(strings.TrimSpace(ddl), ";\n\r\t ")

	fmt.Fprintf(w, "\n-- %s\n", rel)
	for _, seq := range sequencesBehind(d.Driver(), pt.detail) {
		// A serial column's default is nextval() against a sequence the table
		// owns — so dropping the table takes the sequence with it, and the
		// CREATE TABLE that follows references something that no longer exists.
		// pg_dump emits the sequence; the built-in dump has to as well.
		fmt.Fprintf(w, "CREATE SEQUENCE IF NOT EXISTS %s;\n", seq.sequence)
	}
	fmt.Fprintf(w, "%s;\n", ddl)

	rows, err := db.QueryContext(ctx, "SELECT * FROM "+rel)
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
		q, err := d.QuoteIdent(c)
		if err != nil {
			return 0, err
		}
		quoted[i] = q
	}
	// Which columns actually hold bytes.
	//
	// Several drivers hand back []byte for text: MySQL does it for every string
	// and for DECIMAL, so a dump that treated a byte slice as binary wrote every
	// email address as 0x75736572… — which reloads into a VARCHAR as the hex
	// digits, and into a DECIMAL not at all. The column's declared type is the
	// only thing that can tell the two apart.
	binaryCol := make([]bool, len(cols))
	if types, err := rows.ColumnTypes(); err == nil {
		for i, ct := range types {
			binaryCol[i] = isBinaryTypeName(ct.DatabaseTypeName())
		}
	}
	prefix := "INSERT INTO " + rel + " (" + strings.Join(quoted, ", ") + ") VALUES "

	var (
		count   int64
		batch   []string
		batchSz int
	)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		// Oracle has no multi-row VALUES list; every other engine here does,
		// and one statement per row would multiply a large restore by the
		// round-trip time.
		if d.Driver() == DriverOracle {
			for _, tuple := range batch {
				fmt.Fprintf(w, "%s%s;\n", prefix, tuple)
			}
		} else {
			fmt.Fprintf(w, "%s%s;\n", prefix, strings.Join(batch, ", "))
		}
		batch, batchSz = batch[:0], 0
	}
	for rows.Next() {
		vals, err := scanRawRow(rows, len(cols))
		if err != nil {
			return count, err
		}
		parts := make([]string, len(vals))
		for i, v := range vals {
			parts[i] = dumpValue(d.Driver(), v, binaryCol[i])
		}
		tuple := "(" + strings.Join(parts, ", ") + ")"
		batch = append(batch, tuple)
		batchSz += len(tuple)
		count++
		if len(batch) >= genericDumpRowsPerStatement || batchSz >= genericDumpStatementBytes {
			flush()
		}
	}
	if err := rows.Err(); err != nil {
		return count, err
	}
	flush()

	for _, seq := range sequencesBehind(d.Driver(), pt.detail) {
		col, err := d.QuoteIdent(seq.column)
		if err != nil {
			continue
		}
		// Rows restored with explicit ids leave the sequence at 1, so the next
		// insert the application makes collides with the first row in the table.
		// The third argument is is_called: false for an empty table, so the
		// sequence still hands out 1.
		fmt.Fprintf(w, "SELECT setval('%s', GREATEST(COALESCE((SELECT MAX(%s) FROM %s), 0), 1), "+
			"(SELECT COUNT(*) FROM %s) > 0);\n",
			strings.ReplaceAll(seq.sequence, "'", "''"), col, rel, rel)
	}
	return count, nil
}

// sequenceDefault is a column whose default draws from a sequence.
type sequenceDefault struct {
	column   string
	sequence string
}

// nextvalRe pulls the sequence out of a Postgres serial column's default, which
// is stored as nextval('public.things_id_seq'::regclass).
var nextvalRe = regexp.MustCompile(`nextval\(\s*'([^']+)'`)

func sequencesBehind(driver Driver, detail *TableDetail) []sequenceDefault {
	if driver != DriverPostgres {
		// Every other engine here spells this as a column property — AUTO_INCREMENT,
		// IDENTITY, AUTOINCREMENT — which travels with the CREATE TABLE.
		return nil
	}
	out := []sequenceDefault{}
	for _, c := range detail.Columns {
		if m := nextvalRe.FindStringSubmatch(c.Default); m != nil {
			out = append(out, sequenceDefault{column: c.Name, sequence: m[1]})
		}
	}
	return out
}

// restoreGenericSQL replays a dump this package wrote.
func restoreGenericSQL(ctx context.Context, driver Driver, dsn, database, path string) (string, error) {
	d, err := DialectFor(driver)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
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

	stmts := splitSQLStatements(driver, string(data))
	var ran, dropped int
	for _, stmt := range stmts {
		if _, err := conn.ExecContext(ctx, stmt); err != nil {
			// A DROP that fails is the table not being there yet, which is the
			// ordinary case for a restore into an empty database. Everything
			// else stops: half a restore is not a restore, and continuing past
			// a failed CREATE would fill the *previous* table with these rows.
			if isDropStatement(stmt) {
				dropped++
				continue
			}
			return "", fmt.Errorf("statement %d failed: %w\n%s", ran+1, err, truncateForMessage(stmt))
		}
		ran++
	}
	out := fmt.Sprintf("%d statements applied", ran)
	if dropped > 0 {
		out += fmt.Sprintf(", %d drops skipped (nothing to drop)", dropped)
	}
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
	return s[:max] + "…"
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

// resolveDumpSchema decides what to pass as the catalogue's "schema" for a dump
// of a whole database.
//
// The two words mean different things per engine and the difference is not
// cosmetic. On ClickHouse the thing the UI calls a database *is* the schema, so
// passing it is how the dump is scoped at all. On Postgres, MySQL and SQL Server
// the database is chosen by the connection string and an empty schema means
// every schema in it — which is what a database dump should contain.
//
// Oracle is the one that cannot be decided statically, and getting it wrong is
// silent. Its schemas are users, but the name in a connection string is the
// *service* — "FREEPDB1" — so treating that as a schema filtered `all_tables`
// down to an owner nobody has and produced a dump of zero tables that reported
// success. An empty file that claims to be a backup is the worst thing this
// code can produce, so the name is checked against the catalogue and falls back
// to every schema the login can see.
func resolveDumpSchema(ctx context.Context, db *sql.DB, driver Driver, database string) string {
	switch driver {
	case DriverClickHouse:
		return database
	case DriverOracle:
		if database == "" {
			return ""
		}
		var n int
		err := db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM all_tables WHERE owner = :1`, database).Scan(&n)
		if err == nil && n > 0 {
			return database
		}
		return ""
	default:
		return ""
	}
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

// --- literals -------------------------------------------------------------

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

func dumpLiteral(driver Driver, v any) string {
	if v == nil {
		return "NULL"
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

func dumpString(driver Driver, s string) string {
	if strings.ContainsRune(s, 0) || !utf8.ValidString(s) {
		return dumpBytes(driver, []byte(s))
	}
	escaped := strings.ReplaceAll(s, "'", "''")
	if backslashEscapes(driver) {
		escaped = strings.ReplaceAll(s, `\`, `\\`)
		escaped = strings.ReplaceAll(escaped, "'", "''")
	}
	if driver == DriverMSSQL {
		// N'' is what keeps a non-ASCII character intact on the way in: an
		// unprefixed literal is read in the database's code page first and only
		// then widened, so anything outside it is already gone.
		return "N'" + escaped + "'"
	}
	return "'" + escaped + "'"
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
	switch driver {
	case DriverOracle:
		// Oracle will not read an ISO string as a date without being told the
		// format, and its NLS settings are per session — so the format travels
		// with the value rather than being assumed.
		return "TO_TIMESTAMP('" + t.UTC().Format("2006-01-02 15:04:05.999999") +
			"', 'YYYY-MM-DD HH24:MI:SS.FF')"
	case DriverClickHouse:
		// ClickHouse's DateTime has second resolution and rejects a fractional
		// part; DateTime64 accepts this form too.
		return "'" + t.UTC().Format("2006-01-02 15:04:05") + "'"
	default:
		return "'" + t.UTC().Format("2006-01-02 15:04:05.999999") + "'"
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

// scanRawRow reads one row without the normalisation the browse and export
// paths apply. Those turn a byte string into a printable "\x…" preview for a
// grid cell, which is the right answer on screen and the wrong one in a backup:
// the dump has to carry the bytes, not a description of them.
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
	var (
		out  []string
		cur  strings.Builder
		i    int
		n    = len(text)
		bs   = backslashEscapes(driver)
		idOK = identifierQuotes(driver)
	)
	flush := func() {
		s := strings.TrimSpace(cur.String())
		cur.Reset()
		if s != "" {
			out = append(out, s)
		}
	}
	for i < n {
		c := text[i]
		switch {
		case c == '-' && i+1 < n && text[i+1] == '-':
			for i < n && text[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < n && text[i+1] == '*':
			i += 2
			for i+1 < n && !(text[i] == '*' && text[i+1] == '/') {
				i++
			}
			i = min(i+2, n)
		case c == '\'':
			cur.WriteByte(c)
			i++
			for i < n {
				if bs && text[i] == '\\' && i+1 < n {
					cur.WriteString(text[i : i+2])
					i += 2
					continue
				}
				if text[i] == '\'' {
					if i+1 < n && text[i+1] == '\'' {
						cur.WriteString("''")
						i += 2
						continue
					}
					cur.WriteByte('\'')
					i++
					break
				}
				cur.WriteByte(text[i])
				i++
			}
		case idOK[c] != 0:
			closer := idOK[c]
			cur.WriteByte(c)
			i++
			for i < n {
				if text[i] == closer {
					if i+1 < n && text[i+1] == closer {
						cur.WriteByte(closer)
						cur.WriteByte(closer)
						i += 2
						continue
					}
					cur.WriteByte(closer)
					i++
					break
				}
				cur.WriteByte(text[i])
				i++
			}
		case c == ';':
			flush()
			i++
		default:
			cur.WriteByte(c)
			i++
		}
	}
	flush()
	return out
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
