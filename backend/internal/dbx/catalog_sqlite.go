package dbx

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
)

// SQLite keeps one catalogue per attached file — sqlite_master — and the
// statement each object was created with, verbatim. So a definition here is
// always the engine's own text, and what the catalogue cannot say (a check
// constraint, a generated column's expression) is in that text and nowhere
// else.
//
// The engine is the one compiled into this binary, not whatever wrote the
// file, so the table-valued pragmas used below are always available.

// sqliteSchemaPrefix renders `"schema".` for an attached database and nothing
// for the main one, so a statement can address either.
func sqliteSchemaPrefix(schema string) (string, error) {
	if schema == "" || strings.EqualFold(schema, "main") {
		return "", nil
	}
	q, err := quoteDouble(schema)
	if err != nil {
		return "", err
	}
	return q + ".", nil
}

func sqliteSchemaName(schema string) string {
	if schema == "" {
		return "main"
	}
	return schema
}

// sqliteAttached lists the databases on this connection: main, temp once it
// has been used, and whatever was ATTACHed.
func sqliteAttached(ctx context.Context, db *sql.DB) ([]CatalogSchema, error) {
	rows, err := db.QueryContext(ctx, `PRAGMA database_list`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CatalogSchema{}
	for rows.Next() {
		var seq int
		var name, file string
		if err := rows.Scan(&seq, &name, nullText{&file}); err != nil {
			return nil, err
		}
		out = append(out, CatalogSchema{Name: name, Detail: file, Default: name == "main", Tables: -1})
	}
	return out, rows.Err()
}

func (sqliteDialect) catalogSchemas(ctx context.Context, db *sql.DB) ([]CatalogSchema, string, error) {
	schemas, err := sqliteAttached(ctx, db)
	if err != nil {
		return nil, "", err
	}
	for i := range schemas {
		prefix, err := sqliteSchemaPrefix(schemas[i].Name)
		if err != nil {
			continue
		}
		var n int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM `+prefix+`sqlite_master
		  WHERE type IN ('table','view') AND name NOT LIKE 'sqlite!_%' ESCAPE '!'`).Scan(&n); err == nil {
			schemas[i].Tables = n
		}
	}
	return schemas, "main", nil
}

func (d sqliteDialect) catalogGroups(context.Context, *sql.DB) []catalogGroup {
	return []catalogGroup{
		{GroupTables, d.tables},
		{GroupViews, func(ctx context.Context, db *sql.DB, schema string, limit int) ([]CatalogObject, error) {
			return d.masterObjects(ctx, db, schema, limit, "view", KindView)
		}},
		{GroupTriggers, func(ctx context.Context, db *sql.DB, schema string, limit int) ([]CatalogObject, error) {
			return d.masterObjects(ctx, db, schema, limit, "trigger", KindTrigger)
		}},
	}
}

// eachSchema runs read for the named database, or for every attached one when
// none is named, stopping once limit rows have been collected.
func (sqliteDialect) eachSchema(ctx context.Context, db *sql.DB, schema string, limit int,
	read func(schema, prefix string, remaining int) ([]CatalogObject, error)) ([]CatalogObject, error) {
	names := []string{schema}
	if schema == "" {
		attached, err := sqliteAttached(ctx, db)
		if err != nil {
			return nil, err
		}
		names = names[:0]
		for _, s := range attached {
			if s.Name != "temp" {
				names = append(names, s.Name)
			}
		}
	}
	out := []CatalogObject{}
	for _, name := range names {
		if len(out) >= limit {
			break
		}
		prefix, err := sqliteSchemaPrefix(name)
		if err != nil {
			return nil, err
		}
		objects, err := read(sqliteSchemaName(name), prefix, limit-len(out))
		if err != nil {
			return nil, err
		}
		out = append(out, objects...)
	}
	return out, nil
}

// sqliteRowEstimates returns the row counts ANALYZE last recorded, by table.
// They are the planner's statistics, which is what an estimate is on every
// other engine, and they exist only once ANALYZE has run: a database that was
// never analysed has no sqlite_stat1, and its tables no estimate.
func sqliteRowEstimates(ctx context.Context, db *sql.DB, prefix string) map[string]int64 {
	// A stat is "rows in the index, then rows per distinct prefix"; the cast
	// reads the first number. A partial index counts only its own rows, so the
	// largest of a table's entries is the one nearest the table.
	rows, err := db.QueryContext(ctx, `SELECT tbl, max(CAST(stat AS INTEGER)) FROM `+prefix+`sqlite_stat1 GROUP BY tbl`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var table string
		var n sql.NullInt64
		if err := rows.Scan(&table, &n); err != nil {
			return nil
		}
		if n.Valid {
			out[table] = n.Int64
		}
	}
	return out
}

func (d sqliteDialect) tables(ctx context.Context, db *sql.DB, schema string, limit int) ([]CatalogObject, error) {
	return d.eachSchema(ctx, db, schema, limit, func(schema, prefix string, remaining int) ([]CatalogObject, error) {
		estimates := sqliteRowEstimates(ctx, db, prefix)
		// pragma_table_list knows what sqlite_master only implies: which
		// tables are virtual, which are the shadow tables a virtual table
		// keeps its data in, and which were declared STRICT or WITHOUT ROWID.
		// The shadow tables are the virtual table's own storage and are left
		// out, as are the engine's sqlite_ tables. The underscore is escaped:
		// unescaped it is a wildcard, and a table named `sqliteX` vanished.
		return scanObjects(ctx, db, `
		  SELECT name, type, wr, strict FROM pragma_table_list
		  WHERE schema = ? AND type IN ('table','virtual') AND name NOT LIKE 'sqlite!_%' ESCAPE '!'
		  ORDER BY name
		  LIMIT ?`, []any{schema, remaining}, func(rows *sql.Rows) (CatalogObject, error) {
			o := CatalogObject{Kind: KindTable, Schema: schema, Rows: rowEstimate(-1)}
			var tableType string
			var withoutRowid, strict int
			if err := rows.Scan(&o.Name, &tableType, &withoutRowid, &strict); err != nil {
				return o, err
			}
			o.Detail = sqliteTableTraits(tableType, withoutRowid == 1, strict == 1)
			if n, ok := estimates[o.Name]; ok {
				o.Rows = rowEstimate(n)
			}
			return o, nil
		})
	})
}

func sqliteTableTraits(tableType string, withoutRowid, strict bool) string {
	traits := []string{}
	if tableType == "virtual" {
		traits = append(traits, "virtual")
	}
	if strict {
		traits = append(traits, "strict")
	}
	if withoutRowid {
		traits = append(traits, "without rowid")
	}
	return strings.Join(traits, ", ")
}

func (d sqliteDialect) masterObjects(ctx context.Context, db *sql.DB, schema string, limit int, masterType, kind string) ([]CatalogObject, error) {
	return d.eachSchema(ctx, db, schema, limit, func(schema, prefix string, remaining int) ([]CatalogObject, error) {
		return scanObjects(ctx, db, `
		  SELECT name, tbl_name FROM `+prefix+`sqlite_master
		  WHERE type = ? AND name NOT LIKE 'sqlite!_%' ESCAPE '!'
		  ORDER BY name
		  LIMIT ?`, []any{masterType, remaining}, func(rows *sql.Rows) (CatalogObject, error) {
			o := CatalogObject{Kind: kind, Schema: schema}
			var table string
			if err := rows.Scan(&o.Name, &table); err != nil {
				return o, err
			}
			if kind == KindTrigger {
				o.Table = table
			}
			return o, nil
		})
	})
}

// --- one table --------------------------------------------------------------

// sqliteCreateText returns the statement a table or view was created with.
func sqliteCreateText(ctx context.Context, db *sql.DB, schema, name string) string {
	prefix, err := sqliteSchemaPrefix(schema)
	if err != nil {
		return ""
	}
	var text sql.NullString
	_ = db.QueryRowContext(ctx, `SELECT sql FROM `+prefix+`sqlite_master
	  WHERE name = ? AND type IN ('table','view')`, name).Scan(&text)
	return text.String
}

func (sqliteDialect) tableColumns(ctx context.Context, db *sql.DB, schema, table string) ([]Column, error) {
	// table_xinfo rather than table_info: only the former lists generated
	// columns, and a table that has one would otherwise show a column fewer
	// than SELECT * returns.
	rows, err := db.QueryContext(ctx, `
	  SELECT cid, name, type, "notnull", dflt_value, pk, hidden
	  FROM pragma_table_xinfo(?, ?) ORDER BY cid`, table, sqliteSchemaName(schema))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Column{}
	pkColumns := 0
	for rows.Next() {
		var (
			c                        Column
			cid, notNull, pk, hidden int
			dflt                     sql.NullString
		)
		if err := rows.Scan(&cid, &c.Name, &c.Type, &notNull, &dflt, &pk, &hidden); err != nil {
			return nil, err
		}
		if hidden == 1 {
			// A virtual table's hidden column is an argument to the module, not
			// a column a query returns.
			continue
		}
		c.Nullable, c.Default, c.Position = notNull == 0, dflt.String, cid+1
		if pk > 0 {
			c.Key = "PRI"
			pkColumns++
		}
		switch hidden {
		case 2:
			c.GeneratedKind = "virtual"
		case 3:
			c.GeneratedKind = "stored"
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return out, nil
	}
	text := sqliteCreateText(ctx, db, schema, table)
	withoutRowid := sqliteHasTableOption(text, "WITHOUT ROWID")
	for i := range out {
		c := &out[i]
		if c.GeneratedKind != "" {
			c.Generated = sqliteGeneratedExpression(text, c.Name)
			if c.Generated == "" {
				c.Generated = "(see the definition)"
			}
		}
		// A lone INTEGER PRIMARY KEY is the rowid under another name: the
		// engine fills it in when an insert leaves it out.
		if c.Key == "PRI" && pkColumns == 1 && strings.EqualFold(strings.TrimSpace(c.Type), "INTEGER") && !withoutRowid {
			c.Identity = "rowid"
			if strings.Contains(strings.ToUpper(text), "AUTOINCREMENT") {
				c.Identity = "autoincrement"
			}
		}
	}
	return out, nil
}

// sqliteColumnDefinitions splits the body of a CREATE TABLE into its
// comma-separated definitions, respecting parentheses, quotes and comments.
func sqliteColumnDefinitions(createSQL string) []string {
	return createTableItems(DriverSQLite, createSQL)
}

// sqliteTableConstraintWords are the words a table constraint starts with.
// Each is reserved, so an item that starts with anything else is a column.
var sqliteTableConstraintWords = stringSet("constraint", "primary", "unique", "check", "foreign")

var sqliteCreateTableRe = regexp.MustCompile(`(?i)^\s*CREATE\s+(TEMP\s+|TEMPORARY\s+)?TABLE\b`)

// sqliteCheckConstraints reads the CHECK clauses out of the statement a table
// was created with, which is the only place SQLite keeps them. One written on
// a column names that column; one written on the table names none. A check
// SQLite was given no name for has none here either.
func sqliteCheckConstraints(createSQL string) []Constraint {
	out := []Constraint{}
	if !sqliteCreateTableRe.MatchString(createSQL) {
		return out
	}
	for _, item := range createTableItems(DriverSQLite, createSQL) {
		tokens := createItemTokens(DriverSQLite, item)
		if len(tokens) == 0 {
			continue
		}
		columns := []string{}
		if first := tokens[0]; first.name() && !(first.kind == itemWord && sqliteTableConstraintWords[strings.ToLower(first.text)]) {
			columns = []string{first.text}
		}
		for i, tok := range tokens {
			if !tok.keyword("check") || i+1 >= len(tokens) || tokens[i+1].kind != itemGroup {
				continue
			}
			c := Constraint{Type: ConstraintCheck, Columns: columns, Definition: "CHECK (" + strings.TrimSpace(tokens[i+1].text) + ")"}
			if i >= 2 && tokens[i-2].keyword("constraint") && tokens[i-1].name() {
				c.Name = tokens[i-1].text
			}
			out = append(out, c)
		}
	}
	return out
}

// sqliteGeneratedExpression finds a generated column's expression in the
// table's CREATE statement, which is the only place SQLite records it.
func sqliteGeneratedExpression(createSQL, column string) string {
	for _, def := range sqliteColumnDefinitions(createSQL) {
		def = strings.TrimSpace(def)
		name := def
		switch {
		case strings.HasPrefix(def, `"`), strings.HasPrefix(def, "`"), strings.HasPrefix(def, "["):
			closer := def[0]
			if closer == '[' {
				closer = ']'
			}
			end := strings.IndexByte(def[1:], closer)
			if end < 0 {
				continue
			}
			name = def[1 : 1+end]
		default:
			if i := strings.IndexAny(def, " \t\r\n"); i >= 0 {
				name = def[:i]
			}
		}
		if !strings.EqualFold(name, column) {
			continue
		}
		upper := strings.ToUpper(def)
		at := strings.Index(upper, " AS (")
		if at < 0 {
			at = strings.Index(upper, " AS(")
		}
		if at < 0 {
			return ""
		}
		open := at + strings.IndexByte(def[at:], '(')
		depth := 0
		for i := open; i < len(def); i++ {
			switch def[i] {
			case '(':
				depth++
			case ')':
				depth--
				if depth == 0 {
					return strings.TrimSpace(def[open+1 : i])
				}
			}
		}
		return ""
	}
	return ""
}

// sqliteHasTableOption reports whether a CREATE TABLE ends with the given
// table option. Options follow the closing parenthesis, so a column that
// happens to be named STRICT is not one.
func sqliteHasTableOption(createSQL, option string) bool {
	end := strings.LastIndexByte(createSQL, ')')
	if end < 0 {
		return false
	}
	return strings.Contains(strings.ToUpper(createSQL[end:]), option)
}

// sqlitePredicate returns the WHERE clause of a partial index.
func sqlitePredicate(createSQL string) string {
	end := strings.LastIndexByte(createSQL, ')')
	if end < 0 {
		return ""
	}
	// The index's column list closes before WHERE; a parenthesised predicate
	// closes after it, so the search is for the first WHERE outside any
	// parentheses.
	depth := 0
	upper := strings.ToUpper(createSQL)
	for i := 0; i < len(createSQL); i++ {
		switch createSQL[i] {
		case '(':
			depth++
		case ')':
			depth--
		}
		if depth == 0 && strings.HasPrefix(upper[i:], "WHERE") && i > 0 &&
			(createSQL[i-1] == ' ' || createSQL[i-1] == '\n' || createSQL[i-1] == '\t' || createSQL[i-1] == ')') {
			return strings.TrimSpace(createSQL[i+len("WHERE"):])
		}
	}
	return ""
}

func (sqliteDialect) tableIndexes(ctx context.Context, db *sql.DB, schema, table string) ([]Index, error) {
	prefix, err := sqliteSchemaPrefix(schema)
	if err != nil {
		return nil, err
	}
	name := sqliteSchemaName(schema)
	rows, err := db.QueryContext(ctx, `
	  SELECT il.name, il."unique", il.origin, il.partial, ix.cid, ix.name, ix.key,
	         (SELECT m.sql FROM `+prefix+`sqlite_master m WHERE m.type = 'index' AND m.name = il.name)
	  FROM pragma_index_list(?, ?) il
	  JOIN pragma_index_xinfo(il.name, ?) ix
	  ORDER BY il.seq, ix.seqno`, table, name, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	order := []string{}
	byName := map[string]*Index{}
	for rows.Next() {
		var (
			indexName, origin         string
			unique, partial, cid, key int
			column, text              sql.NullString
		)
		if err := rows.Scan(&indexName, &unique, &origin, &partial, &cid, &column, &key, &text); err != nil {
			return nil, err
		}
		ix, ok := byName[indexName]
		if !ok {
			ix = &Index{Name: indexName, Unique: unique == 1, Primary: origin == "pk", Columns: []string{}, Definition: text.String}
			if origin != "c" {
				// Created by a PRIMARY KEY or UNIQUE clause: it goes when the
				// constraint goes, and SQLite will not drop it by name.
				ix.Constraint = indexName
			}
			if partial == 1 {
				ix.Predicate = sqlitePredicate(text.String)
			}
			byName[indexName] = ix
			order = append(order, indexName)
		}
		if key == 0 {
			// The rowid every index carries after its key columns.
			continue
		}
		switch {
		case column.Valid:
			ix.Columns = append(ix.Columns, column.String)
		case cid == -2:
			ix.Columns = append(ix.Columns, "(expression)")
			ix.Expression = true
		default:
			ix.Columns = append(ix.Columns, "rowid")
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]Index, 0, len(order))
	for _, n := range order {
		out = append(out, *byName[n])
	}
	return out, nil
}

// tableConstraints reports the unique constraints, which SQLite materialises
// as indexes, and the check constraints, which it keeps only as text inside
// the CREATE TABLE.
func (d sqliteDialect) tableConstraints(ctx context.Context, db *sql.DB, schema, table string) ([]Constraint, error) {
	indexes, err := d.tableIndexes(ctx, db, schema, table)
	if err != nil {
		return nil, err
	}
	out := []Constraint{}
	for _, ix := range indexes {
		if !ix.Unique || ix.Primary || ix.Constraint == "" {
			continue
		}
		quoted := make([]string, 0, len(ix.Columns))
		for _, c := range ix.Columns {
			q, err := quoteDouble(c)
			if err != nil {
				q = c
			}
			quoted = append(quoted, q)
		}
		out = append(out, Constraint{
			Name: ix.Name, Type: ConstraintUnique, Columns: ix.Columns,
			Definition: "UNIQUE (" + strings.Join(quoted, ", ") + ")",
		})
	}
	return append(out, sqliteCheckConstraints(sqliteCreateText(ctx, db, schema, table))...), nil
}

func (sqliteDialect) tableReferencedBy(ctx context.Context, db *sql.DB, schema, table string) ([]IncomingForeignKey, error) {
	prefix, err := sqliteSchemaPrefix(schema)
	if err != nil {
		return nil, err
	}
	name := sqliteSchemaName(schema)
	// A foreign key cannot cross files, so the tables that can reference this
	// one are the other tables of its own database.
	rows, err := db.QueryContext(ctx, `
	  SELECT m.name, f.id, f."from", f."to", f.on_update, f.on_delete
	  FROM `+prefix+`sqlite_master m
	  JOIN pragma_foreign_key_list(m.name, ?) f
	  WHERE m.type = 'table' AND lower(f."table") = lower(?)
	  ORDER BY m.name, f.id, f.seq`, name, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	acc := newIncomingAcc()
	for rows.Next() {
		var (
			child, from, onUpdate, onDelete string
			id                              int
			to                              sql.NullString
		)
		if err := rows.Scan(&child, &id, &from, &to, &onUpdate, &onDelete); err != nil {
			return nil, err
		}
		in := acc.get(name, child, fmt.Sprintf("fk_%d", id))
		in.Columns, in.RefColumns = append(in.Columns, from), append(in.RefColumns, to.String)
		in.OnUpdate, in.OnDelete = onUpdate, onDelete
	}
	return acc.slice(), rows.Err()
}

func (sqliteDialect) tableFacts(ctx context.Context, db *sql.DB, schema, table string, detail *TableDetail) error {
	var tableType string
	var withoutRowid, strict int
	if err := db.QueryRowContext(ctx, `
	  SELECT type, wr, strict FROM pragma_table_list WHERE schema = ? AND name = ?`,
		sqliteSchemaName(schema), table).Scan(&tableType, &withoutRowid, &strict); err != nil {
		return err
	}
	detail.Schema = sqliteSchemaName(schema)
	switch tableType {
	case "view":
		detail.Type = TableTypeView
	case "virtual":
		detail.Type = "virtual table"
	default:
		detail.Type = TableTypeTable
		if prefix, err := sqliteSchemaPrefix(schema); err == nil {
			if n, ok := sqliteRowEstimates(ctx, db, prefix)[table]; ok {
				detail.Rows = n
			}
		}
	}
	if strict == 1 {
		detail.Facts = append(detail.Facts, ObjectFact{"Strict", "yes"})
	}
	if withoutRowid == 1 {
		detail.Facts = append(detail.Facts, ObjectFact{"Row id", "none (WITHOUT ROWID)"})
	}
	return nil
}

// The three reads below are the dialect's own, pointed at a schema. The
// dialect's are written against the main database and answer for it whatever
// schema they are given.

func (sqliteDialect) tablePrimaryKey(ctx context.Context, db *sql.DB, schema, table string) ([]string, error) {
	// pk is the 1-based position within the key, not a flag, so a composite
	// key keeps its declared order.
	rows, err := db.QueryContext(ctx, `
	  SELECT name FROM pragma_table_xinfo(?, ?) WHERE pk > 0 ORDER BY pk`, table, sqliteSchemaName(schema))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

func (sqliteDialect) tableForeignKeys(ctx context.Context, db *sql.DB, schema, table string) ([]ForeignKey, error) {
	name := sqliteSchemaName(schema)
	rows, err := db.QueryContext(ctx, `
	  SELECT id, "table", "from", "to", on_update, on_delete
	  FROM pragma_foreign_key_list(?, ?) ORDER BY id, seq`, table, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	acc := newFKAcc()
	for rows.Next() {
		var (
			id                                 int
			refTable, from, onUpdate, onDelete string
			to                                 sql.NullString
		)
		if err := rows.Scan(&id, &refTable, &from, &to, &onUpdate, &onDelete); err != nil {
			return nil, err
		}
		// SQLite names no foreign keys, so the constraint id doubles as one. A
		// NULL "to" means the key references the parent's primary key.
		fk := acc.get(fmt.Sprintf("fk_%d", id))
		fk.Columns, fk.RefColumns = append(fk.Columns, from), append(fk.RefColumns, to.String)
		fk.RefSchema, fk.RefTable = name, refTable
		fk.OnUpdate, fk.OnDelete = onUpdate, onDelete
	}
	return acc.slice(), rows.Err()
}

func (sqliteDialect) tableCreateSQL(ctx context.Context, db *sql.DB, schema, table string) (string, error) {
	return sqliteCreateText(ctx, db, schema, table), nil
}

// --- definitions ------------------------------------------------------------

func (sqliteDialect) objectDefinition(ctx context.Context, db *sql.DB, ref ObjectRef) (*ObjectDefinition, error) {
	masterType := ""
	switch ref.Kind {
	case KindView:
		masterType = "view"
	case KindTrigger:
		masterType = "trigger"
	default:
		return nil, fmt.Errorf("%w: %s", ErrNoDefinition, ref.Kind)
	}
	prefix, err := sqliteSchemaPrefix(ref.Schema)
	if err != nil {
		return nil, err
	}
	var text sql.NullString
	var table string
	err = db.QueryRowContext(ctx, `SELECT sql, tbl_name FROM `+prefix+`sqlite_master
	  WHERE type = ? AND name = ?`, masterType, ref.Name).Scan(&text, &table)
	if err == sql.ErrNoRows {
		return nil, ErrObjectNotFound
	}
	if err != nil {
		return nil, err
	}
	def := &ObjectDefinition{
		Kind: ref.Kind, Schema: sqliteSchemaName(ref.Schema), Name: ref.Name,
		Definition: text.String,
	}
	if ref.Kind == KindTrigger {
		def.Table = table
		def.Details = facts("Table", table)
	}
	return def, nil
}
