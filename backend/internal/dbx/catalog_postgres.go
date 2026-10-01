package dbx

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// Postgres answers everything here from pg_catalog. information_schema would
// be the portable choice and is the wrong one three times over: it has no
// functions with their overloads told apart, no triggers with their timing, no
// rows at all for a materialized view's columns, and it reports only what the
// login owns or holds a privilege beyond SELECT on.
//
// The queries are written for version 11 and later, which is where prokind,
// indnkeyatts and conparentid arrived. An older server refuses them, and each
// caller already treats a refused catalogue read as "not known" rather than as
// a failed request.

// pgSchemaFilter narrows a catalogue query to one schema, or — when the bound
// name is empty — to every schema that is not the engine's own. The alias is
// the pg_namespace row in scope.
func pgSchemaFilter(alias string) string {
	return `(($1::text = '' AND ` + alias + `.nspname NOT IN ('pg_catalog','information_schema')
	          AND ` + alias + `.nspname NOT LIKE 'pg!_%' ESCAPE '!') OR ` + alias + `.nspname = $1::text)`
}

// pgSchemaIs matches one named schema, the connection's own when the caller
// named none.
func pgSchemaIs(alias string) string {
	return alias + `.nspname = COALESCE(NULLIF($1::text, ''), current_schema())`
}

func (postgresDialect) catalogSchemas(ctx context.Context, db *sql.DB) ([]CatalogSchema, string, error) {
	rows, err := db.QueryContext(ctx, `
	  SELECT n.nspname,
	         COALESCE(pg_get_userbyid(n.nspowner), ''),
	         COALESCE(obj_description(n.oid, 'pg_namespace'), ''),
	         n.nspname IN ('pg_catalog','information_schema'),
	         COALESCE(n.nspname = current_schema(), false),
	         (SELECT count(*) FROM pg_class c
	           WHERE c.relnamespace = n.oid AND c.relkind IN ('r','p','v','m','f'))
	  FROM pg_namespace n
	  WHERE n.nspname NOT LIKE 'pg!_toast%' ESCAPE '!'
	    AND n.nspname NOT LIKE 'pg!_temp!_%' ESCAPE '!'
	  ORDER BY 4, 1`)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	out, current := []CatalogSchema{}, ""
	for rows.Next() {
		var s CatalogSchema
		if err := rows.Scan(&s.Name, &s.Owner, &s.Comment, &s.System, &s.Default, &s.Tables); err != nil {
			return nil, "", err
		}
		if s.Default {
			current = s.Name
		}
		out = append(out, s)
	}
	return out, current, rows.Err()
}

func (d postgresDialect) catalogGroups(context.Context, *sql.DB) []catalogGroup {
	return []catalogGroup{
		{GroupTables, func(ctx context.Context, db *sql.DB, schema string, limit int) ([]CatalogObject, error) {
			return d.relations(ctx, db, schema, limit, "'r','p','f'")
		}},
		{GroupViews, func(ctx context.Context, db *sql.DB, schema string, limit int) ([]CatalogObject, error) {
			return d.relations(ctx, db, schema, limit, "'v'")
		}},
		{GroupMaterializedViews, func(ctx context.Context, db *sql.DB, schema string, limit int) ([]CatalogObject, error) {
			return d.relations(ctx, db, schema, limit, "'m'")
		}},
		{GroupFunctions, func(ctx context.Context, db *sql.DB, schema string, limit int) ([]CatalogObject, error) {
			return d.routines(ctx, db, schema, limit, false)
		}},
		{GroupProcedures, func(ctx context.Context, db *sql.DB, schema string, limit int) ([]CatalogObject, error) {
			return d.routines(ctx, db, schema, limit, true)
		}},
		{GroupTriggers, d.triggers},
		{GroupSequences, d.sequences},
		{GroupTypes, d.types},
	}
}

// relations lists one family of pg_class rows. kinds is a constant list of
// relkind letters written by the caller above, never request text.
func (postgresDialect) relations(ctx context.Context, db *sql.DB, schema string, limit int, kinds string) ([]CatalogObject, error) {
	// The size of a relation another session dropped a moment ago is NULL
	// rather than an error, hence the COALESCE. A view has neither rows nor
	// bytes of its own.
	return scanObjects(ctx, db, `
	  SELECT n.nspname, c.relname, c.relkind::text,
	         COALESCE(pg_get_userbyid(c.relowner), ''),
	         COALESCE(obj_description(c.oid, 'pg_class'), ''),
	         CASE WHEN c.relkind = 'v' THEN -1 ELSE c.reltuples::bigint END,
	         CASE WHEN c.relkind IN ('r','m') THEN COALESCE(pg_total_relation_size(c.oid), 0) ELSE 0 END,
	         COALESCE((SELECT pc.relname FROM pg_inherits i JOIN pg_class pc ON pc.oid = i.inhparent
	                    WHERE i.inhrelid = c.oid AND c.relispartition LIMIT 1), ''),
	         c.relkind = 'm' AND NOT c.relispopulated
	  FROM pg_class c
	  JOIN pg_namespace n ON n.oid = c.relnamespace
	  WHERE c.relkind IN (`+kinds+`) AND `+pgSchemaFilter("n")+`
	  ORDER BY 1, 2
	  LIMIT $2`, []any{schema, limit}, func(rows *sql.Rows) (CatalogObject, error) {
		var (
			o           CatalogObject
			relkind     string
			estimate    int64
			unpopulated bool
		)
		if err := rows.Scan(&o.Schema, &o.Name, &relkind, &o.Owner, &o.Comment, &estimate, &o.Size, &o.Table, &unpopulated); err != nil {
			return o, err
		}
		switch relkind {
		case "v":
			o.Kind = KindView
		case "m":
			o.Kind = KindMaterializedView
			o.Rows = rowEstimate(estimate)
			if unpopulated {
				o.Detail = "not populated"
			}
		default:
			o.Kind = KindTable
			o.Rows = rowEstimate(estimate)
			switch {
			case relkind == "p":
				o.Detail = TableTypePartitioned
			case relkind == "f":
				o.Detail = "foreign table"
			case o.Table != "":
				o.Detail = TableTypePartition
			}
		}
		return o, nil
	})
}

func (postgresDialect) routines(ctx context.Context, db *sql.DB, schema string, limit int, procedures bool) ([]CatalogObject, error) {
	kind, match := KindFunction, "p.prokind <> 'p'"
	if procedures {
		kind, match = KindProcedure, "p.prokind = 'p'"
	}
	return scanObjects(ctx, db, `
	  SELECT n.nspname, p.proname, p.prokind::text,
	         COALESCE(pg_get_userbyid(p.proowner), ''),
	         COALESCE(obj_description(p.oid, 'pg_proc'), ''),
	         pg_get_function_identity_arguments(p.oid),
	         COALESCE(pg_get_function_result(p.oid), ''),
	         l.lanname,
	         COALESCE(e.extname, '')
	  FROM pg_proc p
	  JOIN pg_namespace n ON n.oid = p.pronamespace
	  JOIN pg_language l ON l.oid = p.prolang
	  LEFT JOIN pg_depend dep ON dep.classid = 'pg_proc'::regclass AND dep.objid = p.oid
	        AND dep.deptype = 'e' AND dep.refclassid = 'pg_extension'::regclass
	  LEFT JOIN pg_extension e ON e.oid = dep.refobjid
	  WHERE `+match+` AND `+pgSchemaFilter("n")+`
	  ORDER BY 1, 2, 6
	  LIMIT $2`, []any{schema, limit}, func(rows *sql.Rows) (CatalogObject, error) {
		o := CatalogObject{Kind: kind}
		var prokind string
		if err := rows.Scan(&o.Schema, &o.Name, &prokind, &o.Owner, &o.Comment, &o.Signature,
			&o.Returns, &o.Language, &o.Extension); err != nil {
			return o, err
		}
		switch prokind {
		case "a":
			o.Detail = "aggregate"
		case "w":
			o.Detail = "window"
		}
		return o, nil
	})
}

func (postgresDialect) triggers(ctx context.Context, db *sql.DB, schema string, limit int) ([]CatalogObject, error) {
	return scanObjects(ctx, db, `
	  SELECT n.nspname, t.tgname, c.relname, t.tgtype::int, t.tgenabled::text,
	         COALESCE(obj_description(t.oid, 'pg_trigger'), '')
	  FROM pg_trigger t
	  JOIN pg_class c ON c.oid = t.tgrelid
	  JOIN pg_namespace n ON n.oid = c.relnamespace
	  WHERE NOT t.tgisinternal AND `+pgSchemaFilter("n")+`
	  ORDER BY 1, 3, 2
	  LIMIT $2`, []any{schema, limit}, func(rows *sql.Rows) (CatalogObject, error) {
		o := CatalogObject{Kind: KindTrigger}
		var tgtype int
		var enabled string
		if err := rows.Scan(&o.Schema, &o.Name, &o.Table, &tgtype, &enabled, &o.Comment); err != nil {
			return o, err
		}
		o.Detail = pgTriggerTiming(tgtype)
		if enabled == "D" {
			o.Detail += ", disabled"
		}
		return o, nil
	})
}

// pgTriggerTiming decodes pg_trigger.tgtype, a bit mask, into the words a
// CREATE TRIGGER would use.
func pgTriggerTiming(tgtype int) string {
	timing := "AFTER"
	switch {
	case tgtype&2 != 0:
		timing = "BEFORE"
	case tgtype&64 != 0:
		timing = "INSTEAD OF"
	}
	events := []string{}
	for _, e := range []struct {
		bit  int
		name string
	}{{4, "INSERT"}, {16, "UPDATE"}, {8, "DELETE"}, {32, "TRUNCATE"}} {
		if tgtype&e.bit != 0 {
			events = append(events, e.name)
		}
	}
	scope := "each statement"
	if tgtype&1 != 0 {
		scope = "each row"
	}
	return timing + " " + strings.Join(events, " OR ") + ", " + scope
}

// pgSequenceOwner is the column a sequence is owned by, which is what makes it
// "the sequence behind orders.id" rather than a free-standing counter.
const pgSequenceOwner = `COALESCE((SELECT tc.relname || '.' || a.attname
	    FROM pg_depend dep
	    JOIN pg_class tc ON tc.oid = dep.refobjid
	    JOIN pg_attribute a ON a.attrelid = dep.refobjid AND a.attnum = dep.refobjsubid
	   WHERE dep.classid = 'pg_class'::regclass AND dep.objid = c.oid
	     AND dep.refclassid = 'pg_class'::regclass AND dep.deptype IN ('a','i')
	   LIMIT 1), '')`

func (postgresDialect) sequences(ctx context.Context, db *sql.DB, schema string, limit int) ([]CatalogObject, error) {
	return scanObjects(ctx, db, `
	  SELECT n.nspname, c.relname,
	         COALESCE(pg_get_userbyid(c.relowner), ''),
	         COALESCE(obj_description(c.oid, 'pg_class'), ''),
	         format_type(s.seqtypid, NULL), s.seqincrement, `+pgSequenceOwner+`
	  FROM pg_class c
	  JOIN pg_namespace n ON n.oid = c.relnamespace
	  JOIN pg_sequence s ON s.seqrelid = c.oid
	  WHERE c.relkind = 'S' AND `+pgSchemaFilter("n")+`
	  ORDER BY 1, 2
	  LIMIT $2`, []any{schema, limit}, func(rows *sql.Rows) (CatalogObject, error) {
		o := CatalogObject{Kind: KindSequence}
		var typ string
		var increment int64
		if err := rows.Scan(&o.Schema, &o.Name, &o.Owner, &o.Comment, &typ, &increment, &o.Table); err != nil {
			return o, err
		}
		o.Detail = fmt.Sprintf("%s, step %d", typ, increment)
		return o, nil
	})
}

// types lists the types a schema defines for itself: enums, domains, ranges
// and free-standing composites. The row type every table carries is a
// composite too and is left out — it is the table, already listed.
func (postgresDialect) types(ctx context.Context, db *sql.DB, schema string, limit int) ([]CatalogObject, error) {
	return scanObjects(ctx, db, `
	  SELECT n.nspname, t.typname, t.typtype::text,
	         COALESCE(pg_get_userbyid(t.typowner), ''),
	         COALESCE(obj_description(t.oid, 'pg_type'), ''),
	         CASE WHEN t.typtype = 'd' THEN format_type(t.typbasetype, t.typtypmod) ELSE '' END,
	         COALESCE((SELECT json_agg(e.enumlabel ORDER BY e.enumsortorder)
	                     FROM pg_enum e WHERE e.enumtypid = t.oid)::text, '[]')
	  FROM pg_type t
	  JOIN pg_namespace n ON n.oid = t.typnamespace
	  WHERE (t.typtype IN ('e','d','r')
	         OR (t.typtype = 'c' AND EXISTS (SELECT 1 FROM pg_class c WHERE c.oid = t.typrelid AND c.relkind = 'c')))
	    AND `+pgSchemaFilter("n")+`
	  ORDER BY 1, 2
	  LIMIT $2`, []any{schema, limit}, func(rows *sql.Rows) (CatalogObject, error) {
		var o CatalogObject
		var typtype, base, labels string
		if err := rows.Scan(&o.Schema, &o.Name, &typtype, &o.Owner, &o.Comment, &base, &labels); err != nil {
			return o, err
		}
		o.Kind = pgTypeKind(typtype)
		switch o.Kind {
		case KindEnum:
			o.Values = jsonStrings(labels)
		case KindDomain:
			o.Detail = base
		}
		return o, nil
	})
}

func pgTypeKind(typtype string) string {
	switch typtype {
	case "e":
		return KindEnum
	case "d":
		return KindDomain
	case "r":
		return KindRange
	case "c":
		return KindComposite
	default:
		return ""
	}
}

// relationTypes names the tables that are partitions of another. The table
// list reports them as ordinary tables, which they are to a SELECT and are not
// to a diagram.
func (postgresDialect) relationTypes(ctx context.Context, db *sql.DB, schema string) (map[catalogTable]string, error) {
	rows, err := db.QueryContext(ctx, `
	  SELECT n.nspname, c.relname
	  FROM pg_class c
	  JOIN pg_namespace n ON n.oid = c.relnamespace
	  WHERE c.relispartition AND c.relkind = 'r' AND ($1::text = '' OR n.nspname = $1::text)`, schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[catalogTable]string{}
	for rows.Next() {
		var schema, name string
		if err := rows.Scan(&schema, &name); err != nil {
			return nil, err
		}
		out[catalogTable{schema, name}] = TableTypePartition
	}
	return out, rows.Err()
}

// --- one table --------------------------------------------------------------

// tableColumns reads pg_attribute, where a type is spelled the way it was
// declared: `text[]` rather than ARRAY, the enum's own name rather than
// USER-DEFINED, and a materialized view has columns at all.
//
// attidentity arrived in 10 and attgenerated in 12. They are read through the
// row as JSON so that a server without the column answers "none" instead of
// refusing the query — which would send the read back to information_schema
// and its ARRAY.
func (postgresDialect) tableColumns(ctx context.Context, db *sql.DB, schema, table string) ([]Column, error) {
	rows, err := db.QueryContext(ctx, `
	  SELECT a.attname,
	         format_type(a.atttypid, a.atttypmod),
	         NOT a.attnotnull,
	         COALESCE(pg_get_expr(ad.adbin, ad.adrelid), ''),
	         a.attnum,
	         COALESCE(col_description(a.attrelid, a.attnum), ''),
	         COALESCE(to_jsonb(a)->>'attidentity', ''),
	         COALESCE(to_jsonb(a)->>'attgenerated', ''),
	         t.typtype::text,
	         t.typcategory::text,
	         COALESCE((SELECT json_agg(e.enumlabel ORDER BY e.enumsortorder) FROM pg_enum e
	                    WHERE e.enumtypid = CASE WHEN t.typtype = 'e' THEN t.oid
	                                             WHEN et.typtype = 'e' THEN et.oid
	                                             WHEN t.typtype = 'd' THEN t.typbasetype END)::text, '')
	  FROM pg_attribute a
	  JOIN pg_class c ON c.oid = a.attrelid
	  JOIN pg_namespace n ON n.oid = c.relnamespace
	  JOIN pg_type t ON t.oid = a.atttypid
	  LEFT JOIN pg_type et ON et.oid = t.typelem AND t.typcategory = 'A'
	  LEFT JOIN pg_attrdef ad ON ad.adrelid = a.attrelid AND ad.adnum = a.attnum
	  WHERE `+pgSchemaIs("n")+` AND c.relname = $2
	    AND c.relkind IN ('r','p','v','m','f')
	    AND a.attnum > 0 AND NOT a.attisdropped
	  ORDER BY a.attnum`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Column{}
	for rows.Next() {
		var (
			c                         Column
			expr, identity, generated string
			typtype, category, labels string
		)
		if err := rows.Scan(&c.Name, &c.Type, &c.Nullable, &expr, &c.Position, &c.Comment,
			&identity, &generated, &typtype, &category, &labels); err != nil {
			return nil, err
		}
		switch {
		case generated == "s":
			c.Generated, c.GeneratedKind = expr, "stored"
		case generated == "v":
			c.Generated, c.GeneratedKind = expr, "virtual"
		default:
			c.Default = expr
		}
		switch identity {
		case "a":
			c.Identity = "always"
		case "d":
			c.Identity = "by default"
		}
		switch {
		case category == "A":
			c.TypeKind = "array"
		case typtype == "e":
			c.TypeKind = "enum"
		case typtype == "d":
			c.TypeKind = "domain"
		case typtype == "c":
			c.TypeKind = "composite"
		case typtype == "r" || typtype == "m":
			c.TypeKind = "range"
		}
		if labels != "" {
			c.EnumValues = jsonStrings(labels)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (postgresDialect) tableIndexes(ctx context.Context, db *sql.DB, schema, table string) ([]Index, error) {
	rows, err := db.QueryContext(ctx, `
	  SELECT i.relname, ix.indisunique, ix.indisprimary, am.amname,
	         COALESCE(pg_get_expr(ix.indpred, ix.indrelid), ''),
	         pg_get_indexdef(ix.indexrelid),
	         COALESCE(pg_relation_size(ix.indexrelid), 0),
	         ix.indisvalid,
	         COALESCE((SELECT con.conname FROM pg_constraint con
	                    WHERE con.conindid = ix.indexrelid AND con.contype IN ('p','u','x') LIMIT 1), ''),
	         k.ord <= ix.indnkeyatts,
	         k.attnum = 0,
	         COALESCE(a.attname, pg_get_indexdef(ix.indexrelid, k.ord::int, true))
	  FROM pg_class t
	  JOIN pg_namespace n ON n.oid = t.relnamespace
	  JOIN pg_index ix ON ix.indrelid = t.oid
	  JOIN pg_class i ON i.oid = ix.indexrelid
	  JOIN pg_am am ON am.oid = i.relam
	  JOIN unnest(ix.indkey) WITH ORDINALITY AS k(attnum, ord) ON true
	  LEFT JOIN pg_attribute a ON a.attrelid = t.oid AND a.attnum = k.attnum AND k.attnum > 0
	  WHERE `+pgSchemaIs("n")+` AND t.relname = $2
	  ORDER BY i.relname, k.ord`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	order := []string{}
	byName := map[string]*Index{}
	for rows.Next() {
		var (
			ix                Index
			valid, key, isExp bool
			column            string
		)
		if err := rows.Scan(&ix.Name, &ix.Unique, &ix.Primary, &ix.Method, &ix.Predicate,
			&ix.Definition, &ix.Size, &valid, &ix.Constraint, &key, &isExp, &column); err != nil {
			return nil, err
		}
		cur, ok := byName[ix.Name]
		if !ok {
			ix.Columns, ix.Invalid = []string{}, !valid
			cur = &ix
			byName[ix.Name] = cur
			order = append(order, ix.Name)
		}
		if !key {
			cur.Include = append(cur.Include, column)
			continue
		}
		cur.Columns = append(cur.Columns, column)
		cur.Expression = cur.Expression || isExp
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

func (postgresDialect) tableConstraints(ctx context.Context, db *sql.DB, schema, table string) ([]Constraint, error) {
	rows, err := db.QueryContext(ctx, `
	  SELECT con.conname, con.contype::text, pg_get_constraintdef(con.oid, true),
	         COALESCE((SELECT json_agg(a.attname ORDER BY k.ord)
	                     FROM unnest(con.conkey) WITH ORDINALITY AS k(attnum, ord)
	                     JOIN pg_attribute a ON a.attrelid = con.conrelid AND a.attnum = k.attnum)::text, '[]')
	  FROM pg_constraint con
	  JOIN pg_class c ON c.oid = con.conrelid
	  JOIN pg_namespace n ON n.oid = c.relnamespace
	  WHERE `+pgSchemaIs("n")+` AND c.relname = $2 AND con.contype IN ('c','u','x')
	  ORDER BY con.contype, con.conname`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Constraint{}
	for rows.Next() {
		var c Constraint
		var contype, columns string
		if err := rows.Scan(&c.Name, &contype, &c.Definition, &columns); err != nil {
			return nil, err
		}
		switch contype {
		case "c":
			c.Type = ConstraintCheck
		case "u":
			c.Type = ConstraintUnique
		default:
			c.Type = ConstraintExclusion
		}
		c.Columns = jsonStrings(columns)
		out = append(out, c)
	}
	return out, rows.Err()
}

func (postgresDialect) tableReferencedBy(ctx context.Context, db *sql.DB, schema, table string) ([]IncomingForeignKey, error) {
	// conparentid = 0 keeps the constraint as it was declared: a foreign key on
	// a partitioned table is cloned onto every partition, and each clone would
	// otherwise be listed as a separate reference.
	rows, err := db.QueryContext(ctx, `
	  SELECT con.conname, rn.nspname, rel.relname, att.attname, att2.attname,
	         con.confupdtype::text, con.confdeltype::text
	  FROM pg_constraint con
	  JOIN pg_class rel ON rel.oid = con.conrelid
	  JOIN pg_namespace rn ON rn.oid = rel.relnamespace
	  JOIN pg_class cl ON cl.oid = con.confrelid
	  JOIN pg_namespace n ON n.oid = cl.relnamespace
	  JOIN unnest(con.conkey) WITH ORDINALITY AS k(attnum, ord) ON true
	  JOIN pg_attribute att ON att.attrelid = con.conrelid AND att.attnum = k.attnum
	  JOIN unnest(con.confkey) WITH ORDINALITY AS fk(attnum, ord) ON fk.ord = k.ord
	  JOIN pg_attribute att2 ON att2.attrelid = con.confrelid AND att2.attnum = fk.attnum
	  WHERE con.contype = 'f' AND con.conparentid = 0
	    AND `+pgSchemaIs("n")+` AND cl.relname = $2
	  ORDER BY rn.nspname, rel.relname, con.conname, k.ord`, schema, table)
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
		in.OnUpdate, in.OnDelete = pgFKAction(upd), pgFKAction(del)
	}
	return acc.slice(), rows.Err()
}

func (postgresDialect) tableFacts(ctx context.Context, db *sql.DB, schema, table string, detail *TableDetail) error {
	var (
		relkind, owner, comment, partKey, bound, parent, persistence string
		rows, size, data, index                                      int64
		isPartition, rowSecurity, populated                          bool
	)
	err := db.QueryRowContext(ctx, `
	  SELECT n.nspname, c.relkind::text,
	         COALESCE(pg_get_userbyid(c.relowner), ''),
	         COALESCE(obj_description(c.oid, 'pg_class'), ''),
	         CASE WHEN c.relkind = 'v' THEN -1 ELSE c.reltuples::bigint END,
	         COALESCE(pg_total_relation_size(c.oid), 0),
	         COALESCE(pg_table_size(c.oid), 0),
	         COALESCE(pg_indexes_size(c.oid), 0),
	         c.relispartition,
	         COALESCE(pg_get_partkeydef(c.oid), ''),
	         COALESCE(pg_get_expr(c.relpartbound, c.oid), ''),
	         COALESCE((SELECT pn.nspname || '.' || pc.relname FROM pg_inherits i
	                     JOIN pg_class pc ON pc.oid = i.inhparent
	                     JOIN pg_namespace pn ON pn.oid = pc.relnamespace
	                    WHERE i.inhrelid = c.oid AND c.relispartition LIMIT 1), ''),
	         c.relrowsecurity, c.relpersistence::text, c.relispopulated
	  FROM pg_class c
	  JOIN pg_namespace n ON n.oid = c.relnamespace
	  WHERE `+pgSchemaIs("n")+` AND c.relname = $2 AND c.relkind IN ('r','p','v','m','f')`,
		schema, table).Scan(&detail.Schema, &relkind, &owner, &comment, &rows, &size, &data, &index,
		&isPartition, &partKey, &bound, &parent, &rowSecurity, &persistence, &populated)
	if err != nil {
		return err
	}
	detail.Owner, detail.Comment, detail.Rows = owner, comment, rows
	detail.Size, detail.DataSize, detail.IndexSize = size, data, index
	switch {
	case relkind == "v":
		detail.Type = TableTypeView
	case relkind == "m":
		detail.Type = TableTypeMaterializedView
	case relkind == "p":
		detail.Type = TableTypePartitioned
	case relkind == "f":
		detail.Type = "foreign table"
	case isPartition:
		detail.Type = TableTypePartition
	default:
		detail.Type = TableTypeTable
	}
	detail.Facts = append(detail.Facts, facts(
		factPartitionKey, partKey,
		"Partition of", parent,
		"Partition bound", bound,
	)...)
	if rowSecurity {
		detail.Facts = append(detail.Facts, ObjectFact{"Row-level security", "enabled"})
	}
	if persistence == "u" {
		detail.Facts = append(detail.Facts, ObjectFact{"Persistence", "unlogged"})
	}
	if relkind == "m" && !populated {
		detail.Facts = append(detail.Facts, ObjectFact{"Populated", "no"})
	}
	return nil
}

// --- definitions ------------------------------------------------------------

func (d postgresDialect) objectDefinition(ctx context.Context, db *sql.DB, ref ObjectRef) (*ObjectDefinition, error) {
	switch ref.Kind {
	case KindView, KindMaterializedView:
		return d.viewDefinition(ctx, db, ref)
	case KindFunction, KindProcedure:
		return d.routineDefinition(ctx, db, ref)
	case KindTrigger:
		return d.triggerDefinition(ctx, db, ref)
	case KindSequence:
		return d.sequenceDefinition(ctx, db, ref)
	case KindEnum, KindDomain, KindComposite, KindRange:
		return d.typeDefinition(ctx, db, ref)
	default:
		return nil, fmt.Errorf("%w: %s", ErrNoDefinition, ref.Kind)
	}
}

// pgRel renders a schema-qualified, quoted name the way a definition should
// spell it.
func pgRel(schema, name string) string {
	rel, err := qualify(postgresDialect{}, schema, name)
	if err != nil {
		return name
	}
	return rel
}

func pgLiteral(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func (postgresDialect) viewDefinition(ctx context.Context, db *sql.DB, ref ObjectRef) (*ObjectDefinition, error) {
	relkind := "v"
	if ref.Kind == KindMaterializedView {
		relkind = "m"
	}
	var (
		schema, body, owner, comment string
		populated                    bool
	)
	err := db.QueryRowContext(ctx, `
	  SELECT n.nspname, pg_get_viewdef(c.oid, true),
	         COALESCE(pg_get_userbyid(c.relowner), ''),
	         COALESCE(obj_description(c.oid, 'pg_class'), ''),
	         c.relispopulated
	  FROM pg_class c
	  JOIN pg_namespace n ON n.oid = c.relnamespace
	  WHERE `+pgSchemaIs("n")+` AND c.relname = $2 AND c.relkind = $3`,
		ref.Schema, ref.Name, relkind).Scan(&schema, nullText{&body}, &owner, &comment, &populated)
	if err == sql.ErrNoRows {
		return nil, ErrObjectNotFound
	}
	if err != nil {
		return nil, err
	}
	def := &ObjectDefinition{Kind: ref.Kind, Schema: schema, Name: ref.Name, Owner: owner, Comment: comment}
	body = strings.TrimRight(strings.TrimSpace(body), ";")
	if body == "" {
		def.Note = "The server returned no text for this view."
		return def, nil
	}
	if ref.Kind == KindMaterializedView {
		data := "WITH DATA"
		if !populated {
			data = "WITH NO DATA"
		}
		def.Definition = "CREATE MATERIALIZED VIEW " + pgRel(schema, ref.Name) + " AS\n" + body + "\n" + data + ";"
		def.Details = facts("Populated", yesNo(populated))
		return def, nil
	}
	def.Definition = "CREATE OR REPLACE VIEW " + pgRel(schema, ref.Name) + " AS\n" + body + ";"
	return def, nil
}

func (postgresDialect) routineDefinition(ctx context.Context, db *sql.DB, ref ObjectRef) (*ObjectDefinition, error) {
	match := "p.prokind <> 'p'"
	if ref.Kind == KindProcedure {
		match = "p.prokind = 'p'"
	}
	rows, err := db.QueryContext(ctx, `
	  SELECT p.oid::bigint, n.nspname, p.prokind::text,
	         pg_get_function_identity_arguments(p.oid),
	         COALESCE(pg_get_function_result(p.oid), ''),
	         l.lanname,
	         COALESCE(pg_get_userbyid(p.proowner), ''),
	         COALESCE(obj_description(p.oid, 'pg_proc'), ''),
	         p.provolatile::text, p.prosecdef, p.proisstrict
	  FROM pg_proc p
	  JOIN pg_namespace n ON n.oid = p.pronamespace
	  JOIN pg_language l ON l.oid = p.prolang
	  WHERE `+match+` AND `+pgSchemaIs("n")+` AND p.proname = $2
	  ORDER BY 4`, ref.Schema, ref.Name)
	if err != nil {
		return nil, err
	}
	type candidate struct {
		oid                                       int64
		schema, prokind, signature, returns, lang string
		owner, comment, volatility                string
		definer, strict                           bool
	}
	var found []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.oid, &c.schema, &c.prokind, &c.signature, &c.returns, &c.lang,
			&c.owner, &c.comment, &c.volatility, &c.definer, &c.strict); err != nil {
			rows.Close()
			return nil, err
		}
		found = append(found, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(found) == 0 {
		return nil, ErrObjectNotFound
	}
	chosen := -1
	for i, c := range found {
		if c.signature == ref.Signature {
			chosen = i
		}
	}
	if chosen < 0 {
		// One routine by that name needs no signature to be told apart; two do,
		// and guessing would show the wrong body under the right name.
		if len(found) > 1 || strings.TrimSpace(ref.Signature) != "" {
			signatures := make([]string, 0, len(found))
			for _, c := range found {
				signatures = append(signatures, "("+c.signature+")")
			}
			if strings.TrimSpace(ref.Signature) != "" {
				return nil, fmt.Errorf("%w: %s has no form taking (%s); it has %s",
					ErrObjectNotFound, ref.Name, ref.Signature, strings.Join(signatures, ", "))
			}
			return nil, fmt.Errorf("%w: %s has %d forms — name one by its signature: %s",
				ErrAmbiguousObject, ref.Name, len(found), strings.Join(signatures, ", "))
		}
		chosen = 0
	}
	c := found[chosen]
	def := &ObjectDefinition{
		Kind: ref.Kind, Schema: c.schema, Name: ref.Name, Signature: c.signature,
		Owner: c.owner, Comment: c.comment, Language: c.lang, Returns: c.returns,
	}
	volatility := map[string]string{"i": "immutable", "s": "stable", "v": "volatile"}[c.volatility]
	security := "invoker"
	if c.definer {
		security = "definer"
	}
	def.Details = facts(
		"Arguments", c.signature,
		"Returns", c.returns,
		"Language", c.lang,
		"Volatility", volatility,
		"Security", security,
		"Strict", yesNo(c.strict),
	)
	if c.prokind == "a" {
		// pg_get_functiondef refuses an aggregate: it has no body, only the
		// functions it is assembled from.
		def.Note = "An aggregate has no body of its own; it is assembled from a state function and a final function."
		return def, nil
	}
	var text string
	if err := db.QueryRowContext(ctx, `SELECT pg_get_functiondef($1::oid)`, c.oid).Scan(nullText{&text}); err != nil {
		def.Note = "The server would not return this routine's text: " + err.Error()
		return def, nil
	}
	def.Definition = text
	return def, nil
}

func (postgresDialect) triggerDefinition(ctx context.Context, db *sql.DB, ref ObjectRef) (*ObjectDefinition, error) {
	rows, err := db.QueryContext(ctx, `
	  SELECT n.nspname, c.relname, pg_get_triggerdef(t.oid, true), t.tgtype::int, t.tgenabled::text,
	         pn.nspname || '.' || p.proname,
	         COALESCE(obj_description(t.oid, 'pg_trigger'), '')
	  FROM pg_trigger t
	  JOIN pg_class c ON c.oid = t.tgrelid
	  JOIN pg_namespace n ON n.oid = c.relnamespace
	  JOIN pg_proc p ON p.oid = t.tgfoid
	  JOIN pg_namespace pn ON pn.oid = p.pronamespace
	  WHERE NOT t.tgisinternal AND `+pgSchemaIs("n")+` AND t.tgname = $2
	    AND ($3::text = '' OR c.relname = $3::text)
	  ORDER BY c.relname`, ref.Schema, ref.Name, ref.Table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var defs []*ObjectDefinition
	for rows.Next() {
		var (
			schema, table, text, enabled, function, comment string
			tgtype                                          int
		)
		if err := rows.Scan(&schema, &table, nullText{&text}, &tgtype, &enabled, &function, &comment); err != nil {
			return nil, err
		}
		state := "enabled"
		if enabled == "D" {
			state = "disabled"
		}
		if text != "" {
			text += ";"
		}
		defs = append(defs, &ObjectDefinition{
			Kind: KindTrigger, Schema: schema, Name: ref.Name, Table: table,
			Definition: text, Comment: comment,
			Details: facts("Table", table, "Fires", pgTriggerTiming(tgtype), "Function", function, "State", state),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	switch len(defs) {
	case 0:
		return nil, ErrObjectNotFound
	case 1:
		return defs[0], nil
	}
	// A trigger's name is unique per table, not per schema.
	tables := make([]string, 0, len(defs))
	for _, def := range defs {
		tables = append(tables, def.Table)
	}
	return nil, fmt.Errorf("%w: triggers named %s exist on %s — name the table",
		ErrAmbiguousObject, ref.Name, strings.Join(tables, ", "))
}

func (postgresDialect) sequenceDefinition(ctx context.Context, db *sql.DB, ref ObjectRef) (*ObjectDefinition, error) {
	var (
		schema, typ, owner, comment, ownedBy string
		start, increment, minimum, maximum   int64
		cache                                int64
		cycle                                bool
		last                                 sql.NullInt64
	)
	err := db.QueryRowContext(ctx, `
	  SELECT n.nspname, format_type(s.seqtypid, NULL), s.seqstart, s.seqincrement, s.seqmin, s.seqmax,
	         s.seqcache, s.seqcycle,
	         COALESCE(pg_get_userbyid(c.relowner), ''),
	         COALESCE(obj_description(c.oid, 'pg_class'), ''),
	         `+pgSequenceOwner+`,
	         (SELECT ps.last_value FROM pg_sequences ps
	           WHERE ps.schemaname = n.nspname AND ps.sequencename = c.relname)
	  FROM pg_class c
	  JOIN pg_namespace n ON n.oid = c.relnamespace
	  JOIN pg_sequence s ON s.seqrelid = c.oid
	  WHERE c.relkind = 'S' AND `+pgSchemaIs("n")+` AND c.relname = $2`,
		ref.Schema, ref.Name).Scan(&schema, &typ, &start, &increment, &minimum, &maximum,
		&cache, &cycle, &owner, &comment, &ownedBy, &last)
	if err == sql.ErrNoRows {
		return nil, ErrObjectNotFound
	}
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "CREATE SEQUENCE %s\n  AS %s\n  INCREMENT BY %d\n  MINVALUE %d\n  MAXVALUE %d\n  START WITH %d\n  CACHE %d",
		pgRel(schema, ref.Name), typ, increment, minimum, maximum, start, cache)
	if cycle {
		b.WriteString("\n  CYCLE")
	} else {
		b.WriteString("\n  NO CYCLE")
	}
	if table, column, ok := strings.Cut(ownedBy, "."); ok {
		b.WriteString("\n  OWNED BY " + pgRel(schema, table) + "." + pgRel("", column))
	}
	b.WriteString(";")
	lastValue := "not used yet"
	if last.Valid {
		lastValue = fmt.Sprint(last.Int64)
	}
	return &ObjectDefinition{
		Kind: KindSequence, Schema: schema, Name: ref.Name, Table: ownedBy,
		Definition: b.String(), Source: DefinitionGenerated, Owner: owner, Comment: comment,
		Details: facts(
			"Type", typ,
			"Last value", lastValue,
			"Increment", fmt.Sprint(increment),
			"Minimum", fmt.Sprint(minimum),
			"Maximum", fmt.Sprint(maximum),
			"Start", fmt.Sprint(start),
			"Cycles", yesNo(cycle),
			"Owned by", ownedBy,
		),
	}, nil
}

func (postgresDialect) typeDefinition(ctx context.Context, db *sql.DB, ref ObjectRef) (*ObjectDefinition, error) {
	var (
		schema, typtype, owner, comment, base, dflt, labels, checks, attrs, subtype string
		notNull                                                                     bool
	)
	err := db.QueryRowContext(ctx, `
	  SELECT n.nspname, t.typtype::text,
	         COALESCE(pg_get_userbyid(t.typowner), ''),
	         COALESCE(obj_description(t.oid, 'pg_type'), ''),
	         CASE WHEN t.typtype = 'd' THEN format_type(t.typbasetype, t.typtypmod) ELSE '' END,
	         t.typnotnull, COALESCE(t.typdefault, ''),
	         COALESCE((SELECT json_agg(e.enumlabel ORDER BY e.enumsortorder)
	                     FROM pg_enum e WHERE e.enumtypid = t.oid)::text, '[]'),
	         COALESCE((SELECT json_agg('CONSTRAINT ' || quote_ident(con.conname) || ' ' ||
	                                   pg_get_constraintdef(con.oid, true) ORDER BY con.conname)
	                     FROM pg_constraint con WHERE con.contypid = t.oid)::text, '[]'),
	         COALESCE((SELECT json_agg(quote_ident(a.attname) || ' ' || format_type(a.atttypid, a.atttypmod)
	                                   ORDER BY a.attnum)
	                     FROM pg_attribute a
	                    WHERE a.attrelid = t.typrelid AND a.attnum > 0 AND NOT a.attisdropped)::text, '[]'),
	         COALESCE((SELECT format_type(r.rngsubtype, NULL) FROM pg_range r WHERE r.rngtypid = t.oid), '')
	  FROM pg_type t
	  JOIN pg_namespace n ON n.oid = t.typnamespace
	  WHERE `+pgSchemaIs("n")+` AND t.typname = $2 AND t.typtype IN ('e','d','c','r')`,
		ref.Schema, ref.Name).Scan(&schema, &typtype, &owner, &comment, &base, &notNull, &dflt,
		&labels, &checks, &attrs, &subtype)
	if err == sql.ErrNoRows {
		return nil, ErrObjectNotFound
	}
	if err != nil {
		return nil, err
	}
	kind := pgTypeKind(typtype)
	if kind != ref.Kind {
		return nil, fmt.Errorf("%w: %s is a %s type, not a %s", ErrObjectNotFound, ref.Name, kind, ref.Kind)
	}
	rel := pgRel(schema, ref.Name)
	def := &ObjectDefinition{
		Kind: kind, Schema: schema, Name: ref.Name, Source: DefinitionGenerated,
		Owner: owner, Comment: comment,
	}
	switch kind {
	case KindEnum:
		def.Values = jsonStrings(labels)
		quoted := make([]string, 0, len(def.Values))
		for _, v := range def.Values {
			quoted = append(quoted, "  "+pgLiteral(v))
		}
		def.Definition = "CREATE TYPE " + rel + " AS ENUM (\n" + strings.Join(quoted, ",\n") + "\n);"
		def.Details = facts("Labels", fmt.Sprint(len(def.Values)))
	case KindDomain:
		text := "CREATE DOMAIN " + rel + " AS " + base
		if dflt != "" {
			text += "\n  DEFAULT " + dflt
		}
		if notNull {
			text += "\n  NOT NULL"
		}
		for _, c := range jsonStrings(checks) {
			text += "\n  " + c
		}
		def.Definition = text + ";"
		def.Details = facts("Base type", base, "Default", dflt, "Not null", yesNo(notNull))
	case KindComposite:
		fields := jsonStrings(attrs)
		for i := range fields {
			fields[i] = "  " + fields[i]
		}
		def.Definition = "CREATE TYPE " + rel + " AS (\n" + strings.Join(fields, ",\n") + "\n);"
		def.Details = facts("Attributes", fmt.Sprint(len(fields)))
	case KindRange:
		def.Definition = "CREATE TYPE " + rel + " AS RANGE (\n  SUBTYPE = " + subtype + "\n);"
		def.Details = facts("Subtype", subtype)
	}
	return def, nil
}

// incomingAcc groups referencing-key rows that arrive a column at a time,
// preserving the order the query returned them in.
type incomingAcc struct {
	order []string
	byKey map[string]*IncomingForeignKey
}

func newIncomingAcc() *incomingAcc { return &incomingAcc{byKey: map[string]*IncomingForeignKey{}} }

func (a *incomingAcc) get(schema, table, name string) *IncomingForeignKey {
	key := schema + "\x00" + table + "\x00" + name
	in, ok := a.byKey[key]
	if !ok {
		in = &IncomingForeignKey{Name: name, Schema: schema, Table: table, Columns: []string{}, RefColumns: []string{}}
		a.byKey[key] = in
		a.order = append(a.order, key)
	}
	return in
}

func (a *incomingAcc) slice() []IncomingForeignKey {
	out := make([]IncomingForeignKey, 0, len(a.order))
	for _, k := range a.order {
		out = append(out, *a.byKey[k])
	}
	return out
}
