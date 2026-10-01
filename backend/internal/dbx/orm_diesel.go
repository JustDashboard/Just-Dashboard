package dbx

import (
	"fmt"
	"strings"
)

// Diesel.
//
// Two files, as a Diesel project has them. schema.rs is what `diesel
// print-schema` writes: a table! per table, a marker type for each SQL type
// Diesel does not know, joinable! where a foreign key gives two tables one
// obvious join. models.rs is the Queryable structs that read from it, plus a
// Rust enum with its FromSql and ToSql for every database enum, since a
// struct with an enum column does not compile without them.
//
// Diesel needs a primary key to describe a table at all, so a table without
// one is reported and left out, as print-schema does.

type dieselGen struct {
	*ormGen
	names *ormNaming
	// tableIdent and colIdent are the names the table! macro declares.
	tableIdent map[*ormTable]string
	colIdent   map[*ormCol]string
	// module is the Rust module a table's table! lives in: "" for the crate's
	// schema root, or the schema's name.
	module  map[*ormTable]string
	modules []string
	// custom are the marker types declared per module, in order of first use.
	custom      map[string]map[string]dieselCustom
	customOrder map[string][]string
	tables      []*ormTable
	backend     string
	features    map[string]bool
}

type dieselCustom struct {
	attr string // the #[diesel(...)] line naming the database type
}

// rustIdent makes a name the table! macro can declare: lower case, as Rust
// names modules and fields, with a reserved word given a trailing underscore.
// The real name is kept beside it in #[sql_name].
func rustIdent(name string) string {
	s := strings.ToLower(sanitizeIdent(name))
	if rustReserved[s] {
		return s + "_"
	}
	return s
}

func generateDiesel(g *ormGen) []ORMFile {
	d := &dieselGen{
		ormGen: g, tableIdent: map[*ormTable]string{}, colIdent: map[*ormCol]string{},
		module: map[*ormTable]string{}, custom: map[string]map[string]dieselCustom{},
		customOrder: map[string][]string{}, features: map[string]bool{},
	}
	d.backend = map[Driver]string{
		DriverMySQL: "diesel::mysql::Mysql", DriverSQLite: "diesel::sqlite::Sqlite",
	}[g.driver]
	if d.backend == "" {
		d.backend = "diesel::pg::Pg"
	}
	d.names = g.names(ormNameRules{
		model: func(t string) string { return pascal(singular(t)) },
		field: sanitizeIdent,
		enum:  pascal,
		top:   strings.Fields("Option String Vec Box Result Self"),
	})

	idents := map[string]*ormNamer{}
	for _, m := range g.models {
		if len(m.PrimaryKey) == 0 {
			g.warn("%s has no primary key. Diesel cannot describe a table without one, so it was left out.", g.label(m))
			continue
		}
		mod := d.moduleFor(m.Schema, g.qualified(m))
		d.module[m] = mod
		if idents[mod] == nil {
			idents[mod] = newORMNamer(false, "sql_types")
		}
		d.tableIdent[m] = idents[mod].take(rustIdent(m.Name))
		cols := newORMNamer(false, d.tableIdent[m], "table", "all_columns", "star", "dsl", "columns")
		for _, c := range m.cols {
			d.colIdent[c] = cols.take(rustIdent(c.Name), rustIdent(c.Name)+"_")
		}
		// table! is generated for a fixed number of columns; wider tables are
		// behind a feature flag.
		switch n := len(m.cols); {
		case n > 128:
			g.warn("%s has %d columns, more than Diesel's table! macro supports (128); it was left out.", g.label(m), n)
			continue
		case n > 64:
			d.features["128-column-tables"] = true
		case n > 32:
			d.features["64-column-tables"] = true
		}
		d.tables = append(d.tables, m)
	}
	if len(d.tables) == 0 {
		return nil
	}

	schema := d.schemaFile()
	models := d.modelsFile()
	return []ORMFile{
		{Filename: "schema.rs", Content: schema},
		{Filename: "models.rs", Content: models},
	}
}

// moduleFor is the Rust module a schema's declarations live in. PostgreSQL's
// default schema is the root, as print-schema has it; any other schema that
// has to be named gets a module of its own, and with tables in several MySQL
// databases every one of them does.
func (d *dieselGen) moduleFor(schema string, qualified bool) string {
	if !qualified || schema == "" || (d.driver == DriverPostgres && schema == d.defaultSchema) {
		return ""
	}
	mod := rustIdent(schema)
	for _, known := range d.modules {
		if known == mod {
			return mod
		}
	}
	d.modules = append(d.modules, mod)
	return mod
}

// markerPath is how a table! in one module names a marker type in another.
func markerPath(from, mod, name string) string {
	switch {
	case from == mod:
		return "super::sql_types::" + name
	case mod == "":
		return "crate::schema::sql_types::" + name
	}
	return "crate::schema::" + mod + "::sql_types::" + name
}

// path is the Rust path to a table's module from the crate root.
func (d *dieselGen) path(m *ormTable) string {
	if mod := d.module[m]; mod != "" {
		return "crate::schema::" + mod + "::" + d.tableIdent[m]
	}
	return "crate::schema::" + d.tableIdent[m]
}

// customType declares a marker type in a module's sql_types for a database
// type Diesel has no name for, and returns its name.
func (d *dieselGen) customType(mod, name, attr string) string {
	if d.custom[mod] == nil {
		d.custom[mod] = map[string]dieselCustom{}
	}
	if _, ok := d.custom[mod][name]; !ok {
		d.custom[mod][name] = dieselCustom{attr: attr}
		d.customOrder[mod] = append(d.customOrder[mod], name)
	}
	return name
}

// sqlType maps a column onto the diesel::sql_types name the table! macro
// takes, and onto the Rust type a model field has. rust is "" where there is
// no Rust type to give it; the column is then in the schema and out of the
// struct.
func (d *dieselGen) sqlType(m *ormTable, c *ormCol) (sql, rust string, maxLength int) {
	sql, rust, maxLength, _ = d.sqlTypeIn(m, c)
	return sql, rust, maxLength
}

// sqlTypeIn is sqlType, also saying which module's sql_types holds the marker
// when the type is one Diesel does not know: marker is "" for a built-in type.
func (d *dieselGen) sqlTypeIn(m *ormTable, c *ormCol) (sql, rust string, maxLength int, marker string) {
	t := c.t
	mod := d.module[m]
	defer func() {
		if t.Array && d.driver == DriverPostgres && !strings.HasPrefix(sql, "Array<") {
			// PostgreSQL cannot promise an array has no NULL elements, so
			// Diesel types every element as nullable.
			sql = "Array<Nullable<" + sql + ">>"
			if rust != "" {
				rust = "Vec<Option<" + rust + ">>"
			}
		}
	}()
	// custom declares a marker for a type Diesel has no name for. On
	// PostgreSQL the marker names the type in the catalogue; on MySQL it names
	// the wire type the column travels as, which is all Diesel's MySQL backend
	// distinguishes.
	custom := func(name string) (string, string, int, string) {
		if i := strings.IndexByte(name, '('); i >= 0 {
			name = name[:i]
		}
		name = strings.TrimSpace(name)
		attr := fmt.Sprintf("#[diesel(postgres_type(name = %s))]", rustString(name))
		if d.driver == DriverMySQL {
			wire := "String"
			switch {
			case t.Kind == ormSet:
				wire = "Set"
			case t.Kind == ormYear:
				wire = "Short"
			case t.Kind == ormBit || t.Name == "bit":
				wire = "Bit"
			case t.Kind == ormGeo:
				wire = "Blob"
			}
			attr = fmt.Sprintf("#[diesel(mysql_type(name = %s))]", rustString(wire))
		}
		ident := pascal(name)
		return d.customType(mod, ident, attr), "", 0, markerPath(mod, mod, ident)
	}
	if t.Enum != nil && t.Kind == ormEnumKind {
		name := d.names.enum[t.Enum]
		// A named enum's marker lives with its own schema, so two tables in
		// different schemas that share the enum share one marker; an enum
		// spelt inside a column type lives with that column's table.
		enumMod := mod
		if !t.Enum.inline {
			enumMod = d.moduleFor(t.Enum.Schema, d.multiSchema || t.Enum.Schema != d.defaultSchema)
		}
		attr := fmt.Sprintf("#[diesel(postgres_type(name = %s))]", rustString(t.Enum.Name))
		if t.Enum.Schema != "" && t.Enum.Schema != d.defaultSchema && d.driver == DriverPostgres {
			attr = fmt.Sprintf("#[diesel(postgres_type(name = %s, schema = %s))]", rustString(t.Enum.Name), rustString(t.Enum.Schema))
		}
		if d.driver == DriverMySQL {
			attr = `#[diesel(mysql_type(name = "Enum"))]`
		}
		// The Rust enum of the same name lives in models.rs.
		sql = d.customType(enumMod, name, attr)
		rust = name
		marker = markerPath(mod, enumMod, name)
	} else {
		switch d.driver {
		case DriverPostgres:
			switch t.Kind {
			case ormBool:
				sql, rust = "Bool", "bool"
			case ormInt16:
				sql, rust = "Int2", "i16"
			case ormInt32:
				sql, rust = "Int4", "i32"
			case ormInt64:
				sql, rust = "Int8", "i64"
				if t.Name == "oid" {
					sql, rust = "Oid", "u32"
				}
			case ormFloat32:
				sql, rust = "Float4", "f32"
			case ormFloat64:
				sql, rust = "Float8", "f64"
			case ormDecimal:
				sql, rust = "Numeric", "bigdecimal::BigDecimal"
				d.features["numeric"] = true
			case ormMoney:
				sql, rust = "Money", "diesel::pg::data_types::PgMoney"
			case ormChar:
				sql, rust, maxLength = "Bpchar", "String", t.Length
			case ormVarchar:
				sql, rust, maxLength = "Varchar", "String", t.Length
			case ormText:
				sql, rust = "Text", "String"
				if t.Name != "text" {
					return custom(t.Name)
				}
			case ormUUID:
				sql, rust = "Uuid", "uuid::Uuid"
				d.features["uuid"] = true
			case ormJSON:
				sql, rust = "Jsonb", "serde_json::Value"
				if t.Name == "json" {
					sql = "Json"
				}
				d.features["serde_json"] = true
			case ormBytes:
				sql, rust = "Bytea", "Vec<u8>"
			case ormDate:
				sql, rust = "Date", "chrono::NaiveDate"
				d.features["chrono"] = true
			case ormTime:
				if t.TZ {
					return custom("timetz")
				}
				sql, rust = "Time", "chrono::NaiveTime"
				d.features["chrono"] = true
			case ormDateTime:
				sql, rust = "Timestamp", "chrono::NaiveDateTime"
				d.features["chrono"] = true
			case ormDateTimeTZ:
				sql, rust = "Timestamptz", "chrono::DateTime<chrono::Utc>"
				d.features["chrono"] = true
			case ormInterval:
				sql, rust = "Interval", "diesel::pg::data_types::PgInterval"
			case ormNet:
				switch t.Name {
				case "inet":
					sql, rust = "Inet", "ipnetwork::IpNetwork"
					d.features["network-address"] = true
				case "cidr":
					sql, rust = "Cidr", "ipnetwork::IpNetwork"
					d.features["network-address"] = true
				case "macaddr":
					sql, rust = "Macaddr", "[u8; 6]"
				default:
					return custom(t.Name)
				}
			default:
				return custom(t.Name)
			}
		case DriverMySQL:
			unsigned := func(sql, signed, uns string) (string, string) {
				if t.Unsigned {
					return "Unsigned<" + sql + ">", uns
				}
				return sql, signed
			}
			switch t.Kind {
			case ormBool:
				sql, rust = "Bool", "bool"
				if t.Name == "bit" {
					return custom("bit")
				}
			case ormInt8:
				sql, rust = unsigned("Tinyint", "i8", "u8")
			case ormInt16:
				sql, rust = unsigned("Smallint", "i16", "u16")
			case ormInt32:
				sql, rust = unsigned("Integer", "i32", "u32")
			case ormInt64:
				sql, rust = unsigned("Bigint", "i64", "u64")
			case ormFloat32:
				sql, rust = "Float", "f32"
			case ormFloat64:
				sql, rust = "Double", "f64"
			case ormDecimal:
				sql, rust = "Decimal", "bigdecimal::BigDecimal"
				d.features["numeric"] = true
			case ormChar:
				sql, rust, maxLength = "Char", "String", t.Length
			case ormVarchar:
				sql, rust, maxLength = "Varchar", "String", t.Length
			case ormText:
				sql, rust = pascal(t.Name), "String"
			case ormJSON:
				sql, rust = "Json", "serde_json::Value"
				d.features["serde_json"] = true
			case ormBytes:
				sql, rust = pascal(t.Name), "Vec<u8>"
				if t.Name == "binary" || t.Name == "varbinary" {
					maxLength = t.Length
				}
			case ormDate:
				sql, rust = "Date", "chrono::NaiveDate"
				d.features["chrono"] = true
			case ormTime:
				sql, rust = "Time", "chrono::NaiveTime"
				d.features["chrono"] = true
			case ormDateTime:
				sql, rust = "Datetime", "chrono::NaiveDateTime"
				d.features["chrono"] = true
			case ormDateTimeTZ:
				sql, rust = "Timestamp", "chrono::NaiveDateTime"
				d.features["chrono"] = true
			case ormSet:
				return custom("set")
			case ormYear:
				return custom("year")
			default:
				return custom(t.Name)
			}
		default: // SQLite
			switch t.Kind {
			case ormBool:
				sql, rust = "Bool", "bool"
			case ormInt8, ormInt16, ormInt32, ormYear:
				sql, rust = "Integer", "i32"
			case ormInt64:
				sql, rust = "BigInt", "i64"
			case ormFloat32:
				sql, rust = "Float", "f32"
			case ormFloat64, ormDecimal, ormMoney:
				// SQLite's numeric affinity is a float once it has a fraction.
				sql, rust = "Double", "f64"
			case ormBytes:
				sql, rust = "Binary", "Vec<u8>"
			case ormDate:
				sql, rust = "Date", "chrono::NaiveDate"
				d.features["chrono"] = true
			case ormTime:
				sql, rust = "Time", "chrono::NaiveTime"
				d.features["chrono"] = true
			case ormDateTime, ormDateTimeTZ:
				sql, rust = "Timestamp", "chrono::NaiveDateTime"
				d.features["chrono"] = true
			default:
				sql, rust = "Text", "String"
			}
		}
	}
	return sql, rust, maxLength, marker
}

func (d *dieselGen) schemaFile() string {
	// Tables first: rendering them is what discovers the marker types.
	rendered := map[string][]string{}
	for _, m := range d.tables {
		rendered[d.module[m]] = append(rendered[d.module[m]], d.tableMacro(m))
	}

	var b strings.Builder
	b.WriteString("// Generated by Just Dashboard from live database introspection.\n")
	b.WriteString("// The shape `diesel print-schema` writes. A reviewed starting point: check it\n")
	b.WriteString("// against your migrations before relying on it.\n")

	block := func(mod, indent string) {
		if names := d.customOrder[mod]; len(names) > 0 {
			fmt.Fprintf(&b, "\n%spub mod sql_types {\n", indent)
			for i, name := range names {
				if i > 0 {
					b.WriteString("\n")
				}
				fmt.Fprintf(&b, "%s    #[derive(diesel::query_builder::QueryId, diesel::sql_types::SqlType)]\n", indent)
				fmt.Fprintf(&b, "%s    %s\n", indent, d.custom[mod][name].attr)
				fmt.Fprintf(&b, "%s    pub struct %s;\n", indent, name)
			}
			fmt.Fprintf(&b, "%s}\n", indent)
		}
		for _, t := range rendered[mod] {
			b.WriteString("\n")
			for _, line := range strings.Split(strings.TrimRight(t, "\n"), "\n") {
				if line == "" {
					b.WriteString("\n")
				} else {
					b.WriteString(indent + line + "\n")
				}
			}
		}
		d.joins(&b, mod, indent)
	}
	block("", "")
	for _, mod := range d.modules {
		fmt.Fprintf(&b, "\npub mod %s {", mod)
		block(mod, "    ")
		b.WriteString("}\n")
	}
	return b.String()
}

func (d *dieselGen) tableMacro(m *ormTable) string {
	mod := d.module[m]
	type column struct {
		attrs []string
		decl  string
	}
	var cols []column
	var markers []string
	seenMarker := map[string]bool{}
	for _, c := range m.cols {
		sql, rust, maxLength, marker := d.sqlTypeIn(m, c)
		if rust == "" {
			d.warn("%s.%s has type %s, which Diesel has no Rust type for; it is in schema.rs and left out of the model struct.", d.label(m), c.Name, c.Type)
		}
		if marker != "" && !seenMarker[marker] {
			seenMarker[marker] = true
			markers = append(markers, marker)
		}
		if c.Nullable {
			sql = "Nullable<" + sql + ">"
		}
		var attrs []string
		if d.colIdent[c] != c.Name {
			attrs = append(attrs, fmt.Sprintf("#[sql_name = %s]", rustString(c.Name)))
		}
		if maxLength > 0 {
			attrs = append(attrs, fmt.Sprintf("#[max_length = %d]", maxLength))
		}
		cols = append(cols, column{attrs: attrs, decl: fmt.Sprintf("%s -> %s,", d.colIdent[c], sql)})
	}

	var b strings.Builder
	b.WriteString("diesel::table! {\n    use diesel::sql_types::*;\n")
	for _, marker := range markers {
		fmt.Fprintf(&b, "    use %s;\n", marker)
	}
	b.WriteString("\n")
	if d.tableIdent[m] != m.Name {
		fmt.Fprintf(&b, "    #[sql_name = %s]\n", rustString(m.Name))
	}
	keys := make([]string, len(m.PrimaryKey))
	for i, col := range m.PrimaryKey {
		keys[i] = d.colIdent[m.byCol[col]]
	}
	prefix := ""
	if mod != "" {
		prefix = mod + "."
		if rustIdent(m.Schema) != m.Schema {
			// The schema's real name is not an identifier Rust accepts, and
			// table! has no way to rename a schema.
			d.warn("Schema %s is not a name Diesel's table! macro can write; %s is declared under %s, which will not match the database.", m.Schema, d.label(m), mod)
		}
	}
	fmt.Fprintf(&b, "    %s%s (%s) {\n", prefix, d.tableIdent[m], strings.Join(keys, ", "))
	for _, c := range cols {
		for _, a := range c.attrs {
			b.WriteString("        " + a + "\n")
		}
		b.WriteString("        " + c.decl + "\n")
	}
	b.WriteString("    }\n}\n")
	return b.String()
}

// joinPairs are the foreign keys Diesel can treat as the one join between two
// tables: a single column, pointing at the parent's primary key, the only key
// between that pair, inside one module.
func (d *dieselGen) joinPairs(mod string) []*ormRel {
	var out []*ormRel
	for _, m := range d.tables {
		if d.module[m] != mod {
			continue
		}
		count := map[*ormTable]int{}
		for _, r := range m.rels {
			count[r.to]++
		}
		for _, r := range m.rels {
			_, described := d.tableIdent[r.to]
			if !described || r.self || d.module[r.to] != mod || count[r.to] != 1 || len(r.fk.Columns) != 1 ||
				len(r.to.PrimaryKey) != 1 || r.fk.RefColumns[0] != r.to.PrimaryKey[0] {
				continue
			}
			out = append(out, r)
		}
	}
	return out
}

func (d *dieselGen) joins(b *strings.Builder, mod, indent string) {
	pairs := d.joinPairs(mod)
	if len(pairs) > 0 {
		b.WriteString("\n")
	}
	for _, r := range pairs {
		fmt.Fprintf(b, "%sdiesel::joinable!(%s -> %s (%s));\n", indent,
			d.tableIdent[r.from], d.tableIdent[r.to], d.colIdent[r.from.byCol[r.fk.Columns[0]]])
	}
	var names []string
	for _, m := range d.tables {
		if d.module[m] == mod {
			names = append(names, d.tableIdent[m])
		}
	}
	if len(names) > 1 {
		fmt.Fprintf(b, "\n%sdiesel::allow_tables_to_appear_in_same_query!(\n", indent)
		for _, n := range names {
			fmt.Fprintf(b, "%s    %s,\n", indent, n)
		}
		fmt.Fprintf(b, "%s);\n", indent)
	}
}

func (d *dieselGen) modelsFile() string {
	var body strings.Builder
	for _, e := range d.enums {
		body.WriteString("\n" + d.rustEnum(e))
	}
	belongs := map[*ormRel]bool{}
	for _, mod := range append([]string{""}, d.modules...) {
		for _, r := range d.joinPairs(mod) {
			belongs[r] = true
		}
	}
	for _, m := range d.tables {
		body.WriteString("\n" + d.model(m, belongs))
	}

	var b strings.Builder
	b.WriteString("// Generated by Just Dashboard from live database introspection.\n")
	b.WriteString("// Queryable structs for the tables in schema.rs. A reviewed starting point.\n")
	if features := ormSortedKeys(d.features); len(features) > 0 {
		fmt.Fprintf(&b, "// These column types need Diesel's %s feature%s.\n",
			strings.Join(features, ", "), map[bool]string{true: "s", false: ""}[len(features) > 1])
	}
	b.WriteString("\nuse diesel::prelude::*;\n")
	b.WriteString(body.String())
	return b.String()
}

// rustVariants gives each enum label a variant name.
func rustVariants(values []string) [][2]string {
	used := newORMNamer(false, "Self")
	out := make([][2]string, 0, len(values))
	for _, v := range values {
		name := pascal(v)
		if len(ormIdentWords(v)) == 0 {
			name = "Empty"
		}
		out = append(out, [2]string{used.take(name), v})
	}
	return out
}

// rustEnum declares a database enum as a Rust enum that Diesel can read and
// write: the two impls are what `diesel print-schema` leaves to the reader,
// and without them no struct with an enum column compiles.
func (d *dieselGen) rustEnum(e *ormEnum) string {
	name := d.names.enum[e]
	// The marker type it converts to and from, in whichever module first used it.
	marker := ""
	for _, mod := range append([]string{""}, d.modules...) {
		if _, ok := d.custom[mod][name]; ok {
			marker = "crate::schema::sql_types::" + name
			if mod != "" {
				marker = "crate::schema::" + mod + "::sql_types::" + name
			}
			break
		}
	}
	if marker == "" {
		return ""
	}
	value := "diesel::pg::PgValue<'_>"
	if d.driver == DriverMySQL {
		value = "diesel::mysql::MysqlValue<'_>"
	}
	variants := rustVariants(e.Values)

	var b strings.Builder
	b.WriteString("#[derive(Debug, Clone, Copy, PartialEq, Eq, diesel::expression::AsExpression, diesel::deserialize::FromSqlRow)]\n")
	fmt.Fprintf(&b, "#[diesel(sql_type = %s)]\n", marker)
	fmt.Fprintf(&b, "pub enum %s {\n", name)
	for _, v := range variants {
		fmt.Fprintf(&b, "    %s,\n", v[0])
	}
	b.WriteString("}\n\n")

	fmt.Fprintf(&b, "impl diesel::serialize::ToSql<%s, %s> for %s {\n", marker, d.backend, name)
	fmt.Fprintf(&b, "    fn to_sql<'b>(&'b self, out: &mut diesel::serialize::Output<'b, '_, %s>) -> diesel::serialize::Result {\n", d.backend)
	b.WriteString("        use std::io::Write;\n        match *self {\n")
	for _, v := range variants {
		fmt.Fprintf(&b, "            %s::%s => out.write_all(%s)?,\n", name, v[0], rustBytes(v[1]))
	}
	b.WriteString("        }\n        Ok(diesel::serialize::IsNull::No)\n    }\n}\n\n")

	fmt.Fprintf(&b, "impl diesel::deserialize::FromSql<%s, %s> for %s {\n", marker, d.backend, name)
	fmt.Fprintf(&b, "    fn from_sql(value: %s) -> diesel::deserialize::Result<Self> {\n", value)
	b.WriteString("        match value.as_bytes() {\n")
	for _, v := range variants {
		fmt.Fprintf(&b, "            %s => Ok(%s::%s),\n", rustBytes(v[1]), name, v[0])
	}
	fmt.Fprintf(&b, "            _ => Err(%s.into()),\n", rustString("unrecognized "+e.Name+" value"))
	b.WriteString("        }\n    }\n}\n")
	return b.String()
}

func (d *dieselGen) model(m *ormTable, belongs map[*ormRel]bool) string {
	name := d.names.model[m]
	derives := []string{"Queryable", "Selectable", "Identifiable"}
	var attrs []string
	attrs = append(attrs, fmt.Sprintf("#[diesel(table_name = %s)]", d.path(m)))
	keys := make([]string, len(m.PrimaryKey))
	for i, col := range m.PrimaryKey {
		keys[i] = d.colIdent[m.byCol[col]]
	}
	if len(keys) != 1 || keys[0] != "id" {
		attrs = append(attrs, fmt.Sprintf("#[diesel(primary_key(%s))]", strings.Join(keys, ", ")))
	}
	for _, r := range m.rels {
		if !belongs[r] {
			continue
		}
		if len(derives) == 3 {
			derives = append(derives, "Associations")
		}
		attrs = append(attrs, fmt.Sprintf("#[diesel(belongs_to(%s, foreign_key = %s))]",
			d.names.model[r.to], d.colIdent[m.byCol[r.fk.Columns[0]]]))
	}
	attrs = append(attrs, fmt.Sprintf("#[diesel(check_for_backend(%s))]", d.backend))

	var b strings.Builder
	if m.Comment != "" {
		fmt.Fprintf(&b, "/// %s\n", ormOneLine(m.Comment))
	}
	fmt.Fprintf(&b, "#[derive(%s, Debug)]\n", strings.Join(derives, ", "))
	for _, a := range attrs {
		b.WriteString(a + "\n")
	}
	fmt.Fprintf(&b, "pub struct %s {\n", name)
	for _, c := range m.cols {
		_, rust, _ := d.sqlType(m, c)
		if rust == "" {
			fmt.Fprintf(&b, "    // %s (%s) has no Rust type here and is not selected.\n", ormOneLine(c.Name), ormOneLine(c.Type))
			continue
		}
		if c.Nullable {
			rust = "Option<" + rust + ">"
		}
		fmt.Fprintf(&b, "    pub %s: %s,\n", d.colIdent[c], rust)
	}
	b.WriteString("}\n")
	return b.String()
}
