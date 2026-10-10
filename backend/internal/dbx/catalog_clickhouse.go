package dbx

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// ClickHouse describes itself in the `system` database. What it calls a table
// covers four different things — a table that stores rows, a view, a
// materialized view that writes into another table, and a dictionary — and
// the engine column is what tells them apart. There are no foreign keys, no
// triggers, no sequences and no routines beyond SQL user-defined functions,
// which belong to the server rather than to any database.

const clickhouseSystemSchemas = "'system','INFORMATION_SCHEMA','information_schema'"

// clickhouseSchemaFilter consumes two parameters: the database, twice.
func clickhouseSchemaFilter() string {
	return `((? = '' AND database NOT IN (` + clickhouseSystemSchemas + `)) OR database = ?)`
}

// clickhouseSchemaIs consumes two parameters and matches one database, the
// connection's own when none is named.
func clickhouseSchemaIs() string {
	return `database = if(? = '', currentDatabase(), ?)`
}

func (clickhouseDialect) catalogSchemas(ctx context.Context, db *sql.DB) ([]CatalogSchema, string, error) {
	// Joined to an aggregate rather than counted in a correlated subquery: the
	// analyzer that is the default since 24.3 refuses to resolve an outer
	// column inside one.
	rows, err := db.QueryContext(ctx, `
	  SELECT d.name, d.engine, toInt64(t.n), d.name = currentDatabase()
	  FROM system.databases d
	  LEFT JOIN (SELECT database, count() AS n FROM system.tables GROUP BY database) t
	    ON t.database = d.name
	  ORDER BY d.name`)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	out, current := []CatalogSchema{}, ""
	for rows.Next() {
		var (
			s         CatalogSchema
			tables    int64
			isDefault uint8
		)
		if err := rows.Scan(&s.Name, &s.Detail, &tables, &isDefault); err != nil {
			return nil, "", err
		}
		s.Tables = int(tables)
		switch s.Name {
		case "system", "INFORMATION_SCHEMA", "information_schema":
			s.System = true
		}
		if isDefault == 1 {
			s.Default, current = true, s.Name
		}
		out = append(out, s)
	}
	return out, current, rows.Err()
}

func (d clickhouseDialect) catalogGroups(context.Context, *sql.DB) []catalogGroup {
	relations := func(kind, match string) func(context.Context, *sql.DB, string, int) ([]CatalogObject, error) {
		return func(ctx context.Context, db *sql.DB, schema string, limit int) ([]CatalogObject, error) {
			return d.relations(ctx, db, schema, limit, kind, match)
		}
	}
	return []catalogGroup{
		{GroupTables, relations(KindTable, "engine NOT IN ('View','LiveView','WindowView','MaterializedView','Dictionary')")},
		{GroupViews, relations(KindView, "engine IN ('View','LiveView','WindowView')")},
		{GroupMaterializedViews, relations(KindMaterializedView, "engine = 'MaterializedView'")},
		{GroupDictionaries, relations(KindDictionary, "engine = 'Dictionary'")},
		{GroupFunctions, d.functions},
	}
}

// relations lists one family of system.tables rows. match is a constant
// predicate written by the caller above, never request text.
func (clickhouseDialect) relations(ctx context.Context, db *sql.DB, schema string, limit int, kind, match string) ([]CatalogObject, error) {
	return scanObjects(ctx, db, `
	  SELECT database, name, engine,
	         ifNull(toInt64(total_rows), -1),
	         toInt64(ifNull(total_bytes, 0)),
	         comment
	  FROM system.tables
	  WHERE `+match+` AND is_temporary = 0 AND `+clickhouseSchemaFilter()+`
	  ORDER BY database, name
	  LIMIT ?`, []any{schema, schema, limit}, func(rows *sql.Rows) (CatalogObject, error) {
		o := CatalogObject{Kind: kind}
		var estimate int64
		if err := rows.Scan(&o.Schema, &o.Name, &o.Detail, &estimate, &o.Size, &o.Comment); err != nil {
			return o, err
		}
		if kind == KindTable {
			o.Rows = rowEstimate(estimate)
		}
		return o, nil
	})
}

// functions lists the SQL user-defined functions. They are server-wide, so
// they carry no schema and are listed whichever database is being read.
func (clickhouseDialect) functions(ctx context.Context, db *sql.DB, _ string, limit int) ([]CatalogObject, error) {
	return scanObjects(ctx, db, `
	  SELECT name FROM system.functions WHERE origin = 'SQLUserDefined' ORDER BY name LIMIT ?`,
		[]any{limit}, func(rows *sql.Rows) (CatalogObject, error) {
			o := CatalogObject{Kind: KindFunction, Language: "SQL"}
			return o, rows.Scan(&o.Name)
		})
}

// relationTypes names what the table list reports as a view and is not one: a
// materialized view writes rows into a table, and a dictionary is a lookup the
// server keeps in memory.
func (clickhouseDialect) relationTypes(ctx context.Context, db *sql.DB, schema string) (map[catalogTable]string, error) {
	rows, err := db.QueryContext(ctx, `
	  SELECT database, name, engine FROM system.tables
	  WHERE engine IN ('MaterializedView','Dictionary') AND (? = '' OR database = ?)`, schema, schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[catalogTable]string{}
	for rows.Next() {
		var schema, name, engine string
		if err := rows.Scan(&schema, &name, &engine); err != nil {
			return nil, err
		}
		out[catalogTable{schema, name}] = clickhouseTableType(engine)
	}
	return out, rows.Err()
}

// --- one table --------------------------------------------------------------

func (clickhouseDialect) tableColumns(ctx context.Context, db *sql.DB, schema, table string) ([]Column, error) {
	rows, err := db.QueryContext(ctx, `
	  SELECT name, type, default_kind, default_expression, position, is_in_primary_key, comment
	  FROM system.columns
	  WHERE `+clickhouseSchemaIs()+` AND table = ?
	  ORDER BY position`, schema, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Column{}
	for rows.Next() {
		var (
			c          Column
			kind, expr string
			position   uint64
			inPK       uint8
		)
		if err := rows.Scan(&c.Name, &c.Type, &kind, &expr, &position, &inPK, &c.Comment); err != nil {
			return nil, err
		}
		c.Position = int(position)
		// Optionality is a wrapper on the type here, and LowCardinality wraps
		// that in turn.
		c.Nullable = strings.HasPrefix(c.Type, "Nullable(") || strings.HasPrefix(c.Type, "LowCardinality(Nullable(")
		if inPK == 1 {
			c.Key = "PRI"
		}
		switch kind {
		case "MATERIALIZED":
			c.Generated, c.GeneratedKind = expr, "stored"
		case "ALIAS":
			c.Generated, c.GeneratedKind = expr, "virtual"
		default:
			c.Default = expr
		}
		switch {
		case strings.HasPrefix(c.Type, "Enum"):
			c.TypeKind, c.EnumValues = "enum", clickhouseEnumValues(c.Type)
		case strings.HasPrefix(c.Type, "Array("):
			c.TypeKind = "array"
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// clickhouseEnumValues reads the labels out of Enum8('a' = 1, 'b' = 2).
func clickhouseEnumValues(columnType string) []string {
	out := []string{}
	for i := 0; i < len(columnType); i++ {
		if columnType[i] != '\'' {
			continue
		}
		var b strings.Builder
		for i++; i < len(columnType); i++ {
			if columnType[i] == '\\' && i+1 < len(columnType) {
				i++
				b.WriteByte(columnType[i])
				continue
			}
			if columnType[i] == '\'' {
				break
			}
			b.WriteByte(columnType[i])
		}
		out = append(out, b.String())
	}
	return out
}

func (d clickhouseDialect) tableIndexes(ctx context.Context, db *sql.DB, schema, table string) ([]Index, error) {
	out := []Index{}
	var sortingKey string
	if err := db.QueryRowContext(ctx, `
	  SELECT sorting_key FROM system.tables WHERE `+clickhouseSchemaIs()+` AND name = ?`,
		schema, schema, table).Scan(&sortingKey); err != nil {
		return nil, err
	}
	if sortingKey != "" {
		cols := []string{}
		for _, c := range strings.Split(sortingKey, ",") {
			if c = strings.TrimSpace(c); c != "" {
				cols = append(cols, c)
			}
		}
		// The sorting key orders the parts on disk. It is listed as the
		// primary entry because it is what the table is addressed by, and it
		// promises nothing about uniqueness.
		out = append(out, Index{Name: "sorting key", Columns: cols, Primary: true, Constraint: "sorting key"})
	}
	rows, err := db.QueryContext(ctx, `
	  SELECT name, expr, type, toInt64(data_compressed_bytes)
	  FROM system.data_skipping_indices
	  WHERE `+clickhouseSchemaIs()+` AND table = ?
	  ORDER BY name`, schema, schema, table)
	if err != nil {
		// The size column is newer than the table it is in.
		rows, err = db.QueryContext(ctx, `
		  SELECT name, expr, type, toInt64(0)
		  FROM system.data_skipping_indices
		  WHERE `+clickhouseSchemaIs()+` AND table = ?
		  ORDER BY name`, schema, schema, table)
	}
	if err != nil {
		// A server too old for this table still has a usable sorting key above.
		return out, nil
	}
	defer rows.Close()
	for rows.Next() {
		var ix Index
		var expr string
		if err := rows.Scan(&ix.Name, &expr, &ix.Method, &ix.Size); err != nil {
			return out, nil
		}
		ix.Columns, ix.Expression = []string{expr}, true
		out = append(out, ix)
	}
	return out, nil
}

// tableConstraints reads a table's constraints out of its CREATE statement:
// ClickHouse's system tables list everything about a table except these, and
// it has no unique constraints and no foreign keys to list.
func (clickhouseDialect) tableConstraints(ctx context.Context, db *sql.DB, schema, table string) ([]Constraint, error) {
	var text string
	if err := db.QueryRowContext(ctx, `
	  SELECT create_table_query FROM system.tables WHERE `+clickhouseSchemaIs()+` AND name = ?`,
		schema, schema, table).Scan(&text); err != nil {
		return nil, err
	}
	return clickhouseConstraints(text), nil
}

// clickhouseConstraints finds the `CONSTRAINT name CHECK expr` items in a
// table's CREATE statement. An ASSUME is listed beside the checks — it is
// dropped the same way — with a definition that says which it is: the server
// trusts an assumption and never tests it.
func clickhouseConstraints(createQuery string) []Constraint {
	out := []Constraint{}
	for _, item := range createTableItems(DriverClickHouse, createQuery) {
		tokens := createItemTokens(DriverClickHouse, item)
		if len(tokens) < 4 || !tokens[0].keyword("constraint") || !tokens[1].name() {
			continue
		}
		if kind := tokens[2]; kind.keyword("check") || kind.keyword("assume") {
			out = append(out, Constraint{
				Name: tokens[1].text, Type: ConstraintCheck, Columns: []string{},
				Definition: strings.ToUpper(kind.text) + " " + strings.TrimSpace(item[kind.end:]),
			})
		}
	}
	return out
}

func (clickhouseDialect) tableReferencedBy(context.Context, *sql.DB, string, string) ([]IncomingForeignKey, error) {
	return []IncomingForeignKey{}, nil
}

func clickhouseTableType(engine string) string {
	switch engine {
	case "View", "LiveView", "WindowView":
		return TableTypeView
	case "MaterializedView":
		return TableTypeMaterializedView
	case "Dictionary":
		return "dictionary"
	default:
		return TableTypeTable
	}
}

func (clickhouseDialect) tableFacts(ctx context.Context, db *sql.DB, schema, table string, detail *TableDetail) error {
	var (
		engine, comment, partitionKey, sortingKey, primaryKey, samplingKey string
		rows, bytes                                                        int64
	)
	err := db.QueryRowContext(ctx, `
	  SELECT database, engine, comment, partition_key, sorting_key, primary_key, sampling_key,
	         ifNull(toInt64(total_rows), -1), toInt64(ifNull(total_bytes, 0))
	  FROM system.tables
	  WHERE `+clickhouseSchemaIs()+` AND name = ?`, schema, schema, table).
		Scan(&detail.Schema, &engine, &comment, &partitionKey, &sortingKey, &primaryKey, &samplingKey, &rows, &bytes)
	if err != nil {
		return err
	}
	detail.Type, detail.Comment = clickhouseTableType(engine), comment
	detail.Rows, detail.Size, detail.DataSize = rows, bytes, bytes
	if detail.Type != TableTypeTable {
		detail.Rows = -1
	}
	// factPartitionKey is not used here on purpose: the DDL shown for a
	// ClickHouse table is the server's own and already carries the clause.
	detail.Facts = append(detail.Facts, facts(
		"Engine", engine,
		"Partitioned by", partitionKey,
		"Ordered by", sortingKey,
		"Primary key", primaryKey,
		"Sampled by", samplingKey,
	)...)
	return nil
}

// --- definitions ------------------------------------------------------------

func (d clickhouseDialect) objectDefinition(ctx context.Context, db *sql.DB, ref ObjectRef) (*ObjectDefinition, error) {
	if ref.Kind == KindFunction {
		var text string
		err := db.QueryRowContext(ctx, `
		  SELECT create_query FROM system.functions WHERE origin = 'SQLUserDefined' AND name = ?`,
			ref.Name).Scan(&text)
		if err == sql.ErrNoRows {
			return nil, ErrObjectNotFound
		}
		if err != nil {
			return nil, err
		}
		return &ObjectDefinition{Kind: KindFunction, Name: ref.Name, Definition: text, Language: "SQL"}, nil
	}
	if ref.Kind != KindView && ref.Kind != KindMaterializedView && ref.Kind != KindDictionary {
		return nil, fmt.Errorf("%w: %s", ErrNoDefinition, ref.Kind)
	}
	var schema, engine, comment, text string
	err := db.QueryRowContext(ctx, `
	  SELECT database, engine, comment, create_table_query FROM system.tables
	  WHERE `+clickhouseSchemaIs()+` AND name = ?`, ref.Schema, ref.Schema, ref.Name).
		Scan(&schema, &engine, &comment, &text)
	if err == sql.ErrNoRows {
		return nil, ErrObjectNotFound
	}
	if err != nil {
		return nil, err
	}
	want := map[string]string{
		KindView: TableTypeView, KindMaterializedView: TableTypeMaterializedView, KindDictionary: "dictionary",
	}[ref.Kind]
	if got := clickhouseTableType(engine); got != want {
		return nil, fmt.Errorf("%w: %s is a %s, not a %s", ErrObjectNotFound, ref.Name, got, want)
	}
	def := &ObjectDefinition{
		Kind: ref.Kind, Schema: schema, Name: ref.Name, Comment: comment,
		Definition: text, Details: facts("Engine", engine),
	}
	// SHOW CREATE lays the same statement out over several lines; the catalogue
	// column is one long one. The catalogue text stands if the SHOW is refused.
	if rel, err := qualify(d, schema, ref.Name); err == nil {
		what := "TABLE"
		if ref.Kind == KindDictionary {
			what = "DICTIONARY"
		}
		var shown string
		if err := db.QueryRowContext(ctx, "SHOW CREATE "+what+" "+rel).Scan(&shown); err == nil && strings.TrimSpace(shown) != "" {
			def.Definition = strings.ReplaceAll(shown, `\n`, "\n")
		}
	}
	return def, nil
}
