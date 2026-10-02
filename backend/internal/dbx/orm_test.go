package dbx

import (
	"strings"
	"testing"
)

func ormFixture() ([]Table, map[string]*TableDetail) {
	tables := []Table{
		{Schema: "public", Name: "users", Type: "table"},
		{Schema: "public", Name: "posts", Type: "table"},
		{Schema: "public", Name: "user_view", Type: "view"},
	}
	details := map[string]*TableDetail{
		"users": {
			Schema: "public", Name: "users",
			Columns: []Column{
				{Name: "id", Type: "integer", Nullable: false},
				{Name: "email", Type: "varchar(255)", Nullable: false},
				{Name: "name", Type: "text", Nullable: true},
			},
			PrimaryKey: []string{"id"},
			Indexes: []Index{
				{Name: "users_pkey", Columns: []string{"id"}, Unique: true, Primary: true},
				{Name: "users_email_key", Columns: []string{"email"}, Unique: true},
			},
		},
		"posts": {
			Schema: "public", Name: "posts",
			Columns: []Column{
				{Name: "id", Type: "bigint", Nullable: false},
				{Name: "title", Type: "text", Nullable: false},
				{Name: "author_id", Type: "integer", Nullable: false},
			},
			PrimaryKey: []string{"id"},
			ForeignKeys: []ForeignKey{
				{Name: "posts_author_fk", Columns: []string{"author_id"},
					RefSchema: "public", RefTable: "users", RefColumns: []string{"id"}},
			},
		},
	}
	return tables, details
}

func TestGeneratePrismaSchema(t *testing.T) {
	tables, details := ormFixture()
	out, err := GenerateORM(ORMPrisma, DriverPostgres, tables, details)
	if err != nil {
		t.Fatal(err)
	}

	must := []string{
		`provider = "postgresql"`,
		"model users {",
		"model posts {",
		"id Int @id",           // single-column PK inline
		"id BigInt @id",        // bigint maps to BigInt
		"email String @unique", // unique index -> @unique
		"name String?",         // nullable -> optional
		`@relation("posts_author_id", fields: [author_id], references: [id])`,
		"posts[]", // back-relation on users
	}
	for _, m := range must {
		if !strings.Contains(out, m) {
			t.Errorf("prisma schema missing %q\n---\n%s", m, out)
		}
	}
	// A view must not become a model.
	if strings.Contains(out, "model user_view") {
		t.Errorf("prisma schema included a view as a model:\n%s", out)
	}
}

func TestGeneratePrismaProviders(t *testing.T) {
	tables, details := ormFixture()
	for driver, provider := range map[Driver]string{
		DriverMySQL: "mysql", DriverSQLite: "sqlite", DriverMSSQL: "sqlserver",
	} {
		out, err := GenerateORM(ORMPrisma, driver, tables, details)
		if err != nil {
			t.Fatalf("%s: %v", driver, err)
		}
		if !strings.Contains(out, `provider = "`+provider+`"`) {
			t.Errorf("%s provider not rendered:\n%s", provider, out)
		}
	}
}

func TestGenerateDrizzleSchema(t *testing.T) {
	tables, details := ormFixture()
	out, err := GenerateORM(ORMDrizzle, DriverPostgres, tables, details)
	if err != nil {
		t.Fatal(err)
	}
	must := []string{
		`from "drizzle-orm/pg-core"`,
		`pgTable("users"`,
		`pgTable("posts"`,
		".primaryKey()",
		".notNull()",
		".unique()",
	}
	for _, m := range must {
		if !strings.Contains(out, m) {
			t.Errorf("drizzle schema missing %q\n---\n%s", m, out)
		}
	}
	if strings.Contains(out, "user_view") {
		t.Errorf("drizzle schema included a view:\n%s", out)
	}
}

func TestGenerateORMRejectsMongo(t *testing.T) {
	if _, err := GenerateORM(ORMPrisma, DriverMongo, nil, nil); err == nil {
		t.Error("expected ORM generation to reject MongoDB")
	}
}

func TestORMTypeParts(t *testing.T) {
	cases := map[string]struct {
		name string
		args []string
	}{
		"varchar(255)":                  {"varchar", []string{"255"}},
		"NUMERIC(10,2)":                 {"numeric", []string{"10", "2"}},
		"integer":                       {"integer", nil},
		"int(10) unsigned zerofill":     {"int unsigned zerofill", []string{"10"}},
		"timestamp(3) with time zone":   {"timestamp with time zone", []string{"3"}},
		"double precision":              {"double precision", nil},
		"enum('a,b','it''s')":           {"enum", []string{"'a,b'", "'it''s'"}},
		"Map(String, Array(Int8))":      {"map", []string{"String", "Array(Int8)"}},
		"DateTime64(3, 'Europe/Paris')": {"datetime64", []string{"3", "'Europe/Paris'"}},
	}
	for in, want := range cases {
		name, args := ormTypeParts(in)
		if name != want.name || strings.Join(args, "|") != strings.Join(want.args, "|") {
			t.Errorf("ormTypeParts(%q) = %q %q, want %q %q", in, name, args, want.name, want.args)
		}
	}
}

func TestPrismaType(t *testing.T) {
	cases := map[string]string{
		"integer":      "Int",
		"bigint":       "BigInt",
		"varchar(255)": "String",
		"boolean":      "Boolean",
		"jsonb":        "Json",
		"timestamptz":  "DateTime",
		"bytea":        "Bytes",
		"numeric(8,2)": "Decimal",
	}
	for in, want := range cases {
		got, _, ok := prismaPostgresType(parseORMType(DriverPostgres, in))
		if !ok || got != want {
			t.Errorf("prisma scalar for %q = %q (supported %v), want %q", in, got, ok, want)
		}
	}
}

func TestSanitizeIdent(t *testing.T) {
	cases := map[string]string{
		"users":     "users",
		"user$name": "user_name",
		"2fa":       "_2fa",
		"weird-col": "weird_col",
	}
	for in, want := range cases {
		if got := sanitizeIdent(in); got != want {
			t.Errorf("sanitizeIdent(%q) = %q, want %q", in, got, want)
		}
	}
}
