package dbx

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// MySQL and MariaDB answer from information_schema, which both keep honest,
// and hand back the original statement for anything through SHOW CREATE. A
// "schema" here is a database: the two words name the same thing on this
// engine, and the server lets one connection read every one it may see.
//
// The two products share a driver and differ in the details that matter to a
// catalogue — MariaDB has sequences and a table name on its check constraints,
// MySQL has neither — so the few places that care ask the server which it is.

var mysqlSystemSchemas = "'mysql','information_schema','performance_schema','sys'"

// mysqlSchemaFilter narrows to one database, or to every database that is not
// the server's own when the bound name is empty. It consumes two parameters.
func mysqlSchemaFilter(column string) string {
	return `((? = '' AND ` + column + ` NOT IN (` + mysqlSystemSchemas + `)) OR ` + column + ` = ?)`
}

// mysqlSchemaIs matches one database, the connection's own when none is named.
func mysqlSchemaIs(column string) string {
	return column + ` = COALESCE(NULLIF(?, ''), DATABASE())`
}

// isMariaDB asks the server which product it is. The driver cannot say: both
// speak the same protocol and announce themselves only in the version string.
func isMariaDB(ctx context.Context, db *sql.DB) bool {
	var version string
	if err := db.QueryRowContext(ctx, "SELECT VERSION()").Scan(&version); err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(version), "mariadb")
}

func (mysqlDialect) catalogSchemas(ctx context.Context, db *sql.DB) ([]CatalogSchema, string, error) {
	rows, err := db.QueryContext(ctx, `
	  SELECT s.SCHEMA_NAME,
	         COALESCE(s.DEFAULT_CHARACTER_SET_NAME, ''),
	         COALESCE(s.DEFAULT_COLLATION_NAME, ''),
	         COUNT(t.TABLE_NAME),
	         COALESCE(s.SCHEMA_NAME = DATABASE(), 0)
	  FROM information_schema.SCHEMATA s
	  LEFT JOIN information_schema.TABLES t ON t.TABLE_SCHEMA = s.SCHEMA_NAME
	  GROUP BY s.SCHEMA_NAME, s.DEFAULT_CHARACTER_SET_NAME, s.DEFAULT_COLLATION_NAME
	  ORDER BY s.SCHEMA_NAME`)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	out, current := []CatalogSchema{}, ""
	for rows.Next() {
		var (
			s                  CatalogSchema
			charset, collation string
			isDefault          int
		)
		if err := rows.Scan(&s.Name, &charset, &collation, &s.Tables, &isDefault); err != nil {
			return nil, "", err
		}
		s.Detail = strings.TrimSpace(charset + " " + collation)
		switch strings.ToLower(s.Name) {
		case "mysql", "information_schema", "performance_schema", "sys":
			s.System = true
		}
		if isDefault == 1 {
			s.Default, current = true, s.Name
		}
		out = append(out, s)
	}
	return out, current, rows.Err()
}

func (d mysqlDialect) catalogGroups(ctx context.Context, db *sql.DB) []catalogGroup {
	groups := []catalogGroup{
		{GroupTables, d.tables},
		{GroupViews, d.views},
		{GroupFunctions, func(ctx context.Context, db *sql.DB, schema string, limit int) ([]CatalogObject, error) {
			return d.routines(ctx, db, schema, limit, "FUNCTION")
		}},
		{GroupProcedures, func(ctx context.Context, db *sql.DB, schema string, limit int) ([]CatalogObject, error) {
			return d.routines(ctx, db, schema, limit, "PROCEDURE")
		}},
		{GroupTriggers, d.triggers},
		{GroupEvents, d.events},
	}
	if isMariaDB(ctx, db) {
		groups = append(groups, catalogGroup{GroupSequences, d.sequences})
	}
	return groups
}

func (mysqlDialect) tables(ctx context.Context, db *sql.DB, schema string, limit int) ([]CatalogObject, error) {
	return scanObjects(ctx, db, `
	  SELECT TABLE_SCHEMA, TABLE_NAME, COALESCE(TABLE_ROWS, -1),
	         COALESCE(DATA_LENGTH + INDEX_LENGTH, 0),
	         COALESCE(TABLE_COMMENT, ''), COALESCE(ENGINE, ''), TABLE_TYPE
	  FROM information_schema.TABLES
	  WHERE TABLE_TYPE IN ('BASE TABLE','SYSTEM VERSIONED') AND `+mysqlSchemaFilter("TABLE_SCHEMA")+`
	  ORDER BY TABLE_SCHEMA, TABLE_NAME
	  LIMIT ?`, []any{schema, schema, limit}, func(rows *sql.Rows) (CatalogObject, error) {
		o := CatalogObject{Kind: KindTable}
		var estimate int64
		var engine, tableType string
		if err := rows.Scan(&o.Schema, &o.Name, &estimate, &o.Size, &o.Comment, &engine, &tableType); err != nil {
			return o, err
		}
		o.Rows, o.Detail = rowEstimate(estimate), engine
		if tableType == "SYSTEM VERSIONED" {
			o.Detail = strings.TrimSpace(engine + ", system versioned")
		}
		return o, nil
	})
}

func (mysqlDialect) views(ctx context.Context, db *sql.DB, schema string, limit int) ([]CatalogObject, error) {
	return scanObjects(ctx, db, `
	  SELECT TABLE_SCHEMA, TABLE_NAME, COALESCE(DEFINER, ''), COALESCE(IS_UPDATABLE, '')
	  FROM information_schema.VIEWS
	  WHERE `+mysqlSchemaFilter("TABLE_SCHEMA")+`
	  ORDER BY TABLE_SCHEMA, TABLE_NAME
	  LIMIT ?`, []any{schema, schema, limit}, func(rows *sql.Rows) (CatalogObject, error) {
		o := CatalogObject{Kind: KindView}
		var updatable string
		if err := rows.Scan(&o.Schema, &o.Name, &o.Owner, &updatable); err != nil {
			return o, err
		}
		if updatable == "YES" {
			o.Detail = "updatable"
		}
		return o, nil
	})
}

func (mysqlDialect) routines(ctx context.Context, db *sql.DB, schema string, limit int, routineType string) ([]CatalogObject, error) {
	kind := KindFunction
	if routineType == "PROCEDURE" {
		kind = KindProcedure
	}
	objects, err := scanObjects(ctx, db, `
	  SELECT ROUTINE_SCHEMA, ROUTINE_NAME, COALESCE(DEFINER, ''), COALESCE(ROUTINE_COMMENT, ''),
	         COALESCE(DTD_IDENTIFIER, ''), COALESCE(ROUTINE_BODY, '')
	  FROM information_schema.ROUTINES
	  WHERE ROUTINE_TYPE = ? AND `+mysqlSchemaFilter("ROUTINE_SCHEMA")+`
	  ORDER BY ROUTINE_SCHEMA, ROUTINE_NAME
	  LIMIT ?`, []any{routineType, schema, schema, limit}, func(rows *sql.Rows) (CatalogObject, error) {
		o := CatalogObject{Kind: kind}
		if err := rows.Scan(&o.Schema, &o.Name, &o.Owner, &o.Comment, &o.Returns, &o.Language); err != nil {
			return o, err
		}
		return o, nil
	})
	if err != nil || len(objects) == 0 {
		return objects, err
	}
	// The argument list is a second view. It is decoration here — MySQL has no
	// overloading, so a routine is already identified by its name — and a
	// server that refuses it still lists its routines.
	rows, err := db.QueryContext(ctx, `
	  SELECT SPECIFIC_SCHEMA, SPECIFIC_NAME, COALESCE(PARAMETER_MODE, ''), COALESCE(PARAMETER_NAME, ''),
	         COALESCE(DTD_IDENTIFIER, '')
	  FROM information_schema.PARAMETERS
	  WHERE ROUTINE_TYPE = ? AND ORDINAL_POSITION > 0 AND `+mysqlSchemaFilter("SPECIFIC_SCHEMA")+`
	  ORDER BY SPECIFIC_SCHEMA, SPECIFIC_NAME, ORDINAL_POSITION`, routineType, schema, schema)
	if err != nil {
		return objects, nil
	}
	defer rows.Close()
	signatures := map[catalogTable][]string{}
	for rows.Next() {
		var schema, name, mode, param, typ string
		if err := rows.Scan(&schema, &name, &mode, &param, &typ); err != nil {
			return objects, nil
		}
		part := strings.TrimSpace(param + " " + typ)
		if mode != "" && mode != "IN" {
			part = mode + " " + part
		}
		key := catalogTable{schema, name}
		signatures[key] = append(signatures[key], part)
	}
	for i := range objects {
		objects[i].Signature = strings.Join(signatures[catalogTable{objects[i].Schema, objects[i].Name}], ", ")
	}
	return objects, nil
}

func (mysqlDialect) triggers(ctx context.Context, db *sql.DB, schema string, limit int) ([]CatalogObject, error) {
	return scanObjects(ctx, db, `
	  SELECT TRIGGER_SCHEMA, TRIGGER_NAME, EVENT_OBJECT_TABLE, ACTION_TIMING, EVENT_MANIPULATION,
	         COALESCE(DEFINER, '')
	  FROM information_schema.TRIGGERS
	  WHERE `+mysqlSchemaFilter("TRIGGER_SCHEMA")+`
	  ORDER BY TRIGGER_SCHEMA, EVENT_OBJECT_TABLE, TRIGGER_NAME
	  LIMIT ?`, []any{schema, schema, limit}, func(rows *sql.Rows) (CatalogObject, error) {
		o := CatalogObject{Kind: KindTrigger}
		var timing, event string
		if err := rows.Scan(&o.Schema, &o.Name, &o.Table, &timing, &event, &o.Owner); err != nil {
			return o, err
		}
		o.Detail = timing + " " + event + ", each row"
		return o, nil
	})
}

func (mysqlDialect) events(ctx context.Context, db *sql.DB, schema string, limit int) ([]CatalogObject, error) {
	return scanObjects(ctx, db, `
	  SELECT EVENT_SCHEMA, EVENT_NAME, COALESCE(DEFINER, ''), COALESCE(EVENT_COMMENT, ''),
	         COALESCE(STATUS, ''), COALESCE(EVENT_TYPE, ''),
	         COALESCE(INTERVAL_VALUE, ''), COALESCE(INTERVAL_FIELD, '')
	  FROM information_schema.EVENTS
	  WHERE `+mysqlSchemaFilter("EVENT_SCHEMA")+`
	  ORDER BY EVENT_SCHEMA, EVENT_NAME
	  LIMIT ?`, []any{schema, schema, limit}, func(rows *sql.Rows) (CatalogObject, error) {
		o := CatalogObject{Kind: KindEvent}
		var status, eventType, value, field string
		if err := rows.Scan(&o.Schema, &o.Name, &o.Owner, &o.Comment, &status, &eventType, &value, &field); err != nil {
			return o, err
		}
		o.Detail = mysqlEventSchedule(eventType, value, field, status)
		return o, nil
	})
}

func mysqlEventSchedule(eventType, value, field, status string) string {
	schedule := "one time"
	if eventType == "RECURRING" {
		schedule = strings.ToLower(strings.TrimSpace("every " + value + " " + field))
	}
	return schedule + ", " + strings.ToLower(status)
}

func (mysqlDialect) sequences(ctx context.Context, db *sql.DB, schema string, limit int) ([]CatalogObject, error) {
	return scanObjects(ctx, db, `
	  SELECT TABLE_SCHEMA, TABLE_NAME, COALESCE(TABLE_COMMENT, '')
	  FROM information_schema.TABLES
	  WHERE TABLE_TYPE = 'SEQUENCE' AND `+mysqlSchemaFilter("TABLE_SCHEMA")+`
	  ORDER BY TABLE_SCHEMA, TABLE_NAME
	  LIMIT ?`, []any{schema, schema, limit}, func(rows *sql.Rows) (CatalogObject, error) {
		o := CatalogObject{Kind: KindSequence}
		return o, rows.Scan(&o.Schema, &o.Name, &o.Comment)
	})
}

// --- one table --------------------------------------------------------------

func (mysqlDialect) tableColumns(ctx context.Context, db *sql.DB, schema, table string) ([]Column, error) {
	rows, err := db.QueryContext(ctx, `
	  SELECT COLUMN_NAME, COLUMN_TYPE, IS_NULLABLE, COLUMN_DEFAULT, ORDINAL_POSITION,
	         COALESCE(COLUMN_KEY, ''), COALESCE(EXTRA, ''), COALESCE(COLUMN_COMMENT, ''),
	         COALESCE(GENERATION_EXPRESSION, ''), VERSION()
	  FROM information_schema.COLUMNS
	  WHERE `+mysqlSchemaIs("TABLE_SCHEMA")+` AND TABLE_NAME = ?
	  ORDER BY ORDINAL_POSITION`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Column{}
	for rows.Next() {
		var (
			c                                    Column
			nullable, extra, generation, version string
			dflt                                 sql.NullString
		)
		// The server's version rides along on every row: which product this is
		// decides how the default is read, and asking separately would be a
		// second round trip for every table of a schema-wide read.
		if err := rows.Scan(&c.Name, &c.Type, &nullable, &dflt, &c.Position, &c.Key,
			&extra, &c.Comment, &generation, &version); err != nil {
			return nil, err
		}
		c.Nullable, extra = nullable == "YES", strings.ToLower(extra)
		c.Default = mysqlDefaultSQL(c.Type, dflt, extra, strings.Contains(strings.ToLower(version), "mariadb"))
		if strings.Contains(extra, "auto_increment") {
			c.Identity = "auto_increment"
		}
		if generation != "" {
			c.Generated, c.GeneratedKind, c.Default = generation, "virtual", ""
			if strings.Contains(extra, "stored") || strings.Contains(extra, "persistent") {
				c.GeneratedKind = "stored"
			}
		}
		if kind, values, ok := mysqlEnumValues(c.Type); ok {
			c.TypeKind, c.EnumValues = kind, values
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// mysqlDefaultSQL turns information_schema's COLUMN_DEFAULT into the text that
// would follow DEFAULT in a statement, which is what every other engine's
// catalogue reports and what a column definition is rebuilt from.
//
// The two products disagree about that column. MariaDB already stores the SQL
// text — a string default arrives quoted, and a nullable column with no
// default says NULL. MySQL stores the bare value, so `ok` may be the string
// 'ok' or an expression, and only the EXTRA column says which.
func mysqlDefaultSQL(columnType string, raw sql.NullString, extra string, maria bool) string {
	if !raw.Valid {
		return ""
	}
	if maria {
		if strings.EqualFold(raw.String, "NULL") {
			return ""
		}
		return raw.String
	}
	if strings.Contains(extra, "default_generated") {
		if strings.HasPrefix(strings.ToUpper(raw.String), "CURRENT_TIMESTAMP") {
			return raw.String
		}
		return "(" + raw.String + ")"
	}
	base := strings.ToLower(columnType)
	if i := strings.IndexAny(base, "( "); i >= 0 {
		base = base[:i]
	}
	switch base {
	case "tinyint", "smallint", "mediumint", "int", "integer", "bigint", "decimal", "numeric",
		"float", "double", "real", "bit", "year", "bool", "boolean":
		return raw.String
	}
	escaped := strings.ReplaceAll(raw.String, `\`, `\\`)
	return "'" + strings.ReplaceAll(escaped, "'", "''") + "'"
}

// mysqlEnumValues reads the labels out of an enum('a','b') or set('a','b')
// column type. MySQL has no enum types, only columns declared with their
// labels inline, so the type text is the only place they are written down.
func mysqlEnumValues(columnType string) (kind string, values []string, ok bool) {
	lower := strings.ToLower(columnType)
	switch {
	case strings.HasPrefix(lower, "enum("):
		kind = "enum"
	case strings.HasPrefix(lower, "set("):
		kind = "set"
	default:
		return "", nil, false
	}
	body := columnType[strings.IndexByte(columnType, '(')+1:]
	end := strings.LastIndexByte(body, ')')
	if end < 0 {
		return "", nil, false
	}
	body = body[:end]
	values = []string{}
	for i := 0; i < len(body); {
		if body[i] != '\'' {
			i++
			continue
		}
		var b strings.Builder
		i++
		for i < len(body) {
			if body[i] == '\'' {
				if i+1 < len(body) && body[i+1] == '\'' {
					b.WriteByte('\'')
					i += 2
					continue
				}
				i++
				break
			}
			b.WriteByte(body[i])
			i++
		}
		values = append(values, b.String())
	}
	return kind, values, true
}

func (mysqlDialect) tableIndexes(ctx context.Context, db *sql.DB, schema, table string) ([]Index, error) {
	rows, err := db.QueryContext(ctx, `
	  SELECT INDEX_NAME, NON_UNIQUE, COLUMN_NAME, COALESCE(INDEX_TYPE, '')
	  FROM information_schema.STATISTICS
	  WHERE `+mysqlSchemaIs("TABLE_SCHEMA")+` AND TABLE_NAME = ?
	  ORDER BY INDEX_NAME, SEQ_IN_INDEX`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	order := []string{}
	byName := map[string]*Index{}
	for rows.Next() {
		var (
			name, method string
			nonUnique    int
			column       sql.NullString
		)
		if err := rows.Scan(&name, &nonUnique, &column, &method); err != nil {
			return nil, err
		}
		ix, ok := byName[name]
		if !ok {
			ix = &Index{Name: name, Unique: nonUnique == 0, Primary: name == "PRIMARY", Method: method, Columns: []string{}}
			if ix.Primary {
				ix.Constraint = name
			}
			byName[name] = ix
			order = append(order, name)
		}
		if column.Valid {
			ix.Columns = append(ix.Columns, column.String)
		} else {
			// A functional key part has no column name. MySQL keeps its text in
			// a column MariaDB does not have, so it is named for what it is
			// rather than read from a view half the servers would refuse.
			ix.Columns = append(ix.Columns, "(expression)")
			ix.Expression = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]Index, 0, len(order))
	for _, name := range order {
		out = append(out, *byName[name])
	}
	return out, nil
}

func (d mysqlDialect) tableConstraints(ctx context.Context, db *sql.DB, schema, table string) ([]Constraint, error) {
	out := []Constraint{}
	// A unique constraint is a unique index on this engine; the index list is
	// where its columns are.
	indexes, err := d.tableIndexes(ctx, db, schema, table)
	if err != nil {
		return nil, err
	}
	for _, ix := range indexes {
		if !ix.Unique || ix.Primary {
			continue
		}
		quoted := make([]string, 0, len(ix.Columns))
		for _, c := range ix.Columns {
			if q, err := d.QuoteIdent(c); err == nil && !ix.Expression {
				quoted = append(quoted, q)
			} else {
				quoted = append(quoted, c)
			}
		}
		out = append(out, Constraint{
			Name: ix.Name, Type: ConstraintUnique, Columns: ix.Columns,
			Definition: "UNIQUE (" + strings.Join(quoted, ", ") + ")",
		})
	}
	// MariaDB names the table on a check constraint and MySQL does not, and a
	// MariaDB column-level check is named after its column — unique within the
	// table and not within the schema — so MariaDB's form is tried first and
	// MySQL's is what a server without that column falls back to.
	checks, err := db.QueryContext(ctx, `
	  SELECT cc.CONSTRAINT_NAME, cc.CHECK_CLAUSE
	  FROM information_schema.CHECK_CONSTRAINTS cc
	  WHERE `+mysqlSchemaIs("cc.CONSTRAINT_SCHEMA")+` AND cc.TABLE_NAME = ?
	  ORDER BY cc.CONSTRAINT_NAME`, schema, table)
	if err != nil {
		checks, err = db.QueryContext(ctx, `
		  SELECT tc.CONSTRAINT_NAME, cc.CHECK_CLAUSE
		  FROM information_schema.TABLE_CONSTRAINTS tc
		  JOIN information_schema.CHECK_CONSTRAINTS cc
		    ON cc.CONSTRAINT_SCHEMA = tc.CONSTRAINT_SCHEMA AND cc.CONSTRAINT_NAME = tc.CONSTRAINT_NAME
		  WHERE tc.CONSTRAINT_TYPE = 'CHECK' AND `+mysqlSchemaIs("tc.TABLE_SCHEMA")+` AND tc.TABLE_NAME = ?
		  ORDER BY tc.CONSTRAINT_NAME`, schema, table)
	}
	if err != nil {
		// A server from before check constraints existed has no such view, and
		// has no check constraints either.
		return out, nil
	}
	defer checks.Close()
	for checks.Next() {
		var name, clause string
		if err := checks.Scan(&name, &clause); err != nil {
			return out, nil
		}
		out = append(out, Constraint{
			Name: name, Type: ConstraintCheck, Columns: []string{},
			Definition: "CHECK (" + clause + ")",
		})
	}
	return out, nil
}

func (mysqlDialect) tableReferencedBy(ctx context.Context, db *sql.DB, schema, table string) ([]IncomingForeignKey, error) {
	rows, err := db.QueryContext(ctx, `
	  SELECT k.CONSTRAINT_NAME, k.TABLE_SCHEMA, k.TABLE_NAME, k.COLUMN_NAME, k.REFERENCED_COLUMN_NAME,
	         COALESCE(r.UPDATE_RULE, ''), COALESCE(r.DELETE_RULE, '')
	  FROM information_schema.KEY_COLUMN_USAGE k
	  LEFT JOIN information_schema.REFERENTIAL_CONSTRAINTS r
	    ON r.CONSTRAINT_NAME = k.CONSTRAINT_NAME AND r.CONSTRAINT_SCHEMA = k.TABLE_SCHEMA
	  WHERE `+mysqlSchemaIs("k.REFERENCED_TABLE_SCHEMA")+` AND k.REFERENCED_TABLE_NAME = ?
	  ORDER BY k.TABLE_SCHEMA, k.TABLE_NAME, k.CONSTRAINT_NAME, k.ORDINAL_POSITION`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	acc := newIncomingAcc()
	for rows.Next() {
		var name, childSchema, child, col, refCol, upd, del string
		if err := rows.Scan(&name, &childSchema, &child, &col, &refCol, &upd, &del); err != nil {
			return nil, err
		}
		in := acc.get(childSchema, child, name)
		in.Columns, in.RefColumns = append(in.Columns, col), append(in.RefColumns, refCol)
		in.OnUpdate, in.OnDelete = upd, del
	}
	return acc.slice(), rows.Err()
}

func (mysqlDialect) tableFacts(ctx context.Context, db *sql.DB, schema, table string, detail *TableDetail) error {
	var (
		tableType, engine, comment, collation, rowFormat string
		rows, data, index                                int64
		autoIncrement                                    sql.NullInt64
	)
	err := db.QueryRowContext(ctx, `
	  SELECT TABLE_SCHEMA, TABLE_TYPE, COALESCE(ENGINE, ''), COALESCE(TABLE_ROWS, -1),
	         COALESCE(DATA_LENGTH, 0), COALESCE(INDEX_LENGTH, 0),
	         COALESCE(TABLE_COMMENT, ''), COALESCE(TABLE_COLLATION, ''), COALESCE(ROW_FORMAT, ''),
	         AUTO_INCREMENT
	  FROM information_schema.TABLES
	  WHERE `+mysqlSchemaIs("TABLE_SCHEMA")+` AND TABLE_NAME = ?`, schema, table).
		Scan(&detail.Schema, &tableType, &engine, &rows, &data, &index, &comment, &collation, &rowFormat, &autoIncrement)
	if err != nil {
		return err
	}
	detail.Rows, detail.DataSize, detail.IndexSize, detail.Size = rows, data, index, data+index
	switch tableType {
	case "BASE TABLE", "SYSTEM VERSIONED":
		detail.Type, detail.Comment = TableTypeTable, comment
	case "VIEW", "SYSTEM VIEW":
		// information_schema writes the word VIEW where a view's comment would be.
		detail.Type, detail.Rows = TableTypeView, -1
	default:
		detail.Type, detail.Comment = strings.ToLower(tableType), comment
	}
	next := ""
	if autoIncrement.Valid {
		next = fmt.Sprint(autoIncrement.Int64)
	}
	detail.Facts = append(detail.Facts, facts(
		"Engine", engine,
		"Collation", collation,
		"Row format", rowFormat,
		"Next auto-increment", next,
	)...)
	return nil
}

// --- definitions ------------------------------------------------------------

// mysqlShowCreate runs SHOW CREATE <what> and returns the statement column.
// The column is found by name: its position and its name both vary by object
// kind, and a view's reply has two more columns than a table's.
func mysqlShowCreate(ctx context.Context, db *sql.DB, what, rel string, columns ...string) (string, error) {
	rows, err := db.QueryContext(ctx, "SHOW CREATE "+what+" "+rel)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	if !rows.Next() {
		return "", rows.Err()
	}
	named, err := scanNamed(rows)
	if err != nil {
		return "", err
	}
	for _, c := range columns {
		if text, ok := named[c]; ok {
			return text, nil
		}
	}
	return "", nil
}

func (d mysqlDialect) objectDefinition(ctx context.Context, db *sql.DB, ref ObjectRef) (*ObjectDefinition, error) {
	schema := ref.Schema
	if schema == "" {
		if err := db.QueryRowContext(ctx, "SELECT COALESCE(DATABASE(), '')").Scan(&schema); err != nil {
			return nil, err
		}
	}
	if schema == "" {
		return nil, fmt.Errorf("a database is required: this connection has none selected")
	}
	rel, err := qualify(d, schema, ref.Name)
	if err != nil {
		return nil, err
	}
	def := &ObjectDefinition{Kind: ref.Kind, Schema: schema, Name: ref.Name}
	var what string
	var columns []string
	switch ref.Kind {
	case KindView:
		var security, updatable, check string
		err = db.QueryRowContext(ctx, `
		  SELECT COALESCE(DEFINER, ''), COALESCE(SECURITY_TYPE, ''), COALESCE(IS_UPDATABLE, ''),
		         COALESCE(CHECK_OPTION, '')
		  FROM information_schema.VIEWS WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?`, schema, ref.Name).
			Scan(&def.Owner, &security, &updatable, &check)
		def.Details = facts("Definer", def.Owner, "Security", strings.ToLower(security),
			"Updatable", strings.ToLower(updatable), "Check option", strings.ToLower(check))
		what, columns = "VIEW", []string{"create view"}
	case KindFunction, KindProcedure:
		routineType := "FUNCTION"
		if ref.Kind == KindProcedure {
			routineType = "PROCEDURE"
		}
		var security, deterministic, access string
		err = db.QueryRowContext(ctx, `
		  SELECT COALESCE(DEFINER, ''), COALESCE(ROUTINE_COMMENT, ''), COALESCE(DTD_IDENTIFIER, ''),
		         COALESCE(ROUTINE_BODY, ''), COALESCE(SECURITY_TYPE, ''), COALESCE(IS_DETERMINISTIC, ''),
		         COALESCE(SQL_DATA_ACCESS, '')
		  FROM information_schema.ROUTINES
		  WHERE ROUTINE_SCHEMA = ? AND ROUTINE_NAME = ? AND ROUTINE_TYPE = ?`, schema, ref.Name, routineType).
			Scan(&def.Owner, &def.Comment, &def.Returns, &def.Language, &security, &deterministic, &access)
		def.Details = facts("Definer", def.Owner, "Returns", def.Returns, "Security", strings.ToLower(security),
			"Deterministic", strings.ToLower(deterministic), "Data access", strings.ToLower(access))
		what, columns = routineType, []string{"create function", "create procedure"}
	case KindTrigger:
		var timing, event string
		err = db.QueryRowContext(ctx, `
		  SELECT EVENT_OBJECT_TABLE, ACTION_TIMING, EVENT_MANIPULATION, COALESCE(DEFINER, '')
		  FROM information_schema.TRIGGERS WHERE TRIGGER_SCHEMA = ? AND TRIGGER_NAME = ?`, schema, ref.Name).
			Scan(&def.Table, &timing, &event, &def.Owner)
		def.Details = facts("Table", def.Table, "Fires", timing+" "+event+", each row", "Definer", def.Owner)
		what, columns = "TRIGGER", []string{"sql original statement"}
	case KindEvent:
		var status, eventType, value, field string
		err = db.QueryRowContext(ctx, `
		  SELECT COALESCE(DEFINER, ''), COALESCE(EVENT_COMMENT, ''), COALESCE(STATUS, ''),
		         COALESCE(EVENT_TYPE, ''), COALESCE(INTERVAL_VALUE, ''), COALESCE(INTERVAL_FIELD, '')
		  FROM information_schema.EVENTS WHERE EVENT_SCHEMA = ? AND EVENT_NAME = ?`, schema, ref.Name).
			Scan(&def.Owner, &def.Comment, &status, &eventType, &value, &field)
		def.Details = facts("Definer", def.Owner, "Schedule", mysqlEventSchedule(eventType, value, field, status))
		what, columns = "EVENT", []string{"create event"}
	case KindSequence:
		var tableType string
		err = db.QueryRowContext(ctx, `
		  SELECT TABLE_TYPE FROM information_schema.TABLES
		  WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? AND TABLE_TYPE = 'SEQUENCE'`, schema, ref.Name).Scan(&tableType)
		what, columns = "SEQUENCE", []string{"create table", "create sequence"}
	default:
		return nil, fmt.Errorf("%w: %s", ErrNoDefinition, ref.Kind)
	}
	if err == sql.ErrNoRows {
		return nil, ErrObjectNotFound
	}
	if err != nil {
		return nil, err
	}
	text, err := mysqlShowCreate(ctx, db, what, rel, columns...)
	switch {
	case err != nil:
		def.Note = "The server would not return this definition: " + err.Error()
	case strings.TrimSpace(text) == "":
		// SHOW CREATE answers NULL for a routine the login may call and may
		// not read.
		def.Note = "The server returned no text: this login may use the object without being allowed to read its definition."
	default:
		def.Definition = text
	}
	return def, nil
}
