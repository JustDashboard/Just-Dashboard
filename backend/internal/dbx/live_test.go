package dbx

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Integration tests against real database servers.
//
// The unit tests prove the generated SQL is the SQL intended. Only a real
// server proves it is SQL that server accepts — and the catalogue queries are
// exactly where that gap bites, because every engine spells its metadata
// differently and no amount of string-matching a query catches a column that
// does not exist on the version you are talking to.
//
// Each engine's DSN comes from an environment variable, defaulting to a local
// instance on the standard port. When an engine is not reachable its tests skip
// with a message naming the variable, so `go test ./...` stays green on a
// machine with nothing installed and gets stricter on one with the servers up.
// That is deliberate: a suite that fails for want of a database teaches people
// to ignore it.

func liveDSN(t *testing.T, env, fallback string) string {
	t.Helper()
	if v := os.Getenv(env); v != "" {
		return v
	}
	return fallback
}

// liveSQL opens a connection or skips the test.
func liveSQL(t *testing.T, driver Driver, env, fallback string) *sql.DB {
	t.Helper()
	dsn := liveDSN(t, env, fallback)
	d, err := DialectFor(driver)
	if err != nil {
		t.Fatalf("DialectFor(%s): %v", driver, err)
	}
	db, err := sql.Open(d.SQLDriverName(), d.NormaliseDSN(dsn))
	if err != nil {
		t.Skipf("%s unavailable (%s): %v", driver, env, err)
	}
	if err := db.PingContext(context.Background()); err != nil {
		db.Close()
		t.Skipf("%s unreachable — set %s to run these (%v)", driver, env, err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// engineFixture is one engine's flavour of the same two-table schema, plus what
// that engine is and is not expected to support.
type engineFixture struct {
	driver Driver
	env    string
	dsn    string
	schema string
	// seed and teardown are run in order; failures in teardown are ignored.
	seed     []string
	teardown []string
	// ClickHouse has no primary-key constraint, no foreign keys and no generic
	// CREATE TABLE, so those assertions are skipped rather than expected to fail.
	relational bool
}

func sqlFixtures() []engineFixture {
	fixtures := []engineFixture{
		{
			driver: DriverPostgres, env: "JD_TEST_POSTGRES_DSN",
			dsn:    "postgres://jdtest:jdtest@127.0.0.1:5432/jdtest?sslmode=disable",
			schema: "public", relational: true,
			teardown: []string{`DROP TABLE IF EXISTS jd_posts`, `DROP TABLE IF EXISTS jd_users`},
			seed: []string{
				`CREATE TABLE jd_users (
					id INTEGER PRIMARY KEY,
					email VARCHAR(255) NOT NULL UNIQUE,
					name TEXT
				)`,
				`CREATE TABLE jd_posts (
					id INTEGER PRIMARY KEY,
					title TEXT NOT NULL,
					author_id INTEGER NOT NULL REFERENCES jd_users(id) ON DELETE CASCADE
				)`,
				`CREATE INDEX jd_posts_author_idx ON jd_posts(author_id)`,
				`INSERT INTO jd_users(id,email,name) VALUES (1,'a@x.io','Ann'),(2,'b@x.io',NULL)`,
				`INSERT INTO jd_posts(id,title,author_id) VALUES (1,'Hello',1),(2,'World',2)`,
			},
		},
		{
			driver: DriverMySQL, env: "JD_TEST_MYSQL_DSN",
			dsn:    "jdtest:jdtest@tcp(127.0.0.1:3306)/jdtest",
			schema: "jdtest", relational: true,
			teardown: []string{`DROP TABLE IF EXISTS jd_posts`, `DROP TABLE IF EXISTS jd_users`},
			seed: []string{
				`CREATE TABLE jd_users (
					id INT PRIMARY KEY,
					email VARCHAR(255) NOT NULL UNIQUE,
					name TEXT
				)`,
				`CREATE TABLE jd_posts (
					id INT PRIMARY KEY,
					title VARCHAR(255) NOT NULL,
					author_id INT NOT NULL,
					CONSTRAINT jd_posts_author_fk FOREIGN KEY (author_id) REFERENCES jd_users(id)
				)`,
				`CREATE INDEX jd_posts_author_idx ON jd_posts(author_id)`,
				`INSERT INTO jd_users(id,email,name) VALUES (1,'a@x.io','Ann'),(2,'b@x.io',NULL)`,
				`INSERT INTO jd_posts(id,title,author_id) VALUES (1,'Hello',1),(2,'World',2)`,
			},
		},
		{
			driver: DriverMSSQL, env: "JD_TEST_MSSQL_DSN",
			dsn:    "sqlserver://sa:JdTest%232024pw@127.0.0.1:1433?database=master",
			schema: "dbo", relational: true,
			teardown: []string{`DROP TABLE IF EXISTS jd_posts`, `DROP TABLE IF EXISTS jd_users`},
			seed: []string{
				`CREATE TABLE jd_users (
					id INT PRIMARY KEY,
					email NVARCHAR(255) NOT NULL UNIQUE,
					name NVARCHAR(MAX)
				)`,
				`CREATE TABLE jd_posts (
					id INT PRIMARY KEY,
					title NVARCHAR(255) NOT NULL,
					author_id INT NOT NULL CONSTRAINT jd_posts_author_fk REFERENCES jd_users(id)
				)`,
				`CREATE INDEX jd_posts_author_idx ON jd_posts(author_id)`,
				`INSERT INTO jd_users(id,email,name) VALUES (1,'a@x.io','Ann'),(2,'b@x.io',NULL)`,
				`INSERT INTO jd_posts(id,title,author_id) VALUES (1,'Hello',1),(2,'World',2)`,
			},
		},
		{
			// Oracle folds an unquoted identifier to upper case, so the fixture
			// quotes its names: that is also how the dashboard addresses them
			// (quoteDouble), so a fixture spelled the engine's way would test a
			// path no user ever takes.
			driver: DriverOracle, env: "JD_TEST_ORACLE_DSN",
			dsn:    "oracle://jdtest:jdtest@127.0.0.1:1521/FREEPDB1",
			schema: "JDTEST", relational: true,
			teardown: []string{
				`DROP TABLE "jd_posts" CASCADE CONSTRAINTS`,
				`DROP TABLE "jd_users" CASCADE CONSTRAINTS`,
				`DROP TABLE "jd_widgets" CASCADE CONSTRAINTS`,
			},
			seed: []string{
				`CREATE TABLE "jd_users" (
					"id" NUMBER(10) PRIMARY KEY,
					"email" VARCHAR2(255) NOT NULL UNIQUE,
					"name" VARCHAR2(4000)
				)`,
				`CREATE TABLE "jd_posts" (
					"id" NUMBER(10) PRIMARY KEY,
					"title" VARCHAR2(255) NOT NULL,
					"author_id" NUMBER(10) NOT NULL,
					CONSTRAINT "jd_posts_author_fk" FOREIGN KEY ("author_id")
						REFERENCES "jd_users"("id") ON DELETE CASCADE
				)`,
				`CREATE INDEX "jd_posts_author_idx" ON "jd_posts"("author_id")`,
				// Oracle has no multi-row VALUES clause.
				`INSERT INTO "jd_users"("id","email","name") VALUES (1,'a@x.io','Ann')`,
				`INSERT INTO "jd_users"("id","email","name") VALUES (2,'b@x.io',NULL)`,
				`INSERT INTO "jd_posts"("id","title","author_id") VALUES (1,'Hello',1)`,
				`INSERT INTO "jd_posts"("id","title","author_id") VALUES (2,'World',2)`,
			},
		},
		{
			// SQLite needs no server, so unlike the others it is always
			// reachable and never skips. Its DSN is a file, kept in the test's
			// own temp directory so a run leaves nothing behind.
			driver: DriverSQLite, env: "JD_TEST_SQLITE_DSN",
			dsn:    sqliteFixtureFile(),
			schema: "", relational: true,
			teardown: []string{`DROP TABLE IF EXISTS jd_posts`, `DROP TABLE IF EXISTS jd_users`},
			seed: []string{
				`CREATE TABLE jd_users (
					id INTEGER PRIMARY KEY,
					email TEXT NOT NULL UNIQUE,
					name TEXT
				)`,
				`CREATE TABLE jd_posts (
					id INTEGER PRIMARY KEY,
					title TEXT NOT NULL,
					author_id INTEGER NOT NULL REFERENCES jd_users(id) ON DELETE CASCADE
				)`,
				`CREATE INDEX jd_posts_author_idx ON jd_posts(author_id)`,
				`INSERT INTO jd_users(id,email,name) VALUES (1,'a@x.io','Ann'),(2,'b@x.io',NULL)`,
				`INSERT INTO jd_posts(id,title,author_id) VALUES (1,'Hello',1),(2,'World',2)`,
			},
		},
		{
			driver: DriverClickHouse, env: "JD_TEST_CLICKHOUSE_DSN",
			dsn:    "clickhouse://default@127.0.0.1:9000/default",
			schema: "default", relational: false,
			teardown: []string{`DROP TABLE IF EXISTS jd_posts`, `DROP TABLE IF EXISTS jd_users`},
			seed: []string{
				`CREATE TABLE jd_users (
					id Int32, email String, name Nullable(String)
				) ENGINE = MergeTree ORDER BY id`,
				`CREATE TABLE jd_posts (
					id Int32, title String, author_id Int32
				) ENGINE = MergeTree ORDER BY id`,
				`INSERT INTO jd_users(id,email,name) VALUES (1,'a@x.io','Ann'),(2,'b@x.io',NULL)`,
				`INSERT INTO jd_posts(id,title,author_id) VALUES (1,'Hello',1),(2,'World',2)`,
			},
		},
	}
	// On MySQL and ClickHouse the schema is the database, so a DSN that names a
	// different one — a fixture server shared between several runs gives each
	// its own — moves the fixture with it. The unqualified seed statements
	// already land there; only the name the assertions qualify by has to follow.
	for i, f := range fixtures {
		if f.driver != DriverMySQL && f.driver != DriverClickHouse {
			continue
		}
		if info, err := ParseDSN(f.driver, os.Getenv(f.env)); err == nil && info.Database != "" {
			fixtures[i].schema = info.Database
		}
	}
	return fixtures
}

func setupFixture(t *testing.T, db *sql.DB, f engineFixture) {
	t.Helper()
	ctx := context.Background()
	for _, s := range f.teardown {
		_, _ = db.ExecContext(ctx, s)
	}
	for _, s := range f.seed {
		if _, err := db.ExecContext(ctx, s); err != nil {
			t.Fatalf("%s seed failed: %v\n%s", f.driver, err, s)
		}
	}
	t.Cleanup(func() {
		for _, s := range f.teardown {
			_, _ = db.ExecContext(context.Background(), s)
		}
	})
}

// TestLiveSQLEngines runs the whole read surface against every reachable SQL
// engine. This is the test that would have caught a catalogue query naming a
// column the server does not have.
func TestLiveSQLEngines(t *testing.T) {
	for _, f := range sqlFixtures() {
		t.Run(string(f.driver), func(t *testing.T) {
			db := liveSQL(t, f.driver, f.env, f.dsn)
			setupFixture(t, db, f)
			ctx := context.Background()

			t.Run("databases", func(t *testing.T) {
				dbs, err := ListDatabases(ctx, db, f.driver)
				if err != nil {
					t.Fatalf("ListDatabases: %v", err)
				}
				if len(dbs) == 0 {
					t.Error("no databases reported")
				}
			})

			t.Run("tables", func(t *testing.T) {
				tables, err := ListTables(ctx, db, f.driver, f.schema)
				if err != nil {
					t.Fatalf("ListTables: %v", err)
				}
				found := map[string]bool{}
				for _, tb := range tables {
					found[tb.Name] = true
				}
				if !found["jd_users"] || !found["jd_posts"] {
					t.Errorf("seeded tables missing from listing: %v", names(tables))
				}
			})

			t.Run("detail", func(t *testing.T) {
				d, err := Detail(ctx, db, f.driver, f.schema, "jd_posts")
				if err != nil {
					t.Fatalf("Detail: %v", err)
				}
				if len(d.Columns) != 3 {
					t.Errorf("jd_posts columns = %d, want 3: %+v", len(d.Columns), d.Columns)
				}
				byName := map[string]Column{}
				for _, c := range d.Columns {
					byName[c.Name] = c
				}
				if byName["title"].Nullable {
					t.Error("title should be NOT NULL")
				}
				if f.relational {
					if len(d.PrimaryKey) != 1 || d.PrimaryKey[0] != "id" {
						t.Errorf("primary key = %v, want [id]", d.PrimaryKey)
					}
					if len(d.ForeignKeys) != 1 {
						t.Errorf("foreign keys = %d, want 1: %+v", len(d.ForeignKeys), d.ForeignKeys)
					} else if d.ForeignKeys[0].RefTable != "jd_users" {
						t.Errorf("fk references %q, want jd_users", d.ForeignKeys[0].RefTable)
					}
					foundIdx := false
					for _, ix := range d.Indexes {
						if strings.Contains(strings.ToLower(ix.Name), "author") {
							foundIdx = true
						}
					}
					if !foundIdx {
						t.Errorf("seeded index missing: %+v", d.Indexes)
					}
				}
				if strings.TrimSpace(d.CreateSQL) == "" {
					t.Error("CreateSQL is empty")
				}
			})

			t.Run("browse_sort_filter_count", func(t *testing.T) {
				all, err := Browse(ctx, db, f.driver, BrowseOptions{
					Schema: f.schema, Table: "jd_users", Limit: 10,
				})
				if err != nil {
					t.Fatalf("Browse: %v", err)
				}
				if all.RowCount != 2 {
					t.Fatalf("rows = %d, want 2", all.RowCount)
				}

				desc, err := Browse(ctx, db, f.driver, BrowseOptions{
					Schema: f.schema, Table: "jd_users", Limit: 10, OrderBy: "id", Desc: true,
				})
				if err != nil {
					t.Fatalf("Browse sorted: %v", err)
				}
				idIdx := indexOf(desc.Columns, "id")
				if idIdx < 0 {
					t.Fatalf("no id column in %v", desc.Columns)
				}
				if fmt.Sprint(desc.Rows[0][idIdx]) != "2" {
					t.Errorf("descending sort put %v first, want 2", desc.Rows[0][idIdx])
				}

				// A text filter, and a "contains" against a numeric column —
				// the latter only works because the dialect casts first.
				pref, err := Browse(ctx, db, f.driver, BrowseOptions{
					Schema: f.schema, Table: "jd_users", Limit: 10,
					Filters: []Filter{{Column: "email", Op: "prefix", Value: "a@"}},
				})
				if err != nil {
					t.Fatalf("Browse prefix filter: %v", err)
				}
				if pref.RowCount != 1 {
					t.Errorf("prefix filter matched %d, want 1", pref.RowCount)
				}
				num, err := Browse(ctx, db, f.driver, BrowseOptions{
					Schema: f.schema, Table: "jd_users", Limit: 10,
					Filters: []Filter{{Column: "id", Op: "contains", Value: "2"}},
				})
				if err != nil {
					t.Fatalf("Browse contains on numeric column: %v", err)
				}
				if num.RowCount != 1 {
					t.Errorf("numeric contains matched %d, want 1", num.RowCount)
				}

				n, err := Count(ctx, db, f.driver, BrowseOptions{
					Schema: f.schema, Table: "jd_users",
					Filters: []Filter{{Column: "name", Op: "not_null"}},
				})
				if err != nil {
					t.Fatalf("Count: %v", err)
				}
				if n != 1 {
					t.Errorf("filtered count = %d, want 1", n)
				}
			})

			t.Run("export", func(t *testing.T) {
				var buf strings.Builder
				count, _, err := ExportTable(ctx, db, f.driver, f.schema, "jd_users", ExportCSV, &buf, 0)
				if err != nil {
					t.Fatalf("ExportTable: %v", err)
				}
				if count != 2 {
					t.Errorf("exported %d rows, want 2", count)
				}
				if !strings.Contains(buf.String(), "a@x.io") {
					t.Errorf("export missing data: %q", buf.String())
				}
			})

			t.Run("outline_and_orm", func(t *testing.T) {
				outline, err := Outline(ctx, db, f.driver, f.schema)
				if err != nil {
					t.Fatalf("Outline: %v", err)
				}
				if cols, ok := outline.Tables["jd_users"]; !ok || !contains(cols, "email") {
					t.Errorf("outline for jd_users = %v", outline.Tables["jd_users"])
				}

				// Generate ORM schemas from genuinely introspected structure.
				tables, err := ListTables(ctx, db, f.driver, f.schema)
				if err != nil {
					t.Fatal(err)
				}
				details := map[string]*TableDetail{}
				for _, tb := range tables {
					if d, err := Detail(ctx, db, f.driver, tb.Schema, tb.Name); err == nil {
						details[tb.Name] = d
					}
				}
				prisma, err := GenerateORM(ORMPrisma, f.driver, tables, details)
				if err != nil {
					t.Fatalf("GenerateORM prisma: %v", err)
				}
				if !strings.Contains(prisma, "model jd_users") {
					t.Errorf("prisma schema missing jd_users:\n%s", prisma)
				}
				drizzle, err := GenerateORM(ORMDrizzle, f.driver, tables, details)
				if err != nil {
					t.Fatalf("GenerateORM drizzle: %v", err)
				}
				if !strings.Contains(drizzle, "jd_users") {
					t.Errorf("drizzle schema missing jd_users:\n%s", drizzle)
				}
			})

			if !f.relational {
				return
			}

			t.Run("row_mutations", func(t *testing.T) {
				if _, err := InsertRow(ctx, db, f.driver, f.schema, "jd_users",
					map[string]any{"id": 3, "email": "c@x.io", "name": "Cy"}); err != nil {
					t.Fatalf("InsertRow: %v", err)
				}
				if _, err := UpdateRow(ctx, db, f.driver, f.schema, "jd_users",
					map[string]any{"name": "Cyrus"}, map[string]any{"id": 3}); err != nil {
					t.Fatalf("UpdateRow: %v", err)
				}
				var name string
				d := mustDialect(t, f.driver)
				rel, _ := qualify(d, f.schema, "jd_users")
				// Quoted, as the product quotes them: unquoted `id` is folded
				// to ID on Oracle and does not match a column created as "id".
				qName, _ := d.QuoteIdent("name")
				qID, _ := d.QuoteIdent("id")
				if err := db.QueryRowContext(ctx,
					"SELECT "+qName+" FROM "+rel+" WHERE "+qID+" = "+d.Placeholder(1), 3).Scan(&name); err != nil {
					t.Fatalf("read back: %v", err)
				}
				if name != "Cyrus" {
					t.Errorf("after update name = %q, want Cyrus", name)
				}
				if _, err := DeleteRow(ctx, db, f.driver, f.schema, "jd_users",
					map[string]any{"id": 3}); err != nil {
					t.Fatalf("DeleteRow: %v", err)
				}
				n, err := Count(ctx, db, f.driver, BrowseOptions{Schema: f.schema, Table: "jd_users"})
				if err != nil {
					t.Fatal(err)
				}
				if n != 2 {
					t.Errorf("after delete count = %d, want 2", n)
				}
			})

			t.Run("import", func(t *testing.T) {
				csv := "id,email,name\n10,j@x.io,Jo\n11,k@x.io,Kit\n"
				res, err := ImportCSV(ctx, db, f.driver, strings.NewReader(csv), ImportOptions{
					Schema: f.schema, Table: "jd_users", HasHeader: true,
				})
				if err != nil {
					t.Fatalf("ImportCSV: %v", err)
				}
				if res.Inserted != 2 || res.Failed != 0 {
					t.Errorf("import inserted=%d failed=%d %v", res.Inserted, res.Failed, res.Errors)
				}
				// Clean up so a rerun starts from the same state.
				_, _ = DeleteRow(ctx, db, f.driver, f.schema, "jd_users", map[string]any{"id": 10})
				_, _ = DeleteRow(ctx, db, f.driver, f.schema, "jd_users", map[string]any{"id": 11})
			})

			t.Run("ddl", func(t *testing.T) {
				_, _ = DropTable(ctx, db, f.driver, f.schema, "jd_widgets")
				if _, err := CreateTable(ctx, db, f.driver, f.schema, "jd_widgets", []NewColumn{
					{Name: "id", Type: ddlIntType(f.driver), PrimaryKey: true, NotNull: true},
					{Name: "label", Type: ddlTextType(f.driver), NotNull: true, Default: "'none'"},
				}); err != nil {
					t.Fatalf("CreateTable: %v", err)
				}
				t.Cleanup(func() { _, _ = DropTable(context.Background(), db, f.driver, f.schema, "jd_widgets") })

				if _, err := AddColumn(ctx, db, f.driver, f.schema, "jd_widgets",
					NewColumn{Name: "qty", Type: ddlIntType(f.driver), Default: "0"}); err != nil {
					t.Fatalf("AddColumn: %v", err)
				}
				if _, err := CreateIndex(ctx, db, f.driver, f.schema, "jd_widgets",
					"jd_widgets_label_idx", []string{"label"}, false); err != nil {
					t.Fatalf("CreateIndex: %v", err)
				}
				d, err := Detail(ctx, db, f.driver, f.schema, "jd_widgets")
				if err != nil {
					t.Fatalf("Detail after DDL: %v", err)
				}
				if len(d.Columns) != 3 {
					t.Errorf("columns after AddColumn = %d, want 3", len(d.Columns))
				}
				foundIdx := false
				for _, ix := range d.Indexes {
					if ix.Name == "jd_widgets_label_idx" {
						foundIdx = true
					}
				}
				if !foundIdx {
					t.Errorf("created index not reported back: %+v", d.Indexes)
				}
				if _, err := DropIndex(ctx, db, f.driver, f.schema, "jd_widgets", "jd_widgets_label_idx"); err != nil {
					t.Fatalf("DropIndex: %v", err)
				}
				if _, err := DropColumn(ctx, db, f.driver, f.schema, "jd_widgets", "qty"); err != nil {
					t.Fatalf("DropColumn: %v", err)
				}
				if _, err := TruncateTable(ctx, db, f.driver, f.schema, "jd_widgets"); err != nil {
					t.Fatalf("TruncateTable: %v", err)
				}
			})
		})
	}
}

// sqliteFixtureFile is the one fixture DSN that is a path rather than a
// server. It lives beside the other test artefacts and is recreated by the
// seed on every run, so a stale file from a failed run cannot skew a later one.
func sqliteFixtureFile() string {
	return filepath.Join(os.TempDir(), "jd-live-fixture.db")
}

func ddlIntType(d Driver) string {
	switch d {
	case DriverPostgres:
		return "integer"
	case DriverMSSQL:
		return "int"
	case DriverOracle:
		return "NUMBER(10)"
	case DriverSQLite:
		return "INTEGER"
	default:
		return "INT"
	}
}

func ddlTextType(d Driver) string {
	switch d {
	case DriverMSSQL:
		return "nvarchar(255)"
	case DriverPostgres:
		return "text"
	case DriverOracle:
		return "VARCHAR2(255)"
	case DriverSQLite:
		return "TEXT"
	default:
		return "varchar(255)"
	}
}

func names(tables []Table) []string {
	out := make([]string, 0, len(tables))
	for _, t := range tables {
		out = append(out, t.Name)
	}
	return out
}

// TestLiveExplainDoesNotExecute is the assertion the plan feature rests on.
//
// Every dialect must describe a statement without running it. A plan button
// that executed what it was asked to explain would be the worst control in the
// product, so this asks each live engine to explain a DELETE and then checks
// the rows are still there.
func TestLiveExplainDoesNotExecute(t *testing.T) {
	for _, f := range sqlFixtures() {
		t.Run(string(f.driver), func(t *testing.T) {
			db := liveSQL(t, f.driver, f.env, f.dsn)
			setupFixture(t, db, f)
			ctx := context.Background()
			d := mustDialect(t, f.driver)

			before, err := Count(ctx, db, f.driver, BrowseOptions{Schema: f.schema, Table: "jd_users"})
			if err != nil {
				t.Fatal(err)
			}

			rel, err := qualify(d, f.schema, "jd_users")
			if err != nil {
				t.Fatal(err)
			}

			// A plain SELECT plan first: it must come back with rows describing
			// the plan rather than the data.
			plan, err := d.ExplainPlan(ctx, db, "SELECT * FROM "+rel)
			if err != nil {
				t.Fatalf("ExplainPlan(select): %v", err)
			}
			if plan.RowCount == 0 {
				t.Errorf("plan came back empty for %s", f.driver)
			}

			// ClickHouse's DELETE is a mutation with different syntax, and it
			// has no rows to protect in the same sense; the SELECT plan above is
			// the meaningful check there.
			if !f.relational {
				return
			}
			if _, err := d.ExplainPlan(ctx, db, "DELETE FROM "+rel); err != nil {
				// Some engines refuse to plan a bare DELETE; that is acceptable.
				t.Logf("%s declined to plan a DELETE: %v", f.driver, err)
			}
			after, err := Count(ctx, db, f.driver, BrowseOptions{Schema: f.schema, Table: "jd_users"})
			if err != nil {
				t.Fatal(err)
			}
			if after != before {
				t.Fatalf("EXPLAIN executed the statement on %s: %d rows became %d",
					f.driver, before, after)
			}
		})
	}
}

// fixtureRel and fixtureCol spell a fixture's table and column the way the
// product spells them — qualified and quoted. A test's own verification SQL
// has to, or it tests a name the dashboard never uses: Oracle folds an
// unquoted identifier to upper case, so a bare `jd_users` is ORA-00942 against
// a table the fixture created as "jd_users".
func fixtureRel(t *testing.T, f engineFixture, table string) string {
	t.Helper()
	rel, err := qualify(mustDialect(t, f.driver), f.schema, table)
	if err != nil {
		t.Fatalf("qualify(%s): %v", table, err)
	}
	return rel
}

func fixtureCol(t *testing.T, f engineFixture, col string) string {
	t.Helper()
	q, err := mustDialect(t, f.driver).QuoteIdent(col)
	if err != nil {
		t.Fatalf("QuoteIdent(%s): %v", col, err)
	}
	return q
}
