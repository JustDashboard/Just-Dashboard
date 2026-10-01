package dbx

import (
	"flag"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Rewrites testdata/orm from what the generators produce now:
//
//	go test ./internal/dbx -run TestORMGolden -update-orm
//
// The flag is named for this suite so it cannot collide with another golden
// suite's in the same package.
var updateORMGolden = flag.Bool("update-orm", false, "rewrite testdata/orm from the generators' current output")

func ormYes() *bool { v := true; return &v }
func ormNo() *bool  { v := false; return &v }

// ormCase is one target over one engine's fixture with one set of options. Its
// name is the directory its output is kept in.
type ormCase struct {
	name   string
	engine string
	req    ORMRequest
}

// ormGoldenCases is every target over the PostgreSQL shop, then each target on
// every other engine whose output differs in kind, then each option that
// changes what is written.
func ormGoldenCases() []ormCase {
	cases := []ormCase{}
	for _, target := range ORMTargets() {
		cases = append(cases, ormCase{
			name: string(target) + "-postgres", engine: "postgres", req: ORMRequest{Target: target},
		})
	}
	engines := map[ORMTarget][]string{
		ORMPrisma:     {"mysql", "sqlite", "sqlserver", "cockroachdb"},
		ORMDrizzle:    {"mysql", "sqlite"},
		ORMTypeScript: {"mysql", "sqlite", "sqlserver", "oracle", "clickhouse"},
		ORMZod:        {"mysql", "clickhouse"},
		ORMKysely:     {"mysql", "sqlite", "sqlserver"},
		ORMTypeORM:    {"mysql", "sqlite", "sqlserver", "oracle"},
		ORMMikroORM:   {"mysql", "sqlite", "sqlserver"},
		ORMSequelize:  {"mysql", "sqlite", "sqlserver"},
		ORMSQLAlchemy: {"mysql", "sqlite", "sqlserver", "oracle"},
		ORMDjango:     {"mysql", "sqlite", "sqlserver", "oracle"},
		ORMGorm:       {"mysql", "sqlite", "sqlserver", "clickhouse"},
		ORMGoStructs:  {"mysql", "sqlite", "sqlserver", "oracle", "clickhouse"},
		ORMDiesel:     {"mysql", "sqlite"},
		ORMEloquent:   {"mysql", "sqlite", "sqlserver"},
		ORMJSONSchema: {"mysql", "clickhouse"},
		ORMGraphQL:    {"mysql", "clickhouse"},
		ORMSQL:        {"mysql", "sqlite", "sqlserver", "oracle", "clickhouse"},
	}
	for _, target := range ORMTargets() {
		for _, engine := range engines[target] {
			req := ORMRequest{Target: target}
			if target == ORMPrisma && engine == "mysql" {
				// Prisma reads one MySQL database at a time; see the refusal test.
				req.Schema = "blog"
			}
			cases = append(cases, ormCase{name: string(target) + "-" + engine, engine: engine, req: req})
		}
	}
	return append(cases,
		ormCase{"prisma-postgres-camel", "postgres", ORMRequest{Target: ORMPrisma, Naming: ORMNamingCamel}},
		ormCase{"prisma-postgres-views", "postgres", ORMRequest{Target: ORMPrisma, Views: ormYes()}},
		ormCase{"prisma-postgres-v7", "postgres", ORMRequest{Target: ORMPrisma, PrismaVersion: "7"}},
		ormCase{"prisma-postgres-bare", "postgres", ORMRequest{
			Target: ORMPrisma, Relations: ormNo(), Enums: ormNo(), Defaults: ormNo()}},
		ormCase{"drizzle-postgres-camel", "postgres", ORMRequest{Target: ORMDrizzle, Naming: ORMNamingCamel}},
		ormCase{"drizzle-postgres-views", "postgres", ORMRequest{Target: ORMDrizzle, Views: ormYes()}},
		ormCase{"typescript-postgres-dates", "postgres", ORMRequest{
			Target: ORMTypeScript, Dates: "Date", Naming: ORMNamingCamel, Enums: ormNo()}},
		ormCase{"zod-postgres-plain", "postgres", ORMRequest{Target: ORMZod, InsertSchemas: ormNo(), Views: ormNo()}},
		ormCase{"kysely-postgres-camel", "postgres", ORMRequest{Target: ORMKysely, Naming: ORMNamingCamel}},
		ormCase{"typeorm-postgres-split", "postgres", ORMRequest{
			Target: ORMTypeORM, Split: ormYes(), Naming: ORMNamingCamel, Views: ormYes()}},
		ormCase{"mikroorm-postgres-split", "postgres", ORMRequest{
			Target: ORMMikroORM, Split: ormYes(), Naming: ORMNamingCamel}},
		ormCase{"sequelize-postgres-split", "postgres", ORMRequest{
			Target: ORMSequelize, Split: ormYes(), Naming: ORMNamingCamel}},
		ormCase{"sqlalchemy-postgres-views", "postgres", ORMRequest{Target: ORMSQLAlchemy, Views: ormYes()}},
		ormCase{"django-postgres-managed", "postgres", ORMRequest{Target: ORMDjango, Managed: ormYes()}},
		ormCase{"gorm-postgres-plain", "postgres", ORMRequest{
			Target: ORMGorm, Relations: ormNo(), JSONTags: ormNo(), Package: "store"}},
		ormCase{"go-postgres-pointer", "postgres", ORMRequest{Target: ORMGoStructs, Nulls: "pointer", Enums: ormNo()}},
		ormCase{"eloquent-postgres-split", "postgres", ORMRequest{Target: ORMEloquent, Split: ormYes()}},
		ormCase{"jsonschema-postgres-camel", "postgres", ORMRequest{
			Target: ORMJSONSchema, Naming: ORMNamingCamel, Views: ormYes()}},
		ormCase{"graphql-postgres-inputs", "postgres", ORMRequest{
			Target: ORMGraphQL, Inputs: ormYes(), Naming: ORMNamingCamel}},
		ormCase{"sql-postgres-guarded", "postgres", ORMRequest{Target: ORMSQL, IfNotExists: ormYes(), Views: ormYes()}},
		// Three tables out of the shop: the relations among them stay, the ones
		// that leave the selection go.
		ormCase{"prisma-postgres-selected", "postgres", ORMRequest{
			Target: ORMPrisma, Tables: []string{"customers", "orders", "public.order_items"}}},
		ormCase{"drizzle-postgres-selected", "postgres", ORMRequest{
			Target: ORMDrizzle, Tables: []string{"customers", "orders", "public.order_items"}}},
	)
}

// generateCase runs a case the way the route does: the selection is applied to
// the fixture, then the options are resolved and the target rendered.
func ormGenerateCase(t *testing.T, c ormCase) *ORMResult {
	t.Helper()
	schema := ormFixtureFor(c.engine)
	if schema == nil {
		t.Fatalf("no fixture for engine %q", c.engine)
	}
	if c.req.Schema != "" {
		// What the loader does when one schema is asked for: nothing else is read.
		narrowed := *schema
		narrowed.Tables = nil
		for _, tb := range schema.Tables {
			if tb.Schema == c.req.Schema {
				narrowed.Tables = append(narrowed.Tables, tb)
			}
		}
		schema = &narrowed
	}
	if len(c.req.Tables) > 0 {
		tables, err := ormSelect(schema.Tables, func(tb ORMTable) (string, string) { return tb.Schema, tb.Name }, c.req.Tables)
		if err != nil {
			t.Fatalf("select: %v", err)
		}
		narrowed := *schema
		narrowed.Tables = tables
		schema = &narrowed
	}
	opts, err := c.req.Options()
	if err != nil {
		t.Fatalf("options: %v", err)
	}
	res, err := GenerateORMFiles(schema, opts)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	return res
}

func TestORMGolden(t *testing.T) {
	root := filepath.Join("testdata", "orm")
	seen := map[string]bool{}
	for _, c := range ormGoldenCases() {
		if seen[c.name] {
			t.Fatalf("two cases are both named %q", c.name)
		}
		seen[c.name] = true
		t.Run(c.name, func(t *testing.T) {
			res := ormGenerateCase(t, c)
			if res.Schema != res.Files[0].Content || res.Filename != res.Files[0].Filename {
				t.Errorf("schema/filename do not repeat the first file (%s)", res.Files[0].Filename)
			}
			got := map[string]string{}
			for _, f := range res.Files {
				if f.Filename != filepath.Base(f.Filename) || f.Filename == "" || strings.HasPrefix(f.Filename, ".") {
					t.Fatalf("generated file name %q is not a bare file name", f.Filename)
				}
				if _, dup := got[f.Filename]; dup {
					t.Fatalf("two generated files are both called %q", f.Filename)
				}
				got[f.Filename] = f.Content
			}
			// What could not be expressed is part of the output: a warning that
			// disappears is a regression nobody would otherwise see.
			got["WARNINGS.txt"] = strings.Join(res.Warnings, "\n") + "\n"

			dir := filepath.Join(root, c.name)
			if *updateORMGolden {
				if err := os.RemoveAll(dir); err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				for name, content := range got {
					if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
						t.Fatal(err)
					}
				}
				return
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatalf("no golden output for this case (run with -update-orm): %v", err)
			}
			var names []string
			for _, e := range entries {
				names = append(names, e.Name())
			}
			sort.Strings(names)
			for _, name := range names {
				want, err := os.ReadFile(filepath.Join(dir, name))
				if err != nil {
					t.Fatal(err)
				}
				content, ok := got[name]
				if !ok {
					t.Errorf("%s is in the golden output and was not generated", name)
					continue
				}
				if content != string(want) {
					t.Errorf("%s differs from the golden output.\n--- got\n%s\n--- want\n%s", name, content, want)
				}
				delete(got, name)
			}
			for name := range got {
				t.Errorf("%s was generated and is not in the golden output", name)
			}
		})
	}

	if *updateORMGolden {
		return
	}
	// A directory no case writes any more would otherwise be kept forever.
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() && !seen[e.Name()] {
			t.Errorf("testdata/orm/%s belongs to no case", e.Name())
		}
	}
}
