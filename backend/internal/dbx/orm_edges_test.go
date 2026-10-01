package dbx

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

// The schemas nobody designs on purpose.
//
// The shop fixtures are awkward the way a real application is. These are the
// cases a real application reaches by accident: a default that looks like a
// call and is a string, an enum kept in a schema of its own, a table called
// "indexes", a column called __proto__, a default with a colon in it.

// MySQL prints 'rgb(0,0,0)' and (json_object()) the same way, bare, and says
// which is which in another column. Reading the first as a call tells an ORM
// the default is an expression; writing the second back without parentheses
// is a statement MySQL refuses.
func TestORMMySQLDefaultsAreWhatTheCatalogueSays(t *testing.T) {
	for _, e := range []ormExpectation{
		{
			name: "prisma", engine: "mysql", req: ORMRequest{Target: ORMPrisma, Schema: "blog"},
			must: []string{
				`colour String @default("rgb(0,0,0)") @db.VarChar(32)`,
				`details Json? @default(dbgenerated("(json_object())"))`,
			},
			mustNot: []string{`dbgenerated("rgb(0,0,0)")`, `dbgenerated("json_object()")`},
		},
		{
			name: "drizzle", engine: "mysql", req: ORMRequest{Target: ORMDrizzle},
			must:    []string{`.default("rgb(0,0,0)")`, "json(\"details\").default(sql`(json_object())`)"},
			mustNot: []string{"sql`rgb(0,0,0)`", "sql`json_object()`"},
		},
		{
			name: "sqlalchemy", engine: "mysql", req: ORMRequest{Target: ORMSQLAlchemy},
			must:    []string{`server_default=text("'rgb(0,0,0)'")`, `server_default=text("(json_object())")`},
			mustNot: []string{`text("rgb(0,0,0)")`, `text("json_object()")`},
		},
		{
			// GORM takes a tag default with parentheses in it for an expression,
			// so the string has to arrive quoted and the call bare.
			name: "gorm", engine: "mysql", req: ORMRequest{Target: ORMGorm},
			must:    []string{`default:'rgb(0,0,0)'"`, `default:(json_object())"`},
			mustNot: []string{`default:rgb(0,0,0)`},
		},
		{
			name: "typeorm", engine: "mysql", req: ORMRequest{Target: ORMTypeORM},
			must: []string{`default: "rgb(0,0,0)"`, `default: () => "(json_object())"`},
		},
	} {
		t.Run(e.name, e.run)
	}

	// A value with a backslash in it, written back as SQL: MySQL reads the
	// backslash as an escape, so it is doubled like the quote.
	col := &ormCol{ORMColumn: &ORMColumn{Name: "c", Type: "varchar(64)", Default: `C:\it's\`}}
	col.t = parseORMType(DriverMySQL, col.Type)
	def := parseORMDefault(DriverMySQL, "", true, col)
	if def.Kind != ormDefString || def.Text != `C:\it's\` {
		t.Fatalf("default = kind %d text %q", def.Kind, def.Text)
	}
	if got, want := def.sql(DriverMySQL), `'C:\\it''s\\'`; got != want {
		t.Errorf("as SQL = %s, want %s", got, want)
	}
}

// A type that is passed through to the target keeps what is inside its quotes.
// Lower-cased, enum('Draft') declares a label the database does not have, and
// "MyShape" names a type that does not exist.
func TestORMPassedThroughTypesKeepTheirQuotedParts(t *testing.T) {
	for _, c := range []struct{ in, lower, upper string }{
		{"character varying(20)", "character varying(20)", "CHARACTER VARYING(20)"},
		{"enum('Draft','It''s')", "enum('Draft','It''s')", "ENUM('Draft','It''s')"},
		{"SET('Red','Green')", "set('Red','Green')", "SET('Red','Green')"},
		{`"MyShape"`, `"MyShape"`, `"MyShape"`},
		{`"Geo".Point3D[]`, `"Geo".point3d[]`, `"Geo".POINT3D[]`},
		{"DateTime64(3, 'UTC')", "datetime64(3, 'UTC')", "DATETIME64(3, 'UTC')"},
		{`Enum8('it\'s A' = 1)`, `enum8('it\'s A' = 1)`, `ENUM8('it\'s A' = 1)`},
		{"`Odd` Type", "`Odd` type", "`Odd` TYPE"},
		{"Broken 'Quote", "broken 'Quote", "BROKEN 'Quote"},
	} {
		if got := ormTypeFold(c.in, strings.ToLower); got != c.lower {
			t.Errorf("lower %q = %q, want %q", c.in, got, c.lower)
		}
		if got := ormTypeFold(c.in, strings.ToUpper); got != c.upper {
			t.Errorf("upper %q = %q, want %q", c.in, got, c.upper)
		}
	}

	for _, e := range []ormExpectation{
		{
			name: "gorm", engine: "mysql", req: ORMRequest{Target: ORMGorm},
			must: []string{`type:enum('Public','Members')`, `EventsAudiencePublic  EventsAudience = "Public"`},
		},
		{
			name: "sequelize", engine: "mysql", req: ORMRequest{Target: ORMSequelize},
			must: []string{`type: "SET('pinned','featured')"`},
		},
		{
			name: "gorm clickhouse", engine: "clickhouse", req: ORMRequest{Target: ORMGorm},
			must: []string{`type:datetime64(3, 'UTC')`},
		},
	} {
		t.Run(e.name, e.run)
	}

	shapes := &ORMSchema{Driver: DriverPostgres, Detailed: true, Tables: []ORMTable{{
		Schema: "public", Name: "shapes", Kind: ORMKindTable,
		Columns: []ORMColumn{
			ormColumn("id", "integer"),
			ormColumn("outline", `"MyShape"`, ormNull),
			ormColumn("origin", `"Geo".point3d`, ormNull),
		},
		PrimaryKey: []string{"id"}, Indexes: []ORMIndex{ormPK("shapes_pkey", "id")},
	}}}
	gorm := ormGenerate(t, shapes, ORMRequest{Target: ORMGorm})
	// Inside a struct tag, so the quotes are escaped once more.
	ormMustContain(t, "models.go", gorm.Schema, `type:\"MyShape\"`, `type:\"Geo\".point3d`)
	sequelize := ormGenerate(t, shapes, ORMRequest{Target: ORMSequelize})
	ormMustContain(t, "models.ts", sequelize.Schema, `type: "\"MyShape\""`, `type: "\"Geo\".POINT3D"`)
	mikro := ormGenerate(t, shapes, ORMRequest{Target: ORMMikroORM})
	ormMustContain(t, "entities.ts", mikro.Schema, `columnType: "\"MyShape\""`)
	// Diesel looks the type up by its name in pg_type, so the name goes in
	// bare and its schema beside it.
	diesel := ormGenerate(t, shapes, ORMRequest{Target: ORMDiesel})
	ormMustContain(t, "schema.rs", diesel.Schema,
		`#[diesel(postgres_type(name = "MyShape"))]`,
		`#[diesel(postgres_type(name = "point3d", schema = "Geo"))]`)
	ormMustNotContain(t, "schema.rs", diesel.Schema, `\"MyShape\"`, `\"Geo\"`)
}

// An enum type can live in a schema none of its tables are in. Prisma has to
// list that schema, and the ORMs that look an enum up by name have to be told
// where it is.
func TestORMEnumOutsideItsTablesSchema(t *testing.T) {
	fixture := func(tableSchemas ...string) *ORMSchema {
		s := &ORMSchema{
			Driver: DriverPostgres, Detailed: true,
			Enums: []ORMEnum{{Schema: "shared", Name: "kind", Values: []string{"a", "b"}}},
		}
		for _, schema := range tableSchemas {
			s.Tables = append(s.Tables, ORMTable{
				Schema: schema, Name: "things_" + schema, Kind: ORMKindTable,
				Columns: []ORMColumn{
					ormColumn("id", "integer"),
					ormColumn("kind", "shared.kind", ormEnumOf("shared", "kind")),
				},
				PrimaryKey: []string{"id"}, Indexes: []ORMIndex{ormPK("things_pkey", "id")},
			})
		}
		return s
	}

	prisma := ormGenerate(t, fixture("public"), ORMRequest{Target: ORMPrisma})
	ormMustContain(t, "schema.prisma", prisma.Schema,
		`schemas  = ["public", "shared"]`, `previewFeatures = ["multiSchema"]`,
		"model things_public {", `@@schema("public")`, "enum kind {", `@@schema("shared")`)

	typeorm := ormGenerate(t, fixture("public"), ORMRequest{Target: ORMTypeORM})
	ormMustContain(t, "entities.ts", typeorm.Schema, `enumName: "shared.kind"`)
	if len(typeorm.Warnings) != 0 {
		t.Errorf("unexpected warnings: %q", typeorm.Warnings)
	}
	// Once the entity states its own schema TypeORM writes that in front of
	// the enum's name, and there is no spelling that reaches another one.
	typeorm = ormGenerate(t, fixture("public", "other"), ORMRequest{Target: ORMTypeORM})
	ormMustContain(t, "entities.ts", typeorm.Schema, `enumName: "kind"`)
	ormMustNotContain(t, "entities.ts", typeorm.Schema, `enumName: "shared.kind"`)
	ormMustContain(t, "warnings", strings.Join(typeorm.Warnings, "\n"),
		"other.things_other.kind uses enum type shared.kind, and TypeORM looks for an entity's enum types in the entity's own schema")

	mikro := ormGenerate(t, fixture("public", "other"), ORMRequest{Target: ORMMikroORM})
	ormMustContain(t, "entities.ts", mikro.Schema, `nativeEnumName: "shared.kind"`)
	// The enum beside its table needs no schema said.
	same := fixture("shared")
	ormMustContain(t, "entities.ts", ormGenerate(t, same, ORMRequest{Target: ORMMikroORM}).Schema, `nativeEnumName: "kind"`)
	ormMustContain(t, "entities.ts", ormGenerate(t, same, ORMRequest{Target: ORMTypeORM}).Schema, `enumName: "kind"`)
}

// Split into a file per class, a class is also a file name, and the layout
// has file names of its own. On a file system that folds case, Index.ts and
// index.ts are one file.
func TestORMSplitLayoutKeepsItsOwnFileNames(t *testing.T) {
	schema := &ORMSchema{Driver: DriverPostgres, Detailed: true}
	for _, name := range []string{"indexes", "enums", "accounts"} {
		schema.Tables = append(schema.Tables, ORMTable{
			Schema: "public", Name: name, Kind: ORMKindTable,
			Columns:    []ORMColumn{ormColumn("id", "integer")},
			PrimaryKey: []string{"id"}, Indexes: []ORMIndex{ormPK(name+"_pkey", "id")},
		})
	}
	for target, class := range map[ORMTarget]string{
		ORMSequelize: "IndexModel", ORMTypeORM: "IndexEntity", ORMMikroORM: "IndexEntity",
	} {
		res := ormGenerate(t, schema, ORMRequest{Target: target, Split: ormYes()})
		seen := map[string]string{}
		for _, f := range res.Files {
			folded := strings.ToLower(f.Filename)
			if other, ok := seen[folded]; ok {
				t.Errorf("%s: %s and %s are one file where case is folded", target, other, f.Filename)
			}
			seen[folded] = f.Filename
		}
		if seen[strings.ToLower(class)+".ts"] != class+".ts" {
			t.Errorf("%s: no %s.ts among %v", target, class, seen)
		}
		ormMustContain(t, string(target)+" index.ts", res.Schema, `export * from "./`+class+`"`, `export * from "./Account"`)
		ormMustNotContain(t, string(target)+" index.ts", res.Schema, `"./Index"`, `"./index"`)
	}
	// In one file nothing is a file name, and the class keeps the plain one.
	single := ormGenerate(t, schema, ORMRequest{Target: ORMSequelize})
	ormMustContain(t, "models.ts", single.Schema, "export class Index extends Model<")
}

// A column can be called something the target language keeps for itself
// without reserving it as a word.
func TestORMColumnsNamedLikeTheLanguagesOwnMembers(t *testing.T) {
	schema := &ORMSchema{Driver: DriverPostgres, Detailed: true, Tables: []ORMTable{{
		Schema: "public", Name: "things", Kind: ORMKindTable,
		Columns: []ORMColumn{
			ormColumn("id", "integer", ormAuto),
			ormColumn("__proto__", "text", ormNull, ormDef("'x'::text")),
			ormColumn("constructor", "text", ormNull),
			ormColumn("__tablename__", "text", ormNull),
			ormColumn("__table_args__", "text", ormNull),
			ormColumn("__private", "text", ormNull),
		},
		PrimaryKey: []string{"id"}, Indexes: []ORMIndex{ormPK("things_pkey", "id")},
	}}}

	// SQLAlchemy reads __tablename__ and __table_args__ off the class, and
	// Python rewrites any other name with two leading underscores.
	py := ormGenerate(t, schema, ORMRequest{Target: ORMSQLAlchemy}).Schema
	ormMustContain(t, "models.py", py,
		`    __tablename__ = "things"`,
		`    col__tablename__: Mapped[Optional[str]] = mapped_column("__tablename__", Text)`,
		`    col__table_args__: Mapped[Optional[str]] = mapped_column("__table_args__", Text)`,
		`    col__private: Mapped[Optional[str]] = mapped_column("__private", Text)`)
	if n := strings.Count(py, "\n    __tablename__"); n != 1 {
		t.Errorf("__tablename__ is assigned %d times in one class:\n%s", n, py)
	}

	// In an object literal __proto__ is not a key: it sets the prototype.
	drizzle := ormGenerate(t, schema, ORMRequest{Target: ORMDrizzle}).Schema
	ormMustContain(t, "schema.ts", drizzle, `__proto___: text("__proto__")`, `constructor_: text("constructor")`)
	ormMustNotContain(t, "schema.ts", drizzle, "  __proto__:", `"__proto__":`)

	// Zod's keys are the row's own, so the key stays and is written computed,
	// which defines it.
	zod := ormGenerate(t, schema, ORMRequest{Target: ORMZod}).Schema
	// And a mask that does not mention constructor says so: TypeScript would
	// otherwise check the one every object literal inherits.
	ormMustContain(t, "schemas.ts", zod, `  ["__proto__"]: z.string().nullable(),`, `["__proto__"]: true`, `"constructor": undefined })`)
	ormMustNotContain(t, "schemas.ts", zod, "  __proto__:", `"__proto__": true`)

	for _, target := range []ORMTarget{ORMTypeORM, ORMMikroORM, ORMSequelize} {
		out := ormGenerate(t, schema, ORMRequest{Target: target}).Schema
		ormMustContain(t, string(target), out, "__proto___", "constructor_")
		ormMustNotContain(t, string(target), out, "  __proto__!:", "  declare __proto__:", "  constructor!:", "  declare constructor:")
	}
}

// SQLite and Oracle keep a default as it was typed. 007 is a number there and
// an octal literal, or an error, everywhere the output is read.
func TestORMNumericDefaultsAreWrittenAsNumbers(t *testing.T) {
	schema := &ORMSchema{Driver: DriverSQLite, Detailed: true, Tables: []ORMTable{{
		Schema: "main", Name: "counters", Kind: ORMKindTable,
		Columns: []ORMColumn{
			ormColumn("id", "INTEGER"),
			ormColumn("n", "INTEGER", ormDef("007")),
			ormColumn("zero", "INTEGER", ormDef("00")),
			ormColumn("half", "REAL", ormDef("00.5")),
			ormColumn("code", "TEXT", ormDef("'007'")),
		},
		PrimaryKey: []string{"id"},
	}}}
	doc := ormGenerate(t, schema, ORMRequest{Target: ORMJSONSchema}).Schema
	var parsed struct {
		Defs map[string]struct {
			Properties map[string]struct {
				Default any `json:"default"`
			} `json:"properties"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal([]byte(doc), &parsed); err != nil {
		t.Fatalf("schema.json is not JSON: %v\n%s", err, doc)
	}
	props := parsed.Defs["Counter"].Properties
	for name, want := range map[string]any{"n": float64(7), "zero": float64(0), "half": 0.5, "code": "007"} {
		if got := props[name].Default; got != want {
			t.Errorf("default of %s = %v, want %v\n%s", name, got, want, doc)
		}
	}
	ormMustContain(t, "models.py", ormGenerate(t, schema, ORMRequest{Target: ORMDjango}).Schema, "db_default=7)", "db_default=0.5)", `db_default="007")`)
	ormMustContain(t, "schema.ts", ormGenerate(t, schema, ORMRequest{Target: ORMDrizzle}).Schema, ".default(7)", ".default(0)", ".default(0.5)", `.default("007")`)
	ormMustContain(t, "entities.ts", ormGenerate(t, schema, ORMRequest{Target: ORMTypeORM}).Schema, "default: 7 }", "default: 0.5 }")
}

// SQLAlchemy's text() reads :name as a bind parameter, inside a string literal
// as much as outside one, and compiles a default with NULL in its place. Each
// expectation here was compiled by SQLAlchemy 2.1 back into the SQL on its
// left.
func TestSQLAlchemyTextKeepsItsColons(t *testing.T) {
	for sql, want := range map[string]string{
		`'{"a":1}'::jsonb`:      `"'{\"a\"\\:1}'::jsonb"`,
		`'see :ref here'::text`: `"'see \\:ref here'::text"`,
		`':x'::text`:            `"'\\:x'::text"`,
		`'a :$1 b'::text`:       `"'a \\:$1 b'::text"`,
		`'é :é'::text`:          `"'é \\:é'::text"`,
		// Not parameters as they stand: a time, a cast, a word between colons.
		`'12:30:00'::text`:                 `"'12:30:00'::text"`,
		`' :ab: '::text`:                   `"' :ab: '::text"`,
		`'x:'::text`:                       `"'x:'::text"`,
		`(now() AT TIME ZONE 'utc'::text)`: `"(now() AT TIME ZONE 'utc'::text)"`,
		// A backslash already in front of a colon is the one text() removes.
		`'a\:b \: c'::text`: `"'a\\\\:b \\\\: c'::text"`,
	} {
		if got := saSQLText(sql); got != want {
			t.Errorf("saSQLText(%s) = %s, want %s", sql, got, want)
		}
	}
	schema := &ORMSchema{Driver: DriverPostgres, Detailed: true, Tables: []ORMTable{{
		Schema: "public", Name: "prefs", Kind: ORMKindTable,
		Columns: []ORMColumn{
			ormColumn("id", "integer"),
			ormColumn("settings", "jsonb", ormDef(`'{"a":1}'::jsonb`)),
			ormColumn("label", "text", ormNull, ormGenerated(`('k:' || ' :v')`)),
		},
		PrimaryKey: []string{"id"},
	}}}
	ormMustContain(t, "models.py", ormGenerate(t, schema, ORMRequest{Target: ORMSQLAlchemy}).Schema,
		`server_default=text("'{\"a\"\\:1}'::jsonb")`, `Computed("('k:' || ' \\:v')", persisted=True)`)
}

// ormNonASCIIFixture is a school in Romanian, French and Russian: tables,
// columns, comments, a default and enum labels that begin with a letter of
// more than one byte.
func ormNonASCIIFixture(driver Driver) *ORMSchema {
	s := &ORMSchema{Driver: driver, Detailed: true}
	schema := "public"
	stare := ormColumn("stare", "public.stare", ormEnumOf("public", "stare"))
	if driver == DriverMySQL {
		schema = "blog"
		stare = ormColumn("stare", "enum('éclair','în lucru','новый','Ștefan','plain_one')")
	} else {
		s.Enums = []ORMEnum{{Schema: "public", Name: "stare", Values: []string{"éclair", "în lucru", "новый", "Ștefan", "plain_one"}}}
	}
	s.Tables = []ORMTable{
		{
			Schema: schema, Name: "școli", Kind: ORMKindTable, Comment: "Școlile din țară",
			Columns:    []ORMColumn{ormColumn("id", "integer", ormAuto), ormColumn("țară", "text", ormNull, ormNote("țara")), stare},
			PrimaryKey: []string{"id"}, Indexes: []ORMIndex{ormPK("scoli_pkey", "id")},
		},
		{
			Schema: schema, Name: "Élèves", Kind: ORMKindTable,
			Columns: []ORMColumn{
				ormColumn("id", "integer", ormAuto), ormColumn("școală_id", "integer"),
				ormColumn("Éa_id", "integer", ormNull), ormColumn("ёж", "text", ormNull, ormDef("'ёж'")),
			},
			PrimaryKey: []string{"id"}, Indexes: []ORMIndex{ormPK("eleves_pkey", "id")},
			ForeignKeys: []ForeignKey{
				ormFK("elevi_scoala_fkey", []string{"școală_id"}, schema, "școli", []string{"id"}, "CASCADE"),
				ormFK("elevi_a_fkey", []string{"Éa_id"}, schema, "școli", []string{"id"}, "SET NULL"),
			},
		},
		{
			Schema: schema, Name: "заказы", Kind: ORMKindTable,
			Columns:    []ORMColumn{ormColumn("id", "integer", ormAuto), ormColumn("ученик", "integer", ormNull)},
			PrimaryKey: []string{"id"}, Indexes: []ORMIndex{ormPK("zakazy_pkey", "id")},
			ForeignKeys: []ForeignKey{
				ormFK("zakazy_uchenik_fkey", []string{"ученик"}, schema, "Élèves", []string{"id"}, "NO ACTION"),
			},
		},
	}
	return s
}

// Names and labels are in the language of the people who made the schema. A
// casing rule that takes a string's first byte for its first letter cuts a
// Romanian or Russian one in half, and what is left is not text any more.
func TestORMNamesOutsideASCIIStayWhole(t *testing.T) {
	django := ormGenerate(t, ormNonASCIIFixture(DriverPostgres), ORMRequest{Target: ORMDjango}).Schema
	ormMustContain(t, "models.py", django,
		`    CLAIR = "éclair", "Éclair"`,
		`    N_LUCRU = "în lucru", "În lucru"`,
		`"новый", "Новый"`,
		`    TEFAN = "Ștefan", "Ștefan"`,
		`    PLAIN_ONE = "plain_one", "Plain one"`)

	// Whole means whole everywhere: no file may hold half a letter, or the
	// replacement character an encoder writes where it found one.
	broken := func(text string) bool {
		return !utf8.ValidString(text) || strings.ContainsRune(text, utf8.RuneError) ||
			strings.Contains(strings.ToLower(text), `\ufffd`)
	}
	for _, driver := range []Driver{DriverPostgres, DriverMySQL} {
		for _, target := range ORMTargets() {
			if ORMUnsupported(target, driver) != "" {
				continue
			}
			requests := []ORMRequest{{Target: target}}
			if ormTargetHasOption(target, "naming") {
				requests = append(requests, ORMRequest{Target: target, Naming: ORMNamingCamel})
			}
			for _, req := range requests {
				res := ormGenerate(t, ormNonASCIIFixture(driver), req)
				for _, f := range res.Files {
					for _, line := range strings.Split(f.Content, "\n") {
						if broken(line) {
							t.Errorf("%s %s (naming %q) %s: %q", driver, target, req.Naming, f.Filename, line)
						}
					}
				}
				for _, w := range res.Warnings {
					if broken(w) {
						t.Errorf("%s %s warning: %q", driver, target, w)
					}
				}
			}
		}
	}
}

// SQL Server keeps the statement that made a view, and a script is no place
// for a view with a table written where it was. CREATE VIEW also has to open
// its batch, so the statement travels inside EXEC, after everything it reads.
func TestORMSQLServerViewsAreWrittenAsTheirOwnStatement(t *testing.T) {
	definition := "CREATE VIEW [sales].[open accounts] AS\nSELECT id, region FROM sales.accounts WHERE region <> 'it''s; DROP TABLE x --'"
	schema := ormMSSQLFixture()
	schema.Tables = append(schema.Tables,
		ORMTable{
			Schema: "sales", Name: "open accounts", Kind: ORMKindView, CreateSQL: definition + ";\n",
			Columns: []ORMColumn{ormColumn("id", "int"), ormColumn("region", "nchar(2)")},
		},
		// Created WITH ENCRYPTION: the server has the view and will not say what it is.
		ORMTable{
			Schema: "sales", Name: "sealed", Kind: ORMKindView,
			Columns: []ORMColumn{ormColumn("id", "int")},
		})

	quoted := "EXEC(N'" + strings.ReplaceAll(definition, "'", "''") + "');"
	plain := ormGenerate(t, schema, ORMRequest{Target: ORMSQL, Views: ormYes()})
	ormMustContain(t, "schema.sql", plain.Schema, "\n"+quoted+"\n")
	ormMustNotContain(t, "schema.sql", plain.Schema, "CREATE TABLE [sales].[open accounts]", "sealed")
	if view, key := strings.Index(plain.Schema, "CREATE VIEW"), strings.LastIndex(plain.Schema, "ALTER TABLE"); view < key {
		t.Errorf("the view is written before the tables it reads are complete:\n%s", plain.Schema)
	}
	ormMustContain(t, "warnings", strings.Join(plain.Warnings, "\n"), "The definition of view sales.sealed could not be read; it was left out.")
	if plain.Counts.Views != 1 || plain.Counts.Tables != 3 {
		t.Errorf("counts = %+v, want the one view that was written and three tables", plain.Counts)
	}

	guarded := ormGenerate(t, schema, ORMRequest{Target: ORMSQL, Views: ormYes(), IfNotExists: ormYes()})
	ormMustContain(t, "schema.sql", guarded.Schema, "IF OBJECT_ID(N'[sales].[open accounts]', N'V') IS NULL\n"+quoted)

	// The definition is text from the catalogue: whatever it holds, it is one
	// string inside one statement.
	without := ormGenerate(t, schema, ORMRequest{Target: ORMSQL})
	if with, base := len(splitSQLStatements(DriverMSSQL, plain.Schema)), len(splitSQLStatements(DriverMSSQL, without.Schema)); with != base+1 {
		t.Errorf("the view added %d statements to the script, want 1\n%s", with-base, plain.Schema)
	}
	if without.Counts.Views != 0 || strings.Contains(without.Schema, "VIEW") {
		t.Errorf("views are in a script that did not ask for them: %+v\n%s", without.Counts, without.Schema)
	}
}

// A view's name sorts it in among the tables it reads. MySQL refuses a view
// whose table is not there yet, so where the script is the engine's own
// statements the views are written after every table, in their own database.
func TestORMSQLWritesViewsAfterTheTablesTheyRead(t *testing.T) {
	schema := ormMySQLFixture()
	for i := range schema.Tables {
		if schema.Tables[i].Name == "published_posts" {
			schema.Tables[i].CreateSQL = "CREATE VIEW `published_posts` AS select `blog`.`posts`.`id` AS `id`,`blog`.`posts`.`title` AS `title` from `blog`.`posts`"
		}
	}
	res := ormGenerate(t, schema, ORMRequest{Target: ORMSQL, Views: ormYes()})
	view := strings.Index(res.Schema, "\nCREATE VIEW `published_posts`")
	if view < 0 || view < strings.LastIndex(res.Schema, "\nCREATE TABLE ") {
		t.Fatalf("the view is not after the last table:\n%s", res.Schema)
	}
	// The script had moved on to another database; it comes back for the view,
	// and does not create the database a second time.
	before := res.Schema[:view]
	if use := strings.LastIndex(before, "\nUSE "); use < 0 || !strings.HasPrefix(before[use:], "\nUSE `blog`;") {
		t.Errorf("the view is not written in its own database:\n%s", res.Schema)
	}
	if n := strings.Count(res.Schema, "CREATE DATABASE IF NOT EXISTS `blog`;"); n != 1 {
		t.Errorf("blog is created %d times\n%s", n, res.Schema)
	}
	if res.Counts.Views != 1 {
		t.Errorf("counts = %+v", res.Counts)
	}
}

// The counts are what the page says the file holds, so a model a target could
// not write is not in them, and neither is a relation that would have led to it.
func TestORMCountsWhatIsInTheOutput(t *testing.T) {
	schema := &ORMSchema{Driver: DriverPostgres, Detailed: true, Tables: []ORMTable{
		{
			Schema: "public", Name: "accounts", Kind: ORMKindTable,
			Columns:    []ORMColumn{ormColumn("id", "integer"), ormColumn("log_at", "timestamp with time zone", ormNull)},
			PrimaryKey: []string{"id"}, Indexes: []ORMIndex{ormPK("accounts_pkey", "id")},
		},
		{
			Schema: "public", Name: "entries", Kind: ORMKindTable,
			Columns:    []ORMColumn{ormColumn("id", "integer"), ormColumn("account_id", "integer")},
			PrimaryKey: []string{"id"}, Indexes: []ORMIndex{ormPK("entries_pkey", "id")},
			ForeignKeys: []ForeignKey{
				ormFK("entries_account_id_fkey", []string{"account_id"}, "public", "accounts", []string{"id"}, "CASCADE"),
			},
		},
		// No key: Diesel leaves it out.
		{
			Schema: "public", Name: "audit_log", Kind: ORMKindTable,
			Columns: []ORMColumn{ormColumn("at", "timestamp with time zone"), ormColumn("account_id", "integer", ormNull)},
			ForeignKeys: []ForeignKey{
				ormFK("audit_log_account_id_fkey", []string{"account_id"}, "public", "accounts", []string{"id"}, "SET NULL"),
			},
		},
		// No definition: the SQL target leaves it out.
		{
			Schema: "public", Name: "balances", Kind: ORMKindView,
			Columns: []ORMColumn{ormColumn("account_id", "integer", ormNull)},
		},
	}}
	for _, c := range []struct {
		req    ORMRequest
		want   ORMCounts
		warned string
	}{
		{ORMRequest{Target: ORMPrisma}, ORMCounts{Tables: 3, Relations: 2}, ""},
		{ORMRequest{Target: ORMDiesel}, ORMCounts{Tables: 2, Relations: 1}, "audit_log has no primary key"},
		{ORMRequest{Target: ORMSQL, Views: ormYes()}, ORMCounts{Tables: 3, Relations: 2}, "The definition of view balances could not be read"},
		{ORMRequest{Target: ORMTypeScript}, ORMCounts{Tables: 3, Views: 1}, ""},
	} {
		res := ormGenerate(t, schema, c.req)
		if res.Counts != c.want {
			t.Errorf("%s: counts = %+v, want %+v", c.req.Target, res.Counts, c.want)
		}
		if c.warned != "" {
			ormMustContain(t, string(c.req.Target)+" warnings", strings.Join(res.Warnings, "\n"), c.warned)
		}
	}
}
