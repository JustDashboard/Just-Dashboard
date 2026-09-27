package dbx

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// Index is one index on a table, with the columns it covers in order.
type Index struct {
	Name    string   `json:"name"`
	Columns []string `json:"columns"`
	Unique  bool     `json:"unique"`
	Primary bool     `json:"primary"`
}

// ForeignKey is one referential constraint. Composite keys keep their columns
// paired with the referenced columns in order, which is what a relationship
// diagram and a "jump to referenced row" action both need.
type ForeignKey struct {
	Name       string   `json:"name"`
	Columns    []string `json:"columns"`
	RefSchema  string   `json:"refSchema,omitempty"`
	RefTable   string   `json:"refTable"`
	RefColumns []string `json:"refColumns"`
	OnUpdate   string   `json:"onUpdate,omitempty"`
	OnDelete   string   `json:"onDelete,omitempty"`
}

// TableDetail is everything the Structure tab shows and everything row editing
// needs: the column list, which columns form the primary key (so an edit can be
// scoped to one row), the indexes, the outgoing foreign keys, and the DDL that
// would recreate the table.
type TableDetail struct {
	Schema      string       `json:"schema"`
	Name        string       `json:"name"`
	Columns     []Column     `json:"columns"`
	PrimaryKey  []string     `json:"primaryKey"`
	Indexes     []Index      `json:"indexes"`
	ForeignKeys []ForeignKey `json:"foreignKeys"`
	CreateSQL   string       `json:"createSql,omitempty"`
}

// Detail assembles a TableDetail with one call per fact rather than one giant
// join, because the failure modes differ and because each engine answers them
// from a different catalogue.
//
// Only the column read is fatal. A table whose indexes cannot be listed because
// the login lacks a grant on the catalogue view is still a table worth showing:
// failing the whole request there would turn a partial answer into no answer,
// and the least-privileged accounts are exactly the ones a dashboard should
// stay usable for.
func Detail(ctx context.Context, db *sql.DB, driver Driver, schema, table string) (*TableDetail, error) {
	d, err := DialectFor(driver)
	if err != nil {
		return nil, err
	}
	cols, err := d.Columns(ctx, db, schema, table)
	if err != nil {
		return nil, err
	}
	detail := &TableDetail{
		Schema: schema, Name: table, Columns: cols,
		PrimaryKey: []string{}, Indexes: []Index{}, ForeignKeys: []ForeignKey{},
	}
	if pk, err := d.PrimaryKey(ctx, db, schema, table); err == nil {
		detail.PrimaryKey = pk
	}
	if ix, err := d.Indexes(ctx, db, schema, table); err == nil {
		detail.Indexes = ix
	}
	if fks, err := d.ForeignKeys(ctx, db, schema, table); err == nil {
		detail.ForeignKeys = fks
	}
	detail.CreateSQL, _ = d.CreateSQL(ctx, db, schema, table, detail)
	return detail, nil
}

// PrimaryKeyColumns returns the ordered primary-key columns. Row editing refuses
// to run without one, so this is a security-relevant answer as much as a
// descriptive one: it is what bounds an UPDATE or DELETE to a single row.
func PrimaryKeyColumns(ctx context.Context, db *sql.DB, driver Driver, schema, table string) ([]string, error) {
	d, err := DialectFor(driver)
	if err != nil {
		return nil, err
	}
	return d.PrimaryKey(ctx, db, schema, table)
}

// Relations returns every foreign key in a schema, which is what the entity
// diagram draws. It reads one table at a time rather than one catalogue-wide
// query because each dialect already knows how to ask for a table's keys, and a
// second per-engine query would be a second thing to get wrong.
func Relations(ctx context.Context, db *sql.DB, driver Driver, schema string) (map[string][]ForeignKey, error) {
	d, err := DialectFor(driver)
	if err != nil {
		return nil, err
	}
	tables, err := d.Tables(ctx, db, schema)
	if err != nil {
		return nil, err
	}
	out := map[string][]ForeignKey{}
	for _, t := range tables {
		if strings.EqualFold(t.Type, "view") {
			continue
		}
		fks, err := d.ForeignKeys(ctx, db, t.Schema, t.Name)
		if err != nil || len(fks) == 0 {
			continue
		}
		out[t.Name] = fks
	}
	return out, nil
}

// synthCreateTable builds a readable CREATE TABLE from an introspected detail,
// for the engines that keep no canonical DDL text of their own. It is a faithful
// summary rather than a byte-exact reproduction — types and defaults come back
// already normalised by the catalogue — so it is labelled as generated where it
// is shown.
func synthCreateTable(d Dialect, schema, table string, detail *TableDetail) string {
	rel, err := qualify(d, schema, table)
	if err != nil {
		rel = table
	}
	var b strings.Builder
	fmt.Fprintf(&b, "CREATE TABLE %s (\n", rel)
	lines := []string{}
	for _, c := range detail.Columns {
		q, err := d.QuoteIdent(c.Name)
		if err != nil {
			q = c.Name
		}
		line := "  " + q + " " + c.Type
		if !c.Nullable {
			line += " NOT NULL"
		}
		if c.Default != "" {
			line += " DEFAULT " + c.Default
		}
		lines = append(lines, line)
	}
	if len(detail.PrimaryKey) > 0 {
		cols := make([]string, 0, len(detail.PrimaryKey))
		for _, c := range detail.PrimaryKey {
			q, _ := d.QuoteIdent(c)
			cols = append(cols, q)
		}
		lines = append(lines, "  PRIMARY KEY ("+strings.Join(cols, ", ")+")")
	}
	for _, fk := range detail.ForeignKeys {
		local := make([]string, 0, len(fk.Columns))
		for _, c := range fk.Columns {
			q, _ := d.QuoteIdent(c)
			local = append(local, q)
		}
		refRel, _ := qualify(d, fk.RefSchema, fk.RefTable)
		ref := make([]string, 0, len(fk.RefColumns))
		for _, c := range fk.RefColumns {
			q, _ := d.QuoteIdent(c)
			ref = append(ref, q)
		}
		line := fmt.Sprintf("  FOREIGN KEY (%s) REFERENCES %s (%s)",
			strings.Join(local, ", "), refRel, strings.Join(ref, ", "))
		if fk.OnDelete != "" && fk.OnDelete != "NO ACTION" {
			line += " ON DELETE " + fk.OnDelete
		}
		if fk.OnUpdate != "" && fk.OnUpdate != "NO ACTION" {
			line += " ON UPDATE " + fk.OnUpdate
		}
		lines = append(lines, line)
	}
	b.WriteString(strings.Join(lines, ",\n"))
	b.WriteString("\n);")
	return b.String()
}

// SchemaOutline is the whole schema flattened to names, which is what the SQL
// editor's completion needs and all it needs.
//
// It is one payload rather than a completion endpoint the editor calls per
// keystroke: a round trip per character is unusable over the VPN tunnel these
// dashboards are reached through, and a schema's shape does not change between
// keystrokes. The cost is one request per table on open, which is why the
// result is small — names only, no types, no constraints.
type SchemaOutline struct {
	Schema string              `json:"schema"`
	Tables map[string][]string `json:"tables"`
}

func Outline(ctx context.Context, db *sql.DB, driver Driver, schema string) (*SchemaOutline, error) {
	d, err := DialectFor(driver)
	if err != nil {
		return nil, err
	}
	tables, err := d.Tables(ctx, db, schema)
	if err != nil {
		return nil, err
	}
	out := &SchemaOutline{Schema: schema, Tables: map[string][]string{}}
	catalog := withSchemaCatalog(ctx, db, d, tables, false)
	for _, t := range tables {
		cols, err := catalog.Columns(ctx, db, t.Schema, t.Name)
		if err != nil {
			// A table whose columns cannot be read still belongs in the
			// completion list by name; dropping it would make the editor claim
			// it does not exist.
			out.Tables[t.Name] = []string{}
			continue
		}
		names := make([]string, 0, len(cols))
		for _, c := range cols {
			names = append(names, c.Name)
		}
		out.Tables[t.Name] = names
	}
	return out, nil
}

// SchemaGraph is a whole schema in the shape a diagram needs: every table with
// its columns, which of them are keys, and the references between them.
//
// It exists because drawing the schema from the routes that already existed
// meant one request for the table list, one for the relations, and then one
// per table for the columns — forty round trips to render one picture, each
// one a chance for the diagram to be half-drawn. Introspecting once on the
// server and sending the finished shape is both faster and atomic: what
// arrives is one schema at one moment rather than forty answers from forty.
type SchemaGraph struct {
	Schema string       `json:"schema"`
	Tables []GraphTable `json:"tables"`
	Edges  []GraphEdge  `json:"edges"`
	// Truncated says the schema was larger than the cap and only part of it is
	// here. A diagram of nine hundred tables is not a diagram, but silently
	// drawing a third of one is worse than saying so.
	Truncated bool `json:"truncated"`
}

type GraphTable struct {
	Schema  string        `json:"schema"`
	Name    string        `json:"name"`
	Type    string        `json:"type"`
	Rows    int64         `json:"rows"`
	Columns []GraphColumn `json:"columns"`
}

type GraphColumn struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Nullable   bool   `json:"nullable"`
	PrimaryKey bool   `json:"primaryKey"`
	// ForeignKey names the table this column points at, so a column can be
	// drawn as a reference without the caller re-deriving it from the edges.
	ForeignKey string `json:"foreignKey,omitempty"`
	Unique     bool   `json:"unique,omitempty"`
}

// GraphEdge is one foreign key, anchored to the columns at both ends so the
// line can be drawn between the rows it actually relates rather than between
// two boxes.
type GraphEdge struct {
	Name       string `json:"name"`
	FromTable  string `json:"fromTable"`
	FromColumn string `json:"fromColumn"`
	ToTable    string `json:"toTable"`
	ToColumn   string `json:"toColumn"`
	OnDelete   string `json:"onDelete,omitempty"`
	// Cardinality is "many-to-one" unless the referencing column is itself
	// unique, which makes it one-to-one — the distinction a reader of the
	// diagram is actually looking for.
	Cardinality string `json:"cardinality"`
}

// maxGraphTables bounds the work and the picture. Past this a diagram is a
// wall, and the introspection behind it is thousands of catalogue queries.
const maxGraphTables = 120

func BuildSchemaGraph(ctx context.Context, db *sql.DB, driver Driver, schema string) (*SchemaGraph, error) {
	d, err := DialectFor(driver)
	if err != nil {
		return nil, err
	}
	tables, err := d.Tables(ctx, db, schema)
	if err != nil {
		return nil, err
	}
	out := &SchemaGraph{Schema: schema, Tables: []GraphTable{}, Edges: []GraphEdge{}}
	if len(tables) > maxGraphTables {
		tables, out.Truncated = tables[:maxGraphTables], true
	}
	catalog := withSchemaCatalog(ctx, db, d, tables, true)

	// Which columns are unique decides whether a reference is one-to-one or
	// many-to-one, and it is read from the indexes that are being fetched
	// anyway rather than from a second pass.
	uniqueCols := map[string]map[string]bool{}

	for _, t := range tables {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		gt := GraphTable{Schema: t.Schema, Name: t.Name, Type: t.Type, Rows: t.Rows, Columns: []GraphColumn{}}
		cols, err := catalog.Columns(ctx, db, t.Schema, t.Name)
		if err != nil {
			// A table whose columns will not read is still part of the shape:
			// dropping it would silently delete a box other tables point at.
			out.Tables = append(out.Tables, gt)
			continue
		}
		pk, _ := catalog.PrimaryKey(ctx, db, t.Schema, t.Name)
		isPK := map[string]bool{}
		for _, c := range pk {
			isPK[c] = true
		}
		uniq := map[string]bool{}
		if indexes, err := catalog.Indexes(ctx, db, t.Schema, t.Name); err == nil {
			for _, ix := range indexes {
				if ix.Unique && len(ix.Columns) == 1 {
					uniq[ix.Columns[0]] = true
				}
			}
		}
		uniqueCols[t.Name] = uniq

		fkOf := map[string]string{}
		if fks, err := catalog.ForeignKeys(ctx, db, t.Schema, t.Name); err == nil {
			for _, fk := range fks {
				for i, col := range fk.Columns {
					refCol := ""
					if i < len(fk.RefColumns) {
						refCol = fk.RefColumns[i]
					}
					fkOf[col] = fk.RefTable
					out.Edges = append(out.Edges, GraphEdge{
						Name: fk.Name, FromTable: t.Name, FromColumn: col,
						ToTable: fk.RefTable, ToColumn: refCol, OnDelete: fk.OnDelete,
					})
				}
			}
		}
		for _, c := range cols {
			gt.Columns = append(gt.Columns, GraphColumn{
				Name: c.Name, Type: c.Type, Nullable: c.Nullable,
				PrimaryKey: isPK[c.Name], ForeignKey: fkOf[c.Name], Unique: uniq[c.Name],
			})
		}
		out.Tables = append(out.Tables, gt)
	}

	// Cardinality is decided after every table is known, because it depends on
	// the referencing column's uniqueness rather than the referenced one's.
	for i, e := range out.Edges {
		if uniqueCols[e.FromTable][e.FromColumn] {
			out.Edges[i].Cardinality = "one-to-one"
		} else {
			out.Edges[i].Cardinality = "many-to-one"
		}
	}
	return out, nil
}
