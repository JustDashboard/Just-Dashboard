package mcp

import (
	"context"
	"database/sql"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
)

// What describe_schema tells a client is a database's structure, read through
// the catalogue the schema pages use and kept to the structure: a table's
// name and kind, its columns' names and types, which are keys and what they
// reference. Defaults, comments and row estimates are left out with the rows,
// because a default or a comment can be a value somebody typed.

// TableName is one table or view a schema holds.
type TableName struct {
	Schema string `json:"schema"`
	Name   string `json:"name"`
	Kind   string `json:"kind"`
}

type Column struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Nullable   bool   `json:"nullable"`
	PrimaryKey bool   `json:"primaryKey,omitempty"`
	Unique     bool   `json:"unique,omitempty"`
	// References is the column this one points at, "schema.table(column)".
	References string `json:"references,omitempty"`
}

type Table struct {
	TableName
	Columns []Column `json:"columns"`
}

// Schema is describe_schema's answer.
type Schema struct {
	Tables []Table `json:"tables"`
	// Undescribed are the tables a whole-schema read named and did not
	// describe, past DescribeTables: name them to have them described.
	Undescribed []string `json:"undescribed,omitempty"`
	// Missing are the names asked for that the schema does not hold.
	Missing []string `json:"missing,omitempty"`
}

// ListedTable is one entry of list_tables: a table and the engine's estimate
// of its rows, which says whether a query over it wants a LIMIT.
type ListedTable struct {
	TableName
	EstimatedRows int64 `json:"estimatedRows"`
}

// ListTables is every table and view of a schema, partitions left out: a
// partition is its parent's storage, and listing each is listing one table
// many times. The second answer says the list stopped at ListMax.
func ListTables(ctx context.Context, db *sql.DB, driver dbx.Driver, schema string) ([]ListedTable, bool, error) {
	listed, err := dbx.ListTables(ctx, db, driver, schema)
	if err != nil {
		return nil, false, err
	}
	out := make([]ListedTable, 0, len(listed))
	for _, t := range listed {
		if strings.EqualFold(t.Type, dbx.TableTypePartition) {
			continue
		}
		if len(out) == ListMax {
			return out, true, nil
		}
		out = append(out, ListedTable{TableName{Schema: t.Schema, Name: t.Name, Kind: t.Type}, t.Rows})
	}
	return out, false, nil
}

// Names are the tables of a list, for Resolve.
func Names(listed []ListedTable) []TableName {
	out := make([]TableName, len(listed))
	for i, t := range listed {
		out[i] = t.TableName
	}
	return out
}

// DescribeSchema reads the named tables, or with none named the first
// DescribeTables of the schema in one read of its catalogue.
func DescribeSchema(ctx context.Context, db *sql.DB, driver dbx.Driver, schema string, names []string) (*Schema, error) {
	out := &Schema{Tables: []Table{}}
	if len(names) > 0 {
		listed, _, err := ListTables(ctx, db, driver, schema)
		if err != nil {
			return nil, err
		}
		known := Names(listed)
		for _, raw := range names {
			name, ok := Resolve(raw, schema, known)
			if !ok {
				out.Missing = append(out.Missing, raw)
				continue
			}
			t, err := describeOne(ctx, db, driver, name)
			if err != nil {
				return nil, err
			}
			out.Tables = append(out.Tables, t)
		}
		return out, nil
	}
	graph, err := dbx.BuildSchemaGraphWithLimit(ctx, db, driver, schema, DescribeTables)
	if err != nil {
		return nil, err
	}
	references := map[string]string{}
	for _, e := range graph.Edges {
		references[e.From+"\x00"+e.FromColumn] = reference(e.ToSchema, e.ToTable, e.ToColumn)
	}
	described := map[string]bool{}
	for _, gt := range graph.Tables {
		t := Table{TableName: TableName{Schema: gt.Schema, Name: gt.Name, Kind: gt.Type}, Columns: []Column{}}
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
		listed, _, err := ListTables(ctx, db, driver, schema)
		if err != nil {
			return nil, err
		}
		for _, k := range listed {
			if !described[dbx.TableKey(k.Schema, k.Name)] {
				out.Undescribed = append(out.Undescribed, Qualified(k.TableName))
			}
		}
	}
	return out, nil
}

// Resolve finds a name a model wrote among a schema's tables: exactly, then
// ignoring case where only one table answers to it. A dotted name is read as
// schema.table first, and as one name holding a dot after.
func Resolve(raw, schema string, known []TableName) (TableName, bool) {
	raw = strings.TrimSpace(raw)
	if s, t, ok := strings.Cut(raw, "."); ok && s != "" && t != "" {
		if found, ok := find(s, t, known); ok {
			return found, true
		}
	}
	return find(schema, raw, known)
}

// find is the one table of that name, or none: a name two schemas both hold
// is a question for the model to answer with schema.table, not one to guess.
func find(schema, table string, known []TableName) (TableName, bool) {
	for _, same := range []func(a, b string) bool{
		func(a, b string) bool { return a == b },
		strings.EqualFold,
	} {
		var found []TableName
		for _, k := range known {
			if same(k.Name, table) && (schema == "" || same(k.Schema, schema)) {
				found = append(found, k)
			}
		}
		if len(found) == 1 {
			return found[0], true
		}
		if len(found) > 1 {
			return TableName{}, false
		}
	}
	return TableName{}, false
}

func describeOne(ctx context.Context, db *sql.DB, driver dbx.Driver, name TableName) (Table, error) {
	detail, err := dbx.DescribeTable(ctx, db, driver, name.Schema, name.Name)
	if err != nil {
		return Table{}, err
	}
	t := Table{TableName: name, Columns: []Column{}}
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
	return t, nil
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

// Qualified is a table as schema.table, or its bare name where the engine has
// no schemas.
func Qualified(t TableName) string {
	if t.Schema == "" {
		return t.Name
	}
	return t.Schema + "." + t.Name
}
