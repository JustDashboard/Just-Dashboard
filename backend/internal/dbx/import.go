package dbx

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Importing is the mirror of exporting, and the asymmetry between them is the
// interesting part: an export cannot fail halfway in a way that matters, and an
// import can. Half-loaded data is worse than none, because the operator cannot
// tell by looking which half arrived.
//
// So the whole load runs inside one transaction and either commits or rolls
// back. The cost is that a very large import holds a transaction open; the
// alternative — committing in batches — turns a failed import into a
// reconciliation job, which is not a trade a dashboard should make on the
// operator's behalf.
//
// The file is read as a stream and written in batches: nothing here holds more
// of it than the rows of one batch and, at the start, the sample the columns
// are worked out from.

// ImportOptions describes how to read the incoming data and where to put it.
// It is what the inline import route has always taken; ImportSpec is the
// larger set the upload route takes.
type ImportOptions struct {
	Schema string
	Table  string
	// Columns names the target columns in file order. Empty means "use the
	// CSV header", which is the common case; a JSON import ignores it and uses
	// each object's own keys.
	Columns []string
	// HasHeader treats the first CSV record as names rather than data.
	HasHeader bool
	// Truncate empties the table first. It is a separate, explicitly requested
	// step rather than an implied part of "import", because replacing a table's
	// contents and adding to them are different intentions.
	Truncate bool
	// StopOnError aborts at the first bad row. With it off, bad rows are
	// counted and reported and the good ones still land — but only if the
	// whole transaction commits, so "skipped" never means "partially applied".
	StopOnError bool
	// NullAs is the literal that means SQL NULL rather than an empty string.
	// CSV cannot distinguish the two, so the caller says which it meant.
	NullAs string
}

// ImportResult reports what happened, with a bounded sample of the failures —
// a file with fifty thousand bad rows should not answer with fifty thousand
// error strings.
type ImportResult struct {
	Inserted  int      `json:"inserted"`
	Failed    int      `json:"failed"`
	Errors    []string `json:"errors"`
	Truncated bool     `json:"errorsTruncated"`
	Statement string   `json:"statement"`
}

const maxImportErrors = 20

// ImportMode is what an import does with the rows already in the table.
type ImportMode string

const (
	// ImportModeInsert adds the file's rows to what is there.
	ImportModeInsert ImportMode = "insert"
	// ImportModeUpsert adds the rows that are new and overwrites the ones
	// whose key is already there.
	ImportModeUpsert ImportMode = "upsert"
	// ImportModeReplace empties the table first.
	ImportModeReplace ImportMode = "replace"
)

func (m ImportMode) Valid() bool {
	return m == ImportModeInsert || m == ImportModeUpsert || m == ImportModeReplace
}

const (
	// importSampleRows is how many rows are read before anything is written:
	// the rows a column's kind is worked out from, and a dry run's whole view
	// of the file.
	importSampleRows = 1000
	// importPreviewRows is how many of them a dry run shows.
	importPreviewRows = 20
	// DefaultImportBatch and MaxImportBatch bound the rows written by one
	// statement, or covered by one savepoint.
	DefaultImportBatch = 500
	MaxImportBatch     = 5000
	// MaxImportErrors is the most row errors a report carries however many
	// were asked for.
	MaxImportErrors = 200
)

// ImportCreate asks for the table to be made from the file.
type ImportCreate struct {
	// Columns overrides what would be inferred, by column name: a type, a NOT
	// NULL, a primary key. A column not named here takes the inferred type.
	Columns []NewColumn
}

// ImportSpec is everything an import can be told.
type ImportSpec struct {
	Schema string
	Table  string
	Format ImportFormat
	// Delimiter separates fields in a delimited file: one character. Empty is
	// a comma, or a tab for TSV.
	Delimiter string
	// Quote is the character a field is wrapped in. Nil is a double quote; a
	// pointer to the empty string means the file quotes nothing.
	Quote *string
	// Header says whether the first record names the columns. Nil is yes.
	Header   *bool
	Encoding string
	// NullToken is the unquoted text that means NULL. Nil is the empty field:
	// a,,c has a NULL in the middle and a,"",c an empty string.
	NullToken *string
	// Mapping sends a source column to a table column. A source column mapped
	// to "" is left out; with no mapping at all, columns are matched by name.
	Mapping map[string]string
	// Columns names the target of each source column by position, for a file
	// with no header.
	Columns []string
	Mode    ImportMode
	// ConflictColumns or ConflictConstraint name the key an upsert matches on;
	// neither means the primary key.
	ConflictColumns    []string
	ConflictConstraint string
	// SkipBadRows leaves a row the table refuses out and carries on. Without
	// it the first such row ends the import and nothing is written.
	SkipBadRows bool
	BatchSize   int
	// DryRun reads the start of the file, works out what would happen, and
	// writes nothing.
	DryRun bool
	// Create makes the table rather than expecting it.
	Create    *ImportCreate
	MaxErrors int

	// trusted skips the catalogue: the columns are the ones the file names
	// and every value is bound as the text it is. This is the inline route's
	// behaviour, kept for it.
	trusted bool
	// nullAs is that route's NULL rule: this exact text, quoted or not, and
	// nothing when it is empty.
	nullAs string
	// sampleAll reads the whole file into the sample, which is only ever
	// right for a body already held in memory.
	sampleAll bool
	// sortKeys orders a JSON file's keys by name rather than as found.
	sortKeys bool
	// keys are the only keys of a JSON file to take, each into the column of
	// its own name, present in the file or not.
	keys []string
}

// ImportColumn is one column of the file and where it goes.
type ImportColumn struct {
	// Source is the column's name in the file, or "column N" where the file
	// names none.
	Source string `json:"source"`
	Index  int    `json:"index"`
	// Target is the table column it is written to; empty when it is not.
	Target string `json:"target"`
	// Inferred is what its values look like in the rows sampled.
	Inferred   string   `json:"inferred"`
	TargetType string   `json:"targetType,omitempty"`
	Examples   []string `json:"examples"`
	// Warning says what the engine is likely to refuse, and on which value.
	Warning string `json:"warning,omitempty"`
}

// ImportRowError is one row that was not written.
type ImportRowError struct {
	// Row counts data rows from 1; Line is where the row starts in the file.
	Row     int    `json:"row"`
	Line    int    `json:"line"`
	Message string `json:"message"`
}

// ImportPreview is the first rows as they would be written: the table's
// columns, and each value as text or null.
type ImportPreview struct {
	Columns []string    `json:"columns"`
	Rows    [][]*string `json:"rows"`
}

// ImportCreated describes the table an import made, or would make.
type ImportCreated struct {
	Statement string      `json:"statement"`
	Columns   []NewColumn `json:"columns"`
	Created   bool        `json:"created"`
}

// ImportReport is what an import did, or — for a dry run — would do.
type ImportReport struct {
	DryRun   bool           `json:"dryRun"`
	Schema   string         `json:"schema"`
	Table    string         `json:"table"`
	Format   ImportFormat   `json:"format"`
	Encoding string         `json:"encoding"`
	Mode     ImportMode     `json:"mode"`
	Columns  []ImportColumn `json:"columns"`
	// RowsRead is how many rows the file held; for a dry run, how many of its
	// first rows were looked at.
	RowsRead int `json:"rowsRead"`
	Inserted int `json:"inserted"`
	// Updated counts rows an upsert found already there.
	Updated int `json:"updated"`
	// Skipped counts rows left out because they could not be read or the
	// table refused them.
	Skipped         int              `json:"skipped"`
	Errors          []ImportRowError `json:"errors"`
	ErrorsTruncated bool             `json:"errorsTruncated"`
	Preview         *ImportPreview   `json:"preview,omitempty"`
	Create          *ImportCreated   `json:"create,omitempty"`
	// Key is the columns an upsert matched on.
	Key       []string `json:"key,omitempty"`
	Statement string   `json:"statement"`
	// Atomic is false where the engine has no transaction to wrap the import
	// in: what was written before a failure stays written.
	Atomic   bool     `json:"atomic"`
	Warnings []string `json:"warnings"`
}

// ImportCSV loads delimited data into a table.
func ImportCSV(ctx context.Context, db *sql.DB, driver Driver, r io.Reader, opts ImportOptions) (*ImportResult, error) {
	return importLegacy(ctx, db, driver, r, opts, ImportFormatCSV)
}

// ImportJSON loads an array of objects. Each object's keys are matched against
// the column list, so a file whose objects carry extra keys still loads and the
// extras are ignored rather than failing the row.
func ImportJSON(ctx context.Context, db *sql.DB, driver Driver, r io.Reader, opts ImportOptions) (*ImportResult, error) {
	return importLegacy(ctx, db, driver, r, opts, ImportFormatJSON)
}

// importLegacy runs the inline route's import through the same engine, so
// what was fixed there — a skipped row no longer poisoning a Postgres
// transaction, an emptied table coming back when the import fails — is fixed
// here too, and answers in the shape that route has always answered in.
func importLegacy(ctx context.Context, db *sql.DB, driver Driver, r io.Reader, opts ImportOptions, format ImportFormat) (*ImportResult, error) {
	header := opts.HasHeader
	spec := ImportSpec{
		Schema: opts.Schema, Table: opts.Table, Format: format,
		Header: &header, Columns: opts.Columns,
		Mode: ImportModeInsert, SkipBadRows: !opts.StopOnError, MaxErrors: maxImportErrors,
		trusted: true, nullAs: opts.NullAs, sampleAll: true, sortKeys: true,
	}
	if opts.Truncate {
		spec.Mode = ImportModeReplace
	}
	if !format.delimited() {
		// For an array of objects the list is which keys to take, by name.
		spec.Columns, spec.keys = nil, opts.Columns
	}
	report, err := Import(ctx, db, driver, r, spec)
	if err != nil {
		var row *importRowFailure
		if errors.As(err, &row) {
			return nil, fmt.Errorf("row %d: %w", row.row, row.err)
		}
		return nil, err
	}
	res := &ImportResult{
		Inserted: report.Inserted, Failed: report.Skipped, Errors: []string{},
		Truncated: report.ErrorsTruncated, Statement: report.Statement,
	}
	for _, e := range report.Errors {
		res.Errors = append(res.Errors, fmt.Sprintf("row %d: %s", e.Row, e.Message))
	}
	return res, nil
}

// importRowFailure is the row that ended an import which was not told to skip
// bad rows.
type importRowFailure struct {
	row  int
	line int
	err  error
}

func (e *importRowFailure) Error() string {
	return fmt.Sprintf("row %d (line %d): %v — nothing was imported", e.row, e.line, e.err)
}

func (e *importRowFailure) Unwrap() error { return e.err }

// plannedColumn is one column an import writes.
type plannedColumn struct {
	source   int
	name     string
	quoted   string
	family   string
	typeName string
}

// importPlan is an import worked out: which fields go to which columns, and
// the statements that put them there.
type importPlan struct {
	d       Dialect
	spec    ImportSpec
	rel     string
	sources []string
	cols    []plannedColumn
	key     []string
	// keyAt are the positions in cols of the key columns, in key order.
	keyAt []int
}

// Import reads r and loads it into a table as spec says.
func Import(ctx context.Context, db *sql.DB, driver Driver, r io.Reader, spec ImportSpec) (*ImportReport, error) {
	d, err := DialectFor(driver)
	if err != nil {
		return nil, err
	}
	if err := normaliseImportSpec(&spec); err != nil {
		return nil, err
	}
	text, encoding, err := decodeText(r, spec.Encoding)
	if err != nil {
		return nil, err
	}
	reader, err := newRecordReader(text, spec)
	if err != nil {
		return nil, err
	}
	report := &ImportReport{
		DryRun: spec.DryRun, Schema: spec.Schema, Table: spec.Table, Format: spec.Format,
		Encoding: encoding, Mode: spec.Mode, Columns: []ImportColumn{}, Errors: []ImportRowError{},
		Warnings: []string{}, Atomic: driver != DriverClickHouse,
	}

	src, err := readImportSample(reader, spec, report)
	if err != nil {
		return nil, err
	}
	profiles := profileSample(src, spec)

	schema := spec.Schema
	if schema == "" && !spec.trusted {
		schema = currentSchema(ctx, db, d)
	}
	targets, create, err := importTargets(ctx, db, d, schema, src, profiles, spec)
	if err != nil {
		return nil, err
	}
	plan, err := planImport(ctx, db, d, schema, src, profiles, targets, spec, report)
	if err != nil {
		return nil, err
	}
	report.Create = create
	report.Key = plan.key
	if report.Statement, err = plan.rowStatement(1); err != nil {
		return nil, err
	}
	if spec.DryRun {
		report.RowsRead = len(src.sample)
		report.Preview = plan.preview(src.sample)
		return report, nil
	}
	if err := runImport(ctx, db, plan, src, reader, report); err != nil {
		return nil, err
	}
	if len(src.strays) > 0 && !spec.trusted {
		names := make([]string, 0, len(src.strays))
		for k := range src.strays {
			names = append(names, k)
		}
		sortStrings(names)
		report.Warnings = append(report.Warnings, fmt.Sprintf(
			"rows after the first %d carry keys the earlier ones did not, and those values were not imported: %s",
			importSampleRows, strings.Join(names, ", ")))
	}
	return report, nil
}

// normaliseImportSpec fills the defaults in and refuses what cannot be read.
func normaliseImportSpec(spec *ImportSpec) error {
	if strings.TrimSpace(spec.Table) == "" {
		return fmt.Errorf("a table is required")
	}
	if spec.Format == "" {
		spec.Format = ImportFormatCSV
	}
	if !spec.Format.Valid() {
		return fmt.Errorf("format %q is not one of csv, tsv, json, ndjson", spec.Format)
	}
	if spec.Mode == "" {
		spec.Mode = ImportModeInsert
	}
	if !spec.Mode.Valid() {
		return fmt.Errorf("mode %q is not one of insert, upsert, replace", spec.Mode)
	}
	if spec.Format.delimited() {
		if spec.Delimiter == "" {
			spec.Delimiter = ","
			if spec.Format == ImportFormatTSV {
				spec.Delimiter = "\t"
			}
		}
		if utf8.RuneCountInString(spec.Delimiter) != 1 || strings.ContainsAny(spec.Delimiter, "\r\n") {
			return fmt.Errorf("the delimiter is one character, and not a line break")
		}
		if spec.Quote != nil && *spec.Quote != "" {
			if utf8.RuneCountInString(*spec.Quote) != 1 || strings.ContainsAny(*spec.Quote, "\r\n") {
				return fmt.Errorf("the quote is one character, and not a line break")
			}
			if *spec.Quote == spec.Delimiter {
				return fmt.Errorf("the quote and the delimiter are the same character")
			}
		}
	}
	switch {
	case spec.BatchSize <= 0:
		spec.BatchSize = DefaultImportBatch
	case spec.BatchSize > MaxImportBatch:
		spec.BatchSize = MaxImportBatch
	}
	switch {
	case spec.MaxErrors <= 0:
		spec.MaxErrors = maxImportErrors
	case spec.MaxErrors > MaxImportErrors:
		spec.MaxErrors = MaxImportErrors
	}
	if spec.Create != nil && spec.Mode != ImportModeInsert {
		return fmt.Errorf("a table that is being created has nothing to %s", spec.Mode)
	}
	if !spec.Format.delimited() && len(spec.Columns) > 0 {
		return fmt.Errorf("columns names targets by position, which a %s file does not have; use a mapping", spec.Format)
	}
	if (spec.ConflictConstraint != "" || len(spec.ConflictColumns) > 0) && spec.Mode != ImportModeUpsert {
		// Accepted and ignored, it would read as an upsert that then failed on
		// its first duplicate.
		return fmt.Errorf("a conflict key is for an upsert; this import's mode is %s", spec.Mode)
	}
	return nil
}

func newRecordReader(text io.Reader, spec ImportSpec) (recordReader, error) {
	switch spec.Format {
	case ImportFormatJSON:
		return newJSONArrayReader(text), nil
	case ImportFormatNDJSON:
		return newNDJSONReader(text), nil
	}
	comma, _ := utf8.DecodeRuneInString(spec.Delimiter)
	quote, hasQuote := '"', true
	if spec.Quote != nil {
		if *spec.Quote == "" {
			hasQuote = false
		} else {
			quote, _ = utf8.DecodeRuneInString(*spec.Quote)
		}
	}
	return newDelimitedReader(text, comma, quote, hasQuote), nil
}

// importSource is the file as far as it has been read: its column names and
// the first rows.
type importSource struct {
	columns []string
	// keyed is a file whose records name their own fields.
	keyed bool
	// positional is a delimited file with no header: its columns have
	// numbers rather than names.
	positional bool
	sample     []importRecord
	// done is set when the sample is the whole file.
	done bool
	// strays are keys met after the sample that the sample did not have. The
	// columns were settled by then, so their values are not imported.
	strays map[string]bool
}

// readImportSample reads the header and the first rows.
func readImportSample(reader recordReader, spec ImportSpec, report *ImportReport) (*importSource, error) {
	src := &importSource{keyed: !spec.Format.delimited()}
	limit := importSampleRows
	if spec.sampleAll {
		limit = math.MaxInt
	}

	if spec.Format.delimited() && (spec.Header == nil || *spec.Header) {
		header, err := reader.next()
		if err == io.EOF {
			return nil, fmt.Errorf("the file is empty")
		}
		if err != nil {
			return nil, fmt.Errorf("could not read the header row: %w", err)
		}
		if header.err != nil {
			return nil, fmt.Errorf("could not read the header row: %w", header.err)
		}
		for _, v := range header.values {
			src.columns = append(src.columns, strings.TrimSpace(strings.TrimPrefix(v.text, "\ufeff")))
		}
	}

	seen := map[string]bool{}
	for len(src.sample) < limit {
		rec, err := reader.next()
		if err == io.EOF {
			src.done = true
			break
		}
		if errors.Is(err, errTruncatedInput) && spec.DryRun {
			// A dry run is given the start of a file. Where it was cut is not
			// something wrong with the file.
			src.done = true
			break
		}
		if err != nil {
			return nil, err
		}
		if src.keyed {
			if status, marker := isExportMarker(rec); marker {
				report.Warnings = append(report.Warnings,
					fmt.Sprintf("this file is an export that was %s: it does not hold every row", status))
				continue
			}
			for _, k := range rec.keys {
				if !seen[k] {
					seen[k] = true
					src.columns = append(src.columns, k)
				}
			}
		}
		src.sample = append(src.sample, rec)
	}
	if spec.DryRun && src.done && len(src.sample) > 0 {
		// The last row of a file's first part may be half a row.
		last := src.sample[len(src.sample)-1]
		if last.err != nil || (!src.keyed && len(src.columns) > 0 && len(last.values) != len(src.columns)) {
			src.sample = src.sample[:len(src.sample)-1]
		}
	}
	if src.keyed && spec.sortKeys {
		sortStrings(src.columns)
	}
	if src.keyed && len(spec.keys) > 0 {
		src.columns = append([]string(nil), spec.keys...)
	}
	if !src.keyed && len(src.columns) == 0 {
		if spec.trusted && len(spec.Columns) == 0 {
			return nil, fmt.Errorf("no target columns: either include a header row or name the columns")
		}
		// No header: the columns are positional, and naming them by position
		// gives the mapping something to point at. Where the caller named the
		// targets, that is how many columns a row has; otherwise the first row
		// says.
		src.positional = true
		width := len(spec.Columns)
		for _, rec := range src.sample {
			if width > 0 {
				break
			}
			if rec.err == nil {
				width = len(rec.values)
			}
		}
		for i := 0; i < width; i++ {
			src.columns = append(src.columns, "column_"+strconv.Itoa(i+1))
		}
	}
	if len(src.columns) == 0 {
		if spec.Format.delimited() {
			return nil, fmt.Errorf("the file holds no rows")
		}
		return nil, fmt.Errorf("the file contained no objects to import")
	}
	return src, nil
}

// fieldsOf lays a record's values out by source column. A keyed record's are
// found by name, so an object with its keys in another order, or without some
// of them, still lines up.
func (src *importSource) fieldsOf(rec importRecord, index map[string]int) ([]importValue, error) {
	if !src.keyed {
		if len(rec.values) != len(src.columns) {
			return nil, fmt.Errorf("row has %d fields, expected %d", len(rec.values), len(src.columns))
		}
		return rec.values, nil
	}
	out := make([]importValue, len(src.columns))
	for i := range out {
		out[i].null = true
	}
	for i, key := range rec.keys {
		if at, ok := index[key]; ok {
			out[at] = rec.values[i]
			continue
		}
		if len(src.strays) < maxStrayKeys {
			if src.strays == nil {
				src.strays = map[string]bool{}
			}
			src.strays[key] = true
		}
	}
	return out, nil
}

// maxStrayKeys bounds how many unexpected keys are remembered for the warning.
const maxStrayKeys = 20

func (src *importSource) index() map[string]int {
	index := make(map[string]int, len(src.columns))
	for i, c := range src.columns {
		if _, dup := index[c]; !dup {
			index[c] = i
		}
	}
	return index
}

// isNull applies the import's NULL rule to one field.
func (spec ImportSpec) isNull(v importValue) bool {
	if v.null {
		return true
	}
	if !spec.Format.delimited() {
		return false
	}
	if spec.trusted {
		return spec.nullAs != "" && v.text == spec.nullAs
	}
	if v.quoted {
		return false
	}
	if spec.NullToken != nil {
		return v.text == *spec.NullToken
	}
	return v.text == ""
}

// profileSample reads the sample for what each source column holds.
func profileSample(src *importSource, spec ImportSpec) []*columnProfile {
	profiles := make([]*columnProfile, len(src.columns))
	for i := range profiles {
		profiles[i] = newColumnProfile()
	}
	if spec.trusted {
		return profiles
	}
	index := src.index()
	for _, rec := range src.sample {
		if rec.err != nil {
			continue
		}
		fields, err := src.fieldsOf(rec, index)
		if err != nil {
			continue
		}
		for i, v := range fields {
			profiles[i].add(v, spec.isNull(v), rec.line)
		}
	}
	for _, p := range profiles {
		p.settle()
	}
	return profiles
}

// targetName is the table column a source column is headed for, before the
// table has been consulted: what the mapping says, or its own name.
func targetName(spec ImportSpec, source string, position int) (string, bool) {
	if spec.Mapping != nil {
		target, mapped := spec.Mapping[source]
		return strings.TrimSpace(target), mapped && strings.TrimSpace(target) != ""
	}
	if position < len(spec.Columns) {
		name := strings.TrimSpace(spec.Columns[position])
		return name, name != ""
	}
	if len(spec.Columns) > 0 {
		return "", false
	}
	return source, source != ""
}

// importTargets returns the columns of the table the import writes to. For a
// table that is to be created those are worked out from the file, and the
// statement that creates it comes back with them.
func importTargets(ctx context.Context, db *sql.DB, d Dialect, schema string, src *importSource, profiles []*columnProfile, spec ImportSpec) ([]Column, *ImportCreated, error) {
	if spec.trusted {
		return nil, nil, nil
	}
	existing, err := d.Columns(ctx, db, schema, spec.Table)
	if err != nil {
		return nil, nil, fmt.Errorf("could not read the columns of %s: %w", spec.Table, err)
	}
	if spec.Create == nil {
		if len(existing) == 0 {
			return nil, nil, fmt.Errorf("there is no table %s to import into; ask for it to be created from the file", spec.Table)
		}
		return existing, nil, nil
	}
	if len(existing) > 0 {
		return nil, nil, fmt.Errorf("a table called %s already exists; import into it rather than creating it", spec.Table)
	}
	overrides := map[string]NewColumn{}
	for _, c := range spec.Create.Columns {
		overrides[c.Name] = c
	}
	created := &ImportCreated{Columns: []NewColumn{}}
	targets := []Column{}
	seen := map[string]bool{}
	for i, source := range src.columns {
		name, ok := targetName(spec, source, i)
		if !ok {
			continue
		}
		if seen[name] {
			return nil, nil, fmt.Errorf("two columns of the file would both become %q", name)
		}
		seen[name] = true
		col := NewColumn{Name: name, Type: inferredColumnType(d.Driver(), profiles[i])}
		if o, ok := overrides[name]; ok {
			if strings.TrimSpace(o.Type) != "" {
				col.Type = strings.TrimSpace(o.Type)
			}
			col.NotNull, col.PrimaryKey, col.Default = o.NotNull, o.PrimaryKey, o.Default
			delete(overrides, name)
		}
		created.Columns = append(created.Columns, col)
		targets = append(targets, Column{Name: name, Type: col.Type, Nullable: !col.NotNull, Position: len(targets) + 1})
	}
	for name := range overrides {
		return nil, nil, fmt.Errorf("the new table has no column %q: the file has no column that maps to it", name)
	}
	if len(created.Columns) == 0 {
		return nil, nil, fmt.Errorf("none of the file's columns are mapped, so there is no table to create")
	}
	created.Statement, err = CreateTableSQL(d.Driver(), spec.Schema, spec.Table, created.Columns)
	if err != nil {
		return nil, nil, err
	}
	return targets, created, nil
}

// planImport settles which source column goes to which table column and, for
// an upsert, which key it matches on.
func planImport(ctx context.Context, db *sql.DB, d Dialect, schema string, src *importSource, profiles []*columnProfile, targets []Column, spec ImportSpec, report *ImportReport) (*importPlan, error) {
	rel, err := qualify(d, spec.Schema, spec.Table)
	if err != nil {
		return nil, err
	}
	plan := &importPlan{d: d, spec: spec, rel: rel, sources: src.columns}

	byName := map[string]Column{}
	for _, t := range targets {
		byName[t.Name] = t
	}
	if spec.Mapping != nil {
		have := src.index()
		for source := range spec.Mapping {
			if _, ok := have[source]; !ok {
				report.Warnings = append(report.Warnings,
					fmt.Sprintf("the mapping names %q, which the file has no column called", source))
			}
		}
	}
	used := map[string]bool{}
	for i, source := range src.columns {
		col := ImportColumn{Source: source, Index: i, Inferred: profiles[i].kind, Examples: profiles[i].examples}
		if col.Examples == nil {
			col.Examples = []string{}
		}
		name, wanted := targetName(spec, source, i)
		var target Column
		switch {
		case !wanted:
		case spec.trusted:
			target = Column{Name: name}
		case spec.Mapping != nil || len(spec.Columns) > 0 || spec.Create != nil:
			// Somebody said where it goes, so it goes there or the import is
			// wrong — not quietly somewhere similar.
			t, ok := byName[name]
			if !ok {
				return nil, fmt.Errorf("%s has no column %q (the file's %q is mapped to it)", spec.Table, name, source)
			}
			target = t
		case src.positional:
			// Nothing names the columns on either side, so they go in the
			// table's own order — which is what a file with no header means.
			if i < len(targets) {
				target = targets[i]
			} else {
				wanted = false
			}
		default:
			if t, ok := matchColumn(source, targets); ok {
				target = t
			} else {
				wanted = false
			}
		}
		if wanted {
			if used[target.Name] {
				return nil, fmt.Errorf("two columns of the file are both headed for %q", target.Name)
			}
			used[target.Name] = true
			quoted, err := d.QuoteIdent(target.Name)
			if err != nil {
				return nil, err
			}
			family := "other"
			if !spec.trusted {
				family = typeFamily(target.Type)
				col.Warning = compatibilityWarning(profiles[i], target)
			}
			col.Target, col.TargetType = target.Name, target.Type
			plan.cols = append(plan.cols, plannedColumn{
				source: i, name: target.Name, quoted: quoted, family: family, typeName: target.Type,
			})
		}
		report.Columns = append(report.Columns, col)
	}
	if len(plan.cols) == 0 {
		return nil, fmt.Errorf("none of the file's columns match a column of %s", spec.Table)
	}
	if !spec.trusted && spec.Create == nil {
		for _, t := range targets {
			// A key column with no default is the usual shape of a column the
			// table numbers itself, so only the others are worth a warning.
			if !used[t.Name] && !t.Nullable && t.Default == "" && t.Key == "" {
				report.Warnings = append(report.Warnings,
					fmt.Sprintf("%s requires a value and has no default, and the file has nothing for it", t.Name))
			}
		}
	}

	if spec.Mode == ImportModeUpsert {
		if plan.key, err = upsertKey(ctx, db, d, schema, spec); err != nil {
			return nil, err
		}
		for _, k := range plan.key {
			at := -1
			for i, c := range plan.cols {
				if c.name == k {
					at = i
				}
			}
			if at < 0 {
				return nil, fmt.Errorf("an upsert matches rows on %s, and the file has no column for %q",
					strings.Join(plan.key, ", "), k)
			}
			plan.keyAt = append(plan.keyAt, at)
		}
	}
	return plan, nil
}

// upsertKey resolves the columns an upsert matches existing rows on. They have
// to be a key the table enforces: matching on anything else is an engine
// error on some engines and a duplicate on the others.
func upsertKey(ctx context.Context, db *sql.DB, d Dialect, schema string, spec ImportSpec) ([]string, error) {
	if d.Driver() == DriverClickHouse {
		return nil, fmt.Errorf("ClickHouse has no upsert: a row with the same sorting key is another row until its engine merges them")
	}
	indexes, err := d.Indexes(ctx, db, schema, spec.Table)
	if err != nil {
		indexes = nil
	}
	if name := strings.TrimSpace(spec.ConflictConstraint); name != "" {
		for _, ix := range indexes {
			if ix.Name == name {
				if !ix.Unique && !ix.Primary {
					return nil, fmt.Errorf("%s is not unique, so it cannot say which row a new one replaces", name)
				}
				if len(ix.Columns) == 0 {
					return nil, fmt.Errorf("%s is not on plain columns, so an upsert cannot match on it", name)
				}
				return ix.Columns, nil
			}
		}
		return nil, fmt.Errorf("%s has no unique constraint or index called %q", spec.Table, name)
	}
	if len(spec.ConflictColumns) > 0 {
		want := append([]string(nil), spec.ConflictColumns...)
		sortStrings(want)
		for _, ix := range indexes {
			if !ix.Unique && !ix.Primary {
				continue
			}
			have := append([]string(nil), ix.Columns...)
			sortStrings(have)
			if strings.Join(have, "\x00") == strings.Join(want, "\x00") {
				return spec.ConflictColumns, nil
			}
		}
		pk, _ := d.PrimaryKey(ctx, db, schema, spec.Table)
		have := append([]string(nil), pk...)
		sortStrings(have)
		if len(pk) > 0 && strings.Join(have, "\x00") == strings.Join(want, "\x00") {
			return spec.ConflictColumns, nil
		}
		return nil, fmt.Errorf("%s has no primary key or unique index on exactly (%s)",
			spec.Table, strings.Join(spec.ConflictColumns, ", "))
	}
	pk, err := d.PrimaryKey(ctx, db, schema, spec.Table)
	if err != nil {
		return nil, fmt.Errorf("could not read the primary key of %s: %w", spec.Table, err)
	}
	if len(pk) == 0 {
		return nil, fmt.Errorf("%s has no primary key; name the unique constraint an upsert should match on", spec.Table)
	}
	return pk, nil
}

// args turns one record into the values bound for the planned columns.
func (p *importPlan) args(fields []importValue) ([]any, error) {
	out := make([]any, len(p.cols))
	for i, c := range p.cols {
		v := fields[c.source]
		if p.spec.isNull(v) {
			out[i] = nil
			continue
		}
		if p.spec.trusted {
			out[i] = trustedValue(v)
			continue
		}
		bound, err := bindValue(p.d.Driver(), v, c.family, c.typeName)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", c.name, err)
		}
		out[i] = bound
	}
	if p.d.Driver() == DriverClickHouse {
		// Already the Go values its driver takes. The exact-number rule below
		// would turn an unsigned integer back into the text the driver
		// refuses.
		return out, nil
	}
	return sqlArguments(out)
}

// trustedValue is a field as the inline route binds it: text as text, and a
// JSON number exact.
func trustedValue(v importValue) any {
	if v.raw != nil {
		return v.raw
	}
	return v.text
}

func (p *importPlan) placeholders(from int) string {
	marks := make([]string, len(p.cols))
	for i := range marks {
		marks[i] = p.d.Placeholder(from + i)
	}
	return "(" + strings.Join(marks, ", ") + ")"
}

func (p *importPlan) columnList() string {
	names := make([]string, len(p.cols))
	for i, c := range p.cols {
		names[i] = c.quoted
	}
	return strings.Join(names, ", ")
}

// insertStatement renders an INSERT of n rows.
func (p *importPlan) insertStatement(n int) string {
	tuples := make([]string, n)
	for i := range tuples {
		tuples[i] = p.placeholders(i*len(p.cols) + 1)
	}
	return "INSERT INTO " + p.rel + " (" + p.columnList() + ") VALUES " + strings.Join(tuples, ", ")
}

// rowStatement is the statement one row is written with — the first n rows,
// for an insert that takes several at once.
func (p *importPlan) rowStatement(n int) (string, error) {
	if p.spec.Mode != ImportModeUpsert {
		return p.insertStatement(n), nil
	}
	return p.upsertStatement()
}

// upsertStatement renders the engine's own form of "insert this row, or
// overwrite the one with its key".
func (p *importPlan) upsertStatement() (string, error) {
	isKey := map[string]bool{}
	keyQuoted := make([]string, len(p.key))
	for i, k := range p.key {
		isKey[k] = true
		q, err := p.d.QuoteIdent(k)
		if err != nil {
			return "", err
		}
		keyQuoted[i] = q
	}
	var rest []plannedColumn
	for _, c := range p.cols {
		if !isKey[c.name] {
			rest = append(rest, c)
		}
	}
	insert := p.insertStatement(1)
	switch p.d.Driver() {
	case DriverPostgres, DriverSQLite:
		target := " ON CONFLICT (" + strings.Join(keyQuoted, ", ") + ")"
		if len(rest) == 0 {
			insert += target + " DO NOTHING"
		} else {
			sets := make([]string, len(rest))
			for i, c := range rest {
				sets[i] = c.quoted + " = EXCLUDED." + c.quoted
			}
			insert += target + " DO UPDATE SET " + strings.Join(sets, ", ")
		}
		if p.d.Driver() == DriverPostgres {
			// xmax is zero on a row this statement inserted and not on one it
			// overwrote, which is the only thing that tells them apart.
			insert += " RETURNING (xmax = 0)"
		}
		return insert, nil
	case DriverMySQL:
		// MySQL matches on whichever unique key the row collides with; it has
		// no way to be told one.
		sets := make([]string, 0, len(rest))
		for _, c := range rest {
			sets = append(sets, c.quoted+" = VALUES("+c.quoted+")")
		}
		if len(sets) == 0 {
			sets = append(sets, keyQuoted[0]+" = "+keyQuoted[0])
		}
		return insert + " ON DUPLICATE KEY UPDATE " + strings.Join(sets, ", "), nil
	case DriverMSSQL, DriverOracle:
		return p.mergeStatement(keyQuoted, rest), nil
	}
	return "", fmt.Errorf("%s has no upsert", p.d.Driver())
}

// mergeStatement is the upsert of the two engines that spell it MERGE.
func (p *importPlan) mergeStatement(keyQuoted []string, rest []plannedColumn) string {
	oracle := p.d.Driver() == DriverOracle
	on := make([]string, len(keyQuoted))
	for i, k := range keyQuoted {
		on[i] = "t." + k + " = s." + k
	}
	sourceCols := make([]string, len(p.cols))
	insertValues := make([]string, len(p.cols))
	for i, c := range p.cols {
		sourceCols[i] = c.quoted
		insertValues[i] = "s." + c.quoted
	}
	var b strings.Builder
	if oracle {
		selects := make([]string, len(p.cols))
		for i, c := range p.cols {
			selects[i] = p.d.Placeholder(i+1) + " AS " + c.quoted
		}
		fmt.Fprintf(&b, "MERGE INTO %s t USING (SELECT %s FROM dual) s ON (%s)",
			p.rel, strings.Join(selects, ", "), strings.Join(on, " AND "))
	} else {
		// HOLDLOCK, or two imports racing on one key both find it missing.
		fmt.Fprintf(&b, "MERGE INTO %s WITH (HOLDLOCK) AS t USING (VALUES %s) AS s (%s) ON %s",
			p.rel, p.placeholders(1), strings.Join(sourceCols, ", "), strings.Join(on, " AND "))
	}
	if len(rest) > 0 {
		sets := make([]string, len(rest))
		for i, c := range rest {
			sets[i] = "t." + c.quoted + " = s." + c.quoted
		}
		b.WriteString(" WHEN MATCHED THEN UPDATE SET " + strings.Join(sets, ", "))
	}
	fmt.Fprintf(&b, " WHEN NOT MATCHED THEN INSERT (%s) VALUES (%s)",
		strings.Join(sourceCols, ", "), strings.Join(insertValues, ", "))
	if !oracle {
		b.WriteString(" OUTPUT $action;")
	}
	return b.String()
}

// existsStatement asks whether a row with this key is already there, for the
// engines whose upsert does not say which it did.
func (p *importPlan) existsStatement() (string, error) {
	conds := make([]string, len(p.key))
	for i, k := range p.key {
		q, err := p.d.QuoteIdent(k)
		if err != nil {
			return "", err
		}
		conds[i] = q + " = " + p.d.Placeholder(i+1)
	}
	return "SELECT COUNT(*) FROM " + p.rel + " WHERE " + strings.Join(conds, " AND "), nil
}

// preview renders the first rows as they would be written.
func (p *importPlan) preview(sample []importRecord) *ImportPreview {
	out := &ImportPreview{Columns: make([]string, len(p.cols)), Rows: [][]*string{}}
	for i, c := range p.cols {
		out.Columns[i] = c.name
	}
	src := &importSource{columns: p.sources, keyed: !p.spec.Format.delimited()}
	index := src.index()
	for _, rec := range sample {
		if len(out.Rows) >= importPreviewRows {
			break
		}
		if rec.err != nil {
			continue
		}
		fields, err := src.fieldsOf(rec, index)
		if err != nil {
			continue
		}
		args, err := p.args(fields)
		if err != nil {
			continue
		}
		row := make([]*string, len(args))
		for i, a := range args {
			if a == nil {
				continue
			}
			text := previewText(a)
			row[i] = &text
		}
		out.Rows = append(out.Rows, row)
	}
	return out
}

func previewText(v any) string {
	switch t := v.(type) {
	case string:
		return clipText(t, 200)
	case []byte:
		return clipText(`\x`+hex.EncodeToString(t), 200)
	case bool:
		return strconv.FormatBool(t)
	}
	return fmt.Sprint(v)
}

// importStrategy is how rows reach the table.
type importStrategy struct {
	// rowsPerStatement is how many rows one INSERT carries; 1 writes them one
	// at a time through a prepared statement.
	rowsPerStatement int
	// savepoints is Postgres, where a refused statement spoils the
	// transaction unless there is a savepoint to go back to.
	savepoints bool
}

// parameterLimit is the most bound values one statement may carry on each
// engine, under each one's hard limit.
func parameterLimit(driver Driver) int {
	switch driver {
	case DriverMSSQL:
		return 2000
	case DriverSQLite:
		return 30000
	}
	return 60000
}

func (p *importPlan) strategy() importStrategy {
	driver := p.d.Driver()
	s := importStrategy{rowsPerStatement: 1, savepoints: driver == DriverPostgres}
	if p.spec.Mode == ImportModeUpsert || driver == DriverOracle || driver == DriverClickHouse {
		// An upsert is counted row by row; Oracle has no multi-row VALUES;
		// ClickHouse's driver gathers a prepared statement's rows itself.
		return s
	}
	s.rowsPerStatement = min(p.spec.BatchSize, max(1, parameterLimit(driver)/len(p.cols)))
	if driver == DriverMSSQL {
		s.rowsPerStatement = min(s.rowsPerStatement, 1000)
	}
	return s
}

// pendingRow is a row read and not yet written.
type pendingRow struct {
	row  int
	line int
	args []any
}

// importRun is an import in progress.
type importRun struct {
	ctx      context.Context
	tx       *sql.Tx
	plan     *importPlan
	strategy importStrategy
	report   *ImportReport
	pending  []pendingRow
	single   *sql.Stmt
	exists   *sql.Stmt
	// statements caches the multi-row INSERT text by row count: every full
	// batch shares one, and the last, shorter one is another.
	statements map[int]string
}

// runImport writes the file: the sample first, then the rest of the stream.
func runImport(ctx context.Context, db *sql.DB, plan *importPlan, src *importSource, reader recordReader, report *ImportReport) error {
	spec, driver := plan.spec, plan.d.Driver()
	// Structure a transaction can take back is created inside it. Elsewhere a
	// CREATE commits whatever came before it, so it goes first and is undone
	// by hand if the import then fails.
	transactionalDDL := driver == DriverPostgres || driver == DriverSQLite
	dropCreated := func() {}
	if report.Create != nil && !transactionalDDL {
		if _, err := db.ExecContext(ctx, report.Create.Statement); err != nil {
			return fmt.Errorf("could not create %s: %w", spec.Table, err)
		}
		report.Create.Created = true
		dropCreated = func() {
			// Only ever a table this import made a moment ago and put nothing
			// lasting into.
			_, _ = db.ExecContext(context.WithoutCancel(ctx), "DROP TABLE "+plan.rel)
			report.Create.Created = false
		}
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		dropCreated()
		return err
	}
	// Rollback on any path that does not reach Commit. It is a no-op after a
	// successful commit, so this is safe to defer unconditionally.
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
			dropCreated()
		}
	}()

	if report.Create != nil && transactionalDDL {
		if _, err := tx.ExecContext(ctx, report.Create.Statement); err != nil {
			return fmt.Errorf("could not create %s: %w", spec.Table, err)
		}
		report.Create.Created = true
	}
	// TRUNCATE commits on its own on MySQL and Oracle, so a failed import that
	// began with one left the table empty. Only Postgres can take a TRUNCATE
	// back; everywhere else the rows are deleted, which the transaction can
	// undo.
	//
	// ClickHouse has no transaction at all. Its driver holds the new rows
	// until the commit, so there the table is emptied last, once every row
	// has been accepted: the old rows are only gone if the new ones are about
	// to arrive. It is emptied over a connection of its own, because the one
	// holding the rows is in the middle of an INSERT and takes nothing else.
	if spec.Mode == ImportModeReplace && driver != DriverClickHouse {
		form := "DELETE FROM " + plan.rel
		if driver == DriverPostgres {
			form = "TRUNCATE TABLE " + plan.rel
		}
		if _, err := tx.ExecContext(ctx, form); err != nil {
			return fmt.Errorf("could not empty the table first: %w", err)
		}
	}

	run := &importRun{
		ctx: ctx, tx: tx, plan: plan, strategy: plan.strategy(), report: report,
		statements: map[int]string{},
	}
	defer run.close()
	if err := run.prepare(); err != nil {
		return err
	}

	index := src.index()
	row := 0
	handle := func(rec importRecord) error {
		if src.keyed {
			if status, marker := isExportMarker(rec); marker {
				report.Warnings = append(report.Warnings,
					fmt.Sprintf("this file is an export that was %s: it does not hold every row", status))
				return nil
			}
		}
		row++
		report.RowsRead = row
		recErr := rec.err
		var args []any
		if recErr == nil {
			var fields []importValue
			if fields, recErr = src.fieldsOf(rec, index); recErr == nil {
				args, recErr = plan.args(fields)
			}
		}
		if recErr != nil {
			return run.bad(row, rec.line, recErr)
		}
		return run.add(pendingRow{row: row, line: rec.line, args: args})
	}
	for _, rec := range src.sample {
		if err := handle(rec); err != nil {
			return err
		}
	}
	// The sample is not needed again, and for a large file it is the one part
	// of it still held.
	src.sample = nil
	for !src.done {
		rec, err := reader.next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if err := handle(rec); err != nil {
			return err
		}
	}
	if err := run.flush(); err != nil {
		return err
	}
	if spec.Mode == ImportModeReplace && driver == DriverClickHouse {
		if _, err := db.ExecContext(ctx, "TRUNCATE TABLE "+plan.rel); err != nil {
			return fmt.Errorf("could not empty the table first: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("the import could not be committed, and nothing was imported: %w", err)
	}
	committed = true
	return nil
}

func (r *importRun) close() {
	if r.single != nil {
		r.single.Close()
	}
	if r.exists != nil {
		r.exists.Close()
	}
}

// prepare readies the statements rows are written one at a time with.
func (r *importRun) prepare() error {
	statement, err := r.plan.rowStatement(1)
	if err != nil {
		return err
	}
	if r.single, err = r.tx.PrepareContext(r.ctx, statement); err != nil {
		return fmt.Errorf("could not prepare the insert: %w", err)
	}
	if r.plan.spec.Mode == ImportModeUpsert {
		switch r.plan.d.Driver() {
		case DriverSQLite, DriverOracle:
			query, err := r.plan.existsStatement()
			if err != nil {
				return err
			}
			if r.exists, err = r.tx.PrepareContext(r.ctx, query); err != nil {
				return fmt.Errorf("could not prepare the key lookup: %w", err)
			}
		}
	}
	return nil
}

// bad records a row that was not written, or ends the import on it.
func (r *importRun) bad(row, line int, err error) error {
	if cerr := r.ctx.Err(); cerr != nil {
		return cerr
	}
	if !r.plan.spec.SkipBadRows {
		return &importRowFailure{row: row, line: line, err: err}
	}
	r.report.Skipped++
	if len(r.report.Errors) < r.plan.spec.MaxErrors {
		r.report.Errors = append(r.report.Errors, ImportRowError{Row: row, Line: line, Message: err.Error()})
	} else {
		r.report.ErrorsTruncated = true
	}
	return nil
}

func (r *importRun) add(row pendingRow) error {
	r.pending = append(r.pending, row)
	if len(r.pending) >= r.plan.spec.BatchSize {
		return r.flush()
	}
	return nil
}

// flush writes the pending rows.
//
// The rows go in together, which is what makes a large import fast. When the
// table refuses them, what went in together is undone and its rows go in one
// by one, which is what finds the row it refused: the engine's error names a
// constraint, not a line of a file.
func (r *importRun) flush() error {
	rows := r.pending
	r.pending = r.pending[:0]
	switch {
	case len(rows) == 0:
		return nil
	case r.strategy.savepoints:
		return r.flushBehindSavepoint(rows)
	case r.strategy.rowsPerStatement == 1:
		// Nothing to undo a batch with, and nothing gained by batching.
		return r.oneByOne(rows)
	}
	// A refused statement changes nothing on these engines, so each one is its
	// own unit: the rows of the one that failed are retried, and no others.
	for len(rows) > 0 {
		n := min(len(rows), r.strategy.rowsPerStatement)
		chunk := rows[:n]
		rows = rows[n:]
		if err := r.insertMany(chunk); err != nil {
			if cerr := r.ctx.Err(); cerr != nil {
				return cerr
			}
			if err := r.oneByOne(chunk); err != nil {
				return err
			}
			continue
		}
		r.report.Inserted += n
	}
	return nil
}

// flushBehindSavepoint is Postgres's batch. There a refused statement spoils
// the whole transaction — every later statement fails, and the commit turns
// into a rollback — unless there is a savepoint to go back to. That is why
// skipping a bad row used to import nothing at all on Postgres.
func (r *importRun) flushBehindSavepoint(rows []pendingRow) error {
	if _, err := r.tx.ExecContext(r.ctx, "SAVEPOINT jd_import_batch"); err != nil {
		return err
	}
	inserted, updated, err := r.together(rows)
	if err == nil {
		if _, err := r.tx.ExecContext(r.ctx, "RELEASE SAVEPOINT jd_import_batch"); err != nil {
			return err
		}
		r.report.Inserted += inserted
		r.report.Updated += updated
		return nil
	}
	if cerr := r.ctx.Err(); cerr != nil {
		return cerr
	}
	if _, err := r.tx.ExecContext(r.ctx, "ROLLBACK TO SAVEPOINT jd_import_batch"); err != nil {
		return err
	}
	if _, err := r.tx.ExecContext(r.ctx, "RELEASE SAVEPOINT jd_import_batch"); err != nil {
		return err
	}
	return r.oneByOne(rows)
}

// together writes a batch in as few statements as the engine allows, stopping
// at the first one refused. The caller holds a savepoint to undo it with.
func (r *importRun) together(rows []pendingRow) (inserted, updated int, err error) {
	if r.strategy.rowsPerStatement == 1 {
		for _, row := range rows {
			wasInsert, err := r.write(row)
			if err != nil {
				return 0, 0, err
			}
			if wasInsert {
				inserted++
			} else {
				updated++
			}
		}
		return inserted, updated, nil
	}
	for len(rows) > 0 {
		n := min(len(rows), r.strategy.rowsPerStatement)
		if err := r.insertMany(rows[:n]); err != nil {
			return 0, 0, err
		}
		inserted += n
		rows = rows[n:]
	}
	return inserted, 0, nil
}

// insertMany writes rows with one INSERT.
func (r *importRun) insertMany(rows []pendingRow) error {
	statement, ok := r.statements[len(rows)]
	if !ok {
		statement = r.plan.insertStatement(len(rows))
		r.statements[len(rows)] = statement
	}
	args := make([]any, 0, len(rows)*len(r.plan.cols))
	for _, row := range rows {
		args = append(args, row.args...)
	}
	_, err := r.tx.ExecContext(r.ctx, statement, args...)
	return err
}

// oneByOne writes rows individually, so that each one refused is known by its
// line and — when bad rows are being skipped — costs only itself.
func (r *importRun) oneByOne(rows []pendingRow) error {
	for _, row := range rows {
		if r.strategy.savepoints {
			if _, err := r.tx.ExecContext(r.ctx, "SAVEPOINT jd_import_row"); err != nil {
				return err
			}
		}
		wasInsert, err := r.write(row)
		if r.strategy.savepoints {
			undo := "RELEASE SAVEPOINT jd_import_row"
			if err != nil {
				undo = "ROLLBACK TO SAVEPOINT jd_import_row"
			}
			if _, serr := r.tx.ExecContext(r.ctx, undo); serr != nil {
				return serr
			}
		}
		if err != nil {
			if r.plan.d.Driver() == DriverClickHouse {
				// The driver gathers the rows and sends them at the end, and
				// a row it refuses spoils what it has gathered: there is no
				// carrying on past one. Nothing has been sent yet.
				if cerr := r.ctx.Err(); cerr != nil {
					return cerr
				}
				return &importRowFailure{row: row.row, line: row.line, err: err}
			}
			if berr := r.bad(row.row, row.line, err); berr != nil {
				return berr
			}
			continue
		}
		if wasInsert {
			r.report.Inserted++
		} else {
			r.report.Updated++
		}
	}
	return nil
}

// write puts one row in, and reports whether it was new.
func (r *importRun) write(row pendingRow) (bool, error) {
	if r.plan.spec.Mode != ImportModeUpsert {
		_, err := r.single.ExecContext(r.ctx, row.args...)
		return true, err
	}
	switch r.plan.d.Driver() {
	case DriverPostgres:
		var inserted bool
		err := r.single.QueryRowContext(r.ctx, row.args...).Scan(&inserted)
		if errors.Is(err, sql.ErrNoRows) {
			// DO NOTHING on a row that was there: nothing came back.
			return false, nil
		}
		return inserted, err
	case DriverMySQL:
		res, err := r.single.ExecContext(r.ctx, row.args...)
		if err != nil {
			return false, err
		}
		// One row affected is an insert; two is an overwrite; none is a row
		// that was already exactly this.
		n, _ := res.RowsAffected()
		return n == 1, nil
	case DriverMSSQL:
		var action string
		err := r.single.QueryRowContext(r.ctx, row.args...).Scan(&action)
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return strings.EqualFold(action, "INSERT"), err
	}
	// SQLite and Oracle do not say which it was, so it is asked first. The
	// lookup and the write are in one transaction, and the answer only decides
	// what is counted.
	key := make([]any, len(r.plan.keyAt))
	for i, at := range r.plan.keyAt {
		key[i] = row.args[at]
	}
	var n int
	if err := r.exists.QueryRowContext(r.ctx, key...).Scan(&n); err != nil {
		return false, err
	}
	_, err := r.single.ExecContext(r.ctx, row.args...)
	return n == 0, err
}

// sortStrings is sort.Strings without pulling the import into this file's
// dependency set twice over.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// ParseImportPreview reads the first few records of a CSV so the UI can show
// the operator what it thinks the columns are before anything is written.
func ParseImportPreview(r io.Reader, hasHeader bool, limit int) ([]string, [][]string, error) {
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	var header []string
	if hasHeader {
		rec, err := cr.Read()
		if err != nil {
			return nil, nil, err
		}
		for _, h := range rec {
			header = append(header, strings.TrimSpace(strings.TrimPrefix(h, "\ufeff")))
		}
	}
	sample := [][]string{}
	for len(sample) < limit {
		rec, err := cr.Read()
		if err != nil {
			break
		}
		sample = append(sample, rec)
	}
	if header == nil && len(sample) > 0 {
		// With no header the columns are positional; naming them by index gives
		// the mapping UI something to point at.
		for i := range sample[0] {
			header = append(header, "column "+strconv.Itoa(i+1))
		}
	}
	return header, sample, nil
}
