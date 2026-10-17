package dbx

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode"
)

// MigrationStep is one change the migration makes, with the statements that
// make it. A step the DDL generator cannot write for the engine is Manual: it
// carries no statements, and Note says why and what to do instead. Destructive
// marks a step that removes something or rewrites a column's values.
type MigrationStep struct {
	Title       string   `json:"title"`
	Statements  []string `json:"statements"`
	Destructive bool     `json:"destructive"`
	Manual      bool     `json:"manual"`
	Note        string   `json:"note,omitempty"`
}

// Migration is the ordered steps that turn the "from" schema into the "to"
// one.
type Migration struct {
	Steps []MigrationStep `json:"steps"`
}

// Script is the migration as one text to copy: a comment for each step, the
// destructive ones marked, and the statements each ended by a semicolon. A
// step that needs the operator's hands says so in comments, so running the
// script never skips it silently.
func (m *Migration) Script() string {
	var b strings.Builder
	for i, step := range m.Steps {
		if i > 0 {
			b.WriteString("\n")
		}
		if step.Manual {
			b.WriteString(commentLine("NEEDS YOUR ATTENTION: " + step.Title))
		} else {
			b.WriteString(commentLine(step.Title))
		}
		if step.Destructive {
			b.WriteString("-- DESTRUCTIVE: this removes data or definitions that cannot be restored from this script\n")
		}
		note := strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(step.Note)
		for _, line := range strings.Split(note, "\n") {
			if strings.TrimSpace(line) != "" {
				b.WriteString(commentLine(line))
			}
		}
		for _, stmt := range step.Statements {
			b.WriteString(strings.TrimRight(strings.TrimSpace(stmt), ";") + ";\n")
		}
	}
	return b.String()
}

// commentLine is text as one line of comment. A title or a note holds names
// the databases chose — an enum label, a column, a routine's signature — and a
// name with a line break in it would otherwise end the comment and start a
// statement of its own.
func commentLine(text string) string { return "-- " + oneLine(text) + "\n" }

// oneLine replaces every control character and line separator with a space.
func oneLine(text string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
			return ' '
		}
		return r
	}, text))
}

// Counts reports how many steps there are, how many are destructive and how
// many need the operator.
func (m *Migration) Counts() (steps, destructive, manual int) {
	for _, s := range m.Steps {
		steps++
		if s.Destructive {
			destructive++
		}
		if s.Manual {
			manual++
		}
	}
	return steps, destructive, manual
}

// PlanMigration plans the statements that turn the "from" schema into the "to"
// one. db is the connection the statements would run on: some plans read the
// column they restate from it. Nothing is executed.
func PlanMigration(ctx context.Context, db *sql.DB, from, to *SchemaSnapshot, diff *SchemaDiff) *Migration {
	p := &migrationPlanner{ctx: ctx, db: db, driver: from.Driver, flavor: from.Flavor,
		schema: from.Schema, toSchema: to.Schema, from: from, to: to, diff: diff, settled: map[string]bool{}}
	p.plan()
	return &Migration{Steps: p.steps}
}

type migrationPlanner struct {
	ctx      context.Context
	db       *sql.DB
	driver   Driver
	flavor   string
	schema   string
	toSchema string
	from, to *SchemaSnapshot
	diff     *SchemaDiff
	steps    []MigrationStep
	// settled holds the changes already handed over as one step, so the half of
	// them that adds what the other half dropped is not written a second time.
	settled map[string]bool
}

// plan lays the steps down in the order the engines accept them: what other
// things are built from comes before them, and what they depend on goes last.
func (p *migrationPlanner) plan() {
	d := p.diff
	removedTables := map[string]bool{}
	for _, t := range d.tables {
		if t.change == ChangeRemoved {
			removedTables[t.name] = true
		}
	}
	views := p.viewMoves()
	cycled := p.cycledForeignKeys()

	if p.driver == DriverPostgres && p.hasRoutineWork() {
		// A function's body is read when it is made, and the order below makes
		// them after what they read; this is for the body that reads what a
		// later step makes.
		p.step("Do not read function bodies while the migration runs", false, "SET check_function_bodies = off")
	}
	p.enums()
	p.sequences(ChangeAdded)
	p.routines(routinesEarly)

	// Everything that depends on a table or a column goes before the change
	// that would break it: views first, then the keys and indexes that hold
	// the old shape in place.
	p.dropViews(views)
	for _, t := range d.tables {
		if t.change == ChangeChanged {
			p.dropForeignKeys(t)
		}
	}
	p.dropCycledForeignKeys(cycled)
	p.dropCrossTableForeignKeys(removedTables)
	for _, t := range d.tables {
		if t.change == ChangeChanged {
			p.dropIndexesAndConstraints(t)
		}
	}

	for _, t := range d.tables {
		if t.change == ChangeAdded {
			p.createTable(t)
		}
	}
	for _, t := range d.tables {
		if t.change == ChangeChanged {
			p.alterColumns(t)
		}
	}
	p.dropTables()

	for _, t := range d.tables {
		switch t.change {
		case ChangeAdded:
			if !p.createTextCarriesIndexes() {
				p.createIndexes(t.name, plainIndexes(t.to))
			}
		case ChangeChanged:
			p.addKeysAndIndexes(t)
		}
	}
	for _, t := range d.tables {
		switch t.change {
		case ChangeAdded:
			if !p.createTextCarriesForeignKeys() {
				p.addForeignKeys(t.name, t.to.ForeignKeys)
			}
		case ChangeChanged:
			for _, f := range t.foreign {
				if f.to != nil && !p.settled[settledKey(PartForeignKey, t.name, f.to.Name)] {
					p.addForeignKeys(t.name, []ForeignKey{*f.to})
				}
			}
		}
	}
	p.addCycledForeignKeys(cycled)
	p.ownSequences()
	p.routines(routinesLate)
	p.createViews(views)
	p.removedRoutines()
	p.sequences(ChangeRemoved, ChangeChanged)
	p.removedEnums()
}

// cycledForeignKey is a foreign key that did not change and is dropped and
// added again anyway, because it uses a column whose type changes.
type cycledForeignKey struct {
	table string
	key   ForeignKey
}

// cycledForeignKeys finds the unchanged keys on, or pointing at, a column that
// changes type. MySQL and SQL Server refuse the change while such a key stands;
// Postgres converts the key with the column.
func (p *migrationPlanner) cycledForeignKeys() []cycledForeignKey {
	if p.driver != DriverMySQL && p.driver != DriverMSSQL {
		return nil
	}
	retyped := map[string][]string{}
	for _, t := range p.diff.tables {
		if t.change == ChangeChanged {
			if cols := retypedColumns(t); len(cols) > 0 {
				retyped[t.name] = cols
			}
		}
	}
	if len(retyped) == 0 {
		return nil
	}
	inDiff := map[string]bool{}
	for _, t := range p.diff.tables {
		for _, f := range t.foreign {
			if f.from != nil {
				inDiff[settledKey(PartForeignKey, t.name, f.from.Name)] = true
			}
		}
	}
	names := make([]string, 0, len(p.from.Tables))
	for name := range p.from.Tables {
		names = append(names, name)
	}
	sort.Strings(names)
	var out []cycledForeignKey
	for _, name := range names {
		if _, gone := p.to.Tables[name]; !gone {
			continue
		}
		for _, fk := range p.from.Tables[name].ForeignKeys {
			if inDiff[settledKey(PartForeignKey, name, fk.Name)] {
				continue
			}
			if anyIn(fk.Columns, retyped[name]) || (fk.RefSchema == "" || fk.RefSchema == p.schema) && anyIn(fk.RefColumns, retyped[fk.RefTable]) {
				out = append(out, cycledForeignKey{name, fk})
			}
		}
	}
	return out
}

func anyIn(list, among []string) bool {
	return slices.ContainsFunc(list, func(s string) bool { return slices.Contains(among, s) })
}

func (p *migrationPlanner) dropCycledForeignKeys(keys []cycledForeignKey) {
	for _, c := range keys {
		plan, err := PlanDropForeignKey(p.driver, p.schema, c.table, c.key.Name)
		p.planned("Drop foreign key "+c.key.Name+" on "+c.table, false, plan, err)
		if err == nil {
			p.steps[len(p.steps)-1].Note = "It uses a column whose type changes, so it is added again after."
		}
	}
}

func (p *migrationPlanner) addCycledForeignKeys(keys []cycledForeignKey) {
	for _, c := range keys {
		p.addForeignKeys(c.table, []ForeignKey{c.key})
	}
}

// --- bookkeeping -------------------------------------------------------------

func (p *migrationPlanner) step(title string, destructive bool, statements ...string) {
	p.steps = append(p.steps, MigrationStep{Title: oneLine(title), Statements: statements, Destructive: destructive})
}

// manual records a change the generator cannot write for this engine.
func (p *migrationPlanner) manual(title string, destructive bool, note string) {
	p.steps = append(p.steps, MigrationStep{Title: oneLine(title), Destructive: destructive, Manual: true, Note: note})
}

// planned adds a generator's plan as a step, or the reason it could not be
// planned as a manual one.
func (p *migrationPlanner) planned(title string, destructive bool, plan *DDLPlan, err error) {
	if err != nil {
		p.manual(title, destructive, err.Error())
		return
	}
	step := MigrationStep{Title: oneLine(title), Statements: plan.Statements, Destructive: destructive}
	if len(plan.Unvouched) > 0 {
		step.Note = "Calls " + strings.Join(plan.Unvouched, ", ") + ", which the query editor will ask you to confirm."
	}
	p.steps = append(p.steps, step)
}

// rel names an object of the "from" schema as a statement writes it. A name
// the engine's quoting refuses is an error, never the bare name: a statement
// built on it would mean something other than what it was asked to.
func (p *migrationPlanner) rel(name string) (string, error) {
	d, err := DialectFor(p.driver)
	if err != nil {
		return "", err
	}
	return ddlRel(d, p.schema, name)
}

// relOr is rel, with the step handed to the operator when the name will not
// quote.
func (p *migrationPlanner) relOr(title string, destructive bool, name string) (string, bool) {
	rel, err := p.rel(name)
	if err != nil {
		p.manual(title, destructive, err.Error())
		return "", false
	}
	return rel, true
}

// rebase writes text taken from the "to" schema as it reads in the "from" one,
// where the two schemas have different names.
func (p *migrationPlanner) rebase(s string) string {
	if p.schema == p.toSchema || s == "" || p.toSchema == "" {
		return s
	}
	return rebaseSchemaName(s, p.toSchema, p.schema, p.driver == DriverMySQL)
}

func (p *migrationPlanner) refSchema(fk ForeignKey) string {
	if fk.RefSchema == "" || fk.RefSchema == p.toSchema {
		return p.schema
	}
	return fk.RefSchema
}

// --- enums and sequences -----------------------------------------------------

func (p *migrationPlanner) enums() {
	for _, e := range p.diff.enums {
		switch e.change {
		case ChangeAdded:
			plan, err := PlanCreateEnum(p.driver, p.schema, e.name, e.to)
			p.planned("Create enum type "+e.name, false, plan, err)
		case ChangeChanged:
			p.changeEnum(e)
		}
	}
}

// changeEnum adds the labels the "to" side has and the "from" side lacks. A
// label cannot be removed or reordered, which is the engine's rule, so a
// change that needs either is left to the operator.
func (p *migrationPlanner) changeEnum(e enumDiff) {
	title := "Change enum type " + e.name
	kept := make([]string, 0, len(e.from))
	for _, label := range e.to {
		if slices.Contains(e.from, label) {
			kept = append(kept, label)
		}
	}
	if len(kept) != len(e.from) || !slices.Equal(kept, e.from) {
		p.manual(title, false, fmt.Sprintf(
			"The type has labels (%s) where the target has (%s). Labels cannot be removed or reordered in place: create a new type, move the columns over to it and drop this one.",
			strings.Join(e.from, ", "), strings.Join(e.to, ", ")))
		return
	}
	for i, label := range e.to {
		if slices.Contains(e.from, label) {
			continue
		}
		spec := EnumValueSpec{Schema: p.schema, Name: e.name, Value: label}
		if i > 0 {
			spec.After = e.to[i-1]
		}
		plan, err := PlanAddEnumValue(p.driver, spec)
		p.planned("Add label "+label+" to enum type "+e.name, false, plan, err)
	}
}

func (p *migrationPlanner) removedEnums() {
	for _, e := range p.diff.enums {
		if e.change != ChangeRemoved {
			continue
		}
		title := "Drop enum type " + e.name
		if rel, ok := p.relOr(title, true, e.name); ok {
			p.step(title, true, "DROP TYPE "+rel)
		}
	}
}

// sequences plans the sequences that were added, changed or removed. A
// sequence a column owns is made with the column, so removing the column or
// its table removes it and it is not dropped a second time.
var ownedByClause = regexp.MustCompile(`(?i)\s+OWNED BY [^;]+`)

func (p *migrationPlanner) sequences(changes ...string) {
	for _, s := range p.diff.sequences {
		if !slices.Contains(changes, s.change) {
			continue
		}
		switch s.change {
		case ChangeAdded:
			// A sequence cannot be made owned by a column that is not there yet;
			// ownSequences hands it over once the tables exist.
			p.step("Create sequence "+s.name, false, ownedByClause.ReplaceAllString(p.rebase(s.to.Definition), ""))
		case ChangeChanged:
			p.manual("Change sequence "+s.name, false, "The sequence is defined as\n"+s.to.Definition+
				"\nin the target and as\n"+s.from.Definition+"\nhere. Alter it from the Query tab.")
		case ChangeRemoved:
			if p.ownerRemoved(s.from.Owner) {
				continue
			}
			title := "Drop sequence " + s.name
			if rel, ok := p.relOr(title, true, s.name); ok {
				p.step(title, true, "DROP SEQUENCE "+rel)
			}
		}
	}
}

// ownerRemoved reports whether the column a sequence belongs to, written as
// "table.column", is going away with the migration.
func (p *migrationPlanner) ownerRemoved(owner string) bool {
	table, column, ok := strings.Cut(owner, ".")
	if !ok {
		return false
	}
	for _, t := range p.diff.tables {
		if t.name != table {
			continue
		}
		if t.change == ChangeRemoved {
			return true
		}
		for _, c := range t.columns {
			if c.Name == column && c.Change == ChangeRemoved {
				return true
			}
		}
	}
	return false
}

// ownSequences hands each sequence the migration created back to the column it
// feeds, so it goes with the column as it does in the "to" schema.
func (p *migrationPlanner) ownSequences() {
	if p.driver != DriverPostgres {
		return
	}
	for _, s := range p.diff.sequences {
		if s.change != ChangeAdded || s.to.Owner == "" {
			continue
		}
		table, column, ok := strings.Cut(s.to.Owner, ".")
		if !ok {
			continue
		}
		d, err := DialectFor(p.driver)
		if err != nil {
			continue
		}
		col, err := d.QuoteIdent(column)
		if err != nil {
			continue
		}
		title := "Make " + table + "." + column + " the owner of sequence " + s.name
		seq, ok := p.relOr(title, false, s.name)
		owner, okOwner := p.relOr(title, false, table)
		if ok && okOwner {
			p.step(title, false, "ALTER SEQUENCE "+seq+" OWNED BY "+owner+"."+col)
		}
	}
}

// --- routines ----------------------------------------------------------------

var definerClause = regexp.MustCompile("(?i)\\s+DEFINER\\s*=\\s*(`[^`]*`|'[^']*'|[^\\s@]+)(\\s*@\\s*(`[^`]*`|'[^']*'|[^\\s]+))?")

// stripDefiner takes MySQL's DEFINER clause out of a definition: the account it
// names may not exist where the statement is run, and it is not a difference
// between two schemas.
func stripDefiner(driver Driver, text string) string {
	if driver != DriverMySQL {
		return text
	}
	return definerClause.ReplaceAllString(text, "")
}

// routinePhase says when in the migration a routine is made.
type routinePhase int

const (
	// routinesEarly are the ones a default, a check, a generated column or an
	// index predicate calls: they must exist before the table is made.
	routinesEarly routinePhase = iota
	// routinesLate are the rest. The engine reads a routine's body when it is
	// made — Postgres checks a SQL function's against its tables, SQL Server
	// its columns — so they wait for the tables and columns they read.
	routinesLate
)

// earlyRoutines names the routines the "to" tables call outside a routine.
func (p *migrationPlanner) earlyRoutines() map[string]bool {
	var corpus strings.Builder
	for _, t := range p.to.Tables {
		for _, c := range t.Columns {
			corpus.WriteString(" " + c.Default + " " + c.Generated)
		}
		for _, c := range t.Constraints {
			corpus.WriteString(" " + c.Definition)
		}
		for _, ix := range t.Indexes {
			corpus.WriteString(" " + ix.Predicate + " " + strings.Join(ix.Columns, " "))
		}
	}
	text := strings.ToLower(corpus.String())
	early := map[string]bool{}
	for key, r := range p.to.Routines {
		if mentions(text, strings.ToLower(r.Name)) {
			early[key] = true
		}
	}
	return early
}

func routineKeyOf(r SnapshotRoutine) string { return routineKey(r.Kind, r.Name, r.Signature) }

// hasRoutineWork reports whether the migration makes or replaces a routine.
func (p *migrationPlanner) hasRoutineWork() bool {
	for _, r := range p.diff.routines {
		if r.change != ChangeRemoved {
			return true
		}
	}
	return false
}

// routines makes and replaces the routines that are due in the phase.
func (p *migrationPlanner) routines(phase routinePhase) {
	early := p.earlyRoutines()
	for _, r := range p.diff.routines {
		if r.change == ChangeRemoved || early[routineKeyOf(*r.to)] != (phase == routinesEarly) {
			continue
		}
		label := r.to.Kind + " " + routineLabel(*r.to)
		definition := p.routineDefinition(*r.to)
		switch {
		case r.change == ChangeAdded:
			p.dropReplaced(*r.to)
			p.step("Create "+label, false, definition)
		case p.driver == DriverMSSQL:
			p.step("Replace "+label, false, createOrAlter(definition))
		case p.driver == DriverPostgres && p.canReplaceInPlace(*r.from, *r.to):
			p.step("Replace "+label, false, definition)
		default:
			// The engine cannot replace it in place — MySQL has no such
			// statement, and Postgres refuses to change a return type.
			if drop, ok := p.dropRoutine("Replace "+label, *r.from); ok {
				p.step("Replace "+label, true, drop, definition)
			}
		}
	}
}

var (
	returnsClause   = regexp.MustCompile(`(?mi)^\s*RETURNS\s+(.+)$`)
	replaceableHead = regexp.MustCompile(`(?i)^\s*CREATE\s+OR\s+REPLACE\b`)
)

// canReplaceInPlace reports whether CREATE OR REPLACE can turn the routine into
// the other: it is written that way, and the return type is the same.
func (p *migrationPlanner) canReplaceInPlace(from, to SnapshotRoutine) bool {
	if !replaceableHead.MatchString(to.Definition) {
		return false
	}
	returns := func(r SnapshotRoutine, rebase bool) string {
		text := r.Definition
		if rebase {
			text = p.rebase(text)
		}
		m := returnsClause.FindStringSubmatch(text)
		if m == nil {
			return ""
		}
		return strings.ToLower(strings.Join(strings.Fields(m[1]), " "))
	}
	return returns(from, false) == returns(to, true)
}

// dropReplaced drops, ahead of a routine that is made, the one of the same name
// that is going away: Postgres cannot rename a parameter of a routine by
// replacing it, and a routine with new parameter names is a new routine to the
// diff and the old one an old one.
func (p *migrationPlanner) dropReplaced(r SnapshotRoutine) {
	for _, old := range p.diff.routines {
		if old.change != ChangeRemoved || old.from.Kind != r.Kind || old.from.Name != r.Name {
			continue
		}
		key := "routine\x00" + routineKeyOf(*old.from)
		if p.settled[key] {
			continue
		}
		p.settled[key] = true
		title := "Drop " + old.from.Kind + " " + routineLabel(*old.from)
		if stmt, ok := p.dropRoutine(title, *old.from); ok {
			p.step(title, true, stmt)
		}
	}
}

func (p *migrationPlanner) removedRoutines() {
	for _, r := range p.diff.routines {
		if r.change != ChangeRemoved || p.settled["routine\x00"+routineKeyOf(*r.from)] {
			continue
		}
		title := "Drop " + r.from.Kind + " " + routineLabel(*r.from)
		if stmt, ok := p.dropRoutine(title, *r.from); ok {
			p.step(title, true, stmt)
		}
	}
}

var (
	// mysqlHead and mssqlHead match the head of a routine or view whose name
	// carries no schema: SQL Server writes a module as it was typed, and MySQL writes a
	// routine's name alone. Run in another schema, or in none, it would make
	// the object where the session happens to be.
	mysqlHead = regexp.MustCompile("(?is)^(\\s*CREATE\\s+(?:ALGORITHM\\s*=\\s*\\w+\\s+)?(?:SQL\\s+SECURITY\\s+\\w+\\s+)?(?:FUNCTION|PROCEDURE|VIEW)\\s+)(`(?:[^`]|``)+`)(\\s|\\(|$)")
	mssqlHead = regexp.MustCompile(`(?is)^(\s*CREATE\s+(?:OR\s+ALTER\s+)?(?:FUNCTION|PROCEDURE|PROC|VIEW)\s+)(\[[^\]]+\]|[A-Za-z_@#][\w@#$]*)(\s|\(|$)`)
)

// routineDefinition is a routine's text written for the "from" schema.
func (p *migrationPlanner) routineDefinition(r SnapshotRoutine) string {
	return p.routineDefinitionText(r.Definition)
}

// qualifiedHead names the object a CREATE statement makes in the "from"
// schema, where its text names it without one.
func (p *migrationPlanner) qualifiedHead(text string) string {
	var head *regexp.Regexp
	switch p.driver {
	case DriverMySQL:
		head = mysqlHead
	case DriverMSSQL:
		head = mssqlHead
	default:
		return text
	}
	d, err := DialectFor(p.driver)
	if err != nil {
		return text
	}
	schema, err := d.QuoteIdent(p.schema)
	if err != nil {
		return text
	}
	return head.ReplaceAllString(text, "${1}"+schema+".${2}${3}")
}

var createRoutine = regexp.MustCompile(`(?is)^((?:\s|--[^\n]*\n|/\*.*?\*/)*)(CREATE)(\s+)(FUNCTION|PROCEDURE|PROC|VIEW)\b`)

// createOrAlter turns SQL Server's CREATE FUNCTION into the form that replaces
// one that exists, however the statement is cased and whatever comments lead it.
func createOrAlter(definition string) string {
	m := createRoutine.FindStringSubmatchIndex(definition)
	if m == nil {
		return definition
	}
	// m[5] is the end of the CREATE keyword.
	keyword := "OR ALTER"
	if word := definition[m[4]:m[5]]; strings.ToLower(word) == word {
		keyword = "or alter"
	}
	return definition[:m[5]] + " " + keyword + definition[m[5]:]
}

// dropRoutine writes the statement that removes a routine, or hands the step
// over when its name will not quote.
func (p *migrationPlanner) dropRoutine(title string, r SnapshotRoutine) (string, bool) {
	word := "FUNCTION"
	if r.Kind == DiffProcedure {
		word = "PROCEDURE"
	}
	rel, ok := p.relOr(title, true, r.Name)
	if !ok {
		return "", false
	}
	stmt := "DROP " + word + " " + rel
	if p.driver == DriverPostgres {
		// The signature is the server's own text of the parameters, written
		// into the statement as it is: one with a line break in it is not.
		if strings.ContainsFunc(r.Signature, func(c rune) bool { return unicode.IsControl(c) }) {
			p.manual(title, true, "The routine's parameters contain a control character, so no statement is written for it. Drop it from the Query tab.")
			return "", false
		}
		stmt += "(" + r.Signature + ")"
	}
	return stmt, true
}

// --- views -------------------------------------------------------------------

// viewMove is a view the migration drops, makes, or both. A view that did not
// change is moved too when it reads one that is dropped or a table whose
// column changes type or goes: the engine refuses the change while the view is
// there, and the view is made again after it with the text it has.
type viewMove struct {
	name     string
	from, to *SnapshotView
	// reads is why an unchanged view is moved, and empty for a changed one.
	reads string
}

// viewMoves lists every view the migration touches.
func (p *migrationPlanner) viewMoves() []viewMove {
	var moves []viewMove
	dropped := map[string]bool{}
	for _, v := range p.diff.views {
		moves = append(moves, viewMove{name: v.name, from: v.from, to: v.to})
		if v.from != nil {
			dropped[v.name] = true
		}
	}
	touched := p.touchedTables()
	names := make([]string, 0, len(p.from.Views))
	for name := range p.from.Views {
		names = append(names, name)
	}
	sort.Strings(names)
	for grew := true; grew; {
		grew = false
		for _, name := range names {
			if dropped[name] {
				continue
			}
			to, kept := p.to.Views[name]
			if !kept {
				continue
			}
			view := p.from.Views[name]
			text := strings.ToLower(view.Definition)
			reads := ""
			for _, other := range names {
				if dropped[other] && mentions(text, strings.ToLower(other)) {
					reads = other
					break
				}
			}
			if reads == "" {
				for _, table := range touched {
					if mentions(text, strings.ToLower(table)) {
						reads = table
						break
					}
				}
			}
			if reads != "" {
				moves = append(moves, viewMove{name: name, from: &view, to: &to, reads: reads})
				dropped[name] = true
				grew = true
			}
		}
	}
	return moves
}

// touchedTables names the tables that lose a column or change one's type, or
// go altogether.
func (p *migrationPlanner) touchedTables() []string {
	var out []string
	for _, t := range p.diff.tables {
		if t.change == ChangeRemoved || len(retypedColumns(t)) > 0 || slices.ContainsFunc(t.columns, func(c columnDiff) bool { return c.Change == ChangeRemoved }) {
			out = append(out, t.name)
		}
	}
	return out
}

// retypedColumns are the columns of a table whose type is not the same after.
func retypedColumns(t tableDiff) []string {
	var out []string
	for _, c := range t.columns {
		if c.Change == ChangeChanged && c.from != nil && c.to != nil && !sameType(c.from.Type, c.to.Type) {
			out = append(out, c.Name)
		}
	}
	return out
}

func sameType(a, b string) bool {
	norm := func(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }
	return norm(a) == norm(b)
}

// dropViews removes the views that went away or changed, and the ones that
// read what does, those that read other views first. They are made again after
// the tables have their new shape, because most engines cannot replace a view
// whose columns changed.
func (p *migrationPlanner) dropViews(moves []viewMove) {
	var drop []viewMove
	for _, m := range moves {
		if m.from != nil {
			drop = append(drop, m)
		}
	}
	for _, m := range orderViews(drop, func(m viewMove) (string, string) { return m.name, m.from.Definition }, false) {
		word := "view"
		if m.from.Materialized {
			word = "materialized view"
		}
		title := "Drop " + word + " " + m.name
		plan, err := PlanDropView(p.driver, p.schema, m.name, m.from.Materialized)
		p.planned(title, m.reads == "", plan, err)
		if m.reads != "" && err == nil {
			p.steps[len(p.steps)-1].Note = "It reads " + m.reads + ", which changes, so it is made again after."
		}
	}
}

func (p *migrationPlanner) createViews(moves []viewMove) {
	var create []viewMove
	for _, m := range moves {
		if m.to != nil {
			create = append(create, m)
		}
	}
	for _, m := range orderViews(create, func(m viewMove) (string, string) { return m.name, m.to.Definition }, true) {
		word := "view"
		if m.to.Materialized {
			word = "materialized view"
		}
		p.step("Create "+word+" "+m.name, false, p.routineDefinitionText(m.to.Definition))
	}
}

// routineDefinitionText is a definition read from the "to" schema, written for
// the "from" one.
func (p *migrationPlanner) routineDefinitionText(text string) string {
	return p.qualifiedHead(p.rebase(text))
}

// orderViews sorts views so each comes after the ones it reads (to create) or
// before them (to drop). A view reads another when its text names it; that is
// a guess from the text, and it is only ever wrong in the direction of a view
// that is listed in an order the engine accepts anyway.
func orderViews[V any](views []V, read func(V) (name, definition string), dependenciesFirst bool) []V {
	type node struct {
		view V
		name string
		deps []int
	}
	nodes := make([]node, len(views))
	for i, v := range views {
		name, _ := read(v)
		nodes[i] = node{view: v, name: name}
	}
	sort.SliceStable(nodes, func(i, j int) bool { return nodes[i].name < nodes[j].name })
	for i := range nodes {
		_, text := read(nodes[i].view)
		text = strings.ToLower(text)
		for j := range nodes {
			if i != j && mentions(text, strings.ToLower(nodes[j].name)) {
				nodes[i].deps = append(nodes[i].deps, j)
			}
		}
	}
	out := make([]V, 0, len(nodes))
	placed := make([]bool, len(nodes))
	for len(out) < len(nodes) {
		progressed := false
		for i := range nodes {
			if placed[i] {
				continue
			}
			ready := true
			for _, d := range nodes[i].deps {
				if !placed[d] {
					ready = false
					break
				}
			}
			if ready {
				placed[i], progressed = true, true
				out = append(out, nodes[i].view)
			}
		}
		if !progressed {
			// A cycle cannot be real in a database; take the rest in name order.
			for i := range nodes {
				if !placed[i] {
					placed[i] = true
					out = append(out, nodes[i].view)
				}
			}
		}
	}
	if !dependenciesFirst {
		slices.Reverse(out)
	}
	return out
}

// mentions reports whether text names the identifier on a word boundary.
func mentions(text, name string) bool {
	for from := 0; ; {
		i := strings.Index(text[from:], name)
		if i < 0 {
			return false
		}
		i += from
		end := i + len(name)
		if (i == 0 || !isIdentByte(text[i-1])) && (end >= len(text) || !isIdentByte(text[end])) {
			return true
		}
		from = end
	}
}

// --- tables ------------------------------------------------------------------

// createTextCarriesForeignKeys and createTextCarriesIndexes say what the
// CREATE TABLE written for a new table already holds, so the steps after it do
// not make it a second time. Every engine's text holds the primary key and the
// unique and check constraints.
func (p *migrationPlanner) createTextCarriesForeignKeys() bool { return p.driver == DriverSQLite }
func (p *migrationPlanner) createTextCarriesIndexes() bool     { return p.driver == DriverMySQL }

func (p *migrationPlanner) createTable(t tableDiff) {
	title := "Create table " + t.name
	statement, err := p.createTableStatement(t.to)
	if err != nil {
		p.manual(title, false, err.Error())
		return
	}
	step := MigrationStep{Title: oneLine(title), Statements: []string{statement}}
	if comments := p.createTableComments(t.to); len(comments) > 0 {
		step.Statements = append(step.Statements, comments...)
	}
	p.steps = append(p.steps, step)
}

// createTableStatement writes the CREATE TABLE for a table the "from" schema
// lacks. Where the engine keeps the text of a table it is that text — the only
// form that holds everything the engine knows about it — and elsewhere it is
// the one the schema page shows, made from the catalogue. Foreign keys are
// left out of both: they are added once every table exists, so the tables can
// be created in any order.
func (p *migrationPlanner) createTableStatement(t *TableDetail) (string, error) {
	d, err := DialectFor(p.driver)
	if err != nil {
		return "", err
	}
	switch p.driver {
	case DriverSQLite:
		if strings.TrimSpace(t.CreateSQL) == "" {
			return "", fmt.Errorf("the engine gave no CREATE statement for the table")
		}
		return strings.TrimRight(strings.TrimSpace(t.CreateSQL), ";"), nil
	case DriverMySQL:
		return p.mysqlCreateTable(d, t)
	}
	// renderCreateTable writes a name the engine will not quote as it is, which
	// is a table that means something else. Refuse it here instead.
	names := append([]string{t.Name}, t.PrimaryKey...)
	for _, c := range t.Columns {
		names = append(names, c.Name)
	}
	for _, c := range t.Constraints {
		names = append(names, c.Name)
	}
	for _, name := range names {
		if name == "" {
			continue
		}
		if _, err := d.QuoteIdent(name); err != nil {
			return "", fmt.Errorf("the name %q cannot be written as an identifier: %v", oneLine(name), err)
		}
	}
	copied := *t
	copied.Columns = make([]Column, len(t.Columns))
	for i, c := range t.Columns {
		c.Type, c.Default, c.Generated = p.rebase(c.Type), p.rebase(c.Default), p.rebase(c.Generated)
		c.Comment = ""
		copied.Columns[i] = c
	}
	copied.Constraints = make([]Constraint, len(t.Constraints))
	for i, c := range t.Constraints {
		c.Definition = p.rebase(c.Definition)
		copied.Constraints[i] = c
	}
	// The exclusion constraints have no home in a diff, and a unique one is
	// written with the table.
	copied.ForeignKeys, copied.Comment = nil, ""
	copied.Indexes = nil
	for _, ix := range t.Indexes {
		// SQL Server names a key nobody named PK__table__hash, after the table
		// and a number of its own; the server makes its own for the new table.
		if ix.Primary && !(p.driver == DriverMSSQL && strings.HasPrefix(ix.Constraint, "PK__")) {
			copied.Indexes = append(copied.Indexes, ix)
		}
	}
	statement := renderCreateTable(d, p.schema, t.Name, &copied, true)
	return strings.TrimRight(strings.TrimSpace(statement), ";"), nil
}

var (
	mysqlCreateHead   = regexp.MustCompile("^CREATE TABLE (`(?:[^`]|``)+`)")
	mysqlAutoIncStart = regexp.MustCompile(` AUTO_INCREMENT=\d+`)
)

// mysqlCreateTable adapts SHOW CREATE TABLE for another schema: the table is
// named in the "from" schema, the foreign keys are taken out for the steps
// that add them, and the counter the source table had reached is not carried
// to a table that is empty.
func (p *migrationPlanner) mysqlCreateTable(d Dialect, t *TableDetail) (string, error) {
	text := strings.TrimSpace(t.CreateSQL)
	head := mysqlCreateHead.FindStringSubmatch(text)
	if head == nil {
		return "", fmt.Errorf("the engine's CREATE statement for the table could not be read")
	}
	rel, err := ddlRel(d, p.schema, t.Name)
	if err != nil {
		return "", err
	}
	lines := strings.Split(text, "\n")
	lines[0] = strings.Replace(lines[0], head[1], rel, 1)
	kept := lines[:0:0]
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "CONSTRAINT ") && strings.Contains(trimmed, " FOREIGN KEY ") {
			continue
		}
		kept = append(kept, line)
	}
	// The last definition line before the closing parenthesis carries no comma.
	for i := len(kept) - 1; i > 0; i-- {
		if strings.HasPrefix(kept[i], ")") {
			kept[i-1] = strings.TrimSuffix(kept[i-1], ",")
			kept[i] = mysqlAutoIncStart.ReplaceAllString(kept[i], "")
			break
		}
	}
	return p.rebase(strings.Join(kept, "\n")), nil
}

// createTableComments writes the comments of a new table for the engines that
// set them with a statement of their own.
func (p *migrationPlanner) createTableComments(t *TableDetail) []string {
	d, err := DialectFor(p.driver)
	if err != nil {
		return nil
	}
	rel, err := p.rel(t.Name)
	if err != nil {
		return nil
	}
	return commentStatements(d, rel, t)
}

func (p *migrationPlanner) dropTables() {
	var gone []*TableDetail
	for _, t := range p.diff.tables {
		if t.change == ChangeRemoved {
			gone = append(gone, t.from)
		}
	}
	for _, t := range orderTablesForDrop(gone) {
		plan, err := PlanDropTable(p.driver, p.schema, t.Name)
		p.planned("Drop table "+t.Name, true, plan, err)
	}
}

// orderTablesForDrop sorts tables so each is dropped before the ones it
// references. The tables of a cycle keep their name order; dropCrossTableForeignKeys
// has already removed the keys that would stop them.
func orderTablesForDrop(tables []*TableDetail) []*TableDetail {
	sorted := slices.Clone(tables)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	names := map[string]bool{}
	for _, t := range sorted {
		names[t.Name] = true
	}
	out := make([]*TableDetail, 0, len(sorted))
	placed := map[string]bool{}
	for len(out) < len(sorted) {
		progressed := false
		for _, t := range sorted {
			if placed[t.Name] {
				continue
			}
			// A table can go once nothing left in the list still points at it.
			blocked := false
			for _, other := range sorted {
				if placed[other.Name] || other.Name == t.Name {
					continue
				}
				for _, fk := range other.ForeignKeys {
					if fk.RefTable == t.Name {
						blocked = true
					}
				}
			}
			if !blocked {
				placed[t.Name], progressed = true, true
				out = append(out, t)
			}
		}
		if !progressed {
			for _, t := range sorted {
				if !placed[t.Name] {
					placed[t.Name] = true
					out = append(out, t)
				}
			}
		}
	}
	return out
}

// dropCrossTableForeignKeys drops, ahead of the tables that go, the keys that
// would make two of them wait for each other. A key between two tables that go
// is only in the way when the tables point at each other, which the order of
// the drops cannot resolve.
func (p *migrationPlanner) dropCrossTableForeignKeys(removed map[string]bool) {
	var gone []*TableDetail
	for _, t := range p.diff.tables {
		if t.change == ChangeRemoved {
			gone = append(gone, t.from)
		}
	}
	ordered := orderTablesForDrop(gone)
	position := map[string]int{}
	for i, t := range ordered {
		position[t.Name] = i
	}
	for _, t := range ordered {
		for _, fk := range t.ForeignKeys {
			ref, ok := position[fk.RefTable]
			if !ok || !removed[fk.RefTable] || ref >= position[t.Name] {
				continue
			}
			// This table is dropped after the one it references: the pair is a cycle.
			plan, err := PlanDropForeignKey(p.driver, p.schema, t.Name, fk.Name)
			p.planned("Drop foreign key "+fk.Name+" on "+t.Name, true, plan, err)
		}
	}
}

// --- keys, indexes and constraints -------------------------------------------

func (p *migrationPlanner) dropForeignKeys(t tableDiff) {
	for _, f := range t.foreign {
		if f.from == nil {
			continue
		}
		plan, err := PlanDropForeignKey(p.driver, p.schema, t.name, f.from.Name)
		if err != nil && f.to != nil {
			// A key the engine cannot drop it cannot add again either: one
			// step says so, in place of two that say the same.
			p.settled[settledKey(PartForeignKey, t.name, f.to.Name)] = true
			p.manual("Change foreign key "+f.from.Name+" on "+t.name, true, err.Error()+"\nIt is "+f.From+" here and "+f.To+" in the target.")
			continue
		}
		p.planned("Drop foreign key "+f.from.Name+" on "+t.name, true, plan, err)
	}
}

func settledKey(part, table, name string) string { return part + "\x00" + table + "\x00" + name }

func (p *migrationPlanner) dropIndexesAndConstraints(t tableDiff) {
	for _, c := range t.constraints {
		if c.from == nil {
			continue
		}
		plan, err := PlanDropConstraint(p.ctx, p.db, p.driver, p.schema, t.name, c.from.Name, c.from.Type)
		if err != nil && c.to != nil {
			p.settled[settledKey(c.Part, t.name, c.to.Name)] = true
			p.manual("Change "+c.Part+" "+c.from.Name+" on "+t.name, true, err.Error()+"\nIt is "+c.From+" here and "+c.To+" in the target.")
			continue
		}
		p.planned("Drop "+c.Part+" "+c.from.Name+" on "+t.name, true, plan, err)
	}
	for _, i := range t.indexes {
		if i.from == nil {
			continue
		}
		plan, err := PlanDropIndex(p.driver, p.schema, t.name, i.from.Name)
		if err != nil && i.to != nil {
			p.settled[settledKey(PartIndex, t.name, i.to.Name)] = true
			p.manual("Change index "+i.from.Name+" on "+t.name, true, err.Error()+"\nIt is "+i.From+" here and "+i.To+" in the target.")
			continue
		}
		p.planned("Drop index "+i.from.Name+" on "+t.name, true, plan, err)
	}
	if t.primary != nil && len(t.primary.from) > 0 {
		p.dropPrimaryKey(t)
	}
}

func (p *migrationPlanner) dropPrimaryKey(t tableDiff) {
	title := "Drop the primary key of " + t.name
	if p.driver == DriverSQLite {
		p.manual(title, true, "SQLite cannot change the primary key of an existing table. Rebuild the table from the Query tab: create the new shape, copy the rows, swap the names.")
		return
	}
	if p.driver == DriverMySQL {
		if rel, ok := p.relOr(title, true, t.name); ok {
			p.step(title, true, "ALTER TABLE "+rel+" DROP PRIMARY KEY")
		}
		return
	}
	name := primaryKeyName(t.from)
	d, err := DialectFor(p.driver)
	if err != nil || name == "" {
		p.manual(title, true, "The engine did not name the primary key, so no statement can drop it. Drop it from the Query tab.")
		return
	}
	q, err := d.QuoteIdent(name)
	if err != nil {
		p.manual(title, true, err.Error())
		return
	}
	if rel, ok := p.relOr(title, true, t.name); ok {
		p.step(title, true, "ALTER TABLE "+rel+" DROP CONSTRAINT "+q)
	}
}

func primaryKeyName(t *TableDetail) string {
	for _, ix := range t.Indexes {
		if ix.Primary {
			if ix.Constraint != "" {
				return ix.Constraint
			}
			return ix.Name
		}
	}
	return ""
}

func (p *migrationPlanner) addKeysAndIndexes(t tableDiff) {
	if t.primary != nil && len(t.primary.to) > 0 {
		title := "Add the primary key of " + t.name
		d, err := DialectFor(p.driver)
		switch {
		case p.driver == DriverSQLite:
			p.manual(title, false, "SQLite cannot add a primary key to an existing table. Rebuild the table from the Query tab: create the new shape, copy the rows, swap the names.")
		case err != nil:
			p.manual(title, false, err.Error())
		default:
			cols, cerr := quoteColumns(d, "a primary key", t.primary.to)
			if cerr != nil {
				p.manual(title, false, cerr.Error())
			} else {
				if rel, ok := p.relOr(title, false, t.name); ok {
					p.step(title, false, "ALTER TABLE "+rel+" ADD PRIMARY KEY ("+cols+")")
				}
			}
		}
	}
	var constraints []Constraint
	for _, c := range t.constraints {
		if c.to != nil && !p.settled[settledKey(c.Part, t.name, c.to.Name)] {
			constraints = append(constraints, *c.to)
		}
	}
	p.addConstraints(t.name, constraints)
	var indexes []Index
	for _, i := range t.indexes {
		if i.to != nil && !p.settled[settledKey(PartIndex, t.name, i.to.Name)] {
			indexes = append(indexes, *i.to)
		}
	}
	p.createIndexes(t.name, indexes)
}

func (p *migrationPlanner) addConstraints(table string, constraints []Constraint) {
	for _, c := range constraints {
		spec := ConstraintSpec{Schema: p.schema, Table: table, Name: c.Name, Type: c.Type}
		if c.Type == ConstraintUnique {
			spec.Columns = c.Columns
		} else {
			spec.Expression = p.rebase(checkExpression(c.Definition))
		}
		plan, err := PlanAddConstraint(p.driver, spec)
		p.planned("Add "+constraintPart(c)+" "+c.Name+" to "+table, false, plan, err)
	}
}

// checkExpression takes the condition out of a clause written CHECK (…).
func checkExpression(definition string) string {
	text := strings.TrimSpace(definition)
	if len(text) >= 5 && strings.EqualFold(text[:5], "CHECK") {
		text = strings.TrimSpace(text[5:])
	}
	return unwrapParens(text)
}

func (p *migrationPlanner) createIndexes(table string, indexes []Index) {
	for _, ix := range indexes {
		title := "Create index " + ix.Name + " on " + table
		spec := IndexSpec{
			Schema: p.schema, Table: table, Name: ix.Name, Columns: ix.Columns, Unique: ix.Unique,
			Method: indexMethodFor(p.driver, ix.Method), Where: p.rebase(ix.Predicate),
		}
		var (
			plan *DDLPlan
			err  error
		)
		if ix.Expression || len(ix.Include) > 0 {
			// The generator names columns; an expression or an included column
			// is only in the engine's own text of the index.
			err = fmt.Errorf("the index has an expression or included columns")
		} else {
			plan, err = PlanCreateIndex(p.ctx, p.db, p.driver, spec)
		}
		if err != nil {
			if definition := strings.TrimSpace(ix.Definition); definition != "" {
				p.step(title, false, p.rebase(definition))
				continue
			}
		}
		p.planned(title, false, plan, err)
	}
}

// indexMethodFor turns the method a catalogue reports into the one a CREATE
// INDEX names, leaving out the engine's own default.
func indexMethodFor(driver Driver, method string) string {
	method = strings.ToLower(strings.TrimSpace(method))
	switch {
	case method == "btree" || method == "nonclustered":
		return ""
	case driver == DriverSQLite:
		return ""
	}
	return method
}

func (p *migrationPlanner) addForeignKeys(table string, keys []ForeignKey) {
	for _, fk := range keys {
		plan, err := PlanAddForeignKey(p.driver, ForeignKeySpec{
			Schema: p.schema, Table: table, Name: fk.Name, Columns: fk.Columns,
			RefSchema: p.refSchema(fk), RefTable: fk.RefTable, RefColumns: fk.RefColumns,
			OnDelete: fk.OnDelete, OnUpdate: fk.OnUpdate,
		})
		p.planned("Add foreign key "+fk.Name+" to "+table, false, plan, err)
	}
}

// --- columns -----------------------------------------------------------------

// castLiteral is a literal Postgres reports with the cast it was stored under:
// 'ok'::text, (0)::numeric. The cast says nothing the column's own type does
// not, and a default is only written by the DDL generator as a bare literal.
var castLiteral = regexp.MustCompile(`^\(?(-?[0-9]+(?:\.[0-9]+)?|'[^'\\]*')\)?::[A-Za-z_][A-Za-z0-9_ ]*(?:\([0-9, ]+\))?(?:\[\])?$`)

// plainDefault takes the redundant cast off a literal default.
func plainDefault(driver Driver, def string) string {
	switch driver {
	case DriverPostgres:
		if m := castLiteral.FindStringSubmatch(strings.TrimSpace(def)); m != nil {
			return m[1]
		}
	case DriverMSSQL:
		// SQL Server reports every default wrapped in parentheses: ((0)), ('x').
		return unwrapParens(def)
	}
	return def
}

// unwrapParens takes off the parentheses that enclose the whole of s, however
// many deep, and leaves any that enclose only a part of it.
func unwrapParens(s string) string {
	s = strings.TrimSpace(s)
	for len(s) >= 2 && s[0] == '(' && s[len(s)-1] == ')' && enclosesAll(s) {
		s = strings.TrimSpace(s[1 : len(s)-1])
	}
	return s
}

// enclosesAll reports whether the parenthesis that opens s closes at its end,
// not counting any inside a quoted string.
func enclosesAll(s string) bool {
	depth, quoted := 0, false
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '\'':
			quoted = !quoted
		case quoted:
		case c == '(':
			depth++
		case c == ')':
			depth--
			if depth == 0 && i != len(s)-1 {
				return false
			}
		}
	}
	return depth == 0
}

func (p *migrationPlanner) alterColumns(t tableDiff) {
	for _, c := range t.columns {
		if c.Change == ChangeAdded {
			p.addColumn(t.name, *c.to)
		}
	}
	for _, c := range t.columns {
		if c.Change == ChangeChanged {
			p.alterColumn(t.name, *c.from, *c.to)
		}
	}
	for _, c := range t.columns {
		if c.Change == ChangeRemoved {
			plan, err := PlanDropColumn(p.ctx, p.db, p.driver, p.schema, t.name, c.from.Name)
			p.planned("Drop column "+c.from.Name+" from "+t.name, true, plan, err)
		}
	}
}

func (p *migrationPlanner) addColumn(table string, c Column) {
	title := "Add column " + c.Name + " to " + table
	if c.Identity != "" || c.Generated != "" {
		p.manual(title, false, "The column is "+describeColumn(c)+", which the add-column form cannot write. Add it from the Query tab.")
		return
	}
	plan, err := PlanAddColumn(p.driver, p.schema, table, NewColumn{
		Name: c.Name, Type: p.rebase(c.Type), NotNull: !c.Nullable, Default: plainDefault(p.driver, p.rebase(c.Default)),
	})
	p.planned(title, false, plan, err)
}

// alterColumn changes what differs about a column: its type, whether it takes
// NULL and its default. Changing the type rewrites every value, so it is
// marked destructive.
func (p *migrationPlanner) alterColumn(table string, from, to Column) {
	title := "Change column " + to.Name + " of " + table
	if from.Identity != to.Identity || from.Generated != to.Generated || from.GeneratedKind != to.GeneratedKind {
		p.manual(title, false, "The column is "+describeColumn(from)+" here and "+describeColumn(to)+
			" in the target. Identity and generated columns cannot be altered in place by the column form; change it from the Query tab.")
		return
	}
	change := ColumnChange{Schema: p.schema, Table: table, Column: to.Name}
	retypes := strings.ToLower(strings.Join(strings.Fields(from.Type), " ")) !=
		strings.ToLower(strings.Join(strings.Fields(p.rebase(to.Type)), " "))
	if retypes {
		change.Type = p.rebase(to.Type)
	}
	if from.Nullable != to.Nullable {
		nullable := to.Nullable
		change.Nullable = &nullable
	}
	if strings.TrimSpace(from.Default) != strings.TrimSpace(p.rebase(to.Default)) {
		if strings.TrimSpace(to.Default) == "" {
			change.DropDefault = true
		} else {
			def := plainDefault(p.driver, p.rebase(to.Default))
			change.Default = &def
		}
	}
	plan, err := PlanAlterColumn(p.ctx, p.db, p.driver, change)
	if err != nil {
		p.manual(title, retypes, err.Error())
		return
	}
	step := MigrationStep{Title: oneLine(title), Statements: plan.Statements, Destructive: retypes}
	if retypes {
		step.Note = "The values are converted to the new type; a value that does not convert stops the statement."
	}
	if change.Nullable != nil && !*change.Nullable {
		step.Note = strings.TrimSpace(step.Note + "\nThe column refuses NULL afterwards, so it fails while any row holds one.")
	}
	p.steps = append(p.steps, step)
}
