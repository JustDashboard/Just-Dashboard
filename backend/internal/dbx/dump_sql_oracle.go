package dbx

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// The built-in dump of an Oracle schema.
//
// Oracle's own dump tool runs on the server and writes to a directory there,
// so this is the only dump the dashboard can take. What it used to write could
// not be loaded back: the indexes behind a primary key were created a second
// time and refused, a table that numbered its own rows refused the numbers,
// and a virtual column refused everything.
//
// The definitions come from DBMS_METADATA, which is the server writing its own
// DDL — every type, every clause — with the storage left out so the file is
// about the schema and not about the disks it was on. What a dump means by
// "the database" is one schema: Oracle's are users, and the file names the one
// it was taken of.

func oracleDumpIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func oracleDumpRel(schema, name string) string {
	return oracleDumpIdent(schema) + "." + oracleDumpIdent(name)
}

func oracleDumpString(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// oracleDumpSession sets how DBMS_METADATA writes a definition for the
// rest of this session: without where the segment is stored, without the
// foreign keys (they go on once the rows are in), and without a terminator.
const oracleDumpSession = `BEGIN
  DBMS_METADATA.SET_TRANSFORM_PARAM(DBMS_METADATA.SESSION_TRANSFORM, 'SEGMENT_ATTRIBUTES', FALSE);
  DBMS_METADATA.SET_TRANSFORM_PARAM(DBMS_METADATA.SESSION_TRANSFORM, 'REF_CONSTRAINTS', FALSE);
  DBMS_METADATA.SET_TRANSFORM_PARAM(DBMS_METADATA.SESSION_TRANSFORM, 'SQLTERMINATOR', FALSE);
END;`

type oracleDumpIdentity struct {
	column     string
	generation string // ALWAYS, BY DEFAULT, BY DEFAULT ON NULL
	last       string
}

func planOracleDump(ctx context.Context, q dumpQueryer, schema string, sel dumpSelection) (*dumpPlan, error) {
	if schema == "" {
		return nil, fmt.Errorf("cannot tell which schema this session is in")
	}
	// SYS, SYSTEM and the rest are the server's own. Their tables are the
	// server's working parts: a file of them is not a backup of anything, and
	// loading it back begins by dropping them. An administrator's login is in
	// one of those schemas unless told which schema to dump.
	var maintained string
	if err := q.QueryRowContext(ctx, `SELECT oracle_maintained FROM all_users WHERE username = :1`, schema).Scan(&maintained); err == nil && maintained == "Y" {
		return nil, fmt.Errorf("%s is one of Oracle's own schemas, which a dump must not replace; name the schema to dump, or connect as its owner", schema)
	}
	if _, err := q.ExecContext(ctx, oracleDumpSession); err != nil {
		return nil, fmt.Errorf("this login cannot read definitions through DBMS_METADATA: %w", err)
	}
	plan := &dumpPlan{notes: []string{
		"not included: procedures, functions, packages, triggers, synonyms, materialized views, grants",
	}}
	ddl := func(kind, name string) (string, error) {
		var text string
		err := q.QueryRowContext(ctx, `SELECT DBMS_METADATA.GET_DDL(:1, :2, :3) FROM dual`, kind, name, schema).Scan(&text)
		return strings.TrimSpace(text), err
	}

	// What is listed as a table and is not one to dump as one: the storage
	// behind a materialized view or its log, a nested table's, an index's own,
	// and — on the versions that list it — the copy a dropped table leaves in
	// the recycle bin.
	rows, err := q.QueryContext(ctx, `
	  SELECT t.table_name, t.temporary,
	         CASE WHEN EXISTS (SELECT 1 FROM all_external_tables e
	                           WHERE e.owner = t.owner AND e.table_name = t.table_name) THEN 'Y' ELSE 'N' END
	  FROM all_tables t
	  WHERE t.owner = :1 AND t.dropped = 'NO' AND t.nested = 'NO' AND t.secondary = 'N'
	    AND (t.iot_type IS NULL OR t.iot_type = 'IOT')
	    AND NOT EXISTS (SELECT 1 FROM all_mviews m WHERE m.owner = t.owner AND m.mview_name = t.table_name)
	    AND NOT EXISTS (SELECT 1 FROM all_mview_logs l WHERE l.log_owner = t.owner AND l.log_table = t.table_name)
	  ORDER BY t.table_name`, schema)
	if err != nil {
		return nil, fmt.Errorf("cannot list tables: %w", err)
	}
	type entry struct {
		name   string
		noRows bool
	}
	var entries []entry
	for rows.Next() {
		var name, temporary, external string
		if err := rows.Scan(&name, &temporary, &external); err != nil {
			rows.Close()
			return nil, err
		}
		// A temporary table's rows are each session's own, and an external
		// table's are a file's.
		entries = append(entries, entry{name: name, noRows: temporary == "Y" || external == "Y"})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	columns, documents, err := oracleDumpColumns(ctx, q, schema)
	if err != nil {
		return nil, fmt.Errorf("cannot read the columns: %w", err)
	}
	identities := oracleDumpIdentities(ctx, q, schema)

	wanted := map[string]bool{}
	var definitions []string
	for _, e := range entries {
		if !sel.wants(schema, e.name) {
			continue
		}
		rel := oracleDumpRel(schema, e.name)
		create, err := ddl("TABLE", e.name)
		if err != nil || create == "" {
			reason := "it has no definition this login can read"
			if err != nil {
				reason = err.Error()
			}
			plan.skipped = append(plan.skipped, rel+": "+reason)
			continue
		}
		wanted[e.name] = true
		definitions = append(definitions, strings.ToLower(create))
		table := dumpTable{
			table: Table{Schema: schema, Name: e.name}, detail: &TableDetail{}, rel: rel,
			create: rawStmt(create),
			// PURGE, or every restore leaves a whole copy of the table it
			// replaced in the recycle bin, counted against the same quota the
			// restored rows need.
			drop:      stmt("DROP TABLE " + rel + " CASCADE CONSTRAINTS PURGE"),
			selectSQL: "SELECT " + strings.Join(columns[e.name], ", ") + " FROM " + rel,
			noData:    e.noRows || len(columns[e.name]) == 0,
			documents: documents[e.name],
		}
		if id, ok := identities[e.name]; ok {
			column := oracleDumpIdent(id.column)
			position := "START WITH LIMIT VALUE"
			if numberText.MatchString(id.last) {
				position = "START WITH " + id.last
			}
			if id.generation == "ALWAYS" {
				// A column the table always numbers itself refuses a number
				// given to it, and there is no way to say "this once". It is
				// loosened for the rows and tightened again after them.
				table.beforeData = []dumpStatement{stmt(fmt.Sprintf(
					"ALTER TABLE %s MODIFY (%s GENERATED BY DEFAULT AS IDENTITY)", rel, column))}
			}
			table.afterData = []dumpStatement{stmt(fmt.Sprintf(
				"ALTER TABLE %s MODIFY (%s GENERATED %s AS IDENTITY (%s))", rel, column, id.generation, position))}
		}
		plan.tables = append(plan.tables, table)
	}

	if err := oracleDumpIndexes(ctx, q, plan, schema, wanted, ddl); err != nil {
		plan.skipped = append(plan.skipped, "indexes: "+err.Error())
	}
	if err := oracleDumpComments(ctx, q, plan, schema, wanted); err != nil {
		plan.skipped = append(plan.skipped, "comments: "+err.Error())
	}
	if err := oracleDumpForeignKeys(ctx, q, plan, schema, wanted, ddl); err != nil {
		plan.skipped = append(plan.skipped, "foreign keys: "+err.Error())
	}
	plan.tables = orderByDependency(plan.tables)
	if err := oracleDumpSequences(ctx, q, plan, schema, sel, definitions, ddl); err != nil {
		plan.skipped = append(plan.skipped, "sequences: "+err.Error())
	}
	if err := oracleDumpViews(ctx, q, plan, schema, sel, ddl); err != nil {
		plan.skipped = append(plan.skipped, "views: "+err.Error())
	}
	return plan, nil
}

// oracleDumpColumns lists each table's columns that can be given a
// value, as they are selected: not the virtual ones, which the server
// computes, and not the ones it keeps for itself behind a function-based
// index.
//
// An XMLTYPE column is listed as virtual — its document is stored in a hidden
// column beside it — and is a column with a value all the same, so it is kept:
// left out with the virtual ones, every document in the table was missing from
// the dump with nothing said. It is read as the text it was written as
// (oracleXMLText), because the driver cannot read the type itself: a document
// comes back laid out afresh and an empty cell fails the statement.
func oracleDumpColumns(ctx context.Context, q dumpQueryer, schema string) (selected map[string][]string, documents map[string]map[string]bool, err error) {
	rows, err := q.QueryContext(ctx, `
	  SELECT table_name, column_name, data_type
	  FROM all_tab_cols
	  WHERE owner = :1 AND user_generated = 'YES'
	    AND (virtual_column = 'NO' OR data_type = 'XMLTYPE')
	  ORDER BY table_name, internal_column_id`, schema)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	selected, documents = map[string][]string{}, map[string]map[string]bool{}
	for rows.Next() {
		var table, column, dataType string
		if err := rows.Scan(&table, &column, nullText{&dataType}); err != nil {
			return nil, nil, err
		}
		quoted := oracleDumpIdent(column)
		expr := quoted
		if dataType == "XMLTYPE" {
			expr = oracleXMLText(quoted) + " AS " + quoted
			if documents[table] == nil {
				documents[table] = map[string]bool{}
			}
			documents[table][quoted] = true
		}
		selected[table] = append(selected[table], expr)
	}
	return selected, documents, rows.Err()
}

// oracleDumpIdentities reads which column of which table numbers itself, and
// where its counter has got to. A server from before identity columns has no
// such view, and no such columns.
func oracleDumpIdentities(ctx context.Context, q dumpQueryer, schema string) map[string]oracleDumpIdentity {
	out := map[string]oracleDumpIdentity{}
	rows, err := q.QueryContext(ctx, `
	  SELECT i.table_name, i.column_name, i.generation_type, c.default_on_null, TO_CHAR(s.last_number)
	  FROM all_tab_identity_cols i
	  JOIN all_tab_columns c
	    ON c.owner = i.owner AND c.table_name = i.table_name AND c.column_name = i.column_name
	  LEFT JOIN all_sequences s
	    ON s.sequence_owner = i.owner AND s.sequence_name = i.sequence_name
	  WHERE i.owner = :1`, schema)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var table, column, generation, onNull, last string
		if err := rows.Scan(&table, &column, &generation, nullText{&onNull}, nullText{&last}); err != nil {
			return out
		}
		id := oracleDumpIdentity{column: column, generation: "BY DEFAULT", last: last}
		switch {
		case generation == "ALWAYS":
			id.generation = "ALWAYS"
		case onNull == "YES":
			id.generation = "BY DEFAULT ON NULL"
		}
		out[table] = id
	}
	return out
}

// oracleDumpIndexes writes the indexes that are objects of their own. One that
// stands behind a primary key or a unique constraint is made by the
// constraint, in the table's own definition; made again it is refused.
func oracleDumpIndexes(ctx context.Context, q dumpQueryer, plan *dumpPlan, schema string, wanted map[string]bool, ddl func(kind, name string) (string, error)) error {
	rows, err := q.QueryContext(ctx, `
	  SELECT i.table_name, i.index_name
	  FROM all_indexes i
	  WHERE i.table_owner = :1 AND i.owner = :1 AND i.dropped = 'NO'
	    AND i.index_type NOT IN ('LOB', 'IOT - TOP')
	    AND NOT EXISTS (SELECT 1 FROM all_constraints c
	                    WHERE c.owner = i.table_owner AND c.table_name = i.table_name
	                      AND c.index_name = i.index_name AND c.constraint_type IN ('P', 'U'))
	  ORDER BY i.table_name, i.index_name`, schema)
	if err != nil {
		return err
	}
	var names []string
	for rows.Next() {
		var table, name string
		if err := rows.Scan(&table, &name); err != nil {
			rows.Close()
			return err
		}
		if wanted[table] {
			names = append(names, name)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, name := range names {
		create, err := ddl("INDEX", name)
		if err != nil {
			plan.skipped = append(plan.skipped, fmt.Sprintf("index %s: %s", name, err.Error()))
			continue
		}
		plan.after = append(plan.after, rawStmt(create))
	}
	return nil
}

func oracleDumpComments(ctx context.Context, q dumpQueryer, plan *dumpPlan, schema string, wanted map[string]bool) error {
	rows, err := q.QueryContext(ctx, `
	  SELECT table_name, comments FROM all_tab_comments
	  WHERE owner = :1 AND table_type = 'TABLE' AND comments IS NOT NULL
	  ORDER BY table_name`, schema)
	if err != nil {
		return err
	}
	for rows.Next() {
		var table, comment string
		if err := rows.Scan(&table, &comment); err != nil {
			rows.Close()
			return err
		}
		if wanted[table] {
			plan.after = append(plan.after, stmt(fmt.Sprintf("COMMENT ON TABLE %s IS %s",
				oracleDumpRel(schema, table), oracleDumpString(comment))))
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	rows, err = q.QueryContext(ctx, `
	  SELECT table_name, column_name, comments FROM all_col_comments
	  WHERE owner = :1 AND comments IS NOT NULL
	  ORDER BY table_name, column_name`, schema)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var table, column, comment string
		if err := rows.Scan(&table, &column, &comment); err != nil {
			return err
		}
		if wanted[table] {
			plan.after = append(plan.after, stmt(fmt.Sprintf("COMMENT ON COLUMN %s.%s IS %s",
				oracleDumpRel(schema, table), oracleDumpIdent(column), oracleDumpString(comment))))
		}
	}
	return rows.Err()
}

// oracleDumpForeignKeys writes the foreign keys last, once every row they cover
// is in. A key on a table this dump leaves alone that points at one it holds
// goes when that one is dropped, so it is put back too.
func oracleDumpForeignKeys(ctx context.Context, q dumpQueryer, plan *dumpPlan, schema string, wanted map[string]bool, ddl func(kind, name string) (string, error)) error {
	rows, err := q.QueryContext(ctx, `
	  SELECT c.table_name, c.constraint_name, r.owner, r.table_name
	  FROM all_constraints c
	  JOIN all_constraints r ON r.owner = c.r_owner AND r.constraint_name = c.r_constraint_name
	  WHERE c.owner = :1 AND c.constraint_type = 'R'
	  ORDER BY c.table_name, c.constraint_name`, schema)
	if err != nil {
		return err
	}
	type key struct{ table, name, refOwner, refTable string }
	var keys []key
	for rows.Next() {
		var k key
		if err := rows.Scan(&k.table, &k.name, &k.refOwner, &k.refTable); err != nil {
			rows.Close()
			return err
		}
		keys = append(keys, k)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	parents := map[string][]string{}
	for _, k := range keys {
		pointsIn := k.refOwner == schema && wanted[k.refTable]
		if !wanted[k.table] && !pointsIn {
			continue
		}
		add, err := ddl("REF_CONSTRAINT", k.name)
		if err != nil {
			plan.skipped = append(plan.skipped, fmt.Sprintf("foreign key %s: %s", k.name, err.Error()))
			continue
		}
		if !wanted[k.table] {
			// -942 is "table or view does not exist": the table the key
			// belongs to has gone since, and there is nothing to put it on.
			add = "BEGIN\n  EXECUTE IMMEDIATE " + oracleDumpString(add) +
				";\nEXCEPTION WHEN OTHERS THEN\n  IF SQLCODE <> -942 THEN RAISE; END IF;\nEND;"
		} else if pointsIn && k.refTable != k.table {
			parents[k.table] = append(parents[k.table], k.refTable)
		}
		plan.after = append(plan.after, rawStmt(add))
	}
	for i := range plan.tables {
		plan.tables[i].parents = parents[plan.tables[i].table.Name]
	}
	return nil
}

// oracleDumpSequences writes the sequences that are objects of their own. The one
// behind an identity column is made by the column.
func oracleDumpSequences(ctx context.Context, q dumpQueryer, plan *dumpPlan, schema string, sel dumpSelection, definitions []string, ddl func(kind, name string) (string, error)) error {
	rows, err := q.QueryContext(ctx, `
	  SELECT s.sequence_name
	  FROM all_sequences s
	  WHERE s.sequence_owner = :1
	    AND NOT EXISTS (SELECT 1 FROM all_tab_identity_cols i
	                    WHERE i.owner = s.sequence_owner AND i.sequence_name = s.sequence_name)
	  ORDER BY s.sequence_name`, schema)
	if err != nil {
		// No identity columns on this server, so none to leave out.
		rows, err = q.QueryContext(ctx, `
		  SELECT sequence_name FROM all_sequences WHERE sequence_owner = :1 ORDER BY sequence_name`, schema)
		if err != nil {
			return err
		}
	}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		names = append(names, name)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, name := range names {
		if !sel.wants(schema, name) {
			continue
		}
		if sel.narrowed() && !sel.named(schema, name) {
			// Nobody's in particular, and the dump is of particular tables —
			// unless one of them draws its default from it.
			drawn := false
			for _, d := range definitions {
				drawn = drawn || containsIdentifier(d, strings.ToLower(name))
			}
			if !drawn {
				continue
			}
		}
		// The definition starts the sequence past every value it has handed
		// out, so its position travels with it.
		create, err := ddl("SEQUENCE", name)
		if err != nil {
			plan.skipped = append(plan.skipped, fmt.Sprintf("sequence %s: %s", name, err.Error()))
			continue
		}
		rel := oracleDumpRel(schema, name)
		plan.sequences = append(plan.sequences, dumpObject{
			rel: rel, name: name, schema: schema, drop: stmt("DROP SEQUENCE " + rel), create: rawStmt(create),
		})
	}
	return nil
}

// oracleDumpViews writes each view after the views it reads.
func oracleDumpViews(ctx context.Context, q dumpQueryer, plan *dumpPlan, schema string, sel dumpSelection, ddl func(kind, name string) (string, error)) error {
	rows, err := q.QueryContext(ctx, `SELECT view_name FROM all_views WHERE owner = :1 ORDER BY view_name`, schema)
	if err != nil {
		return err
	}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		names = append(names, name)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	deps := map[string][]string{}
	if rows, err := q.QueryContext(ctx, `
	  SELECT name, referenced_name FROM all_dependencies
	  WHERE owner = :1 AND type = 'VIEW' AND referenced_owner = :1 AND referenced_type = 'VIEW'
	  ORDER BY name, referenced_name`, schema); err == nil {
		for rows.Next() {
			var view, ref string
			if err := rows.Scan(&view, &ref); err == nil {
				deps[view] = append(deps[view], ref)
			}
		}
		rows.Close()
	}
	views := []dumpObject{}
	for _, name := range names {
		if !sel.wants(schema, name) || (sel.narrowed() && !sel.named(schema, name)) {
			continue
		}
		rel := oracleDumpRel(schema, name)
		create, err := ddl("VIEW", name)
		if err != nil || create == "" {
			reason := "its definition is not readable by this login"
			if err != nil {
				reason = err.Error()
			}
			plan.skipped = append(plan.skipped, fmt.Sprintf("view %s: %s", rel, reason))
			continue
		}
		views = append(views, dumpObject{
			rel: rel, name: name, schema: schema, drop: stmt("DROP VIEW " + rel), create: rawStmt(create), refs: deps[name],
		})
	}
	plan.views = orderObjects(views)
	return nil
}

// --- rows -----------------------------------------------------------------

const (
	// oracleDumpLiteralBytes is the most bytes HEXTORAW can be given as a
	// literal in a statement: four thousand characters of hex.
	oracleDumpLiteralBytes = 2000
	// Inside a block a literal may be eight times that. These keep each piece
	// under it whatever the characters are.
	oracleDumpBlockBytes = 16000
	oracleDumpBlockRunes = 8000
)

// oracleDumpLongRow writes a row that holds a value too long for a literal as a
// block that builds the value in pieces and inserts the row from them.
//
// A literal in a statement stops at four thousand bytes. Text past that used
// to be written as pieces joined in the VALUES list, which the server takes
// seconds over at a megabyte and does not survive at six; bytes past it could
// not be written at all, so a table with a picture in it restored up to the
// picture. It reports false for a row every value of which fits a literal.
// document marks the columns whose text goes back through XMLTYPE's
// constructor (oracleDumpDocument).
func oracleDumpLongRow(rel string, cols []string, vals []any, binary []bool, types []string, document []bool) (string, bool) {
	parts := make([]string, len(vals))
	var declare, build, free strings.Builder
	long := 0
	for i, v := range vals {
		var (
			raw    []byte
			text   string
			isText bool
		)
		switch x := v.(type) {
		case []byte:
			if binary[i] {
				raw = x
			} else {
				text, isText = string(x), true
			}
		case string:
			text, isText = x, true
		}
		if isText && (strings.ContainsRune(text, 0) || !utf8.ValidString(text)) {
			// Not text any literal can carry; it goes as the bytes it is.
			raw, isText = []byte(text), false
		}
		switch {
		case isText && utf8.RuneCountInString(text) > oracleLiteralChars:
			long++
			name := fmt.Sprintf("v%d", i+1)
			fmt.Fprintf(&declare, "  %s CLOB;\n", name)
			fmt.Fprintf(&build, "  DBMS_LOB.CREATETEMPORARY(%s, TRUE);\n", name)
			runes := []rune(text)
			for len(runes) > 0 {
				n := min(oracleDumpBlockRunes, len(runes))
				fmt.Fprintf(&build, "  DBMS_LOB.APPEND(%s, TO_CLOB(%s));\n", name, oracleDumpString(string(runes[:n])))
				runes = runes[n:]
			}
			fmt.Fprintf(&free, "  DBMS_LOB.FREETEMPORARY(%s);\n", name)
			parts[i] = oracleDumpDocument(name, v, document, i)
		case !isText && len(raw) > oracleDumpLiteralBytes:
			long++
			name := fmt.Sprintf("v%d", i+1)
			fmt.Fprintf(&declare, "  %s BLOB;\n", name)
			fmt.Fprintf(&build, "  DBMS_LOB.CREATETEMPORARY(%s, TRUE);\n", name)
			for len(raw) > 0 {
				n := min(oracleDumpBlockBytes, len(raw))
				// Through a variable: HEXTORAW of a long literal is worked
				// out when the block is compiled, and takes seconds a piece.
				fmt.Fprintf(&build, "  s := '%s';\n  DBMS_LOB.WRITEAPPEND(%s, %d, HEXTORAW(s));\n",
					strings.ToUpper(bytesToHex(raw[:n])), name, n)
				raw = raw[n:]
			}
			fmt.Fprintf(&free, "  DBMS_LOB.FREETEMPORARY(%s);\n", name)
			parts[i] = name
		default:
			parts[i] = oracleDumpDocument(dumpColumnValue(DriverOracle, v, binary[i], types[i]), v, document, i)
		}
	}
	if long == 0 {
		return "", false
	}
	return "DECLARE\n  s VARCHAR2(32767);\n" + declare.String() + "BEGIN\n" + build.String() +
		"  INSERT INTO " + rel + " (" + strings.Join(cols, ", ") + ") VALUES (" + strings.Join(parts, ", ") + ");\n" +
		free.String() + "END;", true
}

// oracleDumpDocument writes a value of an XMLTYPE column as the type's own
// constructor over its text. A string short enough for a literal is taken for
// the column as it is, and a CLOB — which is what a longer document has to be
// built as — is refused there; written the one way, both go in. A NULL stays
// a NULL: the constructor refuses one.
func oracleDumpDocument(expr string, value any, document []bool, i int) string {
	if value == nil || i >= len(document) || !document[i] {
		return expr
	}
	return "XMLTYPE(" + expr + ")"
}

var oracleDumpBlockStart = regexp.MustCompile(`(?i)^(DECLARE|BEGIN)\s`)

// oracleDumpIsBlock reports whether a statement is a PL/SQL block rather than SQL.
func oracleDumpIsBlock(statement string) bool {
	return oracleDumpBlockStart.MatchString(strings.TrimSpace(statement) + " ")
}

// --- literals -------------------------------------------------------------

// oracleDumpNumberText is a number as Oracle's driver hands one back: exact
// digits, with or without anything before the point.
var oracleDumpNumberText = regexp.MustCompile(`^-?([0-9]+\.?[0-9]*|\.[0-9]+)([eE][+-]?[0-9]+)?$`)

// oracleDumpTime writes an instant as the column's type reads one.
//
// Oracle will not read a string as a date without being told the format, and
// its NLS settings are per session — so the format travels with the value
// rather than being assumed. A column that keeps a zone is written in the
// value's own, which is part of what it holds; the others are the wall-clock
// time the driver read.
func oracleDumpTime(t time.Time, typeName string) string {
	upper := strings.ToUpper(typeName)
	switch {
	case upper == "DATE":
		return "TO_DATE('" + t.Format("2006-01-02 15:04:05") + "', 'YYYY-MM-DD HH24:MI:SS')"
	case strings.Contains(upper, "TZ") || strings.Contains(upper, "ZONE"):
		return "TO_TIMESTAMP_TZ('" + t.Format("2006-01-02 15:04:05.000000000 -07:00") +
			"', 'YYYY-MM-DD HH24:MI:SS.FF9 TZH:TZM')"
	}
	return "TO_TIMESTAMP('" + t.UTC().Format("2006-01-02 15:04:05.000000000") + "', 'YYYY-MM-DD HH24:MI:SS.FF9')"
}
