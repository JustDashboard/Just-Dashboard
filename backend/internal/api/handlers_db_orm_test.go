package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/go-chi/chi/v5"
)

// The code-generation routes over HTTP.
//
// The generators themselves are tested in dbx against fixtures. What is tested
// here is the part only a request can exercise: that the body's options are
// decoded and refused by name, that a target with no connector for the engine
// is refused before anything is dialled, that the route stays on the read
// surface, and that the answer keeps the two fields the first version of this
// route returned.

// ormRouter is the database routes with a principal of the given role and one
// SQLite connection (id 1) holding a small real schema. SQLite is the engine
// here because it needs no server: the file is created inside the test's own
// file root, which is also what the connection's path is contained to.
func ormRouter(t *testing.T, role auth.Role) (*Server, http.Handler) {
	t.Helper()
	s := testServer(t)
	t.Cleanup(s.Shutdown)

	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			p := &httpx.Principal{
				User: &auth.User{ID: 1, Username: "tester"},
				Role: role, Kind: "session", IP: "127.0.0.1",
			}
			next.ServeHTTP(w, req.WithContext(httpx.WithPrincipal(req.Context(), p)))
		})
	})
	s.mountDatabaseRoutes(r)

	path := filepath.Join(s.Cfg.FileRoots[0], "orm.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE TABLE users (
			id INTEGER PRIMARY KEY,
			email TEXT NOT NULL UNIQUE,
			name TEXT,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE posts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			title VARCHAR(200) NOT NULL,
			published BOOLEAN NOT NULL DEFAULT 0,
			author_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX idx_posts_author ON posts(author_id)`,
		`CREATE VIEW post_titles AS SELECT id, title FROM posts`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("seed: %v\n%s", err, stmt)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	ormAddConnection(t, s, "orm-sqlite", dbx.DriverSQLite, path)
	return s, r
}

func ormAddConnection(t *testing.T, s *Server, name string, driver dbx.Driver, dsn string) int64 {
	t.Helper()
	sealed, err := s.Sealer.Seal(dsn)
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Store.DB.Exec(
		`INSERT INTO db_connections(name, driver, dsn_enc, created_at) VALUES(?,?,?,?)`,
		name, string(driver), sealed, 0)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

type ormAnswer struct {
	Target   string `json:"target"`
	Language string `json:"language"`
	Schema   string `json:"schema"`
	Filename string `json:"filename"`
	Files    []struct {
		Filename string `json:"filename"`
		Content  string `json:"content"`
	} `json:"files"`
	Warnings []string `json:"warnings"`
	Counts   struct {
		Tables    int `json:"tables"`
		Views     int `json:"views"`
		Enums     int `json:"enums"`
		Relations int `json:"relations"`
	} `json:"counts"`
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func ormPost(t *testing.T, r http.Handler, path, body string) (*httptest.ResponseRecorder, ormAnswer) {
	t.Helper()
	rec := do(t, r, http.MethodPost, path, body)
	var res ormAnswer
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode %s: %v: %s", path, err, rec.Body.String())
	}
	return rec, res
}

// The picker is drawn from this list, so the list has to say everything the
// picker needs: what the file is, what language it is in, which engines it
// exists for and why not the others, and which switches it takes.
func TestORMTargetsDescribeEveryGenerator(t *testing.T) {
	_, r := ormRouter(t, auth.RoleReadOnly)
	rec := do(t, r, http.MethodGet, "/databases/orm/targets", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("targets: %d %s", rec.Code, rec.Body.String())
	}
	var res struct {
		Targets []struct {
			ID          string            `json:"id"`
			Label       string            `json:"label"`
			Filename    string            `json:"filename"`
			Description string            `json:"description"`
			Language    string            `json:"language"`
			Group       string            `json:"group"`
			Engines     []string          `json:"engines"`
			Unsupported map[string]string `json:"unsupported"`
			Options     []struct {
				ID      string `json:"id"`
				Label   string `json:"label"`
				Type    string `json:"type"`
				Default any    `json:"default"`
				Choices []struct {
					Value string `json:"value"`
					Label string `json:"label"`
				} `json:"choices"`
			} `json:"options"`
		} `json:"targets"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(res.Targets) != len(dbx.ORMTargets()) {
		t.Fatalf("catalogue lists %d targets, the package has %d", len(res.Targets), len(dbx.ORMTargets()))
	}

	// The first four are the targets this route has always had. Their place,
	// ids, labels, file names and descriptions are what existing callers show.
	original := [][4]string{
		{"prisma", "Prisma", "schema.prisma", "A schema.prisma to drop into an existing Prisma project."},
		{"drizzle", "Drizzle", "schema.ts", "Drizzle ORM table definitions."},
		{"typescript", "TypeScript types", "types.ts", "Plain interfaces — no runtime dependency, useful with any client."},
		{"zod", "Zod schemas", "schemas.ts", "Runtime validators, plus an insert variant with defaults optional."},
	}
	for i, want := range original {
		got := res.Targets[i]
		if got.ID != want[0] || got.Label != want[1] || got.Filename != want[2] || got.Description != want[3] {
			t.Errorf("target %d = %s/%s/%s/%q, want %v", i, got.ID, got.Label, got.Filename, got.Description, want)
		}
	}

	languages := map[string]bool{"prisma": true, "typescript": true, "python": true, "go": true, "rust": true,
		"php": true, "json": true, "graphql": true, "sql": true}
	groups := map[string]bool{"ORM": true, "Query builder": true, "Types & validation": true, "Schema": true}
	engines := []string{"postgres", "mysql", "sqlite", "sqlserver", "clickhouse", "oracle"}
	for _, tg := range res.Targets {
		if tg.Label == "" || tg.Filename == "" || tg.Description == "" {
			t.Errorf("%s is missing a label, a file name or a description", tg.ID)
		}
		if !languages[tg.Language] {
			t.Errorf("%s has language %q", tg.ID, tg.Language)
		}
		if !groups[tg.Group] {
			t.Errorf("%s has group %q", tg.ID, tg.Group)
		}
		if len(tg.Engines) == 0 {
			t.Errorf("%s supports no engine", tg.ID)
		}
		supported := map[string]bool{}
		for _, e := range tg.Engines {
			supported[e] = true
		}
		// Every SQL engine is either supported or refused with a reason.
		for _, e := range engines {
			reason, refused := tg.Unsupported[e]
			switch {
			case supported[e] && refused:
				t.Errorf("%s both supports and refuses %s", tg.ID, e)
			case !supported[e] && strings.TrimSpace(reason) == "":
				t.Errorf("%s does not support %s and gives no reason", tg.ID, e)
			}
		}
		for _, o := range tg.Options {
			if o.ID == "" || o.Label == "" || o.Default == nil {
				t.Errorf("%s has an option with no id, label or default: %+v", tg.ID, o)
			}
			switch o.Type {
			case "boolean":
				if _, ok := o.Default.(bool); !ok {
					t.Errorf("%s.%s is boolean with default %v", tg.ID, o.ID, o.Default)
				}
			case "select":
				found := false
				for _, c := range o.Choices {
					found = found || c.Value == o.Default
				}
				if !found {
					t.Errorf("%s.%s defaults to %v, which is not one of its choices", tg.ID, o.ID, o.Default)
				}
			case "text":
			default:
				t.Errorf("%s.%s has type %q", tg.ID, o.ID, o.Type)
			}
		}
	}
}

// Generation reads the catalogue and writes nothing, so a read-only role gets
// an answer from every target — and each answer keeps `schema` and `filename`,
// the two fields the route returned before it could return more than one file.
func TestORMEveryTargetAnswersAReadOnlyRole(t *testing.T) {
	_, r := ormRouter(t, auth.RoleReadOnly)
	for _, target := range dbx.ORMTargets() {
		rec, res := ormPost(t, r, "/databases/1/orm", `{"target":"`+string(target)+`"}`)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: %d %s", target, rec.Code, rec.Body.String())
			continue
		}
		if res.Schema == "" || res.Filename == "" {
			t.Errorf("%s: schema or filename is empty", target)
		}
		if len(res.Files) == 0 || res.Files[0].Content != res.Schema || res.Files[0].Filename != res.Filename {
			t.Errorf("%s: schema/filename are not the first file", target)
		}
		if res.Warnings == nil {
			t.Errorf("%s: warnings is null, want an array", target)
		}
		if res.Target != string(target) || res.Language == "" {
			t.Errorf("%s: target/language = %q/%q", target, res.Target, res.Language)
		}
		if res.Counts.Tables != 2 {
			t.Errorf("%s: counted %d tables, want 2", target, res.Counts.Tables)
		}
		if strings.Contains(rec.Body.String(), "confirmation") {
			t.Errorf("%s asked for a typed phrase", target)
		}
	}
}

// The body the first version of this route took — a target and a schema —
// still works, and still returns the schema of the whole database.
func TestORMOldRequestStillWorks(t *testing.T) {
	_, r := ormRouter(t, auth.RoleAdmin)
	rec, res := ormPost(t, r, "/databases/1/orm", `{"target":"prisma","schema":"main"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("prisma: %d %s", rec.Code, rec.Body.String())
	}
	for _, want := range []string{
		`provider = "sqlite"`, "model users {", "model posts {", "id Int @id",
		`@relation("posts_author_id", fields: [author_id], references: [id]`,
	} {
		if !strings.Contains(res.Schema, want) {
			t.Errorf("prisma schema missing %q:\n%s", want, res.Schema)
		}
	}
	if res.Filename != "schema.prisma" {
		t.Errorf("filename = %q", res.Filename)
	}
	if strings.Contains(res.Schema, "post_titles") {
		t.Errorf("a view became a Prisma model:\n%s", res.Schema)
	}
}

func TestORMOptionsAreDecodedAndRefusedByName(t *testing.T) {
	_, r := ormRouter(t, auth.RoleAdmin)

	t.Run("selected_tables", func(t *testing.T) {
		rec, res := ormPost(t, r, "/databases/1/orm", `{"target":"drizzle","tables":["users"]}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("%d %s", rec.Code, rec.Body.String())
		}
		if res.Counts.Tables != 1 || strings.Contains(res.Schema, `sqliteTable("posts"`) {
			t.Errorf("selection kept more than users:\n%s", res.Schema)
		}
	})
	t.Run("views_on_request", func(t *testing.T) {
		rec, res := ormPost(t, r, "/databases/1/orm", `{"target":"drizzle","views":true}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("%d %s", rec.Code, rec.Body.String())
		}
		if res.Counts.Views != 1 || !strings.Contains(res.Schema, `sqliteView("post_titles"`) {
			t.Errorf("the view was not emitted:\n%s", res.Schema)
		}
	})
	t.Run("relations_off", func(t *testing.T) {
		rec, res := ormPost(t, r, "/databases/1/orm", `{"target":"prisma","relations":false}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("%d %s", rec.Code, rec.Body.String())
		}
		if strings.Contains(res.Schema, "@relation") || res.Counts.Relations != 0 {
			t.Errorf("relations were emitted with relations:false:\n%s", res.Schema)
		}
	})
	t.Run("camel_naming", func(t *testing.T) {
		rec, res := ormPost(t, r, "/databases/1/orm", `{"target":"prisma","naming":"camel"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("%d %s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(res.Schema, `authorId Int @map("author_id")`) {
			t.Errorf("camelCase fields were not mapped back:\n%s", res.Schema)
		}
	})
	t.Run("more_than_one_file", func(t *testing.T) {
		rec, res := ormPost(t, r, "/databases/1/orm", `{"target":"diesel"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("%d %s", rec.Code, rec.Body.String())
		}
		if len(res.Files) != 2 || res.Files[0].Filename != "schema.rs" || res.Files[1].Filename != "models.rs" {
			t.Errorf("diesel files = %+v", res.Files)
		}
	})

	for name, c := range map[string]struct{ body, want string }{
		"unknown_target":       {`{"target":"hibernate"}`, "target must be one of"},
		"unknown_field":        {`{"target":"prisma","frobnicate":true}`, "frobnicate"},
		"option_of_another":    {`{"target":"prisma","jsonTags":true}`, `option "jsonTags" does not apply to the Prisma target`},
		"choice_out_of_range":  {`{"target":"prisma","naming":"kebab"}`, "naming must be one of preserve, camel"},
		"package_not_a_name":   {`{"target":"gorm","package":"My Models"}`, "package must be a lower-case Go package name"},
		"table_that_is_absent": {`{"target":"prisma","tables":["nope"]}`, `table "nope" is not in the selected schemas`},
		"control_character":    {`{"target":"prisma","tables":["a\u0000b"]}`, "control character"},
	} {
		t.Run(name, func(t *testing.T) {
			rec, res := ormPost(t, r, "/databases/1/orm", c.body)
			if rec.Code != http.StatusBadRequest || res.Error.Code != "bad_request" {
				t.Fatalf("%d %s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(res.Error.Message, c.want) {
				t.Errorf("message = %q, want it to contain %q", res.Error.Message, c.want)
			}
		})
	}
}

// A target with no connector for the engine is refused with the reason, and
// before the connection is opened: these connections point at a port nothing
// listens on, so an answer that mentions the connection means the handler
// dialled first.
func TestORMRefusesEnginesATargetCannotReach(t *testing.T) {
	s, r := ormRouter(t, auth.RoleAdmin)
	clickhouse := ormAddConnection(t, s, "orm-clickhouse", dbx.DriverClickHouse, "clickhouse://u:p@127.0.0.1:1/db")
	oracle := ormAddConnection(t, s, "orm-oracle", dbx.DriverOracle, "oracle://u:p@127.0.0.1:1/X")
	mssql := ormAddConnection(t, s, "orm-mssql", dbx.DriverMSSQL, "sqlserver://u:p@127.0.0.1:1?database=x")

	for _, c := range []struct {
		id     int64
		target string
		want   string
	}{
		{clickhouse, "prisma", "Prisma has no connector for ClickHouse"},
		{oracle, "prisma", "Prisma has no connector for Oracle"},
		{mssql, "drizzle", "none for SQL Server"},
		{clickhouse, "typeorm", "TypeORM has no ClickHouse support"},
		{oracle, "diesel", "none for Oracle"},
		{oracle, "eloquent", "none for Oracle"},
	} {
		rec, res := ormPost(t, r, pathf("/databases/%d/orm", c.id), `{"target":"`+c.target+`"}`)
		if rec.Code != http.StatusBadRequest || res.Error.Code != "bad_request" {
			t.Errorf("%s on connection %d: %d %s", c.target, c.id, rec.Code, rec.Body.String())
			continue
		}
		if !strings.Contains(res.Error.Message, c.want) {
			t.Errorf("%s on connection %d: message = %q, want it to contain %q", c.target, c.id, res.Error.Message, c.want)
		}
	}
}

// MongoDB and Redis have no relational schema. They are refused the way every
// SQL-only route refuses them, by naming the surface the engine does have.
func TestORMLeavesNonSQLEnginesToTheirOwnSurface(t *testing.T) {
	s, r := ormRouter(t, auth.RoleAdmin)
	mongo := ormAddConnection(t, s, "orm-mongo", dbx.DriverMongo, "mongodb://127.0.0.1:1/db")
	redis := ormAddConnection(t, s, "orm-redis", dbx.DriverRedis, "redis://127.0.0.1:1/0")
	for id, driver := range map[int64]string{mongo: "mongodb", redis: "redis"} {
		rec, res := ormPost(t, r, pathf("/databases/%d/orm", id), `{"target":"prisma"}`)
		want := "this endpoint is for SQL engines; " + driver + " uses its own surface"
		if rec.Code != http.StatusBadRequest || res.Error.Message != want {
			t.Errorf("%s: %d %q, want 400 %q", driver, rec.Code, res.Error.Message, want)
		}
	}
}

// TestLiveORMOverHTTP drives the route against a real PostgreSQL catalogue: an
// enum, a foreign key and a view in a schema of their own. It is skipped unless
// JD_TEST_POSTGRES_DSN is set — there is deliberately no default address,
// because the test creates and drops a schema.
func TestLiveORMOverHTTP(t *testing.T) {
	dsn := os.Getenv("JD_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set JD_TEST_POSTGRES_DSN to run this against a real PostgreSQL")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Skipf("postgres unavailable: %v", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		t.Skipf("postgres unreachable: %v", err)
	}
	drop := func() { _, _ = db.Exec(`DROP SCHEMA IF EXISTS jd_orm_api CASCADE`) }
	drop()
	t.Cleanup(drop)
	for _, stmt := range []string{
		`CREATE SCHEMA jd_orm_api`,
		`CREATE TYPE jd_orm_api.plan AS ENUM ('free', 'pro')`,
		`CREATE TABLE jd_orm_api.accounts (
			id bigserial PRIMARY KEY, email text NOT NULL UNIQUE, plan jd_orm_api.plan NOT NULL DEFAULT 'free',
			created_at timestamptz NOT NULL DEFAULT now())`,
		`CREATE TABLE jd_orm_api.invoices (
			id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
			account_id bigint NOT NULL REFERENCES jd_orm_api.accounts(id) ON DELETE CASCADE,
			amount numeric(12,2) NOT NULL)`,
		`CREATE VIEW jd_orm_api.totals AS SELECT account_id, sum(amount) AS total FROM jd_orm_api.invoices GROUP BY 1`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("seed: %v\n%s", err, stmt)
		}
	}

	_, r, id := liveAPIRouter(t, dbx.DriverPostgres, dsn)
	path := pathf("/databases/%d/orm", id)

	rec, res := ormPost(t, r, path, `{"target":"prisma","schema":"jd_orm_api"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("prisma: %d %s", rec.Code, rec.Body.String())
	}
	for _, want := range []string{
		"model accounts {", "id BigInt @id @default(autoincrement())", "plan plan @default(free)",
		"created_at DateTime @default(now()) @db.Timestamptz(6)",
		`id String @id @default(dbgenerated("gen_random_uuid()")) @db.Uuid`,
		"amount Decimal @db.Decimal(12, 2)",
		`account accounts @relation("invoices_account_id", fields: [account_id], references: [id], onDelete: Cascade, onUpdate: NoAction)`,
		`invoices invoices[] @relation("invoices_account_id")`,
		"enum plan {",
	} {
		if !strings.Contains(res.Schema, want) {
			t.Errorf("prisma schema missing %q:\n%s", want, res.Schema)
		}
	}
	if res.Counts.Tables != 2 || res.Counts.Enums != 1 || res.Counts.Relations != 1 || res.Counts.Views != 0 {
		t.Errorf("counts = %+v", res.Counts)
	}
	// One schema, and not the default one: the result has to say how to reach it.
	if !strings.Contains(strings.Join(res.Warnings, "\n"), "?schema=jd_orm_api") {
		t.Errorf("warnings = %v", res.Warnings)
	}

	rec, res = ormPost(t, r, path, `{"target":"sql","schemas":["jd_orm_api"],"views":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("sql: %d %s", rec.Code, rec.Body.String())
	}
	for _, want := range []string{
		`CREATE TYPE "jd_orm_api"."plan" AS ENUM ('free', 'pro');`,
		`CREATE TABLE "jd_orm_api"."accounts" (`, `"id" bigserial NOT NULL,`,
		"CREATE UNIQUE INDEX accounts_email_key ON jd_orm_api.accounts USING btree (email);",
		`ALTER TABLE "jd_orm_api"."invoices" ADD CONSTRAINT "invoices_account_id_fkey" FOREIGN KEY ("account_id") REFERENCES "jd_orm_api"."accounts" ("id") ON DELETE CASCADE;`,
		`CREATE VIEW "jd_orm_api"."totals" AS`,
	} {
		if !strings.Contains(res.Schema, want) {
			t.Errorf("sql output missing %q:\n%s", want, res.Schema)
		}
	}
	if res.Counts.Views != 1 {
		t.Errorf("views counted = %d, want 1", res.Counts.Views)
	}

	rec, res = ormPost(t, r, path, `{"target":"drizzle","schema":"jd_orm_api","tables":["jd_orm_api.accounts"]}`)
	if rec.Code != http.StatusOK || res.Counts.Tables != 1 || strings.Contains(res.Schema, "invoices") {
		t.Errorf("selection: %d %+v\n%s", rec.Code, res.Counts, res.Schema)
	}
	rec, res = ormPost(t, r, path, `{"target":"prisma","schema":"jd_orm_api","tables":["nope"]}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(res.Error.Message, `table "nope" is not in the selected schemas`) {
		t.Errorf("absent table: %d %s", rec.Code, rec.Body.String())
	}
	rec, res = ormPost(t, r, path, `{"target":"prisma","schema":"jd_orm_api_missing"}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(res.Error.Message, "there are no tables here to generate from") {
		t.Errorf("empty schema: %d %s", rec.Code, rec.Body.String())
	}
}
