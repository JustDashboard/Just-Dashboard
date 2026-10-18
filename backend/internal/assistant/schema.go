package assistant

import (
	"context"
	"database/sql"
	"strings"
	"unicode"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
)

// What the assistant is told about a database is its structure, read through
// the same catalogue the schema pages use, and kept to the structure: a
// table's name and kind, its columns' names and types, which are keys and
// what they reference. Row estimates, defaults and comments are left out with
// the rows themselves — a default or a comment can be a value somebody typed.

// TableName is a table or a view the schema holds.
type TableName struct {
	Schema string
	Name   string
	Kind   string
}

type Column struct {
	Name       string
	Type       string
	Nullable   bool
	PrimaryKey bool
	Unique     bool
	// References is the column this one points at, "schema.table(column)".
	References string
}

type Table struct {
	TableName
	Columns []Column
}

// Snapshot is one schema's structure as a conversation began with it.
type Snapshot struct {
	Schema string
	// Tables are the ones described, columns and all.
	Tables []Table
	// Names is every table and view in the schema, described or not.
	Names []TableName
}

const (
	// graphTables are described in one read of the catalogue. Past it the
	// rest are named, and the ones the question names are described as well.
	graphTables = 150
	// mentionedTables bounds how many tables a question may have described
	// one at a time beyond the first read.
	mentionedTables = 8
)

// ReadSchema reads the structure of one schema, with the tables the question
// names among those described however large the schema is.
func ReadSchema(ctx context.Context, db *sql.DB, driver dbx.Driver, schema, question string) (*Snapshot, error) {
	listed, err := dbx.ListTables(ctx, db, driver, schema)
	if err != nil {
		return nil, err
	}
	graph, err := dbx.BuildSchemaGraphWithLimit(ctx, db, driver, schema, graphTables)
	if err != nil {
		return nil, err
	}
	out := &Snapshot{Schema: schema}
	for _, t := range listed {
		if strings.EqualFold(t.Type, dbx.TableTypePartition) {
			continue
		}
		out.Names = append(out.Names, TableName{Schema: t.Schema, Name: t.Name, Kind: t.Type})
	}
	if out.Schema == "" && len(listed) > 0 {
		out.Schema = listed[0].Schema
	}

	references := map[string]string{}
	for _, e := range graph.Edges {
		references[e.From+"\x00"+e.FromColumn] = reference(e.ToSchema, e.ToTable, e.ToColumn)
	}
	described := map[string]bool{}
	for _, gt := range graph.Tables {
		t := Table{TableName: TableName{Schema: gt.Schema, Name: gt.Name, Kind: gt.Type}}
		for _, c := range gt.Columns {
			t.Columns = append(t.Columns, Column{
				Name: c.Name, Type: c.Type, Nullable: c.Nullable, PrimaryKey: c.PrimaryKey, Unique: c.Unique,
				References: references[gt.ID+"\x00"+c.Name],
			})
		}
		out.Tables = append(out.Tables, t)
		described[dbx.TableKey(gt.Schema, gt.Name)] = true
	}
	if graph.Truncated {
		extra := 0
		for _, name := range Mentioned(question, out.Names) {
			if extra == mentionedTables {
				break
			}
			if described[dbx.TableKey(name.Schema, name.Name)] {
				continue
			}
			t, ok := describeOne(ctx, db, driver, name)
			if !ok {
				continue
			}
			out.Tables = append(out.Tables, t)
			extra++
		}
	}
	return out, nil
}

// Describe reads the tables a tool call named, each resolved against the
// schema's own list first so a name in the wrong case still finds its table.
// The names it could not find come back as missing.
func Describe(ctx context.Context, db *sql.DB, driver dbx.Driver, schema string, names []string, known []TableName) (tables []Table, missing []string) {
	for _, raw := range names {
		name, ok := Resolve(raw, schema, known)
		if !ok {
			missing = append(missing, raw)
			continue
		}
		t, ok := describeOne(ctx, db, driver, name)
		if !ok {
			missing = append(missing, raw)
			continue
		}
		tables = append(tables, t)
	}
	return tables, missing
}

// Resolve finds a name the model wrote among the schema's tables: exactly,
// then ignoring case where only one table answers to it. A name with a schema
// in front of it is read as schema.table.
func Resolve(raw, schema string, known []TableName) (TableName, bool) {
	raw = strings.TrimSpace(raw)
	inSchema, table := schema, raw
	if s, t, qualified := strings.Cut(raw, "."); qualified && s != "" && t != "" {
		inSchema, table = s, t
	}
	for _, k := range known {
		if k.Name == table && (k.Schema == inSchema || inSchema == "") {
			return k, true
		}
	}
	var found []TableName
	for _, k := range known {
		if strings.EqualFold(k.Name, table) && (strings.EqualFold(k.Schema, inSchema) || inSchema == "") {
			found = append(found, k)
		}
	}
	if len(found) == 1 {
		return found[0], true
	}
	return TableName{}, false
}

func describeOne(ctx context.Context, db *sql.DB, driver dbx.Driver, name TableName) (Table, bool) {
	detail, err := dbx.DescribeTable(ctx, db, driver, name.Schema, name.Name)
	if err != nil || len(detail.Columns) == 0 {
		return Table{}, false
	}
	t := Table{TableName: name}
	if t.Kind == "" {
		t.Kind = detail.Type
	}
	primary := map[string]bool{}
	for _, c := range detail.PrimaryKey {
		primary[c] = true
	}
	unique := map[string]bool{}
	for _, ix := range detail.Indexes {
		if ix.Unique && len(ix.Columns) == 1 && ix.Predicate == "" && !ix.Expression {
			unique[ix.Columns[0]] = true
		}
	}
	references := map[string]string{}
	for _, fk := range detail.ForeignKeys {
		refSchema := fk.RefSchema
		if refSchema == "" {
			refSchema = name.Schema
		}
		for i, col := range fk.Columns {
			to := ""
			if i < len(fk.RefColumns) {
				to = fk.RefColumns[i]
			}
			references[col] = reference(refSchema, fk.RefTable, to)
		}
	}
	for _, c := range detail.Columns {
		t.Columns = append(t.Columns, Column{
			Name: c.Name, Type: c.Type, Nullable: c.Nullable,
			PrimaryKey: primary[c.Name], Unique: unique[c.Name], References: references[c.Name],
		})
	}
	return t, true
}

func reference(schema, table, column string) string {
	name := table
	if schema != "" {
		name = schema + "." + table
	}
	if column == "" {
		return name
	}
	return name + "(" + column + ")"
}

// Mentioned is the tables whose names appear in a piece of text as words of
// their own, in the schema's order: "orders" in "count the orders" does, and
// "order" does not.
func Mentioned(text string, names []TableName) []TableName {
	words := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '$'
	}) {
		words[w] = true
	}
	var out []TableName
	for _, n := range names {
		if words[strings.ToLower(n.Name)] {
			out = append(out, n)
		}
	}
	return out
}
