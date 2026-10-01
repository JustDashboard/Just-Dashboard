package dbx

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// The built-in dump of a Postgres database.
//
// pg_dump is the right tool and is used when it will run. It will not run
// against a server newer than itself, which on a machine that installed its
// client once and upgraded its server since is every dump — and that is when
// this runs instead. The table DDL used to be put together from
// information_schema, which calls an array column ARRAY and an enum column
// USER-DEFINED, so the CREATE TABLE in the file did not parse and the restore
// stopped after it had dropped the tables.
//
// Everything here is read from pg_catalog with the server's own deparsers, in
// a session whose search path is empty so that each name they print carries
// its schema.

// pgRelation is one table, view or materialized view.
type pgRelation struct {
	oid       int64
	schema    string
	name      string
	kind      string // r table, p partitioned table, v view, m materialized view
	unlogged  bool
	partition bool
	bound     string // FOR VALUES … of a partition
	partKey   string // PARTITION BY … of a partitioned table
	parent    int64
	comment   string
	rel       string
}

func planPostgresDump(ctx context.Context, q dumpQueryer, sel dumpSelection) (*dumpPlan, error) {
	plan := &dumpPlan{notes: []string{
		"not included: functions, triggers, row security policies, grants, owners, domains and composite types",
	}}
	var version int
	if err := q.QueryRowContext(ctx, "SELECT current_setting('server_version_num')::int").Scan(&version); err != nil {
		return nil, fmt.Errorf("cannot read the server version: %w", err)
	}

	relations, err := pgRelations(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("cannot list tables: %w", err)
	}
	byOID := map[int64]*pgRelation{}
	for i := range relations {
		byOID[relations[i].oid] = &relations[i]
	}
	wanted := map[int64]bool{}
	schemas := map[string]bool{}
	for _, r := range relations {
		isView := r.kind == "v" || r.kind == "m"
		keep := sel.wants(r.schema, r.name)
		if isView && sel.narrowed() {
			keep = sel.named(r.schema, r.name)
		}
		if keep {
			wanted[r.oid] = true
			schemas[r.schema] = true
		}
	}

	// Structure the tables stand on.
	for _, schema := range sortedSet(schemas) {
		if schema == "public" {
			continue
		}
		plan.before = append(plan.before, stmt("CREATE SCHEMA IF NOT EXISTS "+pgIdent(schema)))
	}
	if err := pgExtensions(ctx, q, plan); err != nil {
		plan.skipped = append(plan.skipped, "extensions: "+err.Error())
	}
	if err := pgEnums(ctx, q, plan, schemas, sel.narrowed()); err != nil {
		plan.skipped = append(plan.skipped, "enum types: "+err.Error())
	}

	// Tables, each after the parent it is a partition of.
	for i := range relations {
		r := &relations[i]
		if !wanted[r.oid] || (r.kind != "r" && r.kind != "p") {
			continue
		}
		table, err := pgTable(ctx, q, r, byOID, version)
		if err != nil {
			plan.skipped = append(plan.skipped, fmt.Sprintf("%s: %s", r.rel, err.Error()))
			delete(wanted, r.oid)
			continue
		}
		plan.tables = append(plan.tables, table)
	}
	plan.tables = orderByDependency(plan.tables)

	if err := pgSequences(ctx, q, plan, wanted, sel.narrowed(), byOID); err != nil {
		plan.skipped = append(plan.skipped, "sequences: "+err.Error())
	}
	viewIndexes, err := pgConstraintsAndIndexes(ctx, q, plan, wanted, byOID, version)
	if err != nil {
		plan.skipped = append(plan.skipped, "constraints and indexes: "+err.Error())
	}
	if err := pgViews(ctx, q, plan, relations, wanted, viewIndexes); err != nil {
		plan.skipped = append(plan.skipped, "views: "+err.Error())
	}
	if err := pgComments(ctx, q, plan, relations, wanted); err != nil {
		plan.skipped = append(plan.skipped, "comments: "+err.Error())
	}
	return plan, nil
}

func pgIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func pgDumpLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func pgDumpRel(schema, name string) string { return pgIdent(schema) + "." + pgIdent(name) }

func sortedSet(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// pgUserObjects is the condition every catalogue read below shares: in a
// schema that is somebody's, and not something an extension brought with it.
// An extension's views and types are recreated by CREATE EXTENSION; dumping
// them as well makes the restore collide with itself.
const pgUserObjects = `n.nspname !~ '^pg_' AND n.nspname <> 'information_schema'
	AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_depend e
	                WHERE e.classid = %s AND e.objid = %s AND e.deptype = 'e')`

func pgRelations(ctx context.Context, q dumpQueryer) ([]pgRelation, error) {
	rows, err := q.QueryContext(ctx, `
	  SELECT c.oid::bigint, n.nspname, c.relname, c.relkind::text,
	         c.relpersistence = 'u', c.relispartition,
	         COALESCE(pg_catalog.pg_get_expr(c.relpartbound, c.oid), ''),
	         CASE WHEN c.relkind = 'p' THEN pg_catalog.pg_get_partkeydef(c.oid) ELSE '' END,
	         COALESCE((SELECT i.inhparent::bigint FROM pg_catalog.pg_inherits i
	                   WHERE i.inhrelid = c.oid AND c.relispartition LIMIT 1), 0),
	         COALESCE(pg_catalog.obj_description(c.oid, 'pg_class'), '')
	  FROM pg_catalog.pg_class c
	  JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
	  WHERE c.relkind IN ('r','p','v','m') AND c.relpersistence <> 't'
	    AND `+fmt.Sprintf(pgUserObjects, "'pg_catalog.pg_class'::regclass", "c.oid")+`
	  ORDER BY n.nspname, c.relname`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []pgRelation{}
	for rows.Next() {
		var r pgRelation
		if err := rows.Scan(&r.oid, &r.schema, &r.name, &r.kind, &r.unlogged, &r.partition,
			&r.bound, &r.partKey, &r.parent, &r.comment); err != nil {
			return nil, err
		}
		r.rel = pgDumpRel(r.schema, r.name)
		out = append(out, r)
	}
	return out, rows.Err()
}

func pgExtensions(ctx context.Context, q dumpQueryer, plan *dumpPlan) error {
	rows, err := q.QueryContext(ctx, `
	  SELECT e.extname, n.nspname
	  FROM pg_catalog.pg_extension e
	  JOIN pg_catalog.pg_namespace n ON n.oid = e.extnamespace
	  WHERE e.extname <> 'plpgsql'
	  ORDER BY e.extname`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var name, schema string
		if err := rows.Scan(&name, &schema); err != nil {
			return err
		}
		// A column of an extension's type, or a default calling one of its
		// functions, needs the extension there first. In the database the dump
		// came from this is a statement that does nothing.
		create := "CREATE EXTENSION IF NOT EXISTS " + pgIdent(name)
		if schema != "pg_catalog" {
			create += " WITH SCHEMA " + pgIdent(schema)
		}
		plan.before = append(plan.before, stmt(create))
		if name == "timescaledb" {
			// A hypertable's rows live in chunk tables the extension keeps
			// track of in its own catalogue. Dumped as the plain tables they
			// look like from here, they come back as plain tables.
			plan.notes = append(plan.notes,
				"TimescaleDB hypertables do not survive this dump as hypertables; take this database's dumps with pg_dump")
		}
	}
	return rows.Err()
}

// pgEnums writes each enum type as a statement that creates it only where it
// is missing.
//
// It is not dropped first. Tables this dump does not hold may have a column of
// the type, and the only DROP that would succeed then is the one that takes
// those columns with it. A type that is already there is left as it is; a row
// carrying a label the type has since lost is refused by the server, by name.
func pgEnums(ctx context.Context, q dumpQueryer, plan *dumpPlan, schemas map[string]bool, narrowed bool) error {
	rows, err := q.QueryContext(ctx, `
	  SELECT n.nspname, t.typname, e.enumlabel
	  FROM pg_catalog.pg_type t
	  JOIN pg_catalog.pg_enum e ON e.enumtypid = t.oid
	  JOIN pg_catalog.pg_namespace n ON n.oid = t.typnamespace
	  WHERE `+fmt.Sprintf(pgUserObjects, "'pg_catalog.pg_type'::regclass", "t.oid")+`
	  ORDER BY n.nspname, t.typname, e.enumsortorder`)
	if err != nil {
		return err
	}
	defer rows.Close()
	type enum struct {
		schema, name string
		labels       []string
	}
	var (
		enums []enum
		last  *enum
	)
	for rows.Next() {
		var schema, name, label string
		if err := rows.Scan(&schema, &name, &label); err != nil {
			return err
		}
		if last == nil || last.schema != schema || last.name != name {
			enums = append(enums, enum{schema: schema, name: name})
			last = &enums[len(enums)-1]
		}
		last.labels = append(last.labels, pgDumpLiteral(label))
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, e := range enums {
		if narrowed && !schemas[e.schema] {
			continue
		}
		if e.schema != "public" && !schemas[e.schema] {
			plan.before = append(plan.before, stmt("CREATE SCHEMA IF NOT EXISTS "+pgIdent(e.schema)))
		}
		plan.before = append(plan.before, rawStmt(fmt.Sprintf(
			"DO $jd$ BEGIN\n  CREATE TYPE %s AS ENUM (%s);\nEXCEPTION WHEN duplicate_object THEN NULL;\nEND $jd$",
			pgDumpRel(e.schema, e.name), strings.Join(e.labels, ", "))))
	}
	return nil
}

// pgTable builds one table's CREATE statement and the query that reads its
// rows.
func pgTable(ctx context.Context, q dumpQueryer, r *pgRelation, byOID map[int64]*pgRelation, version int) (dumpTable, error) {
	// attgenerated arrived in 12; before it no column could be generated.
	generated := "''"
	if version >= 120000 {
		generated = "a.attgenerated::text"
	}
	rows, err := q.QueryContext(ctx, `
	  SELECT a.attname,
	         pg_catalog.format_type(a.atttypid, a.atttypmod),
	         a.attnotnull,
	         COALESCE(pg_catalog.pg_get_expr(d.adbin, d.adrelid), ''),
	         a.attidentity::text, `+generated+`,
	         COALESCE((SELECT pg_catalog.quote_ident(cn.nspname) || '.' || pg_catalog.quote_ident(c.collname)
	                   FROM pg_catalog.pg_collation c
	                   JOIN pg_catalog.pg_namespace cn ON cn.oid = c.collnamespace
	                   JOIN pg_catalog.pg_type t ON t.oid = a.atttypid
	                   WHERE c.oid = a.attcollation AND a.attcollation <> t.typcollation), '')
	  FROM pg_catalog.pg_attribute a
	  LEFT JOIN pg_catalog.pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
	  WHERE a.attrelid = $1 AND a.attnum > 0 AND NOT a.attisdropped
	  ORDER BY a.attnum`, r.oid)
	if err != nil {
		return dumpTable{}, err
	}
	defer rows.Close()
	var (
		lines      []string
		selectCols []string
		overriding bool
	)
	for rows.Next() {
		var (
			name, typ, def, ident, gen, collation string
			notNull                               bool
		)
		if err := rows.Scan(&name, &typ, &notNull, &def, &ident, &gen, &collation); err != nil {
			return dumpTable{}, err
		}
		line := "  " + pgIdent(name) + " " + typ
		if collation != "" {
			line += " COLLATE " + collation
		}
		switch {
		case gen == "s":
			line += " GENERATED ALWAYS AS (" + def + ") STORED"
		case gen == "v":
			line += " GENERATED ALWAYS AS (" + def + ") VIRTUAL"
		case ident == "a":
			line += " GENERATED ALWAYS AS IDENTITY"
			overriding = true
		case ident == "d":
			line += " GENERATED BY DEFAULT AS IDENTITY"
		case def != "":
			line += " DEFAULT " + def
		}
		if notNull {
			line += " NOT NULL"
		}
		lines = append(lines, line)
		if gen == "" {
			// A generated column is computed on the way in; naming it in an
			// INSERT is an error.
			selectCols = append(selectCols, pgIdent(name))
		}
	}
	if err := rows.Err(); err != nil {
		return dumpTable{}, err
	}

	table := dumpTable{
		table: Table{Schema: r.schema, Name: r.name}, detail: &TableDetail{},
		rel: r.rel, drop: stmt("DROP TABLE IF EXISTS " + r.rel + " CASCADE"),
		overriding: overriding,
	}
	create := "CREATE TABLE "
	if r.unlogged {
		create = "CREATE UNLOGGED TABLE "
	}
	parent := byOID[r.parent]
	switch {
	case r.partition && parent != nil:
		// A partition takes its columns from its parent, and is created as
		// one: declared on its own it would be a separate table with the same
		// rows and none of the routing.
		create += r.rel + " PARTITION OF " + parent.rel + " " + r.bound
		table.parents = append(table.parents, parent.name)
	default:
		create += r.rel + " (\n" + strings.Join(lines, ",\n") + "\n)"
	}
	if r.kind == "p" {
		create += " PARTITION BY " + r.partKey
		// The rows of a partitioned table are its partitions' rows. Reading
		// them here as well would write every one of them twice.
		table.noData = true
	}
	table.create = rawStmt(create)
	if len(selectCols) == 0 {
		table.noData = true
	}
	// ONLY, so a table other tables inherit from gives its own rows and not
	// theirs as well.
	table.selectSQL = "SELECT " + strings.Join(selectCols, ", ") + " FROM ONLY " + r.rel
	return table, nil
}

// pgSequences writes the sequences that are objects of their own, the link
// from each to the column that owns it, and where every sequence — those and
// the ones behind identity columns — had got to.
func pgSequences(ctx context.Context, q dumpQueryer, plan *dumpPlan, wanted map[int64]bool, narrowed bool, byOID map[int64]*pgRelation) error {
	rows, err := q.QueryContext(ctx, `
	  SELECT n.nspname, c.relname,
	         pg_catalog.format_type(s.seqtypid, NULL),
	         s.seqstart::text, s.seqincrement::text, s.seqmin::text, s.seqmax::text,
	         s.seqcache::text, s.seqcycle,
	         COALESCE(pg_catalog.pg_sequence_last_value(c.oid)::text, ''),
	         COALESCE(d.refobjid::bigint, 0),
	         COALESCE((SELECT a.attname FROM pg_catalog.pg_attribute a
	                   WHERE a.attrelid = d.refobjid AND a.attnum = d.refobjsubid), ''),
	         COALESCE(d.deptype::text, '')
	  FROM pg_catalog.pg_class c
	  JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
	  JOIN pg_catalog.pg_sequence s ON s.seqrelid = c.oid
	  LEFT JOIN pg_catalog.pg_depend d
	    ON d.classid = 'pg_catalog.pg_class'::regclass AND d.objid = c.oid
	   AND d.refclassid = 'pg_catalog.pg_class'::regclass AND d.deptype IN ('a','i')
	  WHERE c.relkind = 'S'
	    AND `+fmt.Sprintf(pgUserObjects, "'pg_catalog.pg_class'::regclass", "c.oid")+`
	  ORDER BY n.nspname, c.relname`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			schema, name, typ, start, increment, minValue, maxValue, cache string
			last, ownerColumn, depType                                     string
			cycle                                                          bool
			owner                                                          int64
		)
		if err := rows.Scan(&schema, &name, &typ, &start, &increment, &minValue, &maxValue,
			&cache, &cycle, &last, &owner, &ownerColumn, &depType); err != nil {
			return err
		}
		if owner != 0 && !wanted[owner] {
			// It belongs to a table this dump leaves out.
			continue
		}
		if owner == 0 && narrowed {
			// Nobody's in particular, and the dump is of particular tables.
			continue
		}
		rel := pgDumpRel(schema, name)
		position := fmt.Sprintf("SELECT pg_catalog.setval(%s, %s, false)", pgDumpLiteral(rel), start)
		if last != "" {
			position = fmt.Sprintf("SELECT pg_catalog.setval(%s, %s, true)", pgDumpLiteral(rel), last)
		}
		if depType == "i" {
			// The sequence behind an identity column is made by the column.
			// Only its position is this dump's to restore, and it is found
			// through the column because its name is the server's choice.
			table := byOID[owner]
			if table == nil {
				continue
			}
			value, called := start, "false"
			if last != "" {
				value, called = last, "true"
			}
			plan.afterAll = append(plan.afterAll, stmt(fmt.Sprintf(
				"SELECT pg_catalog.setval(pg_catalog.pg_get_serial_sequence(%s, %s), %s, %s)",
				pgDumpLiteral(table.rel), pgDumpLiteral(ownerColumn), value, called)))
			continue
		}
		create := fmt.Sprintf("CREATE SEQUENCE IF NOT EXISTS %s AS %s INCREMENT BY %s MINVALUE %s MAXVALUE %s START WITH %s CACHE %s",
			rel, typ, increment, minValue, maxValue, start, cache)
		if cycle {
			create += " CYCLE"
		}
		obj := dumpObject{rel: rel, name: name, create: stmt(create)}
		if owner == 0 {
			// A sequence a table owns goes when the table is dropped. One that
			// stands alone has to be dropped by name, or the CREATE above finds
			// it there and leaves whatever it was.
			obj.drop = stmt("DROP SEQUENCE IF EXISTS " + rel + " CASCADE")
		} else if table := byOID[owner]; table != nil {
			plan.after = append(plan.after, stmt(fmt.Sprintf("ALTER SEQUENCE %s OWNED BY %s.%s",
				rel, table.rel, pgIdent(ownerColumn))))
		}
		plan.sequences = append(plan.sequences, obj)
		plan.afterAll = append(plan.afterAll, stmt(position))
	}
	return rows.Err()
}

// pgConstraintsAndIndexes writes what is added once the rows are in: primary
// keys, unique, check and exclusion constraints, then the indexes that are not
// a constraint's, then the foreign keys — last, because each needs the key it
// points at to exist and every row it covers to be there already.
//
// It returns the indexes on materialized views by the view's oid: those can
// only be made once the view is, so they are written with it.
func pgConstraintsAndIndexes(ctx context.Context, q dumpQueryer, plan *dumpPlan, wanted map[int64]bool, byOID map[int64]*pgRelation, version int) (map[int64][]dumpStatement, error) {
	// conparentid arrived in 11. Before it a partition's copy of its parent's
	// constraint is told by not being local.
	inherited := "NOT c.conislocal"
	if version >= 110000 {
		inherited = "c.conparentid <> 0"
	}
	rows, err := q.QueryContext(ctx, `
	  SELECT c.conrelid::bigint, c.confrelid::bigint, c.conname, c.contype::text,
	         pg_catalog.pg_get_constraintdef(c.oid)
	  FROM pg_catalog.pg_constraint c
	  WHERE c.conrelid <> 0 AND c.contype IN ('p','u','c','x','f') AND NOT (`+inherited+`)
	  ORDER BY CASE c.contype WHEN 'p' THEN 0 WHEN 'u' THEN 1 WHEN 'x' THEN 2 WHEN 'c' THEN 3 ELSE 4 END,
	           c.conrelid, c.conname`)
	if err != nil {
		return nil, err
	}
	var foreign []dumpStatement
	for rows.Next() {
		var (
			table, referenced int64
			name, kind, def   string
		)
		if err := rows.Scan(&table, &referenced, &name, &kind, &def); err != nil {
			rows.Close()
			return nil, err
		}
		r := byOID[table]
		if r == nil || (r.kind != "r" && r.kind != "p") {
			continue
		}
		add := fmt.Sprintf("ALTER TABLE %s ADD CONSTRAINT %s %s", r.rel, pgIdent(name), def)
		switch {
		case kind == "f" && wanted[table]:
			foreign = append(foreign, rawStmt(add))
		case kind == "f" && wanted[referenced]:
			// A table this dump leaves out points at one it holds. Dropping
			// the one it holds takes this constraint with it, so it is put
			// back — where the table it belongs to is there to put it on.
			foreign = append(foreign, rawStmt(fmt.Sprintf(
				"DO $jd$ BEGIN\n  IF pg_catalog.to_regclass(%s) IS NOT NULL THEN\n    %s;\n  END IF;\nEND $jd$",
				pgDumpLiteral(r.rel), add)))
		case wanted[table]:
			plan.after = append(plan.after, rawStmt(add))
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = q.QueryContext(ctx, `
	  SELECT i.indrelid::bigint, pg_catalog.pg_get_indexdef(i.indexrelid)
	  FROM pg_catalog.pg_index i
	  WHERE NOT EXISTS (SELECT 1 FROM pg_catalog.pg_constraint c WHERE c.conindid = i.indexrelid)
	    AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_inherits h WHERE h.inhrelid = i.indexrelid)
	  ORDER BY i.indrelid, i.indexrelid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	viewIndexes := map[int64][]dumpStatement{}
	for rows.Next() {
		var (
			table int64
			def   string
		)
		if err := rows.Scan(&table, &def); err != nil {
			return nil, err
		}
		r := byOID[table]
		if r == nil || !wanted[table] {
			continue
		}
		switch r.kind {
		case "m":
			viewIndexes[table] = append(viewIndexes[table], rawStmt(def))
			continue
		case "p":
			// The server prints a partitioned table's index as ON ONLY, which
			// makes the parent's half and leaves it invalid until each
			// partition's is attached. Without ONLY the one statement builds
			// the index on every partition.
			def = strings.Replace(def, " ON ONLY ", " ON ", 1)
		}
		plan.after = append(plan.after, rawStmt(def))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	plan.after = append(plan.after, foreign...)
	return viewIndexes, nil
}

// pgViews writes views and materialized views after everything they read.
//
// A materialized view is created empty and filled at the end. Its rows are
// not data of its own — they are a query's answer, and the query is in the
// file — so dumping them would be dumping a second copy that may already
// disagree with the first.
func pgViews(ctx context.Context, q dumpQueryer, plan *dumpPlan, relations []pgRelation, wanted map[int64]bool, indexes map[int64][]dumpStatement) error {
	deps := map[int64][]int64{}
	rows, err := q.QueryContext(ctx, `
	  SELECT DISTINCT r.ev_class::bigint, d.refobjid::bigint
	  FROM pg_catalog.pg_rewrite r
	  JOIN pg_catalog.pg_depend d
	    ON d.classid = 'pg_catalog.pg_rewrite'::regclass AND d.objid = r.oid
	   AND d.refclassid = 'pg_catalog.pg_class'::regclass
	  WHERE d.refobjid <> r.ev_class`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var view, ref int64
		if err := rows.Scan(&view, &ref); err != nil {
			rows.Close()
			return err
		}
		deps[view] = append(deps[view], ref)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	names := map[int64]string{}
	objects := []dumpObject{}
	oids := []int64{}
	materialized := map[string]bool{}
	for _, r := range relations {
		if !wanted[r.oid] || (r.kind != "v" && r.kind != "m") {
			continue
		}
		var def string
		if err := q.QueryRowContext(ctx, `SELECT pg_catalog.pg_get_viewdef($1::oid, true)`, r.oid).Scan(&def); err != nil {
			plan.skipped = append(plan.skipped, fmt.Sprintf("view %s: %s", r.rel, err.Error()))
			continue
		}
		def = strings.TrimRight(strings.TrimSpace(def), ";")
		// The oid is the name here: two views in different schemas may share
		// one, and the ordering must not confuse them.
		key := fmt.Sprintf("%d", r.oid)
		names[r.oid] = key
		obj := dumpObject{rel: r.rel, name: key}
		if r.kind == "m" {
			obj.drop = stmt("DROP MATERIALIZED VIEW IF EXISTS " + r.rel + " CASCADE")
			obj.create = rawStmt("CREATE MATERIALIZED VIEW " + r.rel + " AS\n" + def + "\nWITH NO DATA")
			obj.after = indexes[r.oid]
			materialized[r.rel] = true
		} else {
			obj.drop = stmt("DROP VIEW IF EXISTS " + r.rel + " CASCADE")
			obj.create = rawStmt("CREATE VIEW " + r.rel + " AS\n" + def)
		}
		objects = append(objects, obj)
		oids = append(oids, r.oid)
	}
	for i, oid := range oids {
		for _, ref := range deps[oid] {
			if name, ok := names[ref]; ok {
				objects[i].refs = append(objects[i].refs, name)
			}
		}
	}
	plan.views = orderObjects(objects)
	// Filled in the views' own order, since one materialized view may read
	// another.
	for _, v := range plan.views {
		if materialized[v.rel] {
			plan.afterAll = append(plan.afterAll, stmt("REFRESH MATERIALIZED VIEW "+v.rel))
		}
	}
	return nil
}

// pgComments carries the comments on tables and their columns, which is where
// a schema's own documentation lives.
func pgComments(ctx context.Context, q dumpQueryer, plan *dumpPlan, relations []pgRelation, wanted map[int64]bool) error {
	byOID := map[int64]*pgRelation{}
	for i := range relations {
		r := &relations[i]
		byOID[r.oid] = r
		if !wanted[r.oid] || r.comment == "" || (r.kind != "r" && r.kind != "p") {
			continue
		}
		plan.after = append(plan.after, stmt(fmt.Sprintf("COMMENT ON TABLE %s IS %s", r.rel, pgDumpLiteral(r.comment))))
	}
	rows, err := q.QueryContext(ctx, `
	  SELECT d.objoid::bigint, a.attname, d.description
	  FROM pg_catalog.pg_description d
	  JOIN pg_catalog.pg_attribute a ON a.attrelid = d.objoid AND a.attnum = d.objsubid
	  WHERE d.classoid = 'pg_catalog.pg_class'::regclass AND d.objsubid > 0
	  ORDER BY d.objoid, d.objsubid`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			oid           int64
			column, descr string
		)
		if err := rows.Scan(&oid, &column, &descr); err != nil {
			return err
		}
		r := byOID[oid]
		if r == nil || !wanted[oid] || (r.kind != "r" && r.kind != "p") {
			continue
		}
		plan.after = append(plan.after, stmt(fmt.Sprintf("COMMENT ON COLUMN %s.%s IS %s",
			r.rel, pgIdent(column), pgDumpLiteral(descr))))
	}
	return rows.Err()
}
