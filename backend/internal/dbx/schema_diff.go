package dbx

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// Comparing two schemas of one engine.
//
// A schema is read into a SchemaSnapshot (schema_diff_read.go), two snapshots
// are compared into a SchemaDiff, and the difference is planned into the
// statements that turn the first into the second (schema_diff_plan.go). Nothing
// here runs a statement: the plan is text the operator copies or opens in the
// query editor, so a mistake in it costs a read, not a database.
//
// The side the statements run on is "from" and the side they make it look like
// is "to". A name that exists on both sides is compared part by part; a name on
// one side is added or removed whole.

// Kinds of object a schema diff compares. Tables are the only one with parts.
const (
	DiffTable            = "table"
	DiffView             = "view"
	DiffMaterializedView = "materialized view"
	DiffSequence         = "sequence"
	DiffEnum             = "enum"
	DiffFunction         = "function"
	DiffProcedure        = "procedure"
)

// What happened to a thing between the two sides.
const (
	ChangeAdded   = "added"
	ChangeRemoved = "removed"
	ChangeChanged = "changed"
)

// The parts of a table the diff names.
const (
	PartColumn     = "column"
	PartPrimaryKey = "primary key"
	PartIndex      = "index"
	PartUnique     = "unique constraint"
	PartCheck      = "check constraint"
	PartForeignKey = "foreign key"
)

// SchemaSnapshot is one schema as the comparison reads it.
type SchemaSnapshot struct {
	Driver Driver
	Flavor string
	Schema string
	Tables map[string]*TableDetail
	Views  map[string]SnapshotView
	// Sequences and Enums are keyed by name; an enum is its labels in order.
	Sequences map[string]SnapshotSequence
	Enums     map[string][]string
	// Routines is keyed by kind, name and signature, because an overload is a
	// different routine.
	Routines map[string]SnapshotRoutine
	// Unread names a catalogue group that could not be read whole, with the
	// reason. A group unread on either side is not compared.
	Unread map[string]string
}

// SnapshotView is a view and the text that defines it.
type SnapshotView struct {
	Name         string
	Materialized bool
	Definition   string
}

// SnapshotSequence is a sequence and the text that creates it. Owner is the
// "table.column" it feeds, which is why it goes with the column and not on its
// own.
type SnapshotSequence struct {
	Name       string
	Owner      string
	Definition string
}

// SnapshotRoutine is a function or a procedure as the text that creates it.
type SnapshotRoutine struct {
	Kind       string
	Name       string
	Signature  string
	Definition string
}

// PartDiff is one difference inside a table or a text object, with each side
// as it would be written in a definition so the two can be read side by side.
type PartDiff struct {
	Part   string `json:"part"`
	Name   string `json:"name"`
	Change string `json:"change"`
	From   string `json:"from,omitempty"`
	To     string `json:"to,omitempty"`
}

// ObjectDiff is one object that differs: added, removed, or changed in the
// parts it lists. A view, function or sequence that changed lists its two
// definitions as the one part.
type ObjectDiff struct {
	Kind   string     `json:"kind"`
	Name   string     `json:"name"`
	Change string     `json:"change"`
	Parts  []PartDiff `json:"parts"`
}

// SchemaDiff is everything that differs, in the order a reader meets it, and
// the typed form the planner works from.
type SchemaDiff struct {
	Objects []ObjectDiff `json:"objects"`

	tables    []tableDiff
	views     []viewDiff
	sequences []sequenceDiff
	enums     []enumDiff
	routines  []routineDiff
}

// Empty reports whether the two schemas are alike in everything compared.
func (d *SchemaDiff) Empty() bool { return len(d.Objects) == 0 }

type tableDiff struct {
	name     string
	change   string
	from, to *TableDetail

	columns     []columnDiff
	primary     *keyDiff
	indexes     []indexDiff
	constraints []constraintDiff
	foreign     []foreignKeyDiff
}

type columnDiff struct {
	PartDiff
	from, to *Column
}

type keyDiff struct {
	PartDiff
	from, to []string
}

type indexDiff struct {
	PartDiff
	from, to *Index
}

type constraintDiff struct {
	PartDiff
	from, to *Constraint
}

type foreignKeyDiff struct {
	PartDiff
	from, to *ForeignKey
}

type viewDiff struct {
	name     string
	change   string
	from, to *SnapshotView
}

type sequenceDiff struct {
	name     string
	change   string
	from, to *SnapshotSequence
}

type enumDiff struct {
	name     string
	change   string
	from, to []string
}

type routineDiff struct {
	change   string
	from, to *SnapshotRoutine
}

// DiffSchemas compares two snapshots of one engine. Where the schemas have
// different names, text that spells the schema's own name out is read with the
// name taken away, so the same table in two schemas is not different for being
// in them.
func DiffSchemas(from, to *SchemaSnapshot) *SchemaDiff {
	mask := schemaMasks(from.Schema, to.Schema, from.Driver == DriverMySQL)
	diff := &SchemaDiff{}
	// A kind of object unread on either side is not compared at all: its
	// absence from one snapshot is the reading's, not the schema's.
	unread := func(groups ...string) bool {
		for _, g := range groups {
			if _, no := from.Unread[g]; no {
				return true
			}
			if _, no := to.Unread[g]; no {
				return true
			}
		}
		return false
	}
	viewsOf := func(s *SchemaSnapshot) map[string]SnapshotView {
		out := map[string]SnapshotView{}
		for name, v := range s.Views {
			if unread(viewGroup(v.Materialized)) {
				continue
			}
			out[name] = v
		}
		return out
	}
	routinesOf := func(s *SchemaSnapshot) map[string]SnapshotRoutine {
		out := map[string]SnapshotRoutine{}
		for key, r := range s.Routines {
			if unread(routineGroup(r.Kind)) {
				continue
			}
			out[key] = r
		}
		return out
	}
	fromViews, toViews := viewsOf(from), viewsOf(to)
	fromRoutines, toRoutines := routinesOf(from), routinesOf(to)
	fromSequences, toSequences := from.Sequences, to.Sequences
	fromEnums, toEnums := from.Enums, to.Enums
	if unread(GroupSequences) {
		fromSequences, toSequences = nil, nil
	}
	if unread(GroupTypes) {
		fromEnums, toEnums = nil, nil
	}

	for _, name := range unionNames(objectNames(from.Tables), objectNames(to.Tables)) {
		a, b := from.Tables[name], to.Tables[name]
		switch {
		case a == nil:
			diff.tables = append(diff.tables, tableDiff{name: name, change: ChangeAdded, to: b})
		case b == nil:
			diff.tables = append(diff.tables, tableDiff{name: name, change: ChangeRemoved, from: a})
		default:
			if t := diffTable(name, a, b, mask); t != nil {
				diff.tables = append(diff.tables, *t)
			}
		}
	}
	for _, name := range unionNames(objectNames(fromViews), objectNames(toViews)) {
		a, inFrom := fromViews[name]
		b, inTo := toViews[name]
		switch {
		case !inFrom:
			diff.views = append(diff.views, viewDiff{name: name, change: ChangeAdded, to: &b})
		case !inTo:
			diff.views = append(diff.views, viewDiff{name: name, change: ChangeRemoved, from: &a})
		case a.Materialized != b.Materialized || mask.text(a.Definition, false) != mask.text(b.Definition, true):
			diff.views = append(diff.views, viewDiff{name: name, change: ChangeChanged, from: &a, to: &b})
		}
	}
	for _, name := range unionNames(objectNames(fromSequences), objectNames(toSequences)) {
		a, inFrom := fromSequences[name]
		b, inTo := toSequences[name]
		switch {
		case !inFrom:
			diff.sequences = append(diff.sequences, sequenceDiff{name: name, change: ChangeAdded, to: &b})
		case !inTo:
			diff.sequences = append(diff.sequences, sequenceDiff{name: name, change: ChangeRemoved, from: &a})
		case mask.text(a.Definition, false) != mask.text(b.Definition, true):
			diff.sequences = append(diff.sequences, sequenceDiff{name: name, change: ChangeChanged, from: &a, to: &b})
		}
	}
	for _, name := range unionNames(objectNames(fromEnums), objectNames(toEnums)) {
		a, inFrom := fromEnums[name]
		b, inTo := toEnums[name]
		switch {
		case !inFrom:
			diff.enums = append(diff.enums, enumDiff{name: name, change: ChangeAdded, to: b})
		case !inTo:
			diff.enums = append(diff.enums, enumDiff{name: name, change: ChangeRemoved, from: a})
		case !slices.Equal(a, b):
			diff.enums = append(diff.enums, enumDiff{name: name, change: ChangeChanged, from: a, to: b})
		}
	}
	for _, key := range unionNames(objectNames(fromRoutines), objectNames(toRoutines)) {
		a, inFrom := fromRoutines[key]
		b, inTo := toRoutines[key]
		switch {
		case !inFrom:
			diff.routines = append(diff.routines, routineDiff{change: ChangeAdded, to: &b})
		case !inTo:
			diff.routines = append(diff.routines, routineDiff{change: ChangeRemoved, from: &a})
		case routineText(mask.text(a.Definition, false)) != routineText(mask.text(b.Definition, true)):
			diff.routines = append(diff.routines, routineDiff{change: ChangeChanged, from: &a, to: &b})
		}
	}
	diff.Objects = diff.describe()
	return diff
}

func objectNames[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// unionNames is the names of both lists once each, in a stable order.
func unionNames(a, b []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(a)+len(b))
	for _, list := range [][]string{a, b} {
		for _, name := range list {
			if !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		}
	}
	sort.Strings(out)
	return out
}

// routineText compares a routine's definition without its whitespace, which an
// engine reformats and a person re-indents.
func routineText(s string) string { return strings.Join(strings.Fields(s), " ") }

// --- schema names ------------------------------------------------------------

// schemaMask removes the schema's own name from a piece of definition text so
// two schemas can be compared by what is in them.
type schemaMask struct {
	from, to  string
	backslash bool
}

func schemaMasks(from, to string, backslash bool) schemaMask {
	if from == to {
		return schemaMask{}
	}
	return schemaMask{from: from, to: to, backslash: backslash}
}

// text returns s with the name of the schema it came from taken out. side is
// true for the "to" side.
func (m schemaMask) text(s string, toSide bool) string {
	name := m.from
	if toSide {
		name = m.to
	}
	if name == "" || s == "" {
		return s
	}
	return stripSchemaName(s, name, m.backslash)
}

func isIdentByte(c byte) bool {
	return c == '_' || c == '$' || isASCIILetter(c) || isASCIIDigit(c) || c >= 0x80
}

// --- tables ------------------------------------------------------------------

func diffTable(name string, from, to *TableDetail, mask schemaMask) *tableDiff {
	t := &tableDiff{name: name, change: ChangeChanged, from: from, to: to}

	fromColumns, toColumns := columnsByName(from.Columns), columnsByName(to.Columns)
	for _, c := range to.Columns {
		a, ok := fromColumns[c.Name]
		c := c
		switch {
		case !ok:
			t.columns = append(t.columns, columnDiff{
				PartDiff: PartDiff{Part: PartColumn, Name: c.Name, Change: ChangeAdded, To: describeColumn(c)}, to: &c,
			})
		case columnKey(a, mask, false) != columnKey(c, mask, true):
			a := a
			t.columns = append(t.columns, columnDiff{
				PartDiff: PartDiff{Part: PartColumn, Name: c.Name, Change: ChangeChanged,
					From: describeColumn(a), To: describeColumn(c)},
				from: &a, to: &c,
			})
		}
	}
	for _, c := range from.Columns {
		if _, ok := toColumns[c.Name]; !ok {
			c := c
			t.columns = append(t.columns, columnDiff{
				PartDiff: PartDiff{Part: PartColumn, Name: c.Name, Change: ChangeRemoved, From: describeColumn(c)}, from: &c,
			})
		}
	}

	if !slices.Equal(from.PrimaryKey, to.PrimaryKey) {
		change := ChangeChanged
		switch {
		case len(from.PrimaryKey) == 0:
			change = ChangeAdded
		case len(to.PrimaryKey) == 0:
			change = ChangeRemoved
		}
		t.primary = &keyDiff{
			PartDiff: PartDiff{Part: PartPrimaryKey, Name: "primary key", Change: change,
				From: describeColumnList(from.PrimaryKey), To: describeColumnList(to.PrimaryKey)},
			from: from.PrimaryKey, to: to.PrimaryKey,
		}
	}

	added, removed, changed := matchParts(plainIndexes(from), plainIndexes(to),
		func(i Index) string { return i.Name },
		func(i Index, toSide bool) string { return indexKey(i, mask, toSide) })
	for _, p := range added {
		t.indexes = append(t.indexes, indexDiff{PartDiff: PartDiff{Part: PartIndex, Name: p.Name, Change: ChangeAdded, To: describeIndex(p)}, to: addrOf(p)})
	}
	for _, p := range removed {
		t.indexes = append(t.indexes, indexDiff{PartDiff: PartDiff{Part: PartIndex, Name: p.Name, Change: ChangeRemoved, From: describeIndex(p)}, from: addrOf(p)})
	}
	for _, p := range changed {
		t.indexes = append(t.indexes, indexDiff{
			PartDiff: PartDiff{Part: PartIndex, Name: p[1].Name, Change: ChangeChanged, From: describeIndex(p[0]), To: describeIndex(p[1])},
			from:     addrOf(p[0]), to: addrOf(p[1]),
		})
	}

	addedC, removedC, changedC := matchParts(ownConstraints(from), ownConstraints(to),
		func(c Constraint) string { return c.Name },
		func(c Constraint, toSide bool) string { return constraintKey(c, mask, toSide) })
	for _, p := range addedC {
		t.constraints = append(t.constraints, constraintDiff{PartDiff: PartDiff{Part: constraintPart(p), Name: p.Name, Change: ChangeAdded, To: describeConstraint(p)}, to: addrOf(p)})
	}
	for _, p := range removedC {
		t.constraints = append(t.constraints, constraintDiff{PartDiff: PartDiff{Part: constraintPart(p), Name: p.Name, Change: ChangeRemoved, From: describeConstraint(p)}, from: addrOf(p)})
	}
	for _, p := range changedC {
		t.constraints = append(t.constraints, constraintDiff{
			PartDiff: PartDiff{Part: constraintPart(p[1]), Name: p[1].Name, Change: ChangeChanged, From: describeConstraint(p[0]), To: describeConstraint(p[1])},
			from:     addrOf(p[0]), to: addrOf(p[1]),
		})
	}

	addedF, removedF, changedF := matchParts(from.ForeignKeys, to.ForeignKeys,
		func(f ForeignKey) string { return f.Name },
		func(f ForeignKey, toSide bool) string { return foreignKeyKey(f, mask, toSide, from.Schema, to.Schema) })
	for _, p := range addedF {
		t.foreign = append(t.foreign, foreignKeyDiff{PartDiff: PartDiff{Part: PartForeignKey, Name: p.Name, Change: ChangeAdded, To: describeForeignKey(p)}, to: addrOf(p)})
	}
	for _, p := range removedF {
		t.foreign = append(t.foreign, foreignKeyDiff{PartDiff: PartDiff{Part: PartForeignKey, Name: p.Name, Change: ChangeRemoved, From: describeForeignKey(p)}, from: addrOf(p)})
	}
	for _, p := range changedF {
		t.foreign = append(t.foreign, foreignKeyDiff{
			PartDiff: PartDiff{Part: PartForeignKey, Name: p[1].Name, Change: ChangeChanged, From: describeForeignKey(p[0]), To: describeForeignKey(p[1])},
			from:     addrOf(p[0]), to: addrOf(p[1]),
		})
	}

	if len(t.columns) == 0 && t.primary == nil && len(t.indexes) == 0 && len(t.constraints) == 0 && len(t.foreign) == 0 {
		return nil
	}
	return t
}

func addrOf[T any](v T) *T { return &v }

func columnsByName(columns []Column) map[string]Column {
	out := make(map[string]Column, len(columns))
	for _, c := range columns {
		out[c.Name] = c
	}
	return out
}

// plainIndexes are the table's indexes that are their own object. The primary
// key and the index behind a unique or exclusion constraint are the
// constraint's; MySQL reports a unique constraint as both, under one name.
func plainIndexes(t *TableDetail) []Index {
	uniques := map[string]bool{}
	for _, c := range t.Constraints {
		if c.Type == ConstraintUnique {
			uniques[c.Name] = true
		}
	}
	out := make([]Index, 0, len(t.Indexes))
	for _, ix := range t.Indexes {
		if ix.Primary || ix.Constraint != "" || (ix.Unique && uniques[ix.Name]) {
			continue
		}
		out = append(out, ix)
	}
	return out
}

// ownConstraints are the unique and check constraints the diff compares. An
// exclusion constraint has no form the DDL generator writes, so it is left to
// the Structure tab.
func ownConstraints(t *TableDetail) []Constraint {
	out := make([]Constraint, 0, len(t.Constraints))
	for _, c := range t.Constraints {
		if c.Type == ConstraintUnique || c.Type == ConstraintCheck {
			out = append(out, c)
		}
	}
	return out
}

func constraintPart(c Constraint) string {
	if c.Type == ConstraintUnique {
		return PartUnique
	}
	return PartCheck
}

// matchParts pairs two lists of named parts. A name on both sides is the same
// part, changed if its definition differs. What is left over is paired by
// definition alone, because an engine invents a name for a part the author did
// not name and invents it differently in two databases — an index or a key
// that differs only by a generated name is not a difference to repair.
func matchParts[T any](from, to []T, name func(T) string, key func(T, bool) string) (added, removed []T, changed [][2]T) {
	byName := map[string]int{}
	for i, p := range to {
		byName[name(p)] = i
	}
	paired := make([]bool, len(to))
	var loose []T
	for _, a := range from {
		i, ok := byName[name(a)]
		if !ok {
			loose = append(loose, a)
			continue
		}
		paired[i] = true
		if key(a, false) != key(to[i], true) {
			changed = append(changed, [2]T{a, to[i]})
		}
	}
	for i, b := range to {
		if paired[i] {
			continue
		}
		match := slices.IndexFunc(loose, func(a T) bool { return key(a, false) == key(b, true) })
		if match >= 0 {
			loose = slices.Delete(loose, match, match+1)
			continue
		}
		added = append(added, b)
	}
	return added, loose, changed
}

func columnKey(c Column, mask schemaMask, toSide bool) string {
	return strings.Join([]string{
		strings.ToLower(strings.Join(strings.Fields(mask.text(c.Type, toSide)), " ")),
		fmt.Sprint(c.Nullable),
		strings.TrimSpace(mask.text(c.Default, toSide)),
		strings.ToLower(c.Identity),
		strings.TrimSpace(mask.text(c.Generated, toSide)),
		c.GeneratedKind,
	}, "\x1f")
}

func indexKey(i Index, mask schemaMask, toSide bool) string {
	return strings.Join([]string{
		strings.Join(i.Columns, "\x1e"),
		fmt.Sprint(i.Unique),
		strings.ToLower(i.Method),
		strings.TrimSpace(mask.text(i.Predicate, toSide)),
		strings.Join(i.Include, "\x1e"),
		fmt.Sprint(i.Expression),
	}, "\x1f")
}

func constraintKey(c Constraint, mask schemaMask, toSide bool) string {
	if c.Type == ConstraintUnique {
		return c.Type + "\x1f" + strings.Join(c.Columns, "\x1e")
	}
	return c.Type + "\x1f" + strings.Join(strings.Fields(strings.ToLower(mask.text(c.Definition, toSide))), " ")
}

func foreignKeyKey(f ForeignKey, mask schemaMask, toSide bool, fromSchema, toSchema string) string {
	ref := f.RefSchema
	// A key into the schema it is in is the same key in another schema.
	if own := map[bool]string{false: fromSchema, true: toSchema}[toSide]; ref == own {
		ref = ""
	}
	return strings.Join([]string{
		strings.Join(f.Columns, "\x1e"), ref, f.RefTable, strings.Join(f.RefColumns, "\x1e"),
		referentialKey(f.OnUpdate), referentialKey(f.OnDelete),
	}, "\x1f")
}

func referentialKey(action string) string {
	action = strings.ToUpper(strings.Join(strings.Fields(action), " "))
	if action == "" {
		return "NO ACTION"
	}
	return action
}

// --- how a part reads --------------------------------------------------------

func describeColumn(c Column) string {
	parts := []string{c.Type}
	if c.Generated != "" {
		parts = append(parts, "GENERATED ALWAYS AS ("+c.Generated+") "+strings.ToUpper(c.GeneratedKind))
	}
	if c.Identity != "" {
		parts = append(parts, strings.ToUpper(c.Identity))
	}
	if !c.Nullable {
		parts = append(parts, "NOT NULL")
	}
	if c.Default != "" {
		parts = append(parts, "DEFAULT "+c.Default)
	}
	return strings.Join(parts, " ")
}

func describeColumnList(columns []string) string {
	if len(columns) == 0 {
		return ""
	}
	return "(" + strings.Join(columns, ", ") + ")"
}

func describeIndex(i Index) string {
	parts := []string{}
	if i.Unique {
		parts = append(parts, "UNIQUE")
	}
	if i.Method != "" {
		parts = append(parts, strings.ToLower(i.Method))
	}
	parts = append(parts, describeColumnList(i.Columns))
	if len(i.Include) > 0 {
		parts = append(parts, "INCLUDE "+describeColumnList(i.Include))
	}
	if i.Predicate != "" {
		parts = append(parts, "WHERE "+i.Predicate)
	}
	return strings.Join(parts, " ")
}

func describeConstraint(c Constraint) string {
	if c.Type == ConstraintUnique {
		return "UNIQUE " + describeColumnList(c.Columns)
	}
	return c.Definition
}

func describeForeignKey(f ForeignKey) string {
	ref := f.RefTable
	if f.RefSchema != "" {
		ref = f.RefSchema + "." + ref
	}
	out := describeColumnList(f.Columns) + " REFERENCES " + ref + " " + describeColumnList(f.RefColumns)
	if a := referentialKey(f.OnDelete); a != "NO ACTION" {
		out += " ON DELETE " + a
	}
	if a := referentialKey(f.OnUpdate); a != "NO ACTION" {
		out += " ON UPDATE " + a
	}
	return out
}

// describe lists the differences as the page draws them: tables, then views,
// then the rest, each group by name.
func (d *SchemaDiff) describe() []ObjectDiff {
	var out []ObjectDiff
	for _, t := range d.tables {
		o := ObjectDiff{Kind: DiffTable, Name: t.name, Change: t.change, Parts: []PartDiff{}}
		switch t.change {
		case ChangeAdded:
			o.Parts = append(o.Parts, tableParts(t.to, ChangeAdded)...)
		case ChangeRemoved:
			o.Parts = append(o.Parts, tableParts(t.from, ChangeRemoved)...)
		default:
			for _, c := range t.columns {
				o.Parts = append(o.Parts, c.PartDiff)
			}
			if t.primary != nil {
				o.Parts = append(o.Parts, t.primary.PartDiff)
			}
			for _, i := range t.indexes {
				o.Parts = append(o.Parts, i.PartDiff)
			}
			for _, c := range t.constraints {
				o.Parts = append(o.Parts, c.PartDiff)
			}
			for _, f := range t.foreign {
				o.Parts = append(o.Parts, f.PartDiff)
			}
		}
		out = append(out, o)
	}
	for _, v := range d.views {
		kind := DiffView
		if (v.to != nil && v.to.Materialized) || (v.from != nil && v.from.Materialized) {
			kind = DiffMaterializedView
		}
		o := ObjectDiff{Kind: kind, Name: v.name, Change: v.change}
		o.Parts = textParts(v.name, v.change, definitionOf(v.from, func(x *SnapshotView) string { return x.Definition }),
			definitionOf(v.to, func(x *SnapshotView) string { return x.Definition }))
		out = append(out, o)
	}
	for _, e := range d.enums {
		o := ObjectDiff{Kind: DiffEnum, Name: e.name, Change: e.change}
		o.Parts = textPart("labels", e.name, e.change, strings.Join(e.from, ", "), strings.Join(e.to, ", "))
		out = append(out, o)
	}
	for _, s := range d.sequences {
		o := ObjectDiff{Kind: DiffSequence, Name: s.name, Change: s.change}
		o.Parts = textParts(s.name, s.change, definitionOf(s.from, func(x *SnapshotSequence) string { return x.Definition }),
			definitionOf(s.to, func(x *SnapshotSequence) string { return x.Definition }))
		out = append(out, o)
	}
	for _, r := range d.routines {
		src := r.to
		if src == nil {
			src = r.from
		}
		o := ObjectDiff{Kind: src.Kind, Name: routineLabel(*src), Change: r.change}
		o.Parts = textParts(src.Name, r.change, definitionOf(r.from, func(x *SnapshotRoutine) string { return x.Definition }),
			definitionOf(r.to, func(x *SnapshotRoutine) string { return x.Definition }))
		out = append(out, o)
	}
	return out
}

func routineLabel(r SnapshotRoutine) string { return r.Name + "(" + r.Signature + ")" }

func definitionOf[T any](object *T, text func(*T) string) string {
	if object == nil {
		return ""
	}
	return text(object)
}

// textParts is the one part an object that is a piece of text has: its
// definition, as it is on each side.
func textParts(name, change, from, to string) []PartDiff {
	return textPart("definition", name, change, from, to)
}

func textPart(part, name, change, from, to string) []PartDiff {
	return []PartDiff{{Part: part, Name: name, Change: change, From: from, To: to}}
}

// tableParts lists a whole table's parts for a table that is only on one side.
func tableParts(t *TableDetail, change string) []PartDiff {
	side := func(text string) (from, to string) {
		if change == ChangeAdded {
			return "", text
		}
		return text, ""
	}
	var parts []PartDiff
	add := func(part, name, text string) {
		from, to := side(text)
		parts = append(parts, PartDiff{Part: part, Name: name, Change: change, From: from, To: to})
	}
	for _, c := range t.Columns {
		add(PartColumn, c.Name, describeColumn(c))
	}
	if len(t.PrimaryKey) > 0 {
		add(PartPrimaryKey, "primary key", describeColumnList(t.PrimaryKey))
	}
	for _, i := range plainIndexes(t) {
		add(PartIndex, i.Name, describeIndex(i))
	}
	for _, c := range ownConstraints(t) {
		add(constraintPart(c), c.Name, describeConstraint(c))
	}
	for _, f := range t.ForeignKeys {
		add(PartForeignKey, f.Name, describeForeignKey(f))
	}
	return parts
}
