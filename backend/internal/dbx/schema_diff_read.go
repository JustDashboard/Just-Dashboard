package dbx

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// MaxDiffTables bounds the tables of one side of a comparison. A table costs
// several catalogue reads, so a schema past this is refused by name instead of
// keeping a request open for minutes.
const MaxDiffTables = 400

// MaxDiffObjects bounds everything else a side holds, tables included, since
// every view, routine and sequence is a definition read and sent.
const MaxDiffObjects = 1500

// ErrSchemaTooLarge is returned for a schema with more tables than a
// comparison reads.
var ErrSchemaTooLarge = errors.New("this schema is too large to compare")

// ReadSchemaSnapshot reads one schema for comparison: its tables in full, and
// its views, sequences, enum types and routines. A kind of object the login
// cannot read, or that the catalogue cut short, is left out of the snapshot
// and named in Unread, so a comparison can say it did not look instead of
// reporting everything of that kind as missing.
func ReadSchemaSnapshot(ctx context.Context, db *sql.DB, driver Driver, flavor, schema string) (*SchemaSnapshot, error) {
	catalog, err := ReadCatalog(ctx, db, driver, CatalogOptions{Schema: schema})
	if err != nil {
		return nil, err
	}
	snap := &SchemaSnapshot{
		Driver: driver, Flavor: flavor, Schema: catalog.Schema,
		Tables: map[string]*TableDetail{}, Views: map[string]SnapshotView{},
		Sequences: map[string]SnapshotSequence{}, Enums: map[string][]string{},
		Routines: map[string]SnapshotRoutine{}, Unread: map[string]string{},
	}
	// Which groups can be compared: read without error and not cut at the limit.
	readable := func(group string) bool {
		if reason, failed := catalog.Errors[group]; failed {
			snap.Unread[group] = reason
			return false
		}
		for _, cut := range catalog.Truncated {
			if cut == group {
				snap.Unread[group] = fmt.Sprintf("more than %d, which is as many as a comparison reads", catalog.Limit)
				return false
			}
		}
		return true
	}

	tables, ok := catalog.Objects[GroupTables], readable(GroupTables)
	if !ok {
		return nil, fmt.Errorf("the tables of %s could not be read: %s", schemaLabel(catalog.Schema), snap.Unread[GroupTables])
	}
	var own []CatalogObject
	for _, t := range tables {
		// A partition is read through its parent.
		if t.Table != "" {
			continue
		}
		own = append(own, t)
	}
	if len(own) > MaxDiffTables {
		return nil, fmt.Errorf("%w: %s has %d tables and a comparison reads up to %d",
			ErrSchemaTooLarge, schemaLabel(catalog.Schema), len(own), MaxDiffTables)
	}
	total := len(own)
	for _, group := range []string{GroupViews, GroupMaterializedViews, GroupSequences, GroupFunctions, GroupProcedures} {
		total += len(catalog.Objects[group])
	}
	for _, t := range catalog.Objects[GroupTypes] {
		if t.Kind == KindEnum {
			total++
		}
	}
	if total > MaxDiffObjects {
		return nil, fmt.Errorf("%w: %s holds %d objects and a comparison reads up to %d",
			ErrSchemaTooLarge, schemaLabel(catalog.Schema), total, MaxDiffObjects)
	}
	for _, t := range own {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		detail, err := Detail(ctx, db, driver, catalog.Schema, t.Name)
		if err != nil {
			return nil, fmt.Errorf("could not read the table %s: %w", t.Name, err)
		}
		snap.Tables[t.Name] = detail
	}

	definition := func(ref ObjectRef) (string, error) {
		def, err := ReadObjectDefinition(ctx, db, driver, ref)
		if err != nil {
			return "", err
		}
		return usableDefinition(driver, ref.Name, def)
	}
	// A group the catalogue reads but whose definitions cannot be had is
	// unread too: a name with no text is nothing to compare.
	skip := func(group string, err error) {
		snap.Unread[group] = err.Error()
	}

	for _, group := range []struct{ name, kind string }{{GroupViews, KindView}, {GroupMaterializedViews, KindMaterializedView}} {
		if !catalogHas(catalog, group.name) || !readable(group.name) {
			continue
		}
		for _, v := range catalog.Objects[group.name] {
			text, err := definition(ObjectRef{Kind: group.kind, Schema: catalog.Schema, Name: v.Name})
			if err != nil {
				skip(group.name, err)
				snap.dropViews(group.kind == KindMaterializedView)
				break
			}
			snap.Views[v.Name] = SnapshotView{Name: v.Name, Materialized: group.kind == KindMaterializedView, Definition: text}
		}
	}
	if catalogHas(catalog, GroupSequences) && readable(GroupSequences) {
		for _, s := range catalog.Objects[GroupSequences] {
			text, err := definition(ObjectRef{Kind: KindSequence, Schema: catalog.Schema, Name: s.Name})
			if err != nil {
				skip(GroupSequences, err)
				snap.Sequences = map[string]SnapshotSequence{}
				break
			}
			snap.Sequences[s.Name] = SnapshotSequence{Name: s.Name, Owner: s.Table, Definition: text}
		}
		snap.dropIdentitySequences()
	}
	if catalogHas(catalog, GroupTypes) && readable(GroupTypes) {
		for _, t := range catalog.Objects[GroupTypes] {
			if t.Kind == KindEnum {
				snap.Enums[t.Name] = t.Values
			}
		}
	}
	for _, group := range []struct{ name, kind string }{{GroupFunctions, KindFunction}, {GroupProcedures, KindProcedure}} {
		if !catalogHas(catalog, group.name) || !readable(group.name) {
			continue
		}
		for _, r := range catalog.Objects[group.name] {
			// A routine an extension installed is the extension's to compare.
			if r.Extension != "" {
				continue
			}
			text, err := definition(ObjectRef{Kind: group.kind, Schema: catalog.Schema, Name: r.Name, Signature: r.Signature})
			if err != nil {
				skip(group.name, err)
				snap.dropRoutines(group.kind)
				break
			}
			snap.Routines[routineKey(group.kind, r.Name, r.Signature)] = SnapshotRoutine{
				Kind: group.kind, Name: r.Name, Signature: r.Signature, Definition: text,
			}
		}
	}
	return snap, nil
}

// usableDefinition is an object's text if the server gave it. A server that
// will not — no privilege to see it, an encrypted module, a NULL where the
// text should be — answers with nothing or with a note; read as an empty text,
// every such object would differ from its twin and be dropped and made again
// with nothing.
func usableDefinition(driver Driver, name string, def *ObjectDefinition) (string, error) {
	text := strings.TrimSpace(stripDefiner(driver, def.Definition))
	if text == "" || def.Note != "" {
		reason := strings.TrimRight(strings.TrimSpace(def.Note), ".")
		if reason == "" {
			reason = "the server gave no definition"
		}
		return "", fmt.Errorf("%s: %s", oneLine(name), oneLine(reason))
	}
	return text, nil
}

// catalogHas reports whether the engine's catalogue has the group at all.
func catalogHas(c *Catalog, group string) bool {
	_, ok := c.Objects[group]
	return ok
}

func (s *SchemaSnapshot) dropViews(materialized bool) {
	for name, v := range s.Views {
		if v.Materialized == materialized {
			delete(s.Views, name)
		}
	}
}

// dropIdentitySequences leaves out the sequence behind an identity column: the
// column makes it, so creating the column creates it.
func (s *SchemaSnapshot) dropIdentitySequences() {
	for name, seq := range s.Sequences {
		table, column, ok := strings.Cut(seq.Owner, ".")
		if !ok || s.Tables[table] == nil {
			continue
		}
		for _, c := range s.Tables[table].Columns {
			if c.Name == column && c.Identity != "" {
				delete(s.Sequences, name)
			}
		}
	}
}

func (s *SchemaSnapshot) dropRoutines(kind string) {
	for key, r := range s.Routines {
		if r.Kind == kind {
			delete(s.Routines, key)
		}
	}
}

func routineKey(kind, name, signature string) string {
	return kind + "\x1f" + name + "\x1f" + signature
}

func schemaLabel(schema string) string {
	if schema == "" {
		return "the schema"
	}
	return strings.TrimSpace(schema)
}

func viewGroup(materialized bool) string {
	if materialized {
		return GroupMaterializedViews
	}
	return GroupViews
}

func routineGroup(kind string) string {
	if kind == KindProcedure {
		return GroupProcedures
	}
	return GroupFunctions
}

// groupWords names a catalogue group as a sentence says it.
var groupWords = map[string]string{
	GroupViews: "views", GroupMaterializedViews: "materialized views", GroupSequences: "sequences",
	GroupTypes: "enum types", GroupFunctions: "functions", GroupProcedures: "procedures",
}

// UnreadNotes says, for each kind of object the snapshot could not read whole,
// that it was left out of the comparison and why. name is the connection.
func (s *SchemaSnapshot) UnreadNotes(name string) []string {
	groups := make([]string, 0, len(s.Unread))
	for group := range s.Unread {
		groups = append(groups, group)
	}
	sort.Strings(groups)
	notes := make([]string, 0, len(groups))
	for _, group := range groups {
		word := groupWords[group]
		if word == "" {
			word = group
		}
		notes = append(notes, fmt.Sprintf("The %s of %s were not compared: %s.", word, name, strings.TrimRight(s.Unread[group], ".")))
	}
	return notes
}
