package dbx

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
)

// Oracle answers from the ALL_ views, which show what the login may see
// rather than only what it owns and need no grant beyond the session. A
// schema is a user. DBMS_METADATA produces exact DDL where the login may call
// it on the object; where it may not, the source views (ALL_SOURCE,
// ALL_VIEWS) still hold the text and the definition is assembled from them.
//
// Every optional text column is scanned through nullText: Oracle has no empty
// string, so the NVL(col, '') that guards a NULL elsewhere is a no-op here.

// oracleSystemOwners are the schemas Oracle ships. ORACLE_MAINTAINED would say
// so exactly, and is a column the oldest servers still in use do not have.
const oracleSystemOwners = `'SYS','SYSTEM','OUTLN','XDB','MDSYS','CTXSYS','DBSNMP','PUBLIC','WMSYS','ORDSYS',
	'ORDDATA','OLAPSYS','LBACSYS','DVSYS','GSMADMIN_INTERNAL','APPQOSSYS','AUDSYS','OJVMSYS','DBSFWUSER',
	'REMOTE_SCHEDULER_AGENT','ORDPLUGINS','SI_INFORMTN_SCHEMA','ANONYMOUS','GGSYS','DIP','ORACLE_OCM'`

// oracleSchemaFilter narrows to one owner, or to every owner that is not one
// of Oracle's own when the bound value is NULL. It reuses bind :1, and every
// query here numbers its binds in the order they first appear: the driver
// sends arguments by position, and the server matches a position to the next
// name it has not yet seen.
func oracleSchemaFilter(column string) string {
	return `((:1 IS NULL AND ` + column + ` NOT IN (` + oracleSystemOwners + `)) OR ` + column + ` = :1)`
}

// oracleSchemaIs matches one owner, the session's current schema when none is
// named.
func oracleSchemaIs(column string) string {
	return column + ` = NVL(:1, SYS_CONTEXT('USERENV','CURRENT_SCHEMA'))`
}

func (oracleDialect) catalogSchemas(ctx context.Context, db *sql.DB) ([]CatalogSchema, string, error) {
	var current string
	if err := db.QueryRowContext(ctx,
		`SELECT SYS_CONTEXT('USERENV','CURRENT_SCHEMA') FROM dual`).Scan(nullText{&current}); err != nil {
		return nil, "", err
	}
	rows, err := db.QueryContext(ctx, `
	  SELECT u.username, CASE WHEN u.oracle_maintained = 'Y' THEN 1 ELSE 0 END
	  FROM all_users u ORDER BY 2, 1`)
	if err != nil {
		// oracle_maintained arrived in 12c. Without it the shipped schemas are
		// recognised by name.
		rows, err = db.QueryContext(ctx, `
		  SELECT u.username, CASE WHEN u.username IN (`+oracleSystemOwners+`) THEN 1 ELSE 0 END
		  FROM all_users u ORDER BY 2, 1`)
		if err != nil {
			return nil, "", err
		}
	}
	defer rows.Close()
	out := []CatalogSchema{}
	for rows.Next() {
		// Counting each user's tables is a pass over ALL_TABLES per row, so the
		// count is reported as unknown rather than making the list slow.
		s := CatalogSchema{Tables: -1}
		var system int
		if err := rows.Scan(&s.Name, &system); err != nil {
			return nil, "", err
		}
		s.System, s.Default = system == 1, s.Name == current
		out = append(out, s)
	}
	return out, current, rows.Err()
}

func (d oracleDialect) catalogGroups(context.Context, *sql.DB) []catalogGroup {
	objects := func(kind, objectType string) func(context.Context, *sql.DB, string, int) ([]CatalogObject, error) {
		return func(ctx context.Context, db *sql.DB, schema string, limit int) ([]CatalogObject, error) {
			return d.objects(ctx, db, schema, limit, kind, objectType)
		}
	}
	return []catalogGroup{
		{GroupTables, d.tables},
		{GroupViews, d.views},
		{GroupMaterializedViews, d.materializedViews},
		{GroupFunctions, objects(KindFunction, "FUNCTION")},
		{GroupProcedures, objects(KindProcedure, "PROCEDURE")},
		{GroupPackages, objects(KindPackage, "PACKAGE")},
		{GroupTriggers, d.triggers},
		{GroupSequences, d.sequences},
		{GroupTypes, objects(KindType, "TYPE")},
		{GroupSynonyms, d.synonyms},
	}
}

func (oracleDialect) tables(ctx context.Context, db *sql.DB, schema string, limit int) ([]CatalogObject, error) {
	// A materialized view's container is a row in ALL_TABLES too, and a
	// dropped table lingers in the recycle bin under a BIN$ name; neither is a
	// table anyone created.
	return scanObjects(ctx, db, `
	  SELECT t.owner, t.table_name, NVL(t.num_rows, -1), c.comments, t.partitioned, t.temporary
	  FROM all_tables t
	  LEFT JOIN all_tab_comments c
	    ON c.owner = t.owner AND c.table_name = t.table_name AND c.table_type = 'TABLE'
	  WHERE `+oracleSchemaFilter("t.owner")+`
	    AND t.dropped = 'NO' AND t.nested = 'NO'
	    AND NOT EXISTS (SELECT 1 FROM all_mviews m WHERE m.owner = t.owner AND m.mview_name = t.table_name)
	  ORDER BY t.owner, t.table_name
	  FETCH FIRST :2 ROWS ONLY`, []any{oracleSchemaArg(schema), limit}, func(rows *sql.Rows) (CatalogObject, error) {
		o := CatalogObject{Kind: KindTable}
		var estimate int64
		var partitioned, temporary string
		if err := rows.Scan(&o.Schema, &o.Name, &estimate, nullText{&o.Comment},
			nullText{&partitioned}, nullText{&temporary}); err != nil {
			return o, err
		}
		o.Rows = rowEstimate(estimate)
		switch {
		case partitioned == "YES":
			o.Detail = TableTypePartitioned
		case temporary == "Y":
			o.Detail = "temporary"
		}
		return o, nil
	})
}

func (oracleDialect) views(ctx context.Context, db *sql.DB, schema string, limit int) ([]CatalogObject, error) {
	return scanObjects(ctx, db, `
	  SELECT v.owner, v.view_name, c.comments
	  FROM all_views v
	  LEFT JOIN all_tab_comments c
	    ON c.owner = v.owner AND c.table_name = v.view_name AND c.table_type = 'VIEW'
	  WHERE `+oracleSchemaFilter("v.owner")+`
	  ORDER BY v.owner, v.view_name
	  FETCH FIRST :2 ROWS ONLY`, []any{oracleSchemaArg(schema), limit}, func(rows *sql.Rows) (CatalogObject, error) {
		o := CatalogObject{Kind: KindView}
		return o, rows.Scan(&o.Schema, &o.Name, nullText{&o.Comment})
	})
}

func (oracleDialect) materializedViews(ctx context.Context, db *sql.DB, schema string, limit int) ([]CatalogObject, error) {
	return scanObjects(ctx, db, `
	  SELECT m.owner, m.mview_name, m.refresh_mode, m.refresh_method, m.staleness
	  FROM all_mviews m
	  WHERE `+oracleSchemaFilter("m.owner")+`
	  ORDER BY m.owner, m.mview_name
	  FETCH FIRST :2 ROWS ONLY`, []any{oracleSchemaArg(schema), limit}, func(rows *sql.Rows) (CatalogObject, error) {
		o := CatalogObject{Kind: KindMaterializedView}
		var mode, method, staleness string
		if err := rows.Scan(&o.Schema, &o.Name, nullText{&mode}, nullText{&method}, nullText{&staleness}); err != nil {
			return o, err
		}
		o.Detail = strings.ToLower(strings.TrimSpace("refresh " + method + " on " + mode + ", " + staleness))
		return o, nil
	})
}

// objects lists one kind of code object from ALL_OBJECTS. objectType is a
// constant written by the caller above, bound rather than concatenated.
func (oracleDialect) objects(ctx context.Context, db *sql.DB, schema string, limit int, kind, objectType string) ([]CatalogObject, error) {
	return scanObjects(ctx, db, `
	  SELECT o.owner, o.object_name, o.status
	  FROM all_objects o
	  WHERE `+oracleSchemaFilter("o.owner")+` AND o.object_type = :2
	  ORDER BY o.owner, o.object_name
	  FETCH FIRST :3 ROWS ONLY`, []any{oracleSchemaArg(schema), objectType, limit}, func(rows *sql.Rows) (CatalogObject, error) {
		o := CatalogObject{Kind: kind}
		var status string
		if err := rows.Scan(&o.Schema, &o.Name, nullText{&status}); err != nil {
			return o, err
		}
		if kind != KindType {
			o.Language = "PL/SQL"
		}
		if status != "" && status != "VALID" {
			// An object that no longer compiles is still there, and is the one
			// the operator came looking for.
			o.Detail = strings.ToLower(status)
		}
		return o, nil
	})
}

func (oracleDialect) triggers(ctx context.Context, db *sql.DB, schema string, limit int) ([]CatalogObject, error) {
	return scanObjects(ctx, db, `
	  SELECT t.owner, t.trigger_name, t.table_name, t.trigger_type, t.triggering_event, t.status
	  FROM all_triggers t
	  WHERE `+oracleSchemaFilter("t.owner")+`
	  ORDER BY t.owner, t.table_name, t.trigger_name
	  FETCH FIRST :2 ROWS ONLY`, []any{oracleSchemaArg(schema), limit}, func(rows *sql.Rows) (CatalogObject, error) {
		o := CatalogObject{Kind: KindTrigger}
		var triggerType, event, status string
		if err := rows.Scan(&o.Schema, &o.Name, nullText{&o.Table}, nullText{&triggerType},
			nullText{&event}, nullText{&status}); err != nil {
			return o, err
		}
		o.Detail = oracleTriggerTiming(triggerType, event, status)
		return o, nil
	})
}

// oracleTriggerTiming joins Oracle's two columns — "BEFORE EACH ROW" and
// "INSERT OR UPDATE" — into the order every other engine's reads.
func oracleTriggerTiming(triggerType, event, status string) string {
	timing, scope := triggerType, "each statement"
	if before, ok := strings.CutSuffix(triggerType, " EACH ROW"); ok {
		timing, scope = before, "each row"
	} else if before, ok := strings.CutSuffix(triggerType, " STATEMENT"); ok {
		timing = before
	}
	out := strings.TrimSpace(timing+" "+strings.TrimSpace(event)) + ", " + scope
	if status == "DISABLED" {
		out += ", disabled"
	}
	return out
}

func (oracleDialect) sequences(ctx context.Context, db *sql.DB, schema string, limit int) ([]CatalogObject, error) {
	return scanObjects(ctx, db, `
	  SELECT s.sequence_owner, s.sequence_name, s.increment_by
	  FROM all_sequences s
	  WHERE `+oracleSchemaFilter("s.sequence_owner")+`
	  ORDER BY s.sequence_owner, s.sequence_name
	  FETCH FIRST :2 ROWS ONLY`, []any{oracleSchemaArg(schema), limit}, func(rows *sql.Rows) (CatalogObject, error) {
		o := CatalogObject{Kind: KindSequence}
		var increment string
		if err := rows.Scan(&o.Schema, &o.Name, nullText{&increment}); err != nil {
			return o, err
		}
		o.Detail = "step " + increment
		return o, nil
	})
}

func (oracleDialect) synonyms(ctx context.Context, db *sql.DB, schema string, limit int) ([]CatalogObject, error) {
	return scanObjects(ctx, db, `
	  SELECT s.owner, s.synonym_name, s.table_owner, s.table_name, s.db_link
	  FROM all_synonyms s
	  WHERE `+oracleSchemaFilter("s.owner")+`
	  ORDER BY s.owner, s.synonym_name
	  FETCH FIRST :2 ROWS ONLY`, []any{oracleSchemaArg(schema), limit}, func(rows *sql.Rows) (CatalogObject, error) {
		o := CatalogObject{Kind: KindSynonym}
		var owner, name, link string
		if err := rows.Scan(&o.Schema, &o.Name, nullText{&owner}, nullText{&name}, nullText{&link}); err != nil {
			return o, err
		}
		o.Detail = oracleSynonymTarget(owner, name, link)
		return o, nil
	})
}

func oracleSynonymTarget(owner, name, link string) string {
	target := name
	if owner != "" {
		target = owner + "." + name
	}
	if link != "" {
		target += "@" + link
	}
	return target
}

// --- one table --------------------------------------------------------------

func (oracleDialect) tableColumns(ctx context.Context, db *sql.DB, schema, table string) ([]Column, error) {
	// char_length rather than data_length for the character types: a
	// VARCHAR2(255 CHAR) in a UTF-8 database has a data_length of 1020, and
	// shown that way it reads as a column four times wider than it is.
	// data_default is a LONG and is selected bare — wrapping it in any
	// function is ORA-00932.
	rows, err := db.QueryContext(ctx, `
	  SELECT c.column_name,
	         c.data_type ||
	           CASE WHEN c.data_type IN ('VARCHAR2','NVARCHAR2','CHAR','NCHAR')
	                THEN '(' || c.char_length || ')'
	                WHEN c.data_type = 'RAW' THEN '(' || c.data_length || ')'
	                WHEN c.data_type = 'NUMBER' AND c.data_precision IS NOT NULL
	                THEN '(' || c.data_precision || ',' || NVL(c.data_scale,0) || ')'
	                ELSE '' END,
	         c.nullable, c.data_default, c.column_id, c.identity_column, c.virtual_column,
	         cm.comments, ic.generation_type
	  FROM all_tab_cols c
	  LEFT JOIN all_col_comments cm
	    ON cm.owner = c.owner AND cm.table_name = c.table_name AND cm.column_name = c.column_name
	  LEFT JOIN all_tab_identity_cols ic
	    ON ic.owner = c.owner AND ic.table_name = c.table_name AND ic.column_name = c.column_name
	  WHERE `+oracleSchemaIs("c.owner")+` AND c.table_name = :2 AND c.hidden_column = 'NO'
	  ORDER BY c.column_id`, oracleSchemaArg(schema), table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Column{}
	for rows.Next() {
		var c Column
		var nullable, expr, identity, virtual, generation string
		if err := rows.Scan(&c.Name, &c.Type, &nullable, nullText{&expr}, &c.Position,
			nullText{&identity}, nullText{&virtual}, nullText{&c.Comment}, nullText{&generation}); err != nil {
			return nil, err
		}
		c.Nullable = nullable == "Y"
		expr = strings.TrimSpace(expr)
		switch {
		case virtual == "YES":
			c.Generated, c.GeneratedKind = expr, "virtual"
		case identity == "YES":
			// The default of an identity column is the sequence Oracle made for
			// it, which is the identity said a second time.
			c.Identity = strings.ToLower(generation)
			if c.Identity == "" {
				c.Identity = "always"
			}
		default:
			c.Default = expr
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (d oracleDialect) tableIndexes(ctx context.Context, db *sql.DB, schema, table string) ([]Index, error) {
	rows, err := db.QueryContext(ctx, `
	  SELECT i.owner, i.index_name, i.uniqueness, i.index_type, i.status, ic.column_name, ic.descend
	  FROM all_indexes i
	  JOIN all_ind_columns ic ON ic.index_name = i.index_name AND ic.index_owner = i.owner
	  WHERE `+oracleSchemaIs("i.table_owner")+` AND i.table_name = :2
	  ORDER BY i.index_name, ic.column_position`, oracleSchemaArg(schema), table)
	if err != nil {
		return nil, err
	}
	type building struct {
		Index
		keys []string
	}
	order := []string{}
	byName := map[string]*building{}
	for rows.Next() {
		var owner, name, uniqueness, method, status, column, descend string
		if err := rows.Scan(&owner, &name, &uniqueness, nullText{&method}, nullText{&status},
			&column, nullText{&descend}); err != nil {
			rows.Close()
			return nil, err
		}
		ix, ok := byName[name]
		if !ok {
			ix = &building{Index: Index{
				Name: name, Unique: uniqueness == "UNIQUE", Method: method,
				Invalid: status == "UNUSABLE", Columns: []string{},
			}}
			byName[name] = ix
			order = append(order, name)
		}
		ix.Columns = append(ix.Columns, column)
		quoted, err := d.QuoteIdent(column)
		if err != nil {
			quoted = column
		}
		if descend == "DESC" {
			quoted += " DESC"
		}
		ix.keys = append(ix.keys, quoted)
		// A function-based index stores its expression in a hidden column the
		// server names SYS_NC…$; the expression itself is read below.
		if strings.HasPrefix(column, "SYS_NC") && strings.HasSuffix(column, "$") {
			ix.Expression = true
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Which indexes exist to enforce a key: those go when the constraint goes.
	if constraints, err := db.QueryContext(ctx, `
	  SELECT c.index_name, c.constraint_name, c.constraint_type
	  FROM all_constraints c
	  WHERE `+oracleSchemaIs("c.owner")+` AND c.table_name = :2
	    AND c.constraint_type IN ('P','U') AND c.index_name IS NOT NULL`, oracleSchemaArg(schema), table); err == nil {
		for constraints.Next() {
			var index, name, ctype string
			if constraints.Scan(&index, &name, &ctype) != nil {
				break
			}
			if ix, ok := byName[index]; ok {
				ix.Constraint, ix.Primary = name, ctype == "P"
			}
		}
		constraints.Close()
	}
	// The text of each expression, in place of the hidden column's name.
	if expressions, err := db.QueryContext(ctx, `
	  SELECT e.index_name, e.column_position, e.column_expression
	  FROM all_ind_expressions e
	  WHERE `+oracleSchemaIs("e.table_owner")+` AND e.table_name = :2`, oracleSchemaArg(schema), table); err == nil {
		for expressions.Next() {
			var index, text string
			var position int
			if expressions.Scan(&index, &position, nullText{&text}) != nil {
				break
			}
			if ix, ok := byName[index]; ok && position >= 1 && position <= len(ix.Columns) && text != "" {
				ix.Columns[position-1], ix.keys[position-1], ix.Expression = text, text, true
			}
		}
		expressions.Close()
	}

	rel, relErr := qualify(d, schema, table)
	out := make([]Index, 0, len(order))
	for _, name := range order {
		ix := byName[name]
		if quoted, err := d.QuoteIdent(ix.Name); err == nil && relErr == nil &&
			(ix.Method == "NORMAL" || ix.Method == "FUNCTION-BASED NORMAL" || ix.Method == "BITMAP") {
			kind := "INDEX"
			switch {
			case ix.Unique:
				kind = "UNIQUE INDEX"
			case ix.Method == "BITMAP":
				kind = "BITMAP INDEX"
			}
			ix.Definition = "CREATE " + kind + " " + quoted + " ON " + rel + " (" + strings.Join(ix.keys, ", ") + ")"
		}
		out = append(out, ix.Index)
	}
	return out, nil
}

// oracleNotNullCheck matches the check constraint Oracle writes for every NOT
// NULL column. It is the column's nullability, already shown as such, and
// listing it again as a constraint would bury the real ones.
var oracleNotNullCheck = regexp.MustCompile(`^"[^"]+" IS NOT NULL$`)

func (d oracleDialect) tableConstraints(ctx context.Context, db *sql.DB, schema, table string) ([]Constraint, error) {
	// search_condition is a LONG and is selected bare.
	rows, err := db.QueryContext(ctx, `
	  SELECT c.constraint_name, c.constraint_type, c.search_condition, cc.column_name
	  FROM all_constraints c
	  LEFT JOIN all_cons_columns cc ON cc.owner = c.owner AND cc.constraint_name = c.constraint_name
	  WHERE `+oracleSchemaIs("c.owner")+` AND c.table_name = :2 AND c.constraint_type IN ('C','U')
	  ORDER BY c.constraint_name, cc.position`, oracleSchemaArg(schema), table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	order := []string{}
	byName := map[string]*Constraint{}
	conditions := map[string]string{}
	for rows.Next() {
		var name, ctype, condition, column string
		if err := rows.Scan(&name, &ctype, nullText{&condition}, nullText{&column}); err != nil {
			return nil, err
		}
		c, ok := byName[name]
		if !ok {
			c = &Constraint{Name: name, Type: ConstraintCheck, Columns: []string{}}
			if ctype == "U" {
				c.Type = ConstraintUnique
			}
			conditions[name] = strings.TrimSpace(condition)
			byName[name] = c
			order = append(order, name)
		}
		if column != "" {
			c.Columns = append(c.Columns, column)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := []Constraint{}
	for _, name := range order {
		c := byName[name]
		if c.Type == ConstraintCheck {
			if oracleNotNullCheck.MatchString(conditions[name]) {
				continue
			}
			c.Definition = "CHECK (" + conditions[name] + ")"
		} else {
			quoted := make([]string, 0, len(c.Columns))
			for _, col := range c.Columns {
				q, err := d.QuoteIdent(col)
				if err != nil {
					q = col
				}
				quoted = append(quoted, q)
			}
			c.Definition = "UNIQUE (" + strings.Join(quoted, ", ") + ")"
		}
		out = append(out, *c)
	}
	return out, nil
}

func (oracleDialect) tableReferencedBy(ctx context.Context, db *sql.DB, schema, table string) ([]IncomingForeignKey, error) {
	rows, err := db.QueryContext(ctx, `
	  SELECT c.constraint_name, c.owner, c.table_name, cc.column_name, rcc.column_name, c.delete_rule
	  FROM all_constraints c
	  JOIN all_cons_columns cc
	    ON cc.constraint_name = c.constraint_name AND cc.owner = c.owner
	  JOIN all_constraints rc
	    ON rc.constraint_name = c.r_constraint_name AND rc.owner = c.r_owner
	  JOIN all_cons_columns rcc
	    ON rcc.constraint_name = rc.constraint_name AND rcc.owner = rc.owner
	   AND rcc.position = cc.position
	  WHERE c.constraint_type = 'R' AND `+oracleSchemaIs("rc.owner")+` AND rc.table_name = :2
	  ORDER BY c.owner, c.table_name, c.constraint_name, cc.position`, oracleSchemaArg(schema), table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	acc := newIncomingAcc()
	for rows.Next() {
		var name, childSchema, child, col, refCol, del string
		if err := rows.Scan(&name, &childSchema, &child, &col, &refCol, nullText{&del}); err != nil {
			return nil, err
		}
		in := acc.get(childSchema, child, name)
		in.Columns, in.RefColumns = append(in.Columns, col), append(in.RefColumns, refCol)
		in.OnDelete = del
	}
	return acc.slice(), rows.Err()
}

func (oracleDialect) tableFacts(ctx context.Context, db *sql.DB, schema, table string, detail *TableDetail) error {
	var owner, tableType, comment string
	if err := db.QueryRowContext(ctx, `
	  SELECT c.owner, c.table_type, c.comments
	  FROM all_tab_comments c
	  WHERE `+oracleSchemaIs("c.owner")+` AND c.table_name = :2`, oracleSchemaArg(schema), table).
		Scan(&owner, nullText{&tableType}, nullText{&comment}); err != nil {
		return err
	}
	detail.Schema, detail.Owner, detail.Comment, detail.Type = owner, owner, comment, TableTypeTable
	if tableType == "VIEW" {
		detail.Type = TableTypeView
		return nil
	}
	var partitioned, temporary, tablespace, analysed string
	var rows int64
	if err := db.QueryRowContext(ctx, `
	  SELECT NVL(t.num_rows, -1), t.partitioned, t.temporary, t.tablespace_name,
	         TO_CHAR(t.last_analyzed, 'YYYY-MM-DD HH24:MI:SS')
	  FROM all_tables t
	  WHERE `+oracleSchemaIs("t.owner")+` AND t.table_name = :2`, oracleSchemaArg(schema), table).
		Scan(&rows, nullText{&partitioned}, nullText{&temporary}, nullText{&tablespace}, nullText{&analysed}); err != nil {
		return err
	}
	detail.Rows = rows
	if partitioned == "YES" {
		detail.Type = TableTypePartitioned
	}
	temp := ""
	if temporary == "Y" {
		temp = "yes"
	}
	// num_rows is a statistic, so the moment it was gathered is part of
	// reading it.
	detail.Facts = append(detail.Facts, facts(
		"Tablespace", tablespace,
		"Statistics gathered", analysed,
		"Temporary", temp,
	)...)
	return nil
}

// --- definitions ------------------------------------------------------------

// oracleObjectTypes maps a catalogue kind to the name ALL_OBJECTS files it
// under and the one DBMS_METADATA asks for, which differ only in whether a
// space is an underscore.
var oracleObjectTypes = map[string][2]string{
	KindView:             {"VIEW", "VIEW"},
	KindMaterializedView: {"MATERIALIZED VIEW", "MATERIALIZED_VIEW"},
	KindFunction:         {"FUNCTION", "FUNCTION"},
	KindProcedure:        {"PROCEDURE", "PROCEDURE"},
	KindPackage:          {"PACKAGE", "PACKAGE"},
	KindTrigger:          {"TRIGGER", "TRIGGER"},
	KindSequence:         {"SEQUENCE", "SEQUENCE"},
	KindType:             {"TYPE", "TYPE"},
	KindSynonym:          {"SYNONYM", "SYNONYM"},
}

func (d oracleDialect) objectDefinition(ctx context.Context, db *sql.DB, ref ObjectRef) (*ObjectDefinition, error) {
	types, ok := oracleObjectTypes[ref.Kind]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNoDefinition, ref.Kind)
	}
	arg := oracleSchemaArg(ref.Schema)
	var owner, status, created, modified string
	err := db.QueryRowContext(ctx, `
	  SELECT o.owner, o.status,
	         TO_CHAR(o.created, 'YYYY-MM-DD HH24:MI:SS'), TO_CHAR(o.last_ddl_time, 'YYYY-MM-DD HH24:MI:SS')
	  FROM all_objects o
	  WHERE `+oracleSchemaIs("o.owner")+` AND o.object_name = :2 AND o.object_type = :3`,
		arg, ref.Name, types[0]).Scan(&owner, nullText{&status}, nullText{&created}, nullText{&modified})
	if err == sql.ErrNoRows {
		return nil, ErrObjectNotFound
	}
	if err != nil {
		return nil, err
	}
	def := &ObjectDefinition{
		Kind: ref.Kind, Schema: owner, Name: ref.Name, Owner: owner,
		Details: facts("Status", strings.ToLower(status), "Created", created, "Last changed", modified),
	}
	if ref.Kind == KindTrigger {
		_ = db.QueryRowContext(ctx, `SELECT t.table_name FROM all_triggers t WHERE t.owner = :1 AND t.trigger_name = :2`,
			owner, ref.Name).Scan(nullText{&def.Table})
	}

	var ddl string
	err = db.QueryRowContext(ctx, `SELECT DBMS_METADATA.GET_DDL(:1, :2, :3) FROM dual`,
		types[1], ref.Name, owner).Scan(nullText{&ddl})
	if err == nil && strings.TrimSpace(ddl) != "" {
		def.Definition = ddl
		return def, nil
	}
	// DBMS_METADATA refuses an object in another schema without
	// SELECT_CATALOG_ROLE. The source views hold the same text for anything
	// the login may see at all.
	text, source := d.assembledDefinition(ctx, db, ref.Kind, owner, ref.Name)
	if strings.TrimSpace(text) == "" {
		def.Note = "The server would not return this definition: this login may see the object without being allowed to read its source."
		return def, nil
	}
	def.Definition, def.Source = text, source
	return def, nil
}

// assembledDefinition builds a definition from the source views when
// DBMS_METADATA is not available.
func (d oracleDialect) assembledDefinition(ctx context.Context, db *sql.DB, kind, owner, name string) (string, string) {
	rel, err := qualify(d, owner, name)
	if err != nil {
		return "", ""
	}
	switch kind {
	case KindFunction, KindProcedure, KindTrigger, KindType, KindPackage:
		sourceType := oracleObjectTypes[kind][0]
		// ALL_SOURCE keeps the statement from the object's name onwards, a
		// line per row with its own line ending.
		text, err := queryText(ctx, db, "", `
		  SELECT s.text FROM all_source s
		  WHERE s.owner = :1 AND s.name = :2 AND s.type = :3 ORDER BY s.line`, owner, name, sourceType)
		if err != nil || strings.TrimSpace(text) == "" {
			return "", ""
		}
		out := "CREATE OR REPLACE " + strings.TrimRight(text, "\n ") + "\n/"
		if kind == KindPackage {
			if body, err := queryText(ctx, db, "", `
			  SELECT s.text FROM all_source s
			  WHERE s.owner = :1 AND s.name = :2 AND s.type = 'PACKAGE BODY' ORDER BY s.line`, owner, name); err == nil &&
				strings.TrimSpace(body) != "" {
				out += "\n\nCREATE OR REPLACE " + strings.TrimRight(body, "\n ") + "\n/"
			}
		}
		return out, DefinitionFromEngine
	case KindView:
		var text string
		if err := db.QueryRowContext(ctx, `SELECT v.text FROM all_views v WHERE v.owner = :1 AND v.view_name = :2`,
			owner, name).Scan(nullText{&text}); err != nil || strings.TrimSpace(text) == "" {
			return "", ""
		}
		return "CREATE OR REPLACE VIEW " + rel + " AS\n" + strings.TrimSpace(text) + ";", DefinitionFromEngine
	case KindMaterializedView:
		var text string
		if err := db.QueryRowContext(ctx, `SELECT m.query FROM all_mviews m WHERE m.owner = :1 AND m.mview_name = :2`,
			owner, name).Scan(nullText{&text}); err != nil || strings.TrimSpace(text) == "" {
			return "", ""
		}
		return "CREATE MATERIALIZED VIEW " + rel + " AS\n" + strings.TrimSpace(text) + ";", DefinitionGenerated
	case KindSequence:
		var minimum, maximum, increment, cache, cycle string
		if err := db.QueryRowContext(ctx, `
		  SELECT TO_CHAR(s.min_value), TO_CHAR(s.max_value), TO_CHAR(s.increment_by), TO_CHAR(s.cache_size), s.cycle_flag
		  FROM all_sequences s WHERE s.sequence_owner = :1 AND s.sequence_name = :2`, owner, name).
			Scan(nullText{&minimum}, nullText{&maximum}, nullText{&increment}, nullText{&cache}, nullText{&cycle}); err != nil {
			return "", ""
		}
		text := "CREATE SEQUENCE " + rel + "\n  MINVALUE " + minimum + "\n  MAXVALUE " + maximum +
			"\n  INCREMENT BY " + increment
		if cache != "" && cache != "0" {
			text += "\n  CACHE " + cache
		} else {
			text += "\n  NOCACHE"
		}
		if cycle == "Y" {
			text += "\n  CYCLE"
		} else {
			text += "\n  NOCYCLE"
		}
		return text + ";", DefinitionGenerated
	case KindSynonym:
		var targetOwner, target, link string
		if err := db.QueryRowContext(ctx, `
		  SELECT s.table_owner, s.table_name, s.db_link FROM all_synonyms s WHERE s.owner = :1 AND s.synonym_name = :2`,
			owner, name).Scan(nullText{&targetOwner}, nullText{&target}, nullText{&link}); err != nil {
			return "", ""
		}
		return "CREATE OR REPLACE SYNONYM " + rel + " FOR " + oracleSynonymTarget(targetOwner, target, link) + ";",
			DefinitionGenerated
	}
	return "", ""
}
