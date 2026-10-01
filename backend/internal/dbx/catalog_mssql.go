package dbx

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
)

// SQL Server answers from the sys catalogue views. They are the ones a login
// with no more than SELECT may read — the dynamic management views that would
// give tidier sizes need VIEW DATABASE STATE — and they carry what
// information_schema leaves out: identity, computed columns, filtered and
// included index columns, and the text of every module.
//
// There is no SHOW CREATE. A view, procedure, function or trigger has its
// original text in sys.sql_modules; a table, a sequence and a type do not,
// and are described from the catalogue instead.

// mssqlSchemaFilter narrows to one schema, or to every schema that is not the
// engine's own when the bound name is empty.
func mssqlSchemaFilter(alias string) string {
	return `((@p1 = '' AND ` + alias + `.name NOT IN ('sys','INFORMATION_SCHEMA')) OR ` + alias + `.name = @p1)`
}

// mssqlSchemaIs matches one schema, the login's default when none is named.
func mssqlSchemaIs(alias string) string {
	return alias + `.name = ISNULL(NULLIF(@p1, ''), SCHEMA_NAME())`
}

func (mssqlDialect) catalogSchemas(ctx context.Context, db *sql.DB) ([]CatalogSchema, string, error) {
	// The fixed database roles each own a schema of their own name. They are
	// part of every database and hold nothing, so they are marked as the
	// engine's own along with sys and INFORMATION_SCHEMA.
	rows, err := db.QueryContext(ctx, `
	  SELECT s.name,
	         ISNULL(USER_NAME(s.principal_id), ''),
	         (SELECT COUNT(*) FROM sys.objects o WHERE o.schema_id = s.schema_id AND o.type IN ('U','V')),
	         CASE WHEN s.name = SCHEMA_NAME() THEN 1 ELSE 0 END,
	         CASE WHEN s.name IN ('sys','INFORMATION_SCHEMA','guest') OR s.name LIKE 'db[_]%' THEN 1 ELSE 0 END
	  FROM sys.schemas s
	  ORDER BY 5, 1`)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	out, current := []CatalogSchema{}, ""
	for rows.Next() {
		var s CatalogSchema
		var isDefault, system int
		if err := rows.Scan(&s.Name, &s.Owner, &s.Tables, &isDefault, &system); err != nil {
			return nil, "", err
		}
		s.System = system == 1
		if isDefault == 1 {
			s.Default, current = true, s.Name
		}
		out = append(out, s)
	}
	return out, current, rows.Err()
}

func (d mssqlDialect) catalogGroups(context.Context, *sql.DB) []catalogGroup {
	return []catalogGroup{
		{GroupTables, d.tables},
		{GroupViews, d.views},
		{GroupFunctions, func(ctx context.Context, db *sql.DB, schema string, limit int) ([]CatalogObject, error) {
			return d.routines(ctx, db, schema, limit, KindFunction, "'FN','IF','TF','FS','FT'")
		}},
		{GroupProcedures, func(ctx context.Context, db *sql.DB, schema string, limit int) ([]CatalogObject, error) {
			return d.routines(ctx, db, schema, limit, KindProcedure, "'P','PC'")
		}},
		{GroupTriggers, d.triggers},
		{GroupSequences, d.sequences},
		{GroupTypes, d.types},
		{GroupSynonyms, d.synonyms},
	}
}

const mssqlDescription = `LEFT JOIN sys.extended_properties ep
	         ON ep.class = 1 AND ep.major_id = o.object_id AND ep.minor_id = 0 AND ep.name = 'MS_Description'`

func (mssqlDialect) tables(ctx context.Context, db *sql.DB, schema string, limit int) ([]CatalogObject, error) {
	return scanObjects(ctx, db, `
	  SELECT TOP (@p2) s.name, o.name,
	         ISNULL(CAST(ep.value AS NVARCHAR(4000)), ''),
	         ISNULL((SELECT SUM(p.rows) FROM sys.partitions p
	                  WHERE p.object_id = o.object_id AND p.index_id IN (0,1)), 0),
	         ISNULL((SELECT SUM(au.total_pages) * 8192 FROM sys.allocation_units au
	                  JOIN sys.partitions p2 ON p2.partition_id = au.container_id
	                 WHERE p2.object_id = o.object_id), 0)
	  FROM sys.tables o
	  JOIN sys.schemas s ON s.schema_id = o.schema_id
	  `+mssqlDescription+`
	  WHERE `+mssqlSchemaFilter("s")+`
	  ORDER BY s.name, o.name`, []any{schema, limit}, func(rows *sql.Rows) (CatalogObject, error) {
		o := CatalogObject{Kind: KindTable}
		var estimate int64
		if err := rows.Scan(&o.Schema, &o.Name, &o.Comment, &estimate, &o.Size); err != nil {
			return o, err
		}
		o.Rows = rowEstimate(estimate)
		return o, nil
	})
}

func (mssqlDialect) views(ctx context.Context, db *sql.DB, schema string, limit int) ([]CatalogObject, error) {
	return scanObjects(ctx, db, `
	  SELECT TOP (@p2) s.name, o.name, ISNULL(CAST(ep.value AS NVARCHAR(4000)), '')
	  FROM sys.views o
	  JOIN sys.schemas s ON s.schema_id = o.schema_id
	  `+mssqlDescription+`
	  WHERE `+mssqlSchemaFilter("s")+`
	  ORDER BY s.name, o.name`, []any{schema, limit}, func(rows *sql.Rows) (CatalogObject, error) {
		o := CatalogObject{Kind: KindView}
		return o, rows.Scan(&o.Schema, &o.Name, &o.Comment)
	})
}

// routines lists procedures or functions. types is a constant list of
// sys.objects type codes written by the caller above, never request text.
func (mssqlDialect) routines(ctx context.Context, db *sql.DB, schema string, limit int, kind, types string) ([]CatalogObject, error) {
	objects, err := scanObjects(ctx, db, `
	  SELECT TOP (@p2) s.name, o.name, RTRIM(o.type), ISNULL(CAST(ep.value AS NVARCHAR(4000)), '')
	  FROM sys.objects o
	  JOIN sys.schemas s ON s.schema_id = o.schema_id
	  `+mssqlDescription+`
	  WHERE o.type IN (`+types+`) AND `+mssqlSchemaFilter("s")+`
	  ORDER BY s.name, o.name`, []any{schema, limit}, func(rows *sql.Rows) (CatalogObject, error) {
		o := CatalogObject{Kind: kind, Language: "T-SQL"}
		var objectType string
		if err := rows.Scan(&o.Schema, &o.Name, &objectType, &o.Comment); err != nil {
			return o, err
		}
		switch objectType {
		case "IF", "TF", "FT":
			o.Returns, o.Detail = "TABLE", "table-valued"
		case "PC", "FS":
			o.Language = "CLR"
		}
		return o, nil
	})
	if err != nil || len(objects) == 0 {
		return objects, err
	}
	// Parameters are a second view; a login refused it still lists its
	// routines. Parameter 0 is a scalar function's return value.
	rows, err := db.QueryContext(ctx, `
	  SELECT s.name, o.name, p.parameter_id, p.name, TYPE_NAME(p.user_type_id), p.is_output
	  FROM sys.parameters p
	  JOIN sys.objects o ON o.object_id = p.object_id
	  JOIN sys.schemas s ON s.schema_id = o.schema_id
	  WHERE o.type IN (`+types+`) AND `+mssqlSchemaFilter("s")+`
	  ORDER BY s.name, o.name, p.parameter_id`, schema)
	if err != nil {
		return objects, nil
	}
	defer rows.Close()
	signatures, returns := map[catalogTable][]string{}, map[catalogTable]string{}
	for rows.Next() {
		var (
			schema, name, param string
			typ                 sql.NullString
			id                  int
			output              bool
		)
		if err := rows.Scan(&schema, &name, &id, &param, &typ, &output); err != nil {
			return objects, nil
		}
		key := catalogTable{schema, name}
		if id == 0 {
			returns[key] = typ.String
			continue
		}
		part := strings.TrimSpace(param + " " + typ.String)
		if output {
			part += " OUTPUT"
		}
		signatures[key] = append(signatures[key], part)
	}
	for i := range objects {
		key := catalogTable{objects[i].Schema, objects[i].Name}
		objects[i].Signature = strings.Join(signatures[key], ", ")
		if objects[i].Returns == "" {
			objects[i].Returns = returns[key]
		}
	}
	return objects, nil
}

func (mssqlDialect) triggers(ctx context.Context, db *sql.DB, schema string, limit int) ([]CatalogObject, error) {
	return scanObjects(ctx, db, `
	  SELECT TOP (@p2) s.name, tr.name, o.name, tr.is_disabled, tr.is_instead_of_trigger,
	         ISNULL(OBJECTPROPERTY(tr.object_id, 'ExecIsInsertTrigger'), 0),
	         ISNULL(OBJECTPROPERTY(tr.object_id, 'ExecIsUpdateTrigger'), 0),
	         ISNULL(OBJECTPROPERTY(tr.object_id, 'ExecIsDeleteTrigger'), 0)
	  FROM sys.triggers tr
	  JOIN sys.objects o ON o.object_id = tr.parent_id
	  JOIN sys.schemas s ON s.schema_id = o.schema_id
	  WHERE tr.parent_class = 1 AND `+mssqlSchemaFilter("s")+`
	  ORDER BY s.name, o.name, tr.name`, []any{schema, limit}, func(rows *sql.Rows) (CatalogObject, error) {
		o := CatalogObject{Kind: KindTrigger}
		var disabled, insteadOf bool
		var onInsert, onUpdate, onDelete int
		if err := rows.Scan(&o.Schema, &o.Name, &o.Table, &disabled, &insteadOf, &onInsert, &onUpdate, &onDelete); err != nil {
			return o, err
		}
		o.Detail = mssqlTriggerTiming(insteadOf, onInsert == 1, onUpdate == 1, onDelete == 1, disabled)
		return o, nil
	})
}

func mssqlTriggerTiming(insteadOf, onInsert, onUpdate, onDelete, disabled bool) string {
	timing := "AFTER"
	if insteadOf {
		timing = "INSTEAD OF"
	}
	events := []string{}
	if onInsert {
		events = append(events, "INSERT")
	}
	if onUpdate {
		events = append(events, "UPDATE")
	}
	if onDelete {
		events = append(events, "DELETE")
	}
	out := strings.TrimSpace(timing+" "+strings.Join(events, ", ")) + ", each statement"
	if disabled {
		out += ", disabled"
	}
	return out
}

func (mssqlDialect) sequences(ctx context.Context, db *sql.DB, schema string, limit int) ([]CatalogObject, error) {
	// increment is a sql_variant, which no driver scans without being told
	// what it holds.
	return scanObjects(ctx, db, `
	  SELECT TOP (@p2) s.name, o.name, TYPE_NAME(o.user_type_id), CAST(o.increment AS BIGINT)
	  FROM sys.sequences o
	  JOIN sys.schemas s ON s.schema_id = o.schema_id
	  WHERE `+mssqlSchemaFilter("s")+`
	  ORDER BY s.name, o.name`, []any{schema, limit}, func(rows *sql.Rows) (CatalogObject, error) {
		o := CatalogObject{Kind: KindSequence}
		var typ string
		var increment int64
		if err := rows.Scan(&o.Schema, &o.Name, &typ, &increment); err != nil {
			return o, err
		}
		o.Detail = fmt.Sprintf("%s, step %d", typ, increment)
		return o, nil
	})
}

func (mssqlDialect) types(ctx context.Context, db *sql.DB, schema string, limit int) ([]CatalogObject, error) {
	return scanObjects(ctx, db, `
	  SELECT TOP (@p2) s.name, t.name, t.is_table_type,
	         ISNULL(TYPE_NAME(t.system_type_id), ''), t.max_length, t.precision, t.scale
	  FROM sys.types t
	  JOIN sys.schemas s ON s.schema_id = t.schema_id
	  WHERE t.is_user_defined = 1 AND `+mssqlSchemaFilter("s")+`
	  ORDER BY s.name, t.name`, []any{schema, limit}, func(rows *sql.Rows) (CatalogObject, error) {
		o := CatalogObject{Kind: KindType}
		var tableType bool
		var base string
		var maxLength, precision, scale int
		if err := rows.Scan(&o.Schema, &o.Name, &tableType, &base, &maxLength, &precision, &scale); err != nil {
			return o, err
		}
		if tableType {
			o.Detail = "table type"
		} else {
			o.Detail = mssqlTypeName(base, maxLength, precision, scale)
		}
		return o, nil
	})
}

func (mssqlDialect) synonyms(ctx context.Context, db *sql.DB, schema string, limit int) ([]CatalogObject, error) {
	return scanObjects(ctx, db, `
	  SELECT TOP (@p2) s.name, o.name, o.base_object_name
	  FROM sys.synonyms o
	  JOIN sys.schemas s ON s.schema_id = o.schema_id
	  WHERE `+mssqlSchemaFilter("s")+`
	  ORDER BY s.name, o.name`, []any{schema, limit}, func(rows *sql.Rows) (CatalogObject, error) {
		o := CatalogObject{Kind: KindSynonym}
		return o, rows.Scan(&o.Schema, &o.Name, &o.Detail)
	})
}

// --- one table --------------------------------------------------------------

// mssqlTypeName puts the length, precision or scale back onto a type name read
// from sys.types. The catalogue stores them beside the name, and the bare name
// is a different type: NVARCHAR with no length is NVARCHAR(1). It spells the
// result the way the information_schema read does, so a column reads the same
// whichever view it came from.
func mssqlTypeName(name string, maxLength, precision, scale int) string {
	switch strings.ToLower(name) {
	case "nvarchar", "nchar":
		if maxLength < 0 {
			return name + "(MAX)"
		}
		// The catalogue counts bytes and these store two per character.
		return name + "(" + itoa(maxLength/2) + ")"
	case "varchar", "char", "varbinary", "binary":
		if maxLength < 0 {
			return name + "(MAX)"
		}
		return name + "(" + itoa(maxLength) + ")"
	case "decimal", "numeric":
		if scale > 0 {
			return name + "(" + itoa(precision) + "," + itoa(scale) + ")"
		}
		return name + "(" + itoa(precision) + ")"
	case "datetime2", "datetimeoffset", "time":
		// 7 is the default and is what the bare name already means.
		if scale != 7 {
			return name + "(" + itoa(scale) + ")"
		}
	}
	return name
}

var mssqlRegularNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// mssqlAliasTypeName spells an alias type the way a column declares it. SQL
// Server looks a bare type name up in dbo and in the login's default schema
// and nowhere else, so a type that lives in any other schema carries it: the
// generated CREATE TABLE named such a type bare, and replaying it failed with
// "cannot find data type". A part is bracketed only where it has to be, which
// keeps the common case reading as it was declared.
func mssqlAliasTypeName(schema, name string) string {
	part := func(p string) string {
		if mssqlRegularNameRe.MatchString(p) {
			return p
		}
		return "[" + strings.ReplaceAll(p, "]", "]]") + "]"
	}
	if schema == "" || strings.EqualFold(schema, "dbo") {
		return part(name)
	}
	return part(schema) + "." + part(name)
}

func (mssqlDialect) tableColumns(ctx context.Context, db *sql.DB, schema, table string) ([]Column, error) {
	rows, err := db.QueryContext(ctx, `
	  SELECT c.name, t.name, c.max_length, c.precision, c.scale, c.is_nullable,
	         ISNULL(dc.definition, ''), c.column_id,
	         ISNULL(CAST(ep.value AS NVARCHAR(4000)), ''),
	         c.is_identity,
	         ISNULL(CAST(ic.seed_value AS BIGINT), 1), ISNULL(CAST(ic.increment_value AS BIGINT), 1),
	         c.is_computed, ISNULL(cc.definition, ''), ISNULL(cc.is_persisted, 0),
	         t.is_user_defined, ts.name
	  FROM sys.columns c
	  JOIN sys.objects o ON o.object_id = c.object_id
	  JOIN sys.schemas s ON s.schema_id = o.schema_id
	  JOIN sys.types t ON t.user_type_id = c.user_type_id
	  JOIN sys.schemas ts ON ts.schema_id = t.schema_id
	  LEFT JOIN sys.default_constraints dc ON dc.object_id = c.default_object_id
	  LEFT JOIN sys.identity_columns ic ON ic.object_id = c.object_id AND ic.column_id = c.column_id
	  LEFT JOIN sys.computed_columns cc ON cc.object_id = c.object_id AND cc.column_id = c.column_id
	  LEFT JOIN sys.extended_properties ep
	         ON ep.class = 1 AND ep.major_id = c.object_id AND ep.minor_id = c.column_id
	        AND ep.name = 'MS_Description'
	  WHERE `+mssqlSchemaIs("s")+` AND o.name = @p2 AND o.type IN ('U','V')
	  ORDER BY c.column_id`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Column{}
	for rows.Next() {
		var (
			c                             Column
			typ, typeSchema, computed     string
			maxLength, precision, scale   int
			identity, isComputed, persist bool
			userDefined                   bool
			seed, increment               int64
		)
		if err := rows.Scan(&c.Name, &typ, &maxLength, &precision, &scale, &c.Nullable,
			&c.Default, &c.Position, &c.Comment, &identity, &seed, &increment,
			&isComputed, &computed, &persist, &userDefined, &typeSchema); err != nil {
			return nil, err
		}
		c.Type = mssqlTypeName(typ, maxLength, precision, scale)
		if userDefined {
			// An alias type is used by its own name; its length belongs to the
			// type's definition, not to the column.
			c.Type, c.TypeKind = mssqlAliasTypeName(typeSchema, typ), "domain"
		}
		if identity {
			c.Identity = fmt.Sprintf("identity(%d,%d)", seed, increment)
		}
		if isComputed {
			c.Generated, c.GeneratedKind, c.Default = computed, "virtual", ""
			if persist {
				c.GeneratedKind = "stored"
			}
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (d mssqlDialect) tableIndexes(ctx context.Context, db *sql.DB, schema, table string) ([]Index, error) {
	rows, err := db.QueryContext(ctx, `
	  SELECT s.name, i.name, i.is_unique, i.is_primary_key, i.is_unique_constraint, i.type_desc,
	         ISNULL(i.filter_definition, ''), i.is_disabled,
	         ic.is_included_column, ic.is_descending_key, c.name,
	         ISNULL((SELECT SUM(au.total_pages) * 8192 FROM sys.partitions p
	                   JOIN sys.allocation_units au ON au.container_id = p.partition_id
	                  WHERE p.object_id = i.object_id AND p.index_id = i.index_id), 0)
	  FROM sys.indexes i
	  JOIN sys.objects o ON o.object_id = i.object_id
	  JOIN sys.schemas s ON s.schema_id = o.schema_id
	  JOIN sys.index_columns ic ON ic.object_id = i.object_id AND ic.index_id = i.index_id
	  JOIN sys.columns c ON c.object_id = i.object_id AND c.column_id = ic.column_id
	  WHERE `+mssqlSchemaIs("s")+` AND o.name = @p2 AND i.name IS NOT NULL
	  ORDER BY i.name, ic.is_included_column, ic.key_ordinal, ic.index_column_id`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type building struct {
		Index
		schema       string
		keys, extras []string
	}
	order := []string{}
	byName := map[string]*building{}
	for rows.Next() {
		var (
			realSchema, name, method, filter, column string
			unique, primary, constraint, disabled    bool
			included, descending                     bool
			size                                     int64
		)
		if err := rows.Scan(&realSchema, &name, &unique, &primary, &constraint, &method, &filter, &disabled,
			&included, &descending, &column, &size); err != nil {
			return nil, err
		}
		ix, ok := byName[name]
		if !ok {
			ix = &building{schema: realSchema, Index: Index{
				Name: name, Unique: unique, Primary: primary, Method: method,
				Predicate: filter, Size: size, Invalid: disabled, Columns: []string{},
			}}
			if primary || constraint {
				ix.Constraint = name
			}
			byName[name] = ix
			order = append(order, name)
		}
		quoted, err := d.QuoteIdent(column)
		if err != nil {
			quoted = column
		}
		if included {
			ix.Include = append(ix.Include, column)
			ix.extras = append(ix.extras, quoted)
			continue
		}
		ix.Columns = append(ix.Columns, column)
		if descending {
			quoted += " DESC"
		}
		ix.keys = append(ix.keys, quoted)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]Index, 0, len(order))
	for _, name := range order {
		ix := byName[name]
		ix.Definition = mssqlIndexDefinition(d, ix.schema, table, ix.Index, ix.keys, ix.extras)
		out = append(out, ix.Index)
	}
	return out, nil
}

// mssqlIndexDefinition writes the CREATE INDEX a rowstore index would be
// recreated with. Columnstore, XML and spatial indexes take options this does
// not model, so they get no statement rather than a wrong one.
func mssqlIndexDefinition(d Dialect, schema, table string, ix Index, keys, includes []string) string {
	if ix.Method != "CLUSTERED" && ix.Method != "NONCLUSTERED" || len(keys) == 0 {
		return ""
	}
	rel, err := qualify(d, schema, table)
	if err != nil {
		return ""
	}
	name, err := d.QuoteIdent(ix.Name)
	if err != nil {
		return ""
	}
	stmt := "CREATE "
	if ix.Unique {
		stmt += "UNIQUE "
	}
	stmt += ix.Method + " INDEX " + name + " ON " + rel + " (" + strings.Join(keys, ", ") + ")"
	if len(includes) > 0 {
		stmt += " INCLUDE (" + strings.Join(includes, ", ") + ")"
	}
	if ix.Predicate != "" {
		stmt += " WHERE " + ix.Predicate
	}
	return stmt
}

func (d mssqlDialect) tableConstraints(ctx context.Context, db *sql.DB, schema, table string) ([]Constraint, error) {
	out := []Constraint{}
	// A unique constraint is the index that enforces it.
	indexes, err := d.tableIndexes(ctx, db, schema, table)
	if err != nil {
		return nil, err
	}
	for _, ix := range indexes {
		if ix.Primary || ix.Constraint == "" {
			continue
		}
		quoted := make([]string, 0, len(ix.Columns))
		for _, c := range ix.Columns {
			q, err := d.QuoteIdent(c)
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
	rows, err := db.QueryContext(ctx, `
	  SELECT cc.name, cc.definition, ISNULL(c.name, '')
	  FROM sys.check_constraints cc
	  JOIN sys.objects o ON o.object_id = cc.parent_object_id
	  JOIN sys.schemas s ON s.schema_id = o.schema_id
	  LEFT JOIN sys.columns c ON c.object_id = cc.parent_object_id AND c.column_id = cc.parent_column_id
	  WHERE `+mssqlSchemaIs("s")+` AND o.name = @p2
	  ORDER BY cc.name`, schema, table)
	if err != nil {
		return out, nil
	}
	defer rows.Close()
	for rows.Next() {
		var name, definition, column string
		if err := rows.Scan(&name, nullText{&definition}, &column); err != nil {
			return out, nil
		}
		c := Constraint{Name: name, Type: ConstraintCheck, Columns: []string{}, Definition: "CHECK " + definition}
		if column != "" {
			c.Columns = []string{column}
		}
		out = append(out, c)
	}
	return out, nil
}

func (mssqlDialect) tableReferencedBy(ctx context.Context, db *sql.DB, schema, table string) ([]IncomingForeignKey, error) {
	rows, err := db.QueryContext(ctx, `
	  SELECT fk.name, ps.name, pt.name, pc.name, rc.name,
	         fk.update_referential_action_desc, fk.delete_referential_action_desc
	  FROM sys.foreign_keys fk
	  JOIN sys.tables pt ON pt.object_id = fk.parent_object_id
	  JOIN sys.schemas ps ON ps.schema_id = pt.schema_id
	  JOIN sys.foreign_key_columns fkc ON fkc.constraint_object_id = fk.object_id
	  JOIN sys.columns pc ON pc.object_id = fkc.parent_object_id AND pc.column_id = fkc.parent_column_id
	  JOIN sys.tables rt ON rt.object_id = fk.referenced_object_id
	  JOIN sys.schemas s ON s.schema_id = rt.schema_id
	  JOIN sys.columns rc ON rc.object_id = fkc.referenced_object_id AND rc.column_id = fkc.referenced_column_id
	  WHERE `+mssqlSchemaIs("s")+` AND rt.name = @p2
	  ORDER BY ps.name, pt.name, fk.name, fkc.constraint_column_id`, schema, table)
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
		in.OnUpdate, in.OnDelete = underscoreToWords(upd), underscoreToWords(del)
	}
	return acc.slice(), rows.Err()
}

func (mssqlDialect) tableFacts(ctx context.Context, db *sql.DB, schema, table string, detail *TableDetail) error {
	var (
		objectType, owner, comment string
		rows, size                 int64
	)
	err := db.QueryRowContext(ctx, `
	  SELECT s.name, RTRIM(o.type),
	         ISNULL(USER_NAME(ISNULL(o.principal_id, s.principal_id)), ''),
	         ISNULL(CAST(ep.value AS NVARCHAR(4000)), ''),
	         ISNULL((SELECT SUM(p.rows) FROM sys.partitions p
	                  WHERE p.object_id = o.object_id AND p.index_id IN (0,1)), -1),
	         ISNULL((SELECT SUM(au.total_pages) * 8192 FROM sys.allocation_units au
	                  JOIN sys.partitions p2 ON p2.partition_id = au.container_id
	                 WHERE p2.object_id = o.object_id), 0)
	  FROM sys.objects o
	  JOIN sys.schemas s ON s.schema_id = o.schema_id
	  `+mssqlDescription+`
	  WHERE `+mssqlSchemaIs("s")+` AND o.name = @p2 AND o.type IN ('U','V')`, schema, table).
		Scan(&detail.Schema, &objectType, &owner, &comment, &rows, &size)
	if err != nil {
		return err
	}
	detail.Owner, detail.Comment, detail.Rows, detail.Size = owner, comment, rows, size
	detail.Type = TableTypeTable
	if objectType == "V" {
		detail.Type, detail.Rows = TableTypeView, -1
	}
	return nil
}

// mssqlDefaultConstraints names the default constraints on a column. SQL
// Server materialises `DEFAULT 0` as a constraint object of its own and then
// refuses to alter or drop the column while that object references it.
func mssqlDefaultConstraints(ctx context.Context, db *sql.DB, schema, table, column string) ([]string, error) {
	rows, err := db.QueryContext(ctx, `
	  SELECT dc.name
	  FROM sys.default_constraints dc
	  JOIN sys.columns c ON c.object_id = dc.parent_object_id AND c.column_id = dc.parent_column_id
	  JOIN sys.objects o ON o.object_id = dc.parent_object_id
	  JOIN sys.schemas s ON s.schema_id = o.schema_id
	  WHERE `+mssqlSchemaIs("s")+` AND o.name = @p2 AND c.name = @p3
	  ORDER BY dc.name`, schema, table, column)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	names := []string{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		names = append(names, n)
	}
	return names, rows.Err()
}

// --- definitions ------------------------------------------------------------

func (d mssqlDialect) objectDefinition(ctx context.Context, db *sql.DB, ref ObjectRef) (*ObjectDefinition, error) {
	switch ref.Kind {
	case KindView:
		return d.moduleDefinition(ctx, db, ref, "'V'")
	case KindProcedure:
		return d.moduleDefinition(ctx, db, ref, "'P','PC'")
	case KindFunction:
		return d.moduleDefinition(ctx, db, ref, "'FN','IF','TF','FS','FT'")
	case KindTrigger:
		return d.moduleDefinition(ctx, db, ref, "'TR'")
	case KindSequence:
		return d.sequenceDefinition(ctx, db, ref)
	case KindType:
		return d.typeDefinition(ctx, db, ref)
	case KindSynonym:
		return d.synonymDefinition(ctx, db, ref)
	default:
		return nil, fmt.Errorf("%w: %s", ErrNoDefinition, ref.Kind)
	}
}

// moduleDefinition reads the text SQL Server kept for a view, procedure,
// function or trigger. types is a constant list of sys.objects type codes.
func (mssqlDialect) moduleDefinition(ctx context.Context, db *sql.DB, ref ObjectRef, types string) (*ObjectDefinition, error) {
	var (
		schema, parent, comment string
		text                    sql.NullString
		created, modified       sql.NullString
	)
	err := db.QueryRowContext(ctx, `
	  SELECT s.name, m.definition, ISNULL(po.name, ''),
	         ISNULL(CAST(ep.value AS NVARCHAR(4000)), ''),
	         CONVERT(NVARCHAR(30), o.create_date, 126), CONVERT(NVARCHAR(30), o.modify_date, 126)
	  FROM sys.objects o
	  JOIN sys.schemas s ON s.schema_id = o.schema_id
	  LEFT JOIN sys.sql_modules m ON m.object_id = o.object_id
	  LEFT JOIN sys.objects po ON po.object_id = o.parent_object_id AND o.type = 'TR'
	  `+mssqlDescription+`
	  WHERE o.type IN (`+types+`) AND `+mssqlSchemaIs("s")+` AND o.name = @p2`,
		ref.Schema, ref.Name).Scan(&schema, &text, &parent, &comment, &created, &modified)
	if err == sql.ErrNoRows {
		return nil, ErrObjectNotFound
	}
	if err != nil {
		return nil, err
	}
	def := &ObjectDefinition{
		Kind: ref.Kind, Schema: schema, Name: ref.Name, Table: parent, Comment: comment,
		Definition: text.String,
		Details:    facts("Table", parent, "Created", created.String, "Modified", modified.String),
	}
	if strings.TrimSpace(text.String) == "" {
		// NULL for a CLR module, an encrypted one, and a login without VIEW
		// DEFINITION on it.
		def.Note = "The server returned no text: the module is encrypted or compiled, or this login may not view its definition."
	}
	return def, nil
}

func (d mssqlDialect) sequenceDefinition(ctx context.Context, db *sql.DB, ref ObjectRef) (*ObjectDefinition, error) {
	var (
		schema, typ                                 string
		start, increment, minimum, maximum, current int64
		cycling                                     bool
	)
	err := db.QueryRowContext(ctx, `
	  SELECT s.name, TYPE_NAME(o.user_type_id),
	         CAST(o.start_value AS BIGINT), CAST(o.increment AS BIGINT),
	         CAST(o.minimum_value AS BIGINT), CAST(o.maximum_value AS BIGINT),
	         CAST(o.current_value AS BIGINT), o.is_cycling
	  FROM sys.sequences o
	  JOIN sys.schemas s ON s.schema_id = o.schema_id
	  WHERE `+mssqlSchemaIs("s")+` AND o.name = @p2`, ref.Schema, ref.Name).
		Scan(&schema, &typ, &start, &increment, &minimum, &maximum, &current, &cycling)
	if err == sql.ErrNoRows {
		return nil, ErrObjectNotFound
	}
	if err != nil {
		return nil, err
	}
	rel, _ := qualify(d, schema, ref.Name)
	cycle := "NO CYCLE"
	if cycling {
		cycle = "CYCLE"
	}
	return &ObjectDefinition{
		Kind: KindSequence, Schema: schema, Name: ref.Name, Source: DefinitionGenerated,
		Definition: fmt.Sprintf("CREATE SEQUENCE %s\n  AS %s\n  START WITH %d\n  INCREMENT BY %d\n  MINVALUE %d\n  MAXVALUE %d\n  %s;",
			rel, typ, start, increment, minimum, maximum, cycle),
		Details: facts(
			"Type", typ,
			"Current value", fmt.Sprint(current),
			"Increment", fmt.Sprint(increment),
			"Minimum", fmt.Sprint(minimum),
			"Maximum", fmt.Sprint(maximum),
			"Start", fmt.Sprint(start),
			"Cycles", yesNo(cycling),
		),
	}, nil
}

func (d mssqlDialect) typeDefinition(ctx context.Context, db *sql.DB, ref ObjectRef) (*ObjectDefinition, error) {
	var (
		schema, base                string
		maxLength, precision, scale int
		nullable, tableType         bool
	)
	err := db.QueryRowContext(ctx, `
	  SELECT s.name, ISNULL(TYPE_NAME(t.system_type_id), ''), t.max_length, t.precision, t.scale,
	         t.is_nullable, t.is_table_type
	  FROM sys.types t
	  JOIN sys.schemas s ON s.schema_id = t.schema_id
	  WHERE t.is_user_defined = 1 AND `+mssqlSchemaIs("s")+` AND t.name = @p2`, ref.Schema, ref.Name).
		Scan(&schema, &base, &maxLength, &precision, &scale, &nullable, &tableType)
	if err == sql.ErrNoRows {
		return nil, ErrObjectNotFound
	}
	if err != nil {
		return nil, err
	}
	def := &ObjectDefinition{Kind: KindType, Schema: schema, Name: ref.Name, Source: DefinitionGenerated}
	if tableType {
		def.Note = "A table type is declared with its columns; SQL Server keeps no statement for it to hand back."
		def.Details = facts("Kind", "table type")
		return def, nil
	}
	rel, _ := qualify(d, schema, ref.Name)
	full := mssqlTypeName(base, maxLength, precision, scale)
	null := " NOT NULL"
	if nullable {
		null = " NULL"
	}
	def.Definition = "CREATE TYPE " + rel + " FROM " + full + null + ";"
	def.Details = facts("Base type", full, "Nullable", yesNo(nullable))
	return def, nil
}

func (d mssqlDialect) synonymDefinition(ctx context.Context, db *sql.DB, ref ObjectRef) (*ObjectDefinition, error) {
	var schema, target string
	err := db.QueryRowContext(ctx, `
	  SELECT s.name, o.base_object_name
	  FROM sys.synonyms o
	  JOIN sys.schemas s ON s.schema_id = o.schema_id
	  WHERE `+mssqlSchemaIs("s")+` AND o.name = @p2`, ref.Schema, ref.Name).Scan(&schema, &target)
	if err == sql.ErrNoRows {
		return nil, ErrObjectNotFound
	}
	if err != nil {
		return nil, err
	}
	rel, _ := qualify(d, schema, ref.Name)
	return &ObjectDefinition{
		Kind: KindSynonym, Schema: schema, Name: ref.Name, Source: DefinitionGenerated,
		Definition: "CREATE SYNONYM " + rel + " FOR " + target + ";",
		Details:    facts("Refers to", target),
	}, nil
}
