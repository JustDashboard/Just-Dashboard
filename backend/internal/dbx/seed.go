package dbx

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"math/rand/v2"
	"sort"
	"strings"
	"time"
)

// Seed data: rows of believable mock data, written into tables that exist.
//
// A seed reads the tables the way the code generators do (LoadORMSchema and
// the prepared model behind every target), so it knows what they know: which
// column the engine numbers itself, which is computed, which is an enum and
// what its labels are, which columns are a key. Each column is given a
// generator — chosen from its name and type, or by the reader — and the rows
// are written parents first, in one transaction, by the import's own writer.
//
// What a foreign key holds is a value the parent has. A parent filled by the
// same seed is written first and its new rows are what the child points at;
// one that is not is read for the keys it already has. A unique column's
// values are kept apart: a number counts up from past the largest one there,
// a text carries the row's own number.
//
// The same seed number over the same tables gives the same rows, which is
// what lets the preview be the rows the write then writes — except where the
// write decides: a key the server numbers, and the rows a foreign key points
// at among those the seed has just written.

// ErrSeedRequest marks a seed that cannot be made as asked: a table that is
// not there, a generator a column cannot take, more rows than a seed writes.
var ErrSeedRequest = errors.New("seed request")

func seedRequestErrorf(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrSeedRequest}, args...)...)
}

// SeedRequestMessage is the sentence an ErrSeedRequest carries for the reader.
func SeedRequestMessage(err error) string {
	return strings.TrimPrefix(err.Error(), ErrSeedRequest.Error()+": ")
}

const (
	// MaxSeedRows is the most rows one table is given by one seed, and
	// MaxSeedTotal the most a seed writes in all: one transaction holds them.
	MaxSeedRows  = 10000
	MaxSeedTotal = 100000
	// MaxSeedTables bounds what one seed reads and writes.
	MaxSeedTables = 100
	// seedPreviewRows is how many rows of each table the preview shows.
	seedPreviewRows = 8
	// seedPoolLimit is the most keys read from a parent for a foreign key to
	// choose among.
	seedPoolLimit = 10000
	// seedChunk is how many rows are made and written at a time. A table
	// that points at itself reads its own keys again between them, and is
	// written in five chunks or so, so that most of its rows have earlier
	// ones to point at — but never fewer than the preview shows, so the
	// preview's rows are all from the first.
	seedChunk = 500
	// seedTupleTries is how often a row's key is drawn again when a
	// key of several columns comes out the same as an earlier row's.
	seedTupleTries = 50
	// seedErrorSample is how many refused rows a table's report names.
	seedErrorSample = 5
)

// SeedRequest asks for rows in some tables.
type SeedRequest struct {
	// Schema is where a table that names none is; empty is the connection's
	// own.
	Schema string             `json:"schema"`
	Tables []SeedTableRequest `json:"tables"`
	// Seed fixes the random stream. Zero asks for a new one, which the answer
	// carries so the next request can ask for the same rows.
	Seed int64 `json:"seed"`
}

// SeedTableRequest is one table and how it is filled.
type SeedTableRequest struct {
	Schema string `json:"schema,omitempty"`
	Name   string `json:"name"`
	Rows   int    `json:"rows"`
	// Columns choose a column's generator by its id; a column not named here
	// keeps the one it opens with.
	Columns map[string]string `json:"columns,omitempty"`
}

// SeedPlan is what a seed would write: the tables in the order they are
// written, how each column is filled, and each table's first rows.
type SeedPlan struct {
	Seed   int64           `json:"seed"`
	Tables []SeedTablePlan `json:"tables"`
	// Rows is how many rows the seed writes in all.
	Rows int `json:"rows"`
	// Atomic is false where the engine has no transaction: a table written
	// before a failure stays written.
	Atomic   bool     `json:"atomic"`
	Warnings []string `json:"warnings"`
}

// SeedTablePlan is one table of a plan.
type SeedTablePlan struct {
	Schema string `json:"schema"`
	Name   string `json:"name"`
	// Rows is how many rows the table is given; Asked is how many were asked
	// for, which is more where the table cannot take as many (Notes say why).
	Rows  int `json:"rows"`
	Asked int `json:"asked"`
	// Existing is how many rows the table held when it was read.
	Existing int64 `json:"existing"`
	// After names the tables written before this one because it points at
	// them.
	After   []string         `json:"after"`
	Columns []SeedColumnPlan `json:"columns"`
	// Preview is the first rows, a cell per column, as text or null; a
	// column whose values the write decides has null cells and says so in
	// its Later.
	Preview [][]*string `json:"preview"`
	// Statement is the INSERT each row is written with.
	Statement string `json:"statement"`
	// Problem is why the table is left out; Notes are what it is written with
	// that the reader should know.
	Problem string   `json:"problem,omitempty"`
	Notes   []string `json:"notes"`
}

// SeedColumnPlan is how one column is filled.
type SeedColumnPlan struct {
	Name string `json:"name"`
	Type string `json:"type"`
	// Generator is the id of what fills it, and Label what it is called.
	// Inferred is the one it has when nobody chooses, so a page can tell a
	// choice from the opening one.
	Generator string `json:"generator"`
	Label     string `json:"label"`
	Inferred  string `json:"inferred"`
	// Choices are the generators the reader may choose instead, the current
	// one among them; empty where the server fills the column itself.
	Choices  []SeedChoice `json:"choices"`
	Nullable bool         `json:"nullable"`
	Unique   bool         `json:"unique"`
	// Reference is the table a foreign key column takes its values from.
	Reference *SeedReference `json:"reference,omitempty"`
	// Later is what stands in the preview for a value the write decides.
	Later string `json:"later,omitempty"`
}

// SeedChoice is a generator a column can be filled by.
type SeedChoice struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Group string `json:"group"`
}

// SeedReference is the parent of a foreign key.
type SeedReference struct {
	Schema  string   `json:"schema,omitempty"`
	Table   string   `json:"table"`
	Columns []string `json:"columns"`
	// Seeded is true when the same seed fills the parent first.
	Seeded bool `json:"seeded"`
}

// --- the model a seed works from ---------------------------------------------

type seedColumn struct {
	name     string
	typeName string
	t        ormType
	driver   Driver
	quoted   string
	family   string
	nullable bool
	// hasDefault is a default the engine fills in when the column is left
	// out, other than numbering it.
	hasDefault bool
	// fixed is seedAuto or seedComputed for a column the server fills.
	fixed string
	// guessed is a column taken to be numbered by the server without the
	// catalogue saying so — SQLite's rowid under another name, an integer key
	// where the catalogue could not be read — which takes a value given it.
	guessed bool
	// unique is a column that is a key on its own.
	unique bool
	// values are what an enum, a set or a CHECK allows.
	values []string
	lo, hi *float64
	link   *seedLink
	gen    *seedGenerator
	// inferred is what gen was before the request chose another.
	inferred *seedGenerator
	// start is the first number a unique or counting column uses; anchor
	// is the moment dates are drawn around.
	start  int
	anchor time.Time
	// counting is a column whose values count up from past its largest.
	counting bool
}

// seedLink is one foreign key of a seeded table.
type seedLink struct {
	fk     ForeignKey
	cols   []*seedColumn
	parent *seedTable
	// refSchema is the parent's schema, the child's own where the key names
	// none.
	refSchema string
	self      bool
	// unique is a key whose columns are a key of the child: each parent row
	// is pointed at once.
	unique bool
	// deferred is a key that closes a cycle. Its parent is written after the
	// child, so it points at the parent's rows from before the seed.
	deferred bool
	// required is a key whose columns cannot be left empty.
	required bool
	pool     [][]seedCell
	// poolFull is a pool cut at seedPoolLimit; loaded says it was read.
	poolFull bool
	loaded   bool
	perm     []int
}

type seedTable struct {
	schema, name string
	label        string
	rel          string
	model        *ormTable
	asked, rows  int
	cols         []*seedColumn
	links        []*seedLink
	// keys are the keys of several columns, by position, that a row's values
	// are checked against so no two new rows share one.
	keys    [][]int
	problem string
	notes   []string
	after   []*seedTable
	// existing is how many rows the table held.
	existing int64
	// autoKey is the one column of the key the server numbers, where it is a
	// whole number: the seed's rows are the ones past maxBefore.
	autoKey   *seedColumn
	maxBefore *int64
	// keptRows are the rows written, kept where a seeded child points at
	// columns of them: what the child chooses among.
	keptRows [][]seedCell
	// referenced are the columns a seeded child points at.
	referenced map[int]bool

	streams  []*rand.Rand
	rowRand  *rand.Rand
	linkRand []*rand.Rand
	seen     []map[string]bool
	made     int
}

// seedCell is a value as it is bound: generated text, a NULL, or a value read
// back from the database as its driver gave it.
type seedCell struct {
	text string
	null bool
	raw  any
	read bool
}

func (c seedCell) key() string {
	switch {
	case c.null:
		return "\x00null"
	case c.read:
		return fmt.Sprint(normaliseValue(c.raw))
	}
	return c.text
}

func (c seedCell) shown() *string {
	if c.null {
		return nil
	}
	text := c.text
	if c.read {
		text = previewText(normaliseValue(c.raw))
	}
	return &text
}

// seedQuerier is what a seed reads through: the pool before a write, the
// write's own transaction during one.
type seedQuerier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type seedRun struct {
	ctx      context.Context
	d        Dialect
	driver   Driver
	seed     int64
	tables   []*seedTable
	order    []*seedTable
	warnings []string
	anchor   time.Time
	// previewing is a plan's run, where a key pointing at rows the seed has
	// not written yet is left for the write to choose.
	previewing bool
	// checks are the CHECK conditions of each table, by ormTableKey, on the
	// engines whose generator catalogue does not read them.
	checks map[string][]string
}

// --- reading the request -----------------------------------------------------

func normaliseSeedRequest(req *SeedRequest) error {
	if len(req.Tables) == 0 {
		return seedRequestErrorf("name at least one table to fill")
	}
	if len(req.Tables) > MaxSeedTables {
		return seedRequestErrorf("a seed fills at most %d tables at once", MaxSeedTables)
	}
	total := 0
	seen := map[string]bool{}
	for i := range req.Tables {
		t := &req.Tables[i]
		t.Name = strings.TrimSpace(t.Name)
		t.Schema = strings.TrimSpace(t.Schema)
		if t.Name == "" {
			return seedRequestErrorf("every table needs a name")
		}
		for _, text := range []string{t.Name, t.Schema} {
			if len(text) > 128 || strings.ContainsFunc(text, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
				return seedRequestErrorf("%q is not a table name", text)
			}
		}
		if t.Rows < 1 || t.Rows > MaxSeedRows {
			return seedRequestErrorf("%s: between 1 and %d rows can be asked for", t.Name, MaxSeedRows)
		}
		key := t.Schema + "\x00" + t.Name
		if seen[key] {
			return seedRequestErrorf("%s is named twice", t.Name)
		}
		seen[key] = true
		total += t.Rows
	}
	if total > MaxSeedTotal {
		return seedRequestErrorf("%d rows is more than one seed writes (%d)", total, MaxSeedTotal)
	}
	if req.Seed == 0 {
		req.Seed = 1 + rand.Int64N(999999)
	}
	return nil
}

// --- planning --------------------------------------------------------------------

// PlanSeed works out what a seed would write and shows each table's first
// rows. It reads the catalogue, the keys a foreign key can point at, and how
// many rows each table holds; it writes nothing.
func PlanSeed(ctx context.Context, db *sql.DB, driver Driver, req SeedRequest) (*SeedPlan, error) {
	run, err := newSeedRun(ctx, db, driver, &req)
	if err != nil {
		return nil, err
	}
	if err := run.read(db); err != nil {
		return nil, err
	}
	return run.plan(), nil
}

// newSeedRun reads the tables a request names and settles how each column is
// filled and in which order the tables are written.
func newSeedRun(ctx context.Context, db *sql.DB, driver Driver, req *SeedRequest) (*seedRun, error) {
	d, err := DialectFor(driver)
	if err != nil {
		return nil, err
	}
	if err := normaliseSeedRequest(req); err != nil {
		return nil, err
	}
	run := &seedRun{
		ctx: ctx, d: d, driver: driver, seed: req.Seed,
		// Midnight, so a preview taken in the morning and the write that
		// follows it at noon draw the same dates.
		anchor: time.Now().UTC().Truncate(24 * time.Hour),
	}
	defaultSchema := strings.TrimSpace(req.Schema)
	if defaultSchema == "" {
		defaultSchema = currentSchema(ctx, db, d)
	}
	schemas := []string{}
	names := []string{}
	seenSchema := map[string]bool{}
	for i := range req.Tables {
		t := &req.Tables[i]
		if t.Schema == "" || driver == DriverSQLite {
			t.Schema = defaultSchema
		}
		if !seenSchema[t.Schema] {
			seenSchema[t.Schema] = true
			schemas = append(schemas, t.Schema)
		}
		names = append(names, t.Schema+"."+t.Name)
	}
	schema, err := LoadORMSchema(ctx, db, driver, ORMScope{
		Schemas: schemas, Tables: names, SkipViews: true,
		// The two engines whose CHECKs this reads only from the CREATE text.
		Statements: driver == DriverMySQL || driver == DriverSQLite,
	})
	if errors.Is(err, ErrORMRequest) {
		return nil, seedRequestErrorf("%s", ORMRequestMessage(err))
	}
	if err != nil {
		return nil, err
	}
	run.warnings = append(run.warnings, schema.Warnings...)
	run.checks = run.readChecks(db, schemas)
	gen := newORMGen(schema, ORMOptions{Target: ORMSQL, Relations: true, Enums: true, Defaults: true})
	models := map[string]*ormTable{}
	for _, m := range gen.models {
		models[ormTableKey(m.Schema, m.Name)] = m
	}
	byKey := map[string]*seedTable{}
	for _, want := range req.Tables {
		m := models[ormTableKey(want.Schema, want.Name)]
		if m == nil {
			for _, t := range schema.Tables {
				if t.Schema == want.Schema && t.Name == want.Name && t.Kind != ORMKindTable && t.Kind != ORMKindPartitioned {
					return nil, seedRequestErrorf("%s is a %s; rows go into tables", want.Name, t.Kind)
				}
			}
			return nil, seedRequestErrorf("%s has no table this connection can read", want.Name)
		}
		if m.view {
			return nil, seedRequestErrorf("%s is a view; rows go into tables", want.Name)
		}
		t, err := run.table(db, m, want)
		if err != nil {
			return nil, err
		}
		run.tables = append(run.tables, t)
		byKey[ormTableKey(t.schema, t.name)] = t
	}
	if len(schemas) > 1 {
		for _, t := range run.tables {
			t.label = t.schema + "." + t.name
		}
	}
	for _, t := range run.tables {
		run.linkTable(t, byKey)
	}
	for _, t := range run.tables {
		if err := run.chooseGenerators(t, req); err != nil {
			return nil, err
		}
	}
	run.orderTables()
	return run, nil
}

// readChecks reads the CHECK conditions the generator catalogue leaves
// unread: SQL Server's and Oracle's, which their catalogues keep as text of
// their own. PostgreSQL's come with the catalogue, and MySQL's and SQLite's
// are in the CREATE statement. A catalogue the login may not read costs the
// seed the checks, and a row one of them refuses is left out and counted.
func (run *seedRun) readChecks(db *sql.DB, schemas []string) map[string][]string {
	out := map[string][]string{}
	var query string
	switch run.driver {
	case DriverMSSQL:
		query = `SELECT t.name, cc.definition FROM sys.check_constraints cc
		         JOIN sys.tables t ON t.object_id = cc.parent_object_id
		         JOIN sys.schemas s ON s.schema_id = t.schema_id
		         WHERE s.name = @p1 AND cc.is_disabled = 0`
	case DriverOracle:
		query = `SELECT table_name, search_condition_vc FROM all_constraints
		         WHERE owner = NVL(:1, SYS_CONTEXT('USERENV','CURRENT_SCHEMA'))
		           AND constraint_type = 'C' AND status = 'ENABLED' AND search_condition_vc IS NOT NULL`
	default:
		return out
	}
	for _, schema := range schemas {
		var arg any = schema
		if run.driver == DriverOracle {
			arg = oracleSchemaArg(schema)
		}
		rows, err := db.QueryContext(run.ctx, query, arg)
		if err != nil {
			continue
		}
		for rows.Next() {
			var table string
			var definition sql.NullString
			if rows.Scan(&table, &definition) == nil && definition.Valid {
				key := ormTableKey(schema, table)
				out[key] = append(out[key], definition.String)
			}
		}
		rows.Close()
	}
	return out
}

// table builds one table's columns from the generator model.
func (run *seedRun) table(db *sql.DB, m *ormTable, want SeedTableRequest) (*seedTable, error) {
	rel, err := qualify(run.d, m.Schema, m.Name)
	if err != nil {
		return nil, err
	}
	t := &seedTable{
		schema: m.Schema, name: m.Name, label: m.Name, rel: rel, model: m,
		asked: want.Rows, rows: want.Rows, referenced: map[int]bool{},
	}
	computed := importComputedColumns(run.ctx, db, run.d, m.Schema, m.Name)
	checks := append([]string{}, run.checks[ormTableKey(m.Schema, m.Name)]...)
	for _, c := range m.Checks {
		checks = append(checks, c.Definition)
	}
	if run.driver == DriverMySQL || run.driver == DriverSQLite {
		checks = append(checks, checkClauses(m.CreateSQL)...)
	}
	held := readSeedChecks(checks)
	// ClickHouse's key orders a table's parts and makes nothing unique.
	keyed := run.driver != DriverClickHouse
	single := map[string]bool{}
	if keyed && len(m.PrimaryKey) == 1 {
		single[m.PrimaryKey[0]] = true
	}
	if keyed {
		for _, ix := range m.uniques {
			if len(ix.Columns) == 1 {
				single[ix.Columns[0]] = true
			}
		}
	}
	for _, mc := range m.cols {
		quoted, err := run.d.QuoteIdent(mc.Name)
		if err != nil {
			return nil, err
		}
		c := &seedColumn{
			name: mc.Name, typeName: mc.Type, t: mc.t, driver: run.driver, quoted: quoted,
			family: typeFamily(mc.Type), nullable: mc.Nullable, unique: single[mc.Name],
			values: mc.t.Values, anchor: run.anchor,
		}
		switch {
		case mc.Generated || computed[mc.Name] || (run.driver == DriverMSSQL && mc.t.Name == "rowversion"):
			c.fixed = seedComputed
		case mc.AutoIncrement || mc.def.Kind == ormDefAuto || mc.def.Kind == ormDefAssumedAuto:
			c.fixed = seedAuto
			c.guessed = run.driver == DriverSQLite || mc.def.Kind == ormDefAssumedAuto
		}
		c.hasDefault = c.fixed == "" && mc.def.Kind != ormDefNone
		if check := held[strings.ToLower(mc.Name)]; check != nil {
			if len(c.values) == 0 && len(check.values) > 0 && (check.numbered == c.isNumber()) {
				c.values = check.values
			}
			c.lo, c.hi = check.bounds(c)
		}
		t.cols = append(t.cols, c)
	}
	if keyed {
		for _, key := range seedKeys(m) {
			if len(key) < 2 {
				continue
			}
			at := make([]int, 0, len(key))
			for _, name := range key {
				if i := t.column(name); i >= 0 {
					at = append(at, i)
				}
			}
			if len(at) == len(key) {
				t.keys = append(t.keys, at)
			}
		}
	}
	if len(m.PrimaryKey) == 1 {
		if i := t.column(m.PrimaryKey[0]); i >= 0 && t.cols[i].fixed == seedAuto && t.cols[i].isWhole() {
			t.autoKey = t.cols[i]
		}
	}
	return t, nil
}

// seedKeys are a table's primary key and its usable unique indexes.
func seedKeys(m *ormTable) [][]string {
	var keys [][]string
	if len(m.PrimaryKey) > 0 {
		keys = append(keys, m.PrimaryKey)
	}
	for _, ix := range m.uniques {
		keys = append(keys, ix.Columns)
	}
	return keys
}

func (t *seedTable) column(name string) int {
	for i, c := range t.cols {
		if c.name == name {
			return i
		}
	}
	return -1
}

// bounds is the CHECK's range in the terms of the column's values: a
// "greater than" on whole numbers starts at the next one up.
func (s *seedCheck) bounds(c *seedColumn) (lo, hi *float64) {
	step := 0.01
	if c.isWhole() {
		step = 1
	}
	if s.lo != nil {
		v := *s.lo
		if s.exclLo {
			v += step
		}
		lo = &v
	}
	if s.hi != nil {
		v := *s.hi
		if s.exclHi {
			v -= step
		}
		hi = &v
	}
	return lo, hi
}

// linkTable ties each foreign key to its parent, among the seeded tables or
// outside them.
func (run *seedRun) linkTable(t *seedTable, byKey map[string]*seedTable) {
	claimed := map[int]bool{}
	for _, fk := range t.model.ForeignKeys {
		if len(fk.Columns) == 0 || len(fk.Columns) != len(fk.RefColumns) {
			continue
		}
		refSchema := fk.RefSchema
		if refSchema == "" || run.driver == DriverSQLite {
			refSchema = t.schema
		}
		link := &seedLink{fk: fk, refSchema: refSchema}
		overlap := false
		for _, name := range fk.Columns {
			i := t.column(name)
			if i < 0 {
				overlap = true
				break
			}
			if claimed[i] {
				overlap = true
			}
			link.cols = append(link.cols, t.cols[i])
		}
		if overlap {
			if len(link.cols) == len(fk.Columns) {
				t.notes = append(t.notes, fmt.Sprintf(
					"%s share columns with another foreign key, which fills them; the key to %s may not hold, and a row it refuses is left out",
					strings.Join(fk.Columns, ", "), fk.RefTable))
			}
			continue
		}
		for _, c := range link.cols {
			claimed[t.column(c.name)] = true
			c.link = link
			if c.fixed == seedAuto && c.guessed {
				// A key that is also a foreign key — a profile's user_id — is
				// the parent's number, not one of its own.
				c.fixed = ""
				if t.autoKey == c {
					t.autoKey = nil
				}
			}
			if !c.nullable {
				link.required = true
			}
		}
		link.parent = byKey[ormTableKey(refSchema, fk.RefTable)]
		link.self = link.parent == t
		link.unique = linkIsKey(t, fk.Columns)
		t.links = append(t.links, link)
		if link.parent != nil && !link.self {
			for _, name := range fk.RefColumns {
				if i := link.parent.column(name); i >= 0 {
					link.parent.referenced[i] = true
				}
			}
		}
	}
}

// linkIsKey says the columns are the child's primary key or one of its unique
// indexes exactly: a one-to-one relation.
func linkIsKey(t *seedTable, cols []string) bool {
	if t.model == nil || len(cols) == 0 {
		return false
	}
	for _, key := range seedKeys(t.model) {
		if sameColumnList(key, cols) || sameColumnSet(key, cols) {
			return true
		}
	}
	return false
}

func sameColumnSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	have := map[string]bool{}
	for _, c := range a {
		have[c] = true
	}
	for _, c := range b {
		if !have[c] {
			return false
		}
	}
	return true
}

// chooseGenerators gives every column what fills it: the server, the
// reader's choice, or the generator its name and type suggest.
func (run *seedRun) chooseGenerators(t *seedTable, req *SeedRequest) error {
	var chosen map[string]string
	for _, want := range req.Tables {
		if want.Name == t.name && (want.Schema == t.schema || run.driver == DriverSQLite) {
			chosen = want.Columns
		}
	}
	for name := range chosen {
		if t.column(name) < 0 {
			return seedRequestErrorf("%s has no column %q", t.label, name)
		}
	}
	for _, c := range t.cols {
		if c.fixed != "" {
			c.gen = seedGeneratorByID[c.fixed]
			if id, ok := chosen[c.name]; ok && id != c.fixed {
				return seedRequestErrorf("%s.%s is filled by the server and takes no generator", t.label, c.name)
			}
			continue
		}
		c.inferred = c.opening(t.name)
		c.gen = c.inferred
		if id, ok := chosen[c.name]; ok {
			g := seedGeneratorByID[id]
			if g == nil || !c.allows(g) {
				return seedRequestErrorf("%s.%s cannot be filled with %q", t.label, c.name, id)
			}
			c.gen = g
		}
		if c.gen == nil {
			t.problem = fmt.Sprintf("%s is %s, which no generator writes; it has no default and cannot be left empty",
				c.name, c.typeName)
			continue
		}
		c.counting = c.gen.writesValue() && (c.gen.id == "sequence" || (c.unique && c.gen.distinct == seedCounting))
	}
	// A key with a column the server fills is apart by that column, and one
	// left to its default or empty is not the seed's to keep apart.
	keys := t.keys[:0]
	for _, key := range t.keys {
		ours := true
		for _, i := range key {
			if g := t.cols[i].gen; g == nil || !(g.writesValue() || g.id == seedReference) {
				ours = false
			}
		}
		if ours {
			keys = append(keys, key)
		}
	}
	t.keys = keys
	// A link the reader chose to leave to its default or empty no longer
	// fills its columns.
	links := t.links[:0]
	for _, link := range t.links {
		filled := false
		for _, c := range link.cols {
			if c.gen != nil && c.gen.id == seedReference {
				filled = true
			}
		}
		if filled {
			links = append(links, link)
			continue
		}
		for _, c := range link.cols {
			c.link = nil
		}
	}
	t.links = links
	if t.problem == "" && len(run.importPlan(t).cols) == 0 {
		if e, ok := run.d.(emptyInserter); ok {
			if _, err := e.emptyInsert(); err != nil {
				t.problem = fmt.Sprintf("every column of %s is filled by the server, and %v", t.label, err)
			}
		}
	}
	return nil
}

// opening is the generator a column has before anybody chooses one.
func (c *seedColumn) opening(table string) *seedGenerator {
	if c.link != nil {
		return seedGeneratorByID[seedReference]
	}
	if g := inferSeedGenerator(c, table); g != nil {
		if c.hasDefault && seedGeneric[g.id] && len(c.values) == 0 && c.lo == nil && c.hi == nil {
			// A column with a default, and nothing in its name or its
			// constraints to say what it holds, keeps its default: a status
			// that defaults to 'draft' is more likely right as 'draft' than
			// as a word chosen for its type.
			return seedGeneratorByID[seedDefault]
		}
		return g
	}
	if c.hasDefault {
		return seedGeneratorByID[seedDefault]
	}
	if c.nullable {
		return seedGeneratorByID[seedNull]
	}
	return nil
}

// seedGeneric are the generators that say nothing about a column but its
// type: a word, a number.
var seedGeneric = map[string]bool{"text": true, "word": true, "integer": true, "decimal": true, "code": true}

// allows says a generator may fill the column.
func (c *seedColumn) allows(g *seedGenerator) bool {
	switch g.id {
	case seedAuto, seedComputed:
		return false
	case seedDefault:
		return c.hasDefault
	case seedNull:
		return c.nullable
	case seedReference:
		return c.link != nil
	}
	return g.fits(c)
}

// choices are the generators the column can be given, in the picker's order.
func (c *seedColumn) choices() []SeedChoice {
	out := []SeedChoice{}
	if c.fixed != "" {
		return out
	}
	for _, group := range seedGroups {
		for _, g := range seedGenerators {
			if g.group == group && c.allows(g) {
				label := g.label
				if g.id == seedReference && c.link != nil {
					label = "A row of " + c.link.fk.RefTable
				}
				out = append(out, SeedChoice{ID: g.id, Label: label, Group: g.group})
			}
		}
	}
	return out
}

// orderTables puts every table after the ones it points at. Where tables
// point at each other the cycle is broken at a key that can be left empty,
// which then points at what its parent held before the seed; a cycle with no
// such key cannot be written in any order.
func (run *seedRun) orderTables() {
	pending := map[*seedTable]bool{}
	for _, t := range run.tables {
		if t.problem == "" {
			pending[t] = true
		}
	}
	edges := func(t *seedTable) []*seedLink {
		var out []*seedLink
		for _, link := range t.links {
			if link.parent != nil && !link.self && !link.deferred && pending[link.parent] && link.parent.problem == "" {
				out = append(out, link)
			}
		}
		return out
	}
	for len(pending) > 0 {
		progressed := false
		for _, t := range run.tables {
			if !pending[t] || len(edges(t)) > 0 {
				continue
			}
			run.order = append(run.order, t)
			delete(pending, t)
			progressed = true
		}
		if progressed {
			continue
		}
		// Every table left points at another one left: a cycle. Break it at
		// the first key that can be left empty.
		broken := false
		for _, t := range run.tables {
			if !pending[t] {
				continue
			}
			for _, link := range edges(t) {
				if !link.required {
					link.deferred = true
					t.notes = append(t.notes, fmt.Sprintf(
						"%s points only at %s rows that were there before: %s and %s point at each other, so %s is written first",
						strings.Join(link.fk.Columns, ", "), link.parent.label, t.label, link.parent.label, t.label))
					broken = true
					break
				}
			}
			if broken {
				break
			}
		}
		if broken {
			continue
		}
		var names []string
		for _, t := range run.tables {
			if pending[t] {
				names = append(names, t.label)
			}
		}
		for _, t := range run.tables {
			if pending[t] {
				t.problem = fmt.Sprintf("%s point at each other with keys that cannot be left empty, so no order of writing them works",
					seedList(names))
				delete(pending, t)
			}
		}
	}
}

func seedAppendTable(list []*seedTable, t *seedTable) []*seedTable {
	for _, have := range list {
		if have == t {
			return list
		}
	}
	return append(list, t)
}

func seedList(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// --- what is there already -------------------------------------------------

// read asks the database what the seed has to know before it writes: how many
// rows each table holds, the largest value of each counting column and of a
// key the server numbers, and the keys a foreign key to a table outside the
// seed can point at. Inside a write it is asked of the write's transaction.
func (run *seedRun) read(q seedQuerier) error {
	for _, t := range run.tables {
		if t.problem != "" {
			continue
		}
		n, err := run.count(q, t.rel)
		if err != nil {
			return fmt.Errorf("could not count the rows of %s: %w", t.label, err)
		}
		t.existing = n
		for _, c := range t.cols {
			c.start = int(n) + 1
			if !c.counting {
				continue
			}
			top, err := run.largest(q, t.rel, c.quoted)
			if err != nil {
				return fmt.Errorf("could not read the largest %s of %s: %w", c.name, t.label, err)
			}
			start := int64(1)
			if top != nil {
				start = *top + 1
			}
			if c.lo != nil && float64(start) < *c.lo {
				start = int64(math.Ceil(*c.lo))
			}
			c.start = int(start)
		}
		if t.autoKey != nil {
			top, err := run.largest(q, t.rel, t.autoKey.quoted)
			if err != nil {
				return fmt.Errorf("could not read the largest %s of %s: %w", t.autoKey.name, t.label, err)
			}
			t.maxBefore = top
		}
	}
	return run.settle(q)
}

// seeded says a foreign key points at rows the seed writes before its table.
func (link *seedLink) seeded() bool {
	return link.parent != nil && link.parent.problem == "" && !link.self && !link.deferred
}

func (run *seedRun) count(q seedQuerier, rel string) (int64, error) {
	var n sql.NullInt64
	err := q.QueryRowContext(run.ctx, "SELECT COUNT(*) FROM "+rel).Scan(&n)
	return n.Int64, err
}

// largest is a column's largest value as a whole number, nil when the table
// is empty or the value is not one.
func (run *seedRun) largest(q seedQuerier, rel, quoted string) (*int64, error) {
	var top sql.NullString
	if err := q.QueryRowContext(run.ctx, "SELECT MAX("+quoted+") FROM "+rel).Scan(&top); err != nil {
		return nil, err
	}
	if !top.Valid {
		return nil, nil
	}
	f, err := parseSeedNumber(top.String)
	if err != nil {
		return nil, nil
	}
	n := int64(math.Floor(f))
	return &n, nil
}

func parseSeedNumber(text string) (float64, error) {
	var f float64
	_, err := fmt.Sscan(strings.TrimSpace(text), &f)
	return f, err
}

// readPool reads the keys a foreign key can point at: what the parent holds,
// or — for a parent the seed has just written — the rows it wrote, where the
// server numbered them past maxBefore.
func (run *seedRun) readPool(q seedQuerier, t *seedTable, link *seedLink, after *int64) error {
	parentRel, err := qualify(run.d, link.refSchema, link.fk.RefTable)
	if err != nil {
		return err
	}
	cols := make([]string, len(link.fk.RefColumns))
	where := make([]string, len(link.fk.RefColumns))
	for i, name := range link.fk.RefColumns {
		quoted, err := run.d.QuoteIdent(name)
		if err != nil {
			return err
		}
		cols[i] = quoted
		where[i] = quoted + " IS NOT NULL"
	}
	var args []any
	if after != nil {
		where = append(where, cols[0]+" > "+run.d.Placeholder(1))
		args = append(args, *after)
		if run.driver == DriverMSSQL {
			args[0] = fmt.Sprint(*after)
		}
	}
	tail, more := run.d.Paginate(seedPoolLimit+1, 0, len(args)+1)
	args = append(args, more...)
	query := "SELECT " + strings.Join(cols, ", ") + " FROM " + parentRel +
		" WHERE " + strings.Join(where, " AND ") + " " + tail
	rows, err := q.QueryContext(run.ctx, query, args...)
	if err != nil {
		return fmt.Errorf("could not read the keys %s points at in %s: %w", t.label, link.fk.RefTable, err)
	}
	defer rows.Close()
	link.pool, link.poolFull = nil, false
	for rows.Next() {
		values := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return err
		}
		if len(link.pool) == seedPoolLimit {
			link.poolFull = true
			break
		}
		tuple := make([]seedCell, len(values))
		for i, v := range values {
			tuple[i] = seedCell{raw: v, read: true}
		}
		link.pool = append(link.pool, tuple)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	sortPool(link.pool)
	link.perm = nil
	return nil
}

// sortPool orders keys so that the same keys, read twice in whatever order
// the engine hands them back, are chosen among the same way.
func sortPool(pool [][]seedCell) {
	sort.SliceStable(pool, func(i, j int) bool {
		for k := range pool[i] {
			a, b := pool[i][k].key(), pool[j][k].key()
			if a != b {
				if fa, ea := parseSeedNumber(a); ea == nil {
					if fb, eb := parseSeedNumber(b); eb == nil && fa != fb {
						return fa < fb
					}
				}
				return a < b
			}
		}
		return false
	})
}

// settle decides, from what is there, what each table can be given: a
// foreign key that must point somewhere and has nowhere leaves its table out,
// and that can leave out a table pointing at it in turn — whose key then
// points at what that table already holds, which is read for it.
func (run *seedRun) settle(q seedQuerier) error {
	for changed := true; changed; {
		changed = false
		for _, t := range run.tables {
			if t.problem != "" {
				continue
			}
			for _, link := range t.links {
				if link.seeded() || link.loaded {
					continue
				}
				if err := run.readPool(q, t, link, nil); err != nil {
					return err
				}
				link.loaded = true
			}
			if problem := run.linkProblem(t); problem != "" {
				t.problem = problem
				changed = true
			}
		}
	}
	for _, t := range run.tables {
		if t.problem != "" {
			t.rows = 0
		}
	}
	// Parents first, so a one-to-one key is held to the rows its parent is
	// given once the parent's own room is known.
	position := map[*seedTable]int{}
	for i, t := range run.order {
		position[t] = i
	}
	for _, t := range run.order {
		if t.problem == "" {
			run.capRows(t)
		}
		t.after = nil
		for _, link := range t.links {
			if link.seeded() {
				t.after = seedAppendTable(t.after, link.parent)
			}
		}
		sort.Slice(t.after, func(i, j int) bool { return position[t.after[i]] < position[t.after[j]] })
	}
	return nil
}

func (run *seedRun) linkProblem(t *seedTable) string {
	for _, link := range t.links {
		if link.seeded() || !link.required || len(link.pool) > 0 {
			continue
		}
		cols := strings.Join(link.fk.Columns, ", ")
		switch {
		case link.self:
			return fmt.Sprintf("%s points at %s itself and cannot be left empty, and %s has no rows yet to point at",
				cols, t.label, t.label)
		case link.parent != nil && link.parent.problem != "":
			return fmt.Sprintf("%s must point at a %s row, and %s is left out of this seed and has none",
				cols, link.fk.RefTable, link.fk.RefTable)
		}
		return fmt.Sprintf("%s must point at a %s row, and %s has none; fill %s too",
			cols, link.fk.RefTable, link.fk.RefTable, link.fk.RefTable)
	}
	return ""
}

// capRows lowers a table's rows to what it can hold: a one-to-one key points
// at each parent row once, and a unique column of few values has room for as
// many rows as it has values.
func (run *seedRun) capRows(t *seedTable) {
	for _, link := range t.links {
		if !link.unique {
			continue
		}
		room := len(link.pool)
		why := fmt.Sprintf("%s has %d rows for it to point at", link.fk.RefTable, room)
		if link.seeded() {
			room = link.parent.rows
			why = fmt.Sprintf("this seed writes %d %s rows for it to point at", room, link.parent.label)
		}
		if room < t.rows {
			t.rows = room
			t.notes = append(t.notes, fmt.Sprintf("%s points at each %s row once, and %s: %d rows are written",
				strings.Join(link.fk.Columns, ", "), link.fk.RefTable, why, room))
		}
	}
	for _, c := range t.cols {
		if !c.unique || c.gen == nil || c.gen.distinct != seedFew {
			continue
		}
		room := len(c.values)
		if c.gen.id == "boolean" {
			room = 2
		}
		if c.gen.id == "choice" && c.t.Kind == ormSet {
			continue
		}
		if room < t.rows {
			t.rows = room
			t.notes = append(t.notes, fmt.Sprintf("%s is unique and has %d values, so %d rows are written", c.name, room, room))
		}
	}
	if t.rows < 0 {
		t.rows = 0
	}
}

// --- making rows ---------------------------------------------------------------

func seedStream(seed int64, parts ...string) *rand.Rand {
	h := fnv.New64a()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return rand.New(rand.NewPCG(uint64(seed), h.Sum64()))
}

// begin readies a table's random streams: one per column, one for what a
// row's columns share, one per foreign key.
func (run *seedRun) begin(t *seedTable) {
	t.streams = make([]*rand.Rand, len(t.cols))
	for i, c := range t.cols {
		t.streams[i] = seedStream(run.seed, t.schema, t.name, "column", c.name)
	}
	t.rowRand = seedStream(run.seed, t.schema, t.name, "row")
	t.linkRand = make([]*rand.Rand, len(t.links))
	for i, link := range t.links {
		t.linkRand[i] = seedStream(run.seed, t.schema, t.name, "link", strings.Join(link.fk.Columns, ","))
	}
	t.seen = make([]map[string]bool, len(t.keys))
	for i := range t.seen {
		t.seen[i] = map[string]bool{}
	}
	t.made = 0
}

// next makes the table's next row, a cell per column; ok is false when the
// table has no room for another (a one-to-one key with no parent row left).
func (run *seedRun) next(t *seedTable) (cells []seedCell, ok bool) {
	n := t.made
	t.made++
	row := newSeedRow(t.rowRand)
	cells = make([]seedCell, len(t.cols))
	for i, c := range t.cols {
		if c.gen == nil || !c.gen.writesValue() {
			if c.gen != nil && c.gen.id == seedNull {
				cells[i] = seedCell{null: true}
			}
			continue
		}
		cells[i] = run.value(t, i, row, n)
	}
	for li, link := range t.links {
		if !run.pick(t, li, link, n, cells) {
			return nil, false
		}
	}
	// A key of several columns: draw the row's key columns again until it
	// differs from every row made before it.
	for k, key := range t.keys {
		if run.previewing && run.keyWaits(t, key) {
			continue
		}
		for try := 0; ; try++ {
			id := tupleKey(cells, key)
			if !t.seen[k][id] {
				t.seen[k][id] = true
				break
			}
			if try == seedTupleTries {
				return nil, false
			}
			again := newSeedRow(t.rowRand)
			for _, i := range key {
				c := t.cols[i]
				switch {
				case c.link != nil && !c.link.unique:
					// A one-to-one key's row is apart already; any other is
					// drawn again.
					for li, link := range t.links {
						if link == c.link {
							run.pick(t, li, link, n, cells)
						}
					}
				case c.gen != nil && c.gen.writesValue():
					cells[i] = run.value(t, i, again, n)
				}
			}
		}
	}
	return cells, true
}

// keyWaits says a key has a column the preview cannot fill: it points at
// rows the seed writes first.
func (run *seedRun) keyWaits(t *seedTable, key []int) bool {
	for _, i := range key {
		if link := t.cols[i].link; link != nil && link.seeded() {
			return true
		}
	}
	return false
}

func (run *seedRun) value(t *seedTable, i int, row *seedRow, n int) seedCell {
	text, null := t.cols[i].gen.value(t.cols[i], t.streams[i], row, n)
	if null {
		return seedCell{null: true}
	}
	return seedCell{text: text}
}

func tupleKey(cells []seedCell, at []int) string {
	parts := make([]string, len(at))
	for i, j := range at {
		parts[i] = cells[j].key()
	}
	return strings.Join(parts, "\x00")
}

// pick fills a foreign key's columns with one of the keys it can point at.
func (run *seedRun) pick(t *seedTable, li int, link *seedLink, n int, cells []seedCell) bool {
	empty := func() bool {
		if link.required {
			return false
		}
		for _, c := range link.cols {
			cells[t.column(c.name)] = seedCell{null: true}
		}
		return true
	}
	if run.previewing && link.seeded() {
		return true
	}
	if len(link.pool) == 0 {
		return empty()
	}
	var tuple []seedCell
	if link.unique {
		if link.perm == nil {
			link.perm = t.linkRand[li].Perm(len(link.pool))
		}
		if n >= len(link.perm) {
			return false
		}
		tuple = link.pool[link.perm[n]]
	} else {
		tuple = link.pool[t.linkRand[li].IntN(len(link.pool))]
	}
	for k, c := range link.cols {
		cells[t.column(c.name)] = tuple[k]
	}
	return true
}

// --- the plan as the page reads it -------------------------------------------

func (run *seedRun) plan() *SeedPlan {
	run.previewing = true
	defer func() { run.previewing = false }()
	out := &SeedPlan{
		Seed: run.seed, Tables: []SeedTablePlan{}, Atomic: run.driver != DriverClickHouse,
		Warnings: append([]string{}, run.warnings...),
	}
	written := map[*seedTable]bool{}
	tables := append([]*seedTable{}, run.order...)
	for _, t := range run.order {
		written[t] = true
	}
	for _, t := range run.tables {
		if !written[t] {
			tables = append(tables, t)
		}
	}
	for _, t := range tables {
		p := SeedTablePlan{
			Schema: t.schema, Name: t.name, Rows: t.rows, Asked: t.asked, Existing: t.existing,
			After: []string{}, Columns: []SeedColumnPlan{}, Preview: [][]*string{},
			Problem: t.problem, Notes: append([]string{}, t.notes...),
		}
		for _, parent := range t.after {
			p.After = append(p.After, parent.label)
		}
		for _, c := range t.cols {
			p.Columns = append(p.Columns, run.columnPlan(t, c))
		}
		if t.problem == "" {
			p.Statement = run.statement(t, run.importPlan(t))
			run.begin(t)
			for n := 0; n < min(t.rows, seedPreviewRows); n++ {
				cells, ok := run.next(t)
				if !ok {
					break
				}
				shown := make([]*string, len(cells))
				for i, cell := range cells {
					if c := t.cols[i]; c.gen != nil && (c.gen.writesValue() || c.gen.id == seedNull || run.pickedNow(c)) {
						shown[i] = cell.shown()
					}
				}
				p.Preview = append(p.Preview, shown)
			}
			out.Rows += t.rows
		}
		out.Tables = append(out.Tables, p)
	}
	return out
}

// pickedNow says the preview shows a foreign key's real values: its parent
// is not written by the seed, so what it points at is there now.
func (run *seedRun) pickedNow(c *seedColumn) bool {
	if c.link == nil || c.gen.id != seedReference {
		return false
	}
	return !c.link.seeded()
}

func (run *seedRun) columnPlan(t *seedTable, c *seedColumn) SeedColumnPlan {
	p := SeedColumnPlan{
		Name: c.name, Type: c.typeName, Choices: c.choices(), Nullable: c.nullable, Unique: c.unique,
	}
	if c.gen != nil {
		p.Generator, p.Label = c.gen.id, c.gen.label
	}
	if c.inferred != nil {
		p.Inferred = c.inferred.id
	} else if c.gen != nil {
		p.Inferred = c.gen.id
	}
	if c.link != nil {
		parent := c.link.fk.RefTable
		p.Reference = &SeedReference{
			Schema: c.link.fk.RefSchema, Table: parent, Columns: c.link.fk.RefColumns,
			Seeded: c.link.seeded(),
		}
		if c.gen != nil && c.gen.id == seedReference {
			p.Label = "A row of " + parent
			switch {
			case !run.pickedNow(c):
				p.Later = "a new " + parent + " row"
			case c.link.self && len(c.link.pool) == 0:
				// The first rows have nothing to point at; the ones after
				// them point at those.
				p.Later = "an earlier row, or empty"
			}
		}
	}
	if c.gen != nil {
		switch c.gen.id {
		case seedAuto:
			p.Later = "numbered by the server"
		case seedComputed:
			p.Later = "worked out by the server"
		case seedDefault:
			p.Later = "its default"
		}
	}
	return p
}
