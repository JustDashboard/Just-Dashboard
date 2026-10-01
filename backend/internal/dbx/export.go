package dbx

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// ExportFormat is a wire format a result set can be downloaded as.
type ExportFormat string

const (
	ExportCSV  ExportFormat = "csv"
	ExportJSON ExportFormat = "json"
	// ExportNDJSON is one object per line, which is what streams into another
	// tool without that tool holding the whole array first.
	ExportNDJSON ExportFormat = "ndjson"
	// ExportSQL is INSERT statements in the dialect the rows came from.
	ExportSQL ExportFormat = "sql"
	ExportTSV ExportFormat = "tsv"
)

// ExportFormats lists every format in the order the UI offers them.
func ExportFormats() []ExportFormat {
	return []ExportFormat{ExportCSV, ExportTSV, ExportJSON, ExportNDJSON, ExportSQL}
}

func (f ExportFormat) Valid() bool {
	switch f {
	case ExportCSV, ExportJSON, ExportNDJSON, ExportSQL, ExportTSV:
		return true
	}
	return false
}

// ContentType and Extension keep the HTTP header and the download filename in
// step with the format in one place.
func (f ExportFormat) ContentType() string {
	switch f {
	case ExportJSON:
		return "application/json"
	case ExportNDJSON:
		return "application/x-ndjson"
	case ExportSQL:
		return "application/sql"
	case ExportTSV:
		return "text/tab-separated-values"
	}
	return "text/csv"
}

func (f ExportFormat) Extension() string { return string(f) }

const (
	// DefaultExportRows is the cap when the caller names none.
	DefaultExportRows = 100_000
	// MaxExportRows is the most one export will write whatever was asked for.
	MaxExportRows = 1_000_000
)

// ClampExportRows turns a requested row limit into the one that will be used.
// A limit past the ceiling is the ceiling: it used to fall back to the default,
// so asking for two million rows produced a tenth of what asking for one
// million did.
func ClampExportRows(n int) int {
	switch {
	case n <= 0:
		return DefaultExportRows
	case n > MaxExportRows:
		return MaxExportRows
	}
	return n
}

// ExportStatus is how an export ended.
type ExportStatus string

const (
	ExportComplete ExportStatus = "complete"
	// ExportTruncated is a file that holds the first rows up to the limit and
	// no more. It is a whole, well-formed file, which is exactly why it has to
	// say so: nothing else about it looks unfinished.
	ExportTruncated ExportStatus = "truncated"
	ExportFailed    ExportStatus = "failed"
)

// ExportMarkerKey is the one key of the object a JSON or NDJSON export ends
// with when it is not complete. An import skips an object that carries only
// this key rather than loading it as a row.
const ExportMarkerKey = "__export"

// ExportOptions is everything about an export that is not which rows.
type ExportOptions struct {
	Format  ExportFormat
	MaxRows int
	// Columns keeps only these result columns, in this order. Empty keeps all.
	Columns []string
	// Driver, Schema and Table are what the SQL format writes its INSERT
	// statements for. The other formats ignore them.
	Driver Driver
	Schema string
	Table  string
}

// exportQueryer is the part of *sql.DB and *sql.Tx an export reads through.
type exportQueryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// StreamExport runs a query and writes its rows straight to w in the chosen
// format, one row at a time, rather than materialising the whole result in
// memory the way RunQuery does. A table export can be far larger than anything
// worth holding in a browser, so the export path and the browse path are
// deliberately different: browse caps and buffers, export streams and does not.
//
// The row cap still exists — an unbounded export from a dashboard is a foot-gun
// — but it is high, and hitting it is reported to the caller so a truncated file
// is never mistaken for a complete one.
func StreamExport(ctx context.Context, db *sql.DB, query string, args []any, format ExportFormat, w io.Writer, maxRows int) (int, bool, error) {
	return streamExport(ctx, db, query, args, ExportOptions{Format: format, MaxRows: maxRows}, w)
}

// streamExport is the one loop every export goes through.
//
// Nothing is written until the statement has run and named its columns, so a
// statement the engine refuses is an error the caller can still answer with,
// not half a file. Past that point a failure is written into the file where the
// format has somewhere to put it (see exportEncoder.finish) and returned.
func streamExport(ctx context.Context, q exportQueryer, query string, args []any, opts ExportOptions, w io.Writer) (int, bool, error) {
	if !opts.Format.Valid() {
		return 0, false, fmt.Errorf("unsupported export format %q", opts.Format)
	}
	maxRows := ClampExportRows(opts.MaxRows)
	args, err := sqlArguments(args)
	if err != nil {
		return 0, false, err
	}
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return 0, false, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return 0, false, err
	}
	keep, names, err := projectColumns(cols, opts.Columns)
	if err != nil {
		return 0, false, err
	}
	binary := make([]bool, len(cols))
	typeNames := make([]string, len(cols))
	if types, err := rows.ColumnTypes(); err == nil {
		for i, ct := range types {
			typeNames[i] = ct.DatabaseTypeName()
			binary[i] = isBinaryTypeName(typeNames[i])
		}
	}
	enc, err := newExportEncoder(opts, w, names, pick(binary, keep), pick(typeNames, keep))
	if err != nil {
		return 0, false, err
	}
	if err := enc.begin(); err != nil {
		return 0, false, err
	}

	count, truncated := 0, false
	fail := func(err error) (int, bool, error) {
		// Best effort: the writer may be the thing that failed.
		_ = enc.finish(ExportFailed, count, err.Error())
		return count, truncated, err
	}
	picked := make([]any, len(keep))
	for rows.Next() {
		if count >= maxRows {
			truncated = true
			break
		}
		vals, err := scanRawRow(rows, len(cols))
		if err != nil {
			return fail(err)
		}
		for i, k := range keep {
			picked[i] = vals[k]
		}
		if err := enc.row(picked); err != nil {
			return fail(err)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return fail(err)
	}
	status := ExportComplete
	if truncated {
		status = ExportTruncated
	}
	if err := enc.finish(status, count, ""); err != nil {
		return count, truncated, err
	}
	return count, truncated, nil
}

// projectColumns resolves a projection against a result's columns. A name the
// result does not carry is an error rather than a column silently left out: a
// file missing the column somebody asked for looks like a file whose column
// was empty.
func projectColumns(cols, want []string) ([]int, []string, error) {
	if len(want) == 0 {
		keep := make([]int, len(cols))
		for i := range cols {
			keep[i] = i
		}
		return keep, cols, nil
	}
	index := make(map[string]int, len(cols))
	for i, c := range cols {
		if _, dup := index[c]; !dup {
			index[c] = i
		}
	}
	keep := make([]int, 0, len(want))
	names := make([]string, 0, len(want))
	for _, name := range want {
		i, ok := index[name]
		if !ok {
			return nil, nil, fmt.Errorf("no column %q in this result", name)
		}
		keep = append(keep, i)
		names = append(names, cols[i])
	}
	return keep, names, nil
}

func pick[T any](all []T, keep []int) []T {
	out := make([]T, len(keep))
	for i, k := range keep {
		out[i] = all[k]
	}
	return out
}

// ExportBrowse streams the rows the grid is showing — the same relation, the
// same conditions, the same order, without the page — to w.
//
// It goes through the same assembly as Browse rather than re-deriving the
// statement, which is what keeps the two honest with each other: the export
// used to be an unconditional SELECT * of the table, so a filtered view and its
// download disagreed about everything but the column names. The relation and
// every identifier still come from validated, quoted identifiers, and the
// conditions still travel as bound arguments.
func ExportBrowse(ctx context.Context, db *sql.DB, driver Driver, opts BrowseOptions, format ExportFormat, w io.Writer, maxRows int) (int, bool, error) {
	return ExportSelection(ctx, db, driver, opts, ExportOptions{Format: format, MaxRows: maxRows}, w)
}

// ExportSelection is ExportBrowse with everything an export can be asked for:
// a projection, and the INSERT form, which is written for the table the rows
// were read from.
func ExportSelection(ctx context.Context, db *sql.DB, driver Driver, browse BrowseOptions, opts ExportOptions, w io.Writer) (int, bool, error) {
	d, err := DialectFor(driver)
	if err != nil {
		return 0, false, err
	}
	if err := guardCredentials(ctx, db, d, browse.Schema, browse.Table); err != nil {
		return 0, false, err
	}
	// What the grid's page read asks of the catalogue, the export asks too:
	// Oracle has to be told how to read a date a filter compares with, and
	// its driver cannot read every column as SELECT * returns it. Nothing is
	// asked of any other engine.
	browse = withCatalog(ctx, db, d, browse)
	sel, err := browseSelect(d, browse)
	if err != nil {
		return 0, false, err
	}
	opts.Driver, opts.Schema, opts.Table = driver, browse.Schema, browse.Table
	return streamExport(ctx, db, sel.query, sel.args, opts, w)
}

// ExportTable streams an entire table to w, unfiltered and unordered.
func ExportTable(ctx context.Context, db *sql.DB, driver Driver, schema, table string, format ExportFormat, w io.Writer, maxRows int) (int, bool, error) {
	return ExportBrowse(ctx, db, driver, BrowseOptions{Schema: schema, Table: table}, format, w, maxRows)
}

// ExportQuery streams the result of one statement the operator wrote.
//
// The caller has already established that the statement reads: this function
// does not classify. What it adds is the engine's own word for it where the
// engine has one — a read-only transaction on Postgres and MySQL — so a
// statement the classifier read as a SELECT and the server reads as a write
// is refused by the server rather than run.
func ExportQuery(ctx context.Context, db *sql.DB, driver Driver, statement string, opts ExportOptions, w io.Writer) (int, bool, error) {
	opts.Driver = driver
	if driver != DriverPostgres && driver != DriverMySQL {
		return streamExport(ctx, db, statement, nil, opts, w)
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return 0, false, err
	}
	// Nothing was written, so there is nothing a commit would keep.
	defer tx.Rollback()
	return streamExport(ctx, tx, statement, nil, opts, w)
}

// exportValue is normaliseValue for a file rather than for a grid cell.
//
// The grid cuts a binary value to a preview, which is right on screen and wrong
// in an export: a file whose blob column holds "\x89504e47… (48211 bytes)" has
// lost the blob and says so only to somebody who opens it. Here the whole value
// goes out, in the same \x notation, and everything else is rendered exactly as
// the grid renders it so the two cannot disagree about a number or a date.
func exportValue(v any) any {
	if b, ok := v.([]byte); ok {
		if isPrintableText(b) {
			return string(b)
		}
		return `\x` + hex.EncodeToString(b)
	}
	return normaliseValue(v)
}

// exportValueOf is exportValue for a value whose column type is known, for
// the two SQL Server types its driver hands back as something they are not.
//
// A uniqueidentifier arrives as sixteen bytes in the order the server stores
// them, which written as bytes is neither the identifier anybody knows it by
// nor anything the column takes back. A time of day arrives as that time on
// the first of January of the year one.
func exportValueOf(v any, typeName string) any {
	switch x := v.(type) {
	case []byte:
		if typeName == "UNIQUEIDENTIFIER" && len(x) == 16 {
			return exportGUID(x)
		}
	case time.Time:
		if typeName == "TIME" {
			return x.Format("15:04:05.999999999")
		}
	}
	return exportValue(v)
}

// exportGUID spells a uniqueidentifier from its stored bytes. The first three
// groups are kept low byte first; the rest are kept as written.
func exportGUID(b []byte) string {
	return fmt.Sprintf("%02X%02X%02X%02X-%02X%02X-%02X%02X-%02X%02X-%02X%02X%02X%02X%02X%02X",
		b[3], b[2], b[1], b[0], b[5], b[4], b[7], b[6], b[8], b[9], b[10], b[11], b[12], b[13], b[14], b[15])
}

// exportEncoder writes one format. begin writes whatever precedes the first
// row, row one row of raw scanned values, and finish whatever follows the last
// — including, where the format can carry one, the line that says the file is
// not everything.
type exportEncoder interface {
	begin() error
	row(vals []any) error
	finish(status ExportStatus, rows int, reason string) error
}

func newExportEncoder(opts ExportOptions, w io.Writer, cols []string, binary []bool, types []string) (exportEncoder, error) {
	switch opts.Format {
	case ExportCSV, ExportTSV:
		cw := csv.NewWriter(w)
		if opts.Format == ExportTSV {
			cw.Comma = '\t'
		}
		return &delimitedEncoder{w: cw, cols: cols, types: types, rec: make([]string, len(cols))}, nil
	case ExportJSON, ExportNDJSON:
		keys := make([][]byte, len(cols))
		for i, c := range cols {
			k, err := json.Marshal(c)
			if err != nil {
				return nil, err
			}
			keys[i] = k
		}
		return &jsonEncoder{w: w, keys: keys, types: types, lines: opts.Format == ExportNDJSON}, nil
	case ExportSQL:
		return newSQLEncoder(opts, w, cols, binary, types)
	}
	return nil, fmt.Errorf("unsupported export format %q", opts.Format)
}

// delimitedEncoder is CSV and TSV. Neither has anywhere to put a remark that a
// reader would not take for a row, so how the export ended travels beside the
// file rather than in it.
type delimitedEncoder struct {
	w     *csv.Writer
	cols  []string
	types []string
	rec   []string
}

func (e *delimitedEncoder) begin() error { return e.w.Write(e.cols) }

func (e *delimitedEncoder) row(vals []any) error {
	for i, v := range vals {
		e.rec[i] = csvCell(exportValueOf(v, e.types[i]))
	}
	return e.w.Write(e.rec)
}

func (e *delimitedEncoder) finish(ExportStatus, int, string) error {
	e.w.Flush()
	return e.w.Error()
}

// csvCell renders a value as a spreadsheet-friendly string. A NULL becomes an
// empty field (distinct from the empty string only in JSON, which CSV cannot
// express); nested objects and arrays are JSON-encoded so a cell is never a
// Go-syntax "map[...]".
func csvCell(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case int64:
		return strconv.FormatInt(t, 10)
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return fmt.Sprintf("%v", t)
		}
		return string(b)
	}
}

// jsonEncoder writes an array of objects, or one object per line. Keys keep
// the result's column order: an object whose keys were sorted reads as a
// different table from the one on screen.
//
// A file that is complete carries rows and nothing else. One that is not ends
// with an object holding only ExportMarkerKey, which is the one place in a JSON
// document a remark can go without making it something a parser refuses.
type jsonEncoder struct {
	w     io.Writer
	keys  [][]byte
	types []string
	lines bool
	buf   bytes.Buffer
	wrote bool
}

func (e *jsonEncoder) begin() error {
	if e.lines {
		return nil
	}
	_, err := io.WriteString(e.w, "[\n")
	return err
}

func (e *jsonEncoder) row(vals []any) error {
	e.buf.Reset()
	e.buf.WriteByte('{')
	for i, v := range vals {
		if i > 0 {
			e.buf.WriteByte(',')
		}
		e.buf.Write(e.keys[i])
		e.buf.WriteByte(':')
		b, err := json.Marshal(exportValueOf(v, e.types[i]))
		if err != nil {
			return err
		}
		e.buf.Write(b)
	}
	e.buf.WriteByte('}')
	return e.object(e.buf.Bytes())
}

func (e *jsonEncoder) object(b []byte) error {
	if e.lines {
		if _, err := e.w.Write(b); err != nil {
			return err
		}
		_, err := io.WriteString(e.w, "\n")
		return err
	}
	if e.wrote {
		if _, err := io.WriteString(e.w, ",\n"); err != nil {
			return err
		}
	}
	e.wrote = true
	_, err := e.w.Write(b)
	return err
}

func (e *jsonEncoder) finish(status ExportStatus, rows int, reason string) error {
	if status != ExportComplete {
		marker := map[string]any{"status": status, "rows": rows}
		if reason != "" {
			marker["error"] = reason
		}
		b, err := json.Marshal(map[string]any{ExportMarkerKey: marker})
		if err != nil {
			return err
		}
		if err := e.object(b); err != nil {
			return err
		}
	}
	if e.lines {
		return nil
	}
	tail := "]\n"
	if e.wrote {
		tail = "\n]\n"
	}
	_, err := io.WriteString(e.w, tail)
	return err
}

// sqlEncoder writes INSERT statements for the engine the rows came from, with
// the literal renderer the built-in dump and copy-as-SQL use, from the values
// as the driver scanned them rather than from their on-screen form.
//
// SQL has comments, so this is the one format that always says how it ended.
type sqlEncoder struct {
	w      io.Writer
	driver Driver
	rel    string
	binary []bool
	types  []string
	batch  *insertBatcher
}

func newSQLEncoder(opts ExportOptions, w io.Writer, cols []string, binary []bool, types []string) (*sqlEncoder, error) {
	d, err := DialectFor(opts.Driver)
	if err != nil {
		return nil, fmt.Errorf("the SQL format is written for a SQL engine: %w", err)
	}
	table := opts.Table
	if strings.TrimSpace(table) == "" {
		return nil, fmt.Errorf("the SQL format needs the name of the table its statements insert into")
	}
	rel, err := qualify(d, opts.Schema, table)
	if err != nil {
		return nil, err
	}
	quoted := make([]string, len(cols))
	for i, c := range cols {
		q, err := d.QuoteIdent(c)
		if err != nil {
			return nil, err
		}
		quoted[i] = q
	}
	return &sqlEncoder{
		w: w, driver: opts.Driver, rel: rel, binary: binary, types: types,
		batch: newInsertBatcher(w, opts.Driver, rel, quoted, exportRowsPerStatement),
	}, nil
}

// exportRowsPerStatement is smaller than the dump's: an export is read and
// pasted in pieces more often than it is replayed whole.
const exportRowsPerStatement = 100

func (e *sqlEncoder) begin() error {
	_, err := fmt.Fprintf(e.w, "-- Just Dashboard export\n-- engine: %s\n-- table: %s\n-- taken: %s\n\n",
		e.driver, sqlComment(e.rel), time.Now().UTC().Format(time.RFC3339))
	return err
}

func (e *sqlEncoder) row(vals []any) error {
	return e.batch.addRow(vals, e.binary, e.types)
}

func (e *sqlEncoder) finish(status ExportStatus, rows int, reason string) error {
	if err := e.batch.flush(); err != nil {
		return err
	}
	line := fmt.Sprintf("\n-- export %s: %d rows", status, rows)
	switch status {
	case ExportTruncated:
		line = fmt.Sprintf("\n-- export truncated at %d rows: the table holds more than the limit", rows)
	case ExportFailed:
		line = fmt.Sprintf("\n-- export failed after %d rows: %s", rows, sqlComment(reason))
	}
	_, err := io.WriteString(e.w, line+"\n")
	return err
}
