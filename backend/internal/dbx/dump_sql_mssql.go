package dbx

import (
	"context"
	"database/sql"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"
)

// The built-in dump of a SQL Server database.
//
// SQL Server has no dump tool this image could carry, so this is the only dump
// there is. Its table definitions used to be put together from
// INFORMATION_SCHEMA, which does not say that a column numbers itself, is
// computed from the others, or is a rowversion. What came back from a restore
// was a table of the same name that no longer handed out an id, held the
// computed values as if somebody had typed them, and — where there was a
// rowversion — did not come back at all, because the server refuses to be
// given one.
//
// Everything here is read from the sys catalogue of the database the
// connection is in, and nothing in the file names that database, so the same
// file loads into the one it came from and into one made a moment ago.

type mssqlObject struct {
	id     int64
	schema string
	name   string
	rel    string
	view   bool
}

type mssqlColumn struct {
	name, typeName                 string
	maxLength, precision, scale    int
	nullable, identity, computed   bool
	rowGUID                        bool
	collation                      string
	defaultName, defaultDefinition string
	computedDefinition             string
	persisted                      bool
	seed, increment, last          string
}

type mssqlIndexColumn struct {
	name       string
	descending bool
	included   bool
}

type mssqlIndex struct {
	name             string
	kind             int // 1 clustered, 2 nonclustered; anything else is not written
	unique           bool
	primary          bool
	uniqueConstraint bool
	filter           string
	disabled         bool
	columns          []mssqlIndexColumn
}

// mssqlIdent quotes a name with brackets. It is not quoteBracket: that one
// refuses a name with a control character in it, and a dump has to carry
// whatever the catalogue holds.
func mssqlIdent(name string) string {
	return "[" + strings.ReplaceAll(name, "]", "]]") + "]"
}

func mssqlRel(schema, name string) string { return mssqlIdent(schema) + "." + mssqlIdent(name) }

func mssqlString(s string) string { return "N'" + strings.ReplaceAll(s, "'", "''") + "'" }

func planMSSQLDump(ctx context.Context, q dumpQueryer, sel dumpSelection) (*dumpPlan, error) {
	plan := &dumpPlan{
		notes: []string{
			"not included: procedures, functions, triggers, user-defined types, permissions, extended properties",
		},
		session: []dumpStatement{
			// What a computed column, a filtered index and an indexed view all
			// require of the session that creates or writes them. The
			// dashboard's own connection has them; a client this file is fed to
			// may not.
			stmt("SET ANSI_NULLS, ANSI_PADDING, ANSI_WARNINGS, ARITHABORT, CONCAT_NULL_YIELDS_NULL, QUOTED_IDENTIFIER ON"),
			stmt("SET NUMERIC_ROUNDABORT OFF"),
			stmt("SET NOCOUNT ON"),
		},
	}

	objects, err := mssqlObjects(ctx, q, plan)
	if err != nil {
		return nil, fmt.Errorf("cannot list tables: %w", err)
	}
	wanted := map[int64]*mssqlObject{}
	schemas := map[string]bool{}
	for i := range objects {
		o := &objects[i]
		keep := sel.wants(o.schema, o.name)
		if o.view && sel.narrowed() {
			keep = sel.named(o.schema, o.name)
		}
		if keep {
			wanted[o.id] = o
			schemas[o.schema] = true
		}
	}

	columns, err := mssqlColumns(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("cannot read the columns: %w", err)
	}
	indexes, err := mssqlIndexes(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("cannot read the indexes: %w", err)
	}
	var collation sql.NullString
	// A column says its collation only where it differs from the database's,
	// so a restore into a database set up another way follows that database.
	_ = q.QueryRowContext(ctx, `SELECT CAST(DATABASEPROPERTYEX(DB_NAME(), 'Collation') AS nvarchar(128))`).Scan(&collation)

	var defaults []string
	for i := range objects {
		o := &objects[i]
		if o.view || wanted[o.id] == nil {
			continue
		}
		table, texts := mssqlTable(o, columns[o.id], indexes[o.id], collation.String)
		defaults = append(defaults, texts...)
		for _, ix := range indexes[o.id] {
			if ix.primary {
				continue
			}
			if create, reason := mssqlIndexStatement(o.rel, ix); create != "" {
				plan.after = append(plan.after, rawStmt(create))
			} else if reason != "" {
				plan.skipped = append(plan.skipped, fmt.Sprintf("index %s on %s: %s", ix.name, o.rel, reason))
			}
		}
		plan.tables = append(plan.tables, table)
	}

	if err := mssqlChecks(ctx, q, plan, wanted); err != nil {
		plan.skipped = append(plan.skipped, "check constraints: "+err.Error())
	}
	if err := mssqlForeignKeys(ctx, q, plan, objects, wanted); err != nil {
		plan.skipped = append(plan.skipped, "foreign keys: "+err.Error())
	}
	// With the foreign keys read, a table's parents are known: a dump of the
	// rows alone goes into tables whose keys are already enforced, parent
	// first.
	plan.tables = orderByDependency(plan.tables)

	if err := mssqlSequences(ctx, q, plan, sel, schemas, defaults); err != nil {
		plan.skipped = append(plan.skipped, "sequences: "+err.Error())
	}
	if err := mssqlViews(ctx, q, plan, objects, wanted, indexes); err != nil {
		plan.skipped = append(plan.skipped, "views: "+err.Error())
	}

	// A schema other than dbo is made where it is missing, and before anything
	// that lives in it.
	var before []dumpStatement
	for _, schema := range sortedSet(schemas) {
		if schema == "dbo" {
			continue
		}
		before = append(before, stmt(fmt.Sprintf("IF SCHEMA_ID(%s) IS NULL EXEC(%s)",
			mssqlString(schema), mssqlString("CREATE SCHEMA "+mssqlIdent(schema)))))
	}
	plan.before = append(before, plan.before...)
	return plan, nil
}

// mssqlObjects lists the tables and views that are somebody's. A table the
// dump cannot carry as a table — one the server keeps history for, one that
// lives in memory or outside the database — is named in the file as left out
// rather than written as something it is not.
func mssqlObjects(ctx context.Context, q dumpQueryer, plan *dumpPlan) ([]mssqlObject, error) {
	rows, err := q.QueryContext(ctx, `
	  SELECT o.object_id, s.name, o.name, CASE WHEN o.type = 'V' THEN 1 ELSE 0 END,
	         ISNULL(t.temporal_type, 0), ISNULL(t.is_memory_optimized, 0),
	         ISNULL(t.is_external, 0), ISNULL(t.is_filetable, 0)
	  FROM sys.objects o
	  JOIN sys.schemas s ON s.schema_id = o.schema_id
	  LEFT JOIN sys.tables t ON t.object_id = o.object_id
	  WHERE o.type IN ('U', 'V') AND o.is_ms_shipped = 0
	  ORDER BY s.name, o.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []mssqlObject{}
	for rows.Next() {
		var (
			o                                    mssqlObject
			temporal                             int
			memoryOptimized, external, fileTable bool
		)
		if err := rows.Scan(&o.id, &o.schema, &o.name, &o.view, &temporal, &memoryOptimized, &external, &fileTable); err != nil {
			return nil, err
		}
		o.rel = mssqlRel(o.schema, o.name)
		reason := ""
		switch {
		case temporal != 0:
			reason = "a table the server keeps history for (system-versioned) is not dumped"
		case memoryOptimized:
			reason = "a memory-optimized table is not dumped"
		case external:
			reason = "an external table holds no rows of its own"
		case fileTable:
			reason = "a FileTable is not dumped"
		}
		if reason != "" {
			plan.skipped = append(plan.skipped, o.rel+": "+reason)
			continue
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func mssqlColumns(ctx context.Context, q dumpQueryer) (map[int64][]mssqlColumn, error) {
	// An alias type is written as the type it stands for, so the file does not
	// depend on a CREATE TYPE it does not carry.
	rows, err := q.QueryContext(ctx, `
	  SELECT c.object_id, c.name,
	         CASE WHEN ty.is_user_defined = 1 AND ty.is_assembly_type = 0 AND ty.is_table_type = 0
	              THEN TYPE_NAME(ty.system_type_id) ELSE ty.name END,
	         c.max_length, c.precision, c.scale, c.is_nullable, c.is_identity, c.is_computed, c.is_rowguidcol,
	         ISNULL(c.collation_name, ''),
	         ISNULL(dc.name, ''), ISNULL(dc.definition, ''),
	         ISNULL(cc.definition, ''), ISNULL(cc.is_persisted, 0),
	         ISNULL(CAST(ic.seed_value AS varchar(60)), ''),
	         ISNULL(CAST(ic.increment_value AS varchar(60)), ''),
	         ISNULL(CAST(ic.last_value AS varchar(60)), '')
	  FROM sys.columns c
	  JOIN sys.objects o ON o.object_id = c.object_id AND o.type = 'U' AND o.is_ms_shipped = 0
	  JOIN sys.types ty ON ty.user_type_id = c.user_type_id
	  LEFT JOIN sys.default_constraints dc ON dc.object_id = c.default_object_id
	  LEFT JOIN sys.computed_columns cc ON cc.object_id = c.object_id AND cc.column_id = c.column_id
	  LEFT JOIN sys.identity_columns ic ON ic.object_id = c.object_id AND ic.column_id = c.column_id
	  ORDER BY c.object_id, c.column_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][]mssqlColumn{}
	for rows.Next() {
		var (
			table int64
			c     mssqlColumn
		)
		if err := rows.Scan(&table, &c.name, &c.typeName, &c.maxLength, &c.precision, &c.scale,
			&c.nullable, &c.identity, &c.computed, &c.rowGUID, &c.collation,
			&c.defaultName, &c.defaultDefinition, &c.computedDefinition, &c.persisted,
			&c.seed, &c.increment, &c.last); err != nil {
			return nil, err
		}
		out[table] = append(out[table], c)
	}
	return out, rows.Err()
}

// mssqlType puts the size back on a type name: the catalogue keeps a length
// in bytes, a precision and a scale beside it.
func mssqlType(c mssqlColumn) string {
	name := strings.ToLower(c.typeName)
	length := func(unit int) string {
		if c.maxLength < 0 {
			return name + "(max)"
		}
		return fmt.Sprintf("%s(%d)", name, c.maxLength/unit)
	}
	switch name {
	case "varchar", "char", "varbinary", "binary":
		return length(1)
	case "nvarchar", "nchar":
		return length(2)
	case "decimal", "numeric":
		return fmt.Sprintf("%s(%d,%d)", name, c.precision, c.scale)
	case "datetime2", "datetimeoffset", "time":
		return fmt.Sprintf("%s(%d)", name, c.scale)
	case "float":
		return fmt.Sprintf("float(%d)", c.precision)
	}
	return c.typeName
}

// mssqlTable builds one table's CREATE statement, the query that reads its
// rows, and what has to surround them. It also returns the text of its
// defaults, which is where a sequence the table draws from is named.
func mssqlTable(o *mssqlObject, columns []mssqlColumn, indexes []mssqlIndex, databaseCollation string) (dumpTable, []string) {
	var (
		lines, selectCols, defaults []string
		identity                    *mssqlColumn
	)
	for i := range columns {
		c := &columns[i]
		name := mssqlIdent(c.name)
		typ := mssqlType(*c)
		// A rowversion is the server's own stamp, a computed column the
		// server's own arithmetic: neither can be given a value.
		writable := !c.computed && !strings.EqualFold(c.typeName, "timestamp")
		if c.computed {
			line := "  " + name + " AS " + c.computedDefinition
			if c.persisted {
				line += " PERSISTED"
				if !c.nullable {
					line += " NOT NULL"
				}
			}
			lines = append(lines, line)
			continue
		}
		line := "  " + name + " " + typ
		if c.collation != "" && c.collation != databaseCollation {
			line += " COLLATE " + c.collation
		}
		if c.identity && numberText.MatchString(c.seed) && numberText.MatchString(c.increment) {
			line += fmt.Sprintf(" IDENTITY(%s,%s)", c.seed, c.increment)
			identity = c
		}
		if c.rowGUID {
			line += " ROWGUIDCOL"
		}
		if c.defaultDefinition != "" {
			line += " CONSTRAINT " + mssqlIdent(c.defaultName) + " DEFAULT " + c.defaultDefinition
			defaults = append(defaults, c.defaultDefinition)
		}
		if c.nullable {
			line += " NULL"
		} else {
			line += " NOT NULL"
		}
		lines = append(lines, line)
		if writable {
			selectCols = append(selectCols, name)
		}
	}
	// The primary key is part of the table: it decides how the rows are laid
	// out, and laying them out once is cheaper than rearranging them after.
	for _, ix := range indexes {
		if !ix.primary || ix.kind > 2 || len(ix.columns) == 0 {
			continue
		}
		lines = append(lines, fmt.Sprintf("  CONSTRAINT %s PRIMARY KEY %s (%s)",
			mssqlIdent(ix.name), mssqlClustering(ix.kind), mssqlKeyColumns(ix.columns)))
	}

	table := dumpTable{
		table: Table{Schema: o.schema, Name: o.name}, detail: &TableDetail{}, rel: o.rel,
		create:    rawStmt("CREATE TABLE " + o.rel + " (\n" + strings.Join(lines, ",\n") + "\n)"),
		drop:      stmt("DROP TABLE IF EXISTS " + o.rel),
		selectSQL: "SELECT " + strings.Join(selectCols, ", ") + " FROM " + o.rel,
		noData:    len(selectCols) == 0,
	}
	if identity != nil {
		// The server numbers this column itself and refuses a number given to
		// it, unless told for this one table that it will be. Only one table at
		// a time can be told so.
		table.beforeData = []dumpStatement{stmt("SET IDENTITY_INSERT " + o.rel + " ON")}
		table.afterData = []dumpStatement{stmt("SET IDENTITY_INSERT " + o.rel + " OFF")}
		if reseed := mssqlReseed(o.rel, identity.last, identity.increment); reseed != "" {
			table.afterData = append(table.afterData, stmt(reseed))
		}
	}
	return table, defaults
}

// mssqlReseed puts an identity counter back where it was. The rows alone do
// not say: the id of a row since deleted is not handed out again, so the
// counter may be ahead of the largest one present.
//
// A table that has never held a row takes the value given as its next id; one
// that has takes it as the last id used. Which this is, is only known where
// the dump is loaded.
func mssqlReseed(rel, last, increment string) string {
	current, ok := new(big.Int).SetString(last, 10)
	step, okStep := new(big.Int).SetString(increment, 10)
	if !ok || !okStep {
		return ""
	}
	next := new(big.Int).Add(current, step)
	name := mssqlString(rel)
	return fmt.Sprintf(
		"IF EXISTS (SELECT 1 FROM %s) DBCC CHECKIDENT (%s, RESEED, %s) WITH NO_INFOMSGS ELSE DBCC CHECKIDENT (%s, RESEED, %s) WITH NO_INFOMSGS",
		rel, name, current.String(), name, next.String())
}

func mssqlClustering(kind int) string {
	if kind == 1 {
		return "CLUSTERED"
	}
	return "NONCLUSTERED"
}

func mssqlKeyColumns(columns []mssqlIndexColumn) string {
	parts := []string{}
	for _, c := range columns {
		if c.included {
			continue
		}
		part := mssqlIdent(c.name)
		if c.descending {
			part += " DESC"
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, ", ")
}

func mssqlIndexes(ctx context.Context, q dumpQueryer) (map[int64][]mssqlIndex, error) {
	// A partitioned index lists its partitioning column as well; that row is
	// neither a key nor included, and is left out.
	rows, err := q.QueryContext(ctx, `
	  SELECT i.object_id, i.index_id, i.name, i.type, i.is_unique, i.is_primary_key, i.is_unique_constraint,
	         ISNULL(i.filter_definition, ''), i.is_disabled,
	         c.name, ic.is_descending_key, ic.is_included_column
	  FROM sys.indexes i
	  JOIN sys.objects o ON o.object_id = i.object_id AND o.type IN ('U', 'V') AND o.is_ms_shipped = 0
	  JOIN sys.index_columns ic ON ic.object_id = i.object_id AND ic.index_id = i.index_id
	  JOIN sys.columns c ON c.object_id = ic.object_id AND c.column_id = ic.column_id
	  WHERE i.index_id > 0 AND i.is_hypothetical = 0 AND (ic.key_ordinal > 0 OR ic.is_included_column = 1)
	  ORDER BY i.object_id, i.index_id, ic.is_included_column, ic.key_ordinal, ic.index_column_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][]mssqlIndex{}
	var (
		lastTable int64
		lastIndex = -1
	)
	for rows.Next() {
		var (
			table   int64
			indexID int
			ix      mssqlIndex
			col     mssqlIndexColumn
		)
		if err := rows.Scan(&table, &indexID, &ix.name, &ix.kind, &ix.unique, &ix.primary, &ix.uniqueConstraint,
			&ix.filter, &ix.disabled, &col.name, &col.descending, &col.included); err != nil {
			return nil, err
		}
		if table != lastTable || indexID != lastIndex {
			out[table] = append(out[table], ix)
			lastTable, lastIndex = table, indexID
		}
		list := out[table]
		list[len(list)-1].columns = append(list[len(list)-1].columns, col)
	}
	return out, rows.Err()
}

// mssqlIndexStatement writes one index or unique constraint, or says why it
// is not written.
func mssqlIndexStatement(rel string, ix mssqlIndex) (statement, skipped string) {
	switch {
	case ix.kind > 2:
		return "", "only row-store indexes are dumped"
	case ix.disabled:
		// It enforces nothing now, and the rows may be why: built again over
		// them it could refuse them.
		return "", "it is disabled"
	}
	keys := mssqlKeyColumns(ix.columns)
	if keys == "" {
		return "", ""
	}
	if ix.uniqueConstraint {
		return fmt.Sprintf("ALTER TABLE %s ADD CONSTRAINT %s UNIQUE %s (%s)",
			rel, mssqlIdent(ix.name), mssqlClustering(ix.kind), keys), ""
	}
	unique := ""
	if ix.unique {
		unique = "UNIQUE "
	}
	create := fmt.Sprintf("CREATE %s%s INDEX %s ON %s (%s)", unique, mssqlClustering(ix.kind), mssqlIdent(ix.name), rel, keys)
	var included []string
	for _, c := range ix.columns {
		if c.included {
			included = append(included, mssqlIdent(c.name))
		}
	}
	if len(included) > 0 {
		create += " INCLUDE (" + strings.Join(included, ", ") + ")"
	}
	if ix.filter != "" {
		create += " WHERE " + ix.filter
	}
	return create, ""
}

func mssqlChecks(ctx context.Context, q dumpQueryer, plan *dumpPlan, wanted map[int64]*mssqlObject) error {
	rows, err := q.QueryContext(ctx, `
	  SELECT cc.parent_object_id, cc.name, cc.definition, cc.is_disabled
	  FROM sys.check_constraints cc
	  WHERE cc.is_ms_shipped = 0
	  ORDER BY cc.parent_object_id, cc.name`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			table            int64
			name, definition string
			disabled         bool
		)
		if err := rows.Scan(&table, &name, &definition, &disabled); err != nil {
			return err
		}
		o := wanted[table]
		if o == nil || o.view {
			continue
		}
		plan.after = append(plan.after, mssqlConstraint(o.rel, name, "CHECK "+definition, disabled)...)
	}
	return rows.Err()
}

// mssqlConstraint adds a constraint as it was: checked against the rows where
// it was enforced, and switched off again where it was not.
func mssqlConstraint(rel, name, definition string, disabled bool) []dumpStatement {
	check := "WITH CHECK"
	if disabled {
		check = "WITH NOCHECK"
	}
	out := []dumpStatement{rawStmt(fmt.Sprintf("ALTER TABLE %s %s ADD CONSTRAINT %s %s", rel, check, mssqlIdent(name), definition))}
	if disabled {
		out = append(out, stmt(fmt.Sprintf("ALTER TABLE %s NOCHECK CONSTRAINT %s", rel, mssqlIdent(name))))
	}
	return out
}

// mssqlForeignKeys takes every foreign key that touches a dumped table off
// before the tables are dropped, and puts each back once all the rows are in.
//
// SQL Server has no cascading drop: a table something points at cannot be
// dropped, whichever order the tables go in when two point at each other, and
// not at all when the table pointing at it is one this dump leaves alone. Off
// first and back on last serves all three.
func mssqlForeignKeys(ctx context.Context, q dumpQueryer, plan *dumpPlan, objects []mssqlObject, wanted map[int64]*mssqlObject) error {
	byID := map[int64]*mssqlObject{}
	for i := range objects {
		byID[objects[i].id] = &objects[i]
	}
	rows, err := q.QueryContext(ctx, `
	  SELECT fk.object_id, fk.name, fk.parent_object_id, fk.referenced_object_id,
	         fk.delete_referential_action_desc, fk.update_referential_action_desc, fk.is_disabled,
	         pc.name, rc.name
	  FROM sys.foreign_keys fk
	  JOIN sys.foreign_key_columns fkc ON fkc.constraint_object_id = fk.object_id
	  JOIN sys.columns pc ON pc.object_id = fkc.parent_object_id AND pc.column_id = fkc.parent_column_id
	  JOIN sys.columns rc ON rc.object_id = fkc.referenced_object_id AND rc.column_id = fkc.referenced_column_id
	  WHERE fk.is_ms_shipped = 0
	  ORDER BY fk.parent_object_id, fk.name, fk.object_id, fkc.constraint_column_id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	type foreignKey struct {
		name               string
		parent, referenced int64
		onDelete, onUpdate string
		disabled           bool
		columns, refs      []string
	}
	var (
		keys []foreignKey
		last int64
	)
	for rows.Next() {
		var (
			id          int64
			fk          foreignKey
			column, ref string
		)
		if err := rows.Scan(&id, &fk.name, &fk.parent, &fk.referenced, &fk.onDelete, &fk.onUpdate,
			&fk.disabled, &column, &ref); err != nil {
			return err
		}
		if len(keys) == 0 || id != last {
			keys = append(keys, fk)
			last = id
		}
		k := &keys[len(keys)-1]
		k.columns = append(k.columns, mssqlIdent(column))
		k.refs = append(k.refs, mssqlIdent(ref))
	}
	if err := rows.Err(); err != nil {
		return err
	}

	parents := map[int64][]string{}
	var foreign []dumpStatement
	for _, fk := range keys {
		parent, referenced := byID[fk.parent], byID[fk.referenced]
		if parent == nil || referenced == nil || (wanted[fk.parent] == nil && wanted[fk.referenced] == nil) {
			continue
		}
		// The key is an object in its table's schema, which is how it is found.
		plan.beforeDrops = append(plan.beforeDrops, stmt(fmt.Sprintf(
			"IF OBJECT_ID(%s, 'F') IS NOT NULL ALTER TABLE %s DROP CONSTRAINT %s",
			mssqlString(mssqlRel(parent.schema, fk.name)), parent.rel, mssqlIdent(fk.name))))

		definition := fmt.Sprintf("FOREIGN KEY (%s) REFERENCES %s (%s)",
			strings.Join(fk.columns, ", "), referenced.rel, strings.Join(fk.refs, ", "))
		if action := strings.ReplaceAll(fk.onDelete, "_", " "); action != "" && action != "NO ACTION" {
			definition += " ON DELETE " + action
		}
		if action := strings.ReplaceAll(fk.onUpdate, "_", " "); action != "" && action != "NO ACTION" {
			definition += " ON UPDATE " + action
		}
		add := mssqlConstraint(parent.rel, fk.name, definition, fk.disabled)
		if wanted[fk.parent] == nil {
			// A table this dump leaves alone points at one it holds. Its key
			// was taken off so that one could be replaced, and goes back where
			// the table it belongs to is still there.
			for i := range add {
				add[i] = rawStmt(fmt.Sprintf("IF OBJECT_ID(%s, 'U') IS NOT NULL %s", mssqlString(parent.rel), add[i].sql))
			}
		} else if fk.parent != fk.referenced {
			parents[fk.parent] = append(parents[fk.parent], referenced.name)
		}
		foreign = append(foreign, add...)
	}
	ids := map[string]int64{}
	for id, o := range wanted {
		ids[o.rel] = id
	}
	for i := range plan.tables {
		plan.tables[i].parents = parents[ids[plan.tables[i].rel]]
	}
	plan.after = append(plan.after, foreign...)
	return nil
}

// mssqlSequences writes the sequences that are objects of their own, and
// where each had got to.
func mssqlSequences(ctx context.Context, q dumpQueryer, plan *dumpPlan, sel dumpSelection, schemas map[string]bool, defaults []string) error {
	const columns = `SCHEMA_NAME(s.schema_id), s.name, TYPE_NAME(s.system_type_id), s.precision,
	         CAST(s.start_value AS varchar(60)), CAST(s.increment AS varchar(60)),
	         CAST(s.minimum_value AS varchar(60)), CAST(s.maximum_value AS varchar(60)),
	         s.is_cycling, s.is_cached, ISNULL(s.cache_size, 0),
	         CAST(s.current_value AS varchar(60))`
	// last_used_value says whether a value was ever drawn; a server older
	// than 2017 does not have it.
	rows, err := q.QueryContext(ctx, `SELECT `+columns+`, CASE WHEN s.last_used_value IS NULL THEN 0 ELSE 1 END
	  FROM sys.sequences s WHERE s.is_ms_shipped = 0 ORDER BY 1, 2`)
	if err != nil {
		rows, err = q.QueryContext(ctx, `SELECT `+columns+`, 1 FROM sys.sequences s WHERE s.is_ms_shipped = 0 ORDER BY 1, 2`)
		if err != nil {
			return err
		}
	}
	defer rows.Close()
	lowered := make([]string, len(defaults))
	for i, d := range defaults {
		lowered[i] = strings.ToLower(d)
	}
	for rows.Next() {
		var (
			schema, name, typ                             string
			precision, cacheSize                          int
			start, increment, minValue, maxValue, current string
			cycling, cached, used                         bool
		)
		if err := rows.Scan(&schema, &name, &typ, &precision, &start, &increment, &minValue, &maxValue,
			&cycling, &cached, &cacheSize, &current, &used); err != nil {
			return err
		}
		if !sel.wants(schema, name) {
			continue
		}
		if sel.narrowed() && !sel.named(schema, name) {
			// Nobody's in particular, and the dump is of particular tables —
			// unless one of them draws its default from it.
			drawn := false
			for _, d := range lowered {
				drawn = drawn || containsIdentifier(d, strings.ToLower(name))
			}
			if !drawn {
				continue
			}
		}
		for _, n := range []string{start, increment, minValue, maxValue, current} {
			if !numberText.MatchString(n) {
				return fmt.Errorf("sequence %s reports %q where a number belongs", name, n)
			}
		}
		rel := mssqlRel(schema, name)
		switch strings.ToLower(typ) {
		case "decimal", "numeric":
			typ = fmt.Sprintf("%s(%d,0)", typ, precision)
		}
		create := fmt.Sprintf("CREATE SEQUENCE %s AS %s START WITH %s INCREMENT BY %s MINVALUE %s MAXVALUE %s",
			rel, typ, start, increment, minValue, maxValue)
		if cycling {
			create += " CYCLE"
		}
		switch {
		case !cached:
			create += " NO CACHE"
		case cacheSize > 0:
			create += fmt.Sprintf(" CACHE %d", cacheSize)
		}
		obj := dumpObject{rel: rel, name: name, schema: schema}
		if sel.narrowed() {
			// A table this dump leaves alone may draw from it too, and a
			// sequence something draws from cannot be dropped. It is made
			// where it is missing and left where it is not.
			obj.create = stmt(fmt.Sprintf("IF OBJECT_ID(%s, 'SO') IS NULL EXEC(%s)", mssqlString(rel), mssqlString(create)))
		} else {
			obj.drop = stmt("DROP SEQUENCE IF EXISTS " + rel)
			obj.create = stmt(create)
		}
		plan.sequences = append(plan.sequences, obj)
		schemas[schema] = true
		if next := mssqlSequenceNext(current, increment, minValue, maxValue, cycling, used); next != "" {
			plan.afterAll = append(plan.afterAll, stmt("ALTER SEQUENCE "+rel+" RESTART WITH "+next))
		}
	}
	return rows.Err()
}

// mssqlSequenceNext is the value a sequence hands out next, or nothing when
// that is still its first.
func mssqlSequenceNext(current, increment, minValue, maxValue string, cycling, used bool) string {
	if !used {
		return ""
	}
	value, ok := new(big.Int).SetString(current, 10)
	step, okStep := new(big.Int).SetString(increment, 10)
	low, okLow := new(big.Int).SetString(minValue, 10)
	high, okHigh := new(big.Int).SetString(maxValue, 10)
	if !ok || !okStep || !okLow || !okHigh {
		return ""
	}
	next := new(big.Int).Add(value, step)
	switch {
	case next.Cmp(high) > 0 && cycling:
		next = low
	case next.Cmp(low) < 0 && cycling:
		next = high
	case next.Cmp(high) > 0 || next.Cmp(low) < 0:
		// Used up. The last value it gave is the nearest thing to a next one
		// the server will accept.
		next = value
	}
	return next.String()
}

// mssqlViews writes each view after the views it reads, with the indexes that
// make it a stored one.
func mssqlViews(ctx context.Context, q dumpQueryer, plan *dumpPlan, objects []mssqlObject, wanted map[int64]*mssqlObject, indexes map[int64][]mssqlIndex) error {
	definitions := map[int64]string{}
	rows, err := q.QueryContext(ctx, `
	  SELECT m.object_id, ISNULL(m.definition, '')
	  FROM sys.sql_modules m
	  JOIN sys.objects o ON o.object_id = m.object_id AND o.type = 'V' AND o.is_ms_shipped = 0`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var (
			id         int64
			definition string
		)
		if err := rows.Scan(&id, &definition); err != nil {
			rows.Close()
			return err
		}
		definitions[id] = definition
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	deps := map[int64][]int64{}
	if rows, err := q.QueryContext(ctx, `
	  SELECT d.referencing_id, d.referenced_id
	  FROM sys.sql_expression_dependencies d
	  JOIN sys.objects o ON o.object_id = d.referencing_id AND o.type = 'V'
	  WHERE d.referenced_id IS NOT NULL AND d.referenced_id <> d.referencing_id`); err == nil {
		for rows.Next() {
			var view, ref int64
			if err := rows.Scan(&view, &ref); err == nil {
				deps[view] = append(deps[view], ref)
			}
		}
		rows.Close()
	}

	views := []dumpObject{}
	ids := []int64{}
	for i := range objects {
		o := &objects[i]
		if !o.view || wanted[o.id] == nil {
			continue
		}
		definition := strings.TrimSpace(definitions[o.id])
		if definition == "" {
			// WITH ENCRYPTION keeps the text from everybody, this dump included.
			plan.skipped = append(plan.skipped, "view "+o.rel+": its definition is not readable (encrypted, or not this login's to read)")
			continue
		}
		// The oid is the name here: two views in different schemas may share
		// one, and the ordering must not confuse them.
		obj := dumpObject{
			rel: o.rel, name: fmt.Sprintf("%d", o.id), schema: o.schema, label: o.name,
			drop: stmt("DROP VIEW IF EXISTS " + o.rel), create: rawStmt(definition),
		}
		for _, ix := range indexes[o.id] {
			if create, reason := mssqlIndexStatement(o.rel, ix); create != "" {
				obj.after = append(obj.after, rawStmt(create))
			} else if reason != "" {
				plan.skipped = append(plan.skipped, fmt.Sprintf("index %s on %s: %s", ix.name, o.rel, reason))
			}
		}
		views = append(views, obj)
		ids = append(ids, o.id)
	}
	for i, id := range ids {
		refs := append([]int64(nil), deps[id]...)
		sort.Slice(refs, func(a, b int) bool { return refs[a] < refs[b] })
		for _, ref := range refs {
			views[i].refs = append(views[i].refs, fmt.Sprintf("%d", ref))
		}
	}
	plan.views = orderObjects(views)
	return nil
}

// --- literals -------------------------------------------------------------

// mssqlColumnLiteral renders the values SQL Server's driver hands back in a
// form that says nothing of what they are: a number as the bytes of its
// digits, a uniqueidentifier and the other fixed binary types as bytes under
// a type name with no "binary" in it.
func mssqlColumnLiteral(v any, typeName string) (string, bool) {
	b, ok := v.([]byte)
	if !ok {
		return "", false
	}
	switch strings.ToUpper(typeName) {
	case "DECIMAL", "NUMERIC", "MONEY", "SMALLMONEY":
		// Quoted, it is a string the server converts by the session's
		// language; bare, it is the number.
		if numberText.MatchString(string(b)) {
			return string(b), true
		}
	case "UNIQUEIDENTIFIER", "HIERARCHYID", "GEOGRAPHY", "GEOMETRY", "UDT":
		// The bytes as the server stores them, which is the order it reads
		// them back in. Taken for text, sixteen bytes that happen to be valid
		// UTF-8 would have been written as a string.
		return dumpBytes(DriverMSSQL, b), true
	}
	return "", false
}

// mssqlTimeLiteral writes an instant as the column's type reads one.
//
// The ISO form with a T is the one spelling SQL Server reads the same way
// under every language and date-format setting. How many digits of a second
// it will take depends on the type: the old datetime refuses more than three.
func mssqlTimeLiteral(t time.Time, typeName string) string {
	layout := "2006-01-02T15:04:05.9999999"
	switch strings.ToUpper(typeName) {
	case "DATE":
		layout = "2006-01-02"
	case "TIME":
		layout = "15:04:05.9999999"
	case "SMALLDATETIME":
		layout = "2006-01-02T15:04:05"
	case "DATETIME":
		layout = "2006-01-02T15:04:05.999"
	case "DATETIMEOFFSET":
		// In its own offset: that is part of the value, and read back.
		layout = "2006-01-02T15:04:05.9999999-07:00"
	}
	return "'" + t.Format(layout) + "'"
}
