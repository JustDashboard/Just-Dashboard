package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
)

// Every structure form, over HTTP, against the two engines whose statements
// are least like anyone else's. Each change is asked for twice — shown, then
// run — because what a form is for is that the statement in the dialog is the
// statement that runs, and only a server can say the statement runs at all.

// liveForms drives the schema routes of one live connection.
//
// A fresh one is made for each group of changes. The destructive budget is
// ten requests in a burst for one principal, a preview of a drop spends it as
// the drop does, and a server made per group is how the real limiter stays in
// the path without the test having to wait for it to refill.
type liveForms struct {
	t      *testing.T
	router http.Handler
	id     int64
}

func newLiveForms(t *testing.T, driver dbx.Driver, dsn string) *liveForms {
	t.Helper()
	_, router, id := liveAPIRouter(t, driver, dsn)
	return &liveForms{t: t, router: router, id: id}
}

func (f *liveForms) path(suffix string) string { return pathf("/databases/%d", f.id) + suffix }

func (f *liveForms) send(method, suffix, body string) map[string]any {
	f.t.Helper()
	rec := do(f.t, f.router, method, f.path(suffix), body)
	if rec.Code != http.StatusOK {
		f.t.Fatalf("%s %s %s: %d %s", method, suffix, body, rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		f.t.Fatalf("%s %s: %v in %s", method, suffix, err, rec.Body.String())
	}
	return out
}

// change previews a structure change and then runs it, and holds the two to
// the same statement. It returns that statement.
func (f *liveForms) change(method, suffix, body string) string {
	f.t.Helper()
	preview := f.send(method, suffix+"?preview=1", body)
	statement, _ := preview["statement"].(string)
	if statement == "" || preview["preview"] != true {
		f.t.Fatalf("%s %s?preview=1 = %v", method, suffix, preview)
	}
	ran := f.send(method, suffix, body)
	if ran["statement"] != statement || ran["preview"] != nil {
		f.t.Errorf("%s %s ran\n%v\nafter previewing\n%s", method, suffix, ran["statement"], statement)
	}
	return statement
}

// refused expects a change this engine has no form for to be turned down the
// same way shown or run, with a reason that names what the engine lacks.
func (f *liveForms) refused(method, suffix, body, says string) {
	f.t.Helper()
	for _, query := range []string{"?preview=1", ""} {
		rec := do(f.t, f.router, method, f.path(suffix+query), body)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), says) {
			f.t.Errorf("%s %s%s = %d %s, want a 400 saying %q", method, suffix, query, rec.Code, rec.Body.String(), says)
		}
	}
}

func (f *liveForms) get(suffix string, into any) {
	f.t.Helper()
	if code := getJSON(f.t, f.router, f.path(suffix), into); code != http.StatusOK {
		f.t.Fatalf("GET %s = %d", suffix, code)
	}
}

func liveOpen(t *testing.T, driver dbx.Driver, dsn string) *sql.DB {
	t.Helper()
	d, err := dbx.DialectFor(driver)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open(d.SQLDriverName(), d.NormaliseDSN(dsn))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.PingContext(context.Background()); err != nil {
		t.Skipf("%s unreachable: %v", driver, err)
	}
	return db
}

func liveExec(t *testing.T, db *sql.DB, statements ...string) {
	t.Helper()
	for _, s := range statements {
		if _, err := db.ExecContext(context.Background(), s); err != nil {
			t.Fatalf("%v\n%s", err, s)
		}
	}
}

func liveColumn(t *testing.T, detail dbx.TableDetail, name string) dbx.Column {
	t.Helper()
	for _, c := range detail.Columns {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no column %s in %+v", name, detail.Columns)
	return dbx.Column{}
}

func liveNamed[T any](list []T, name func(T) string, want string) *T {
	for i := range list {
		if name(list[i]) == want {
			return &list[i]
		}
	}
	return nil
}

func liveObject(objects []dbx.CatalogObject, name string) *dbx.CatalogObject {
	return liveNamed(objects, func(o dbx.CatalogObject) string { return o.Name }, name)
}

func liveConstraint(detail dbx.TableDetail, name string) *dbx.Constraint {
	return liveNamed(detail.Constraints, func(c dbx.Constraint) string { return c.Name }, name)
}

func liveIndex(detail dbx.TableDetail, name string) *dbx.Index {
	return liveNamed(detail.Indexes, func(ix dbx.Index) string { return ix.Name }, name)
}

// sqlServerLiveDSN returns the address these tests build their schema at on
// the SQL Server the environment names, or skips. The fixture's DSN names
// master, which every suite pointed at that server shares, so a DSN that names
// master (or nothing) is pointed at a database of this suite's own instead —
// the one dbx's live tests use, with other schemas in it. A DSN that names
// any other database is taken at its word.
func sqlServerLiveDSN(t *testing.T) string {
	t.Helper()
	raw := os.Getenv("JD_TEST_MSSQL_DSN")
	if raw == "" {
		t.Skip("set JD_TEST_MSSQL_DSN to run this")
	}
	dsn, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("JD_TEST_MSSQL_DSN is not a sqlserver:// address: %v", err)
	}
	query := dsn.Query()
	if named := query.Get("database"); named != "" && !strings.EqualFold(named, "master") {
		return raw
	}
	const database = "jd_schema_live"
	server := liveOpen(t, dbx.DriverMSSQL, raw)
	ctx := context.Background()
	if _, err := server.ExecContext(ctx, `IF DB_ID(N'`+database+`') IS NULL CREATE DATABASE `+database); err != nil {
		// Another package's tests may have found it missing in the same moment
		// and created it first.
		var id sql.NullInt64
		if scanErr := server.QueryRowContext(ctx, `SELECT DB_ID(N'`+database+`')`).Scan(&id); scanErr != nil || !id.Valid {
			t.Fatalf("could not create %s: %v", database, err)
		}
	}
	query.Set("database", database)
	dsn.RawQuery = query.Encode()
	return dsn.String()
}

func TestLiveAPISchemaFormsSQLServer(t *testing.T) {
	const driver = dbx.DriverMSSQL
	dsn := sqlServerLiveDSN(t)
	db := liveOpen(t, driver, dsn)
	drop := []string{
		`DROP VIEW IF EXISTS jd_api.open_jobs`,
		`DROP TABLE IF EXISTS jd_api.jobs`, `DROP TABLE IF EXISTS jd_api.tasks`, `DROP TABLE IF EXISTS jd_api.owners`,
		`DROP SCHEMA IF EXISTS jd_api`,
	}
	liveExec(t, db, drop...)
	t.Cleanup(func() {
		for _, s := range drop {
			_, _ = db.ExecContext(context.Background(), s)
		}
	})

	t.Run("build", func(t *testing.T) {
		f := newLiveForms(t, driver, dsn)
		if got := f.change(http.MethodPost, "/ddl/schema", `{"name":"jd_api"}`); got != "CREATE SCHEMA [jd_api]" {
			t.Errorf("schema: %s", got)
		}
		// A preview is the statement and nothing else: the table it showed is
		// not there afterwards.
		owners := `{"schema":"jd_api","table":"owners","columns":[
			{"name":"id","type":"int","primaryKey":true,"notNull":true},{"name":"name","type":"nvarchar(100)"}]}`
		f.send(http.MethodPost, "/ddl/table?preview=1", owners)
		if code := getJSON(t, f.router, f.path("/table?schema=jd_api&table=owners"), nil); code != http.StatusNotFound {
			t.Fatalf("the table a preview showed = %d, want 404", code)
		}
		f.change(http.MethodPost, "/ddl/table", owners)
		f.change(http.MethodPost, "/ddl/table", `{"schema":"jd_api","table":"tasks","columns":[
			{"name":"id","type":"int","primaryKey":true,"notNull":true},
			{"name":"owner_id","type":"int"},
			{"name":"title","type":"nvarchar(200)","notNull":true,"default":"'untitled'"},
			{"name":"effort","type":"varchar(10)"}]}`)
		f.change(http.MethodPost, "/ddl/column", `{"schema":"jd_api","table":"tasks",
			"column":{"name":"seen","type":"datetime2(3)","notNull":true,"default":"sysutcdatetime()"}}`)

		// Type, nullability and default in one request are one atomic batch;
		// a default is a constraint object, dropped by the name the server gave it.
		got := f.change(http.MethodPatch, "/ddl/column", `{"schema":"jd_api","table":"tasks","name":"effort","type":"int","nullable":false,"default":"1"}`)
		if !strings.Contains(got, "ALTER COLUMN [effort] int NOT NULL") || !strings.Contains(got, "BEGIN TRANSACTION") ||
			!strings.Contains(got, "DEFAULT 1 FOR [effort]") {
			t.Errorf("alter column: %s", got)
		}
		if got := f.change(http.MethodPatch, "/ddl/column", `{"schema":"jd_api","table":"tasks","name":"effort","dropDefault":true}`); !strings.Contains(got, "DROP CONSTRAINT") {
			t.Errorf("drop default: %s", got)
		}
		if got := f.change(http.MethodPatch, "/ddl/column", `{"schema":"jd_api","table":"tasks","name":"title","nullable":true}`); got != "ALTER TABLE [jd_api].[tasks] ALTER COLUMN [title] nvarchar(200) NULL" {
			t.Errorf("relax a column: %s", got)
		}

		got = f.change(http.MethodPost, "/ddl/index", `{"schema":"jd_api","table":"tasks","name":"tasks_effort_idx","fields":["effort"],
			"method":"nonclustered","where":"[effort] > 0","concurrently":true}`)
		if got != "CREATE NONCLUSTERED INDEX [tasks_effort_idx] ON [jd_api].[tasks] ([effort]) WHERE ([effort] > 0) WITH (ONLINE = ON)" {
			t.Errorf("index: %s", got)
		}
		if got := f.change(http.MethodPost, "/ddl/rename", `{"schema":"jd_api","table":"tasks","kind":"column","name":"title","to":"caption"}`); got != "EXEC sp_rename N'[jd_api].[tasks].[title]', N'caption', 'COLUMN'" {
			t.Errorf("rename column: %s", got)
		}
		if got := f.change(http.MethodPost, "/ddl/rename", `{"schema":"jd_api","table":"tasks","to":"jobs"}`); got != "EXEC sp_rename N'[jd_api].[tasks]', N'jobs'" {
			t.Errorf("rename table: %s", got)
		}

		got = f.change(http.MethodPost, "/ddl/foreign-key", `{"schema":"jd_api","table":"jobs","columns":["owner_id"],
			"refTable":"owners","refColumns":["id"],"onDelete":"cascade"}`)
		if got != "ALTER TABLE [jd_api].[jobs] ADD CONSTRAINT [jobs_owner_id_fkey] FOREIGN KEY ([owner_id]) REFERENCES [jd_api].[owners] ([id]) ON DELETE CASCADE" {
			t.Errorf("foreign key: %s", got)
		}
		f.change(http.MethodPost, "/ddl/constraint", `{"schema":"jd_api","table":"jobs","type":"unique","columns":["owner_id","caption"]}`)
		f.change(http.MethodPost, "/ddl/constraint", `{"schema":"jd_api","table":"jobs","type":"check","name":"jobs_caption_len","expression":"len([caption]) > 0"}`)

		f.change(http.MethodPost, "/ddl/view", `{"schema":"jd_api","name":"open_jobs","query":"SELECT id FROM jd_api.jobs"}`)
		if got := f.change(http.MethodPost, "/ddl/view", `{"schema":"jd_api","name":"open_jobs","query":"SELECT id, caption FROM jd_api.jobs","replace":true}`); !strings.HasPrefix(got, "CREATE OR ALTER VIEW [jd_api].[open_jobs] AS") {
			t.Errorf("replace view: %s", got)
		}

		// The same batch adds a description that is not there and updates one
		// that is.
		f.change(http.MethodPost, "/ddl/comment", `{"schema":"jd_api","table":"jobs","comment":"What is left to do"}`)
		f.change(http.MethodPost, "/ddl/comment", `{"schema":"jd_api","table":"jobs","column":"effort","comment":"In days"}`)
		f.change(http.MethodPost, "/ddl/comment", `{"schema":"jd_api","table":"jobs","column":"effort","comment":"In hours, it's said"}`)

		f.refused(http.MethodPost, "/ddl/enum", `{"schema":"jd_api","name":"state","values":["new"]}`, "SQL Server has no enum types")
		f.refused(http.MethodPost, "/ddl/enum/value", `{"schema":"jd_api","name":"state","value":"done"}`, "SQL Server has no enum types")
		f.refused(http.MethodPost, "/ddl/view", `{"schema":"jd_api","name":"m","query":"SELECT 1 AS one","materialized":true}`, "no materialized views")
		f.refused(http.MethodPost, "/ddl/index", `{"schema":"jd_api","table":"jobs","fields":["id"],"ifNotExists":true}`, "IF NOT EXISTS")
		// A statement after the type is a second statement to SQL Server, which
		// needs nothing between two.
		f.refused(http.MethodPost, "/ddl/column", `{"schema":"jd_api","table":"jobs","column":{"name":"x","type":"int DROP TABLE jd_api.owners"}}`, "not one this form can build")
	})

	t.Run("read", func(t *testing.T) {
		f := newLiveForms(t, driver, dsn)
		var catalog dbx.Catalog
		f.get("/catalog?schema=jd_api", &catalog)
		if catalog.Schema != "jd_api" || catalog.DefaultSchema != "dbo" || catalog.Errors != nil ||
			len(catalog.Objects[dbx.GroupTables]) != 2 || liveObject(catalog.Objects[dbx.GroupTables], "jobs") == nil ||
			liveObject(catalog.Objects[dbx.GroupViews], "open_jobs") == nil {
			t.Errorf("catalogue = %+v", catalog)
		}
		if jobs := liveObject(catalog.Objects[dbx.GroupTables], "jobs"); jobs == nil || jobs.Comment != "What is left to do" {
			t.Errorf("jobs in the catalogue = %+v", jobs)
		}
		var view, table dbx.ObjectDefinition
		f.get("/object?kind=view&schema=jd_api&name=open_jobs", &view)
		if !strings.Contains(view.Definition, "SELECT id, caption FROM jd_api.jobs") || view.Source != dbx.DefinitionFromEngine {
			t.Errorf("view = %+v", view)
		}
		f.get("/object?kind=table&schema=jd_api&name=jobs", &table)
		if !strings.Contains(table.Definition, "CREATE TABLE [jd_api].[jobs]") || table.Source != dbx.DefinitionGenerated {
			t.Errorf("table definition = %+v", table)
		}
		if code := getJSON(t, f.router, f.path("/object?kind=procedure&schema=jd_api&name=nope"), nil); code != http.StatusNotFound {
			t.Errorf("a procedure that is not there = %d", code)
		}

		var jobs, owners dbx.TableDetail
		f.get("/table?schema=jd_api&table=jobs", &jobs)
		if jobs.Comment != "What is left to do" || liveColumn(t, jobs, "effort").Comment != "In hours, it's said" ||
			liveColumn(t, jobs, "effort").Type != "int" || liveColumn(t, jobs, "effort").Nullable || liveColumn(t, jobs, "effort").Default != "" ||
			!liveColumn(t, jobs, "caption").Nullable || liveColumn(t, jobs, "caption").Default != "('untitled')" ||
			liveColumn(t, jobs, "seen").Default == "" {
			t.Errorf("columns = %+v", jobs.Columns)
		}
		if c := liveConstraint(jobs, "jobs_caption_len"); c == nil || c.Type != dbx.ConstraintCheck {
			t.Errorf("constraints = %+v", jobs.Constraints)
		}
		if c := liveConstraint(jobs, "jobs_owner_id_caption_key"); c == nil || c.Type != dbx.ConstraintUnique {
			t.Errorf("constraints = %+v", jobs.Constraints)
		}
		if ix := liveIndex(jobs, "tasks_effort_idx"); ix == nil || ix.Predicate == "" || ix.Method != "NONCLUSTERED" {
			t.Errorf("indexes = %+v", jobs.Indexes)
		}
		if len(jobs.ForeignKeys) != 1 || jobs.ForeignKeys[0].Name != "jobs_owner_id_fkey" || jobs.ForeignKeys[0].OnDelete != "CASCADE" {
			t.Errorf("foreign keys = %+v", jobs.ForeignKeys)
		}
		f.get("/table?schema=jd_api&table=owners", &owners)
		if len(owners.ReferencedBy) != 1 || owners.ReferencedBy[0].Table != "jobs" {
			t.Errorf("owners is referenced by %+v", owners.ReferencedBy)
		}
		var graph dbx.SchemaGraph
		f.get("/graph?schema=jd_api", &graph)
		if len(graph.Edges) != 1 || graph.Edges[0].From != "jd_api.jobs" || graph.Edges[0].To != "jd_api.owners" {
			t.Errorf("graph edges = %+v", graph.Edges)
		}
		var relations map[string][]dbx.ForeignKey
		f.get("/relations?schema=jd_api", &relations)
		if len(relations["jd_api.jobs"]) != 1 {
			t.Errorf("relations = %+v", relations)
		}
	})

	t.Run("drop_constraints_and_view", func(t *testing.T) {
		f := newLiveForms(t, driver, dsn)
		f.change(http.MethodDelete, "/ddl/constraint", `{"schema":"jd_api","table":"jobs","name":"jobs_owner_id_caption_key"}`)
		f.change(http.MethodDelete, "/ddl/constraint", `{"schema":"jd_api","table":"jobs","name":"jobs_caption_len","type":"check"}`)
		f.change(http.MethodDelete, "/ddl/foreign-key", `{"schema":"jd_api","table":"jobs","name":"jobs_owner_id_fkey"}`)
		f.change(http.MethodDelete, "/ddl/view", `{"schema":"jd_api","name":"open_jobs"}`)
		var jobs dbx.TableDetail
		f.get("/table?schema=jd_api&table=jobs", &jobs)
		if len(jobs.Constraints) != 0 || len(jobs.ForeignKeys) != 0 {
			t.Errorf("after the drops: constraints %+v, foreign keys %+v", jobs.Constraints, jobs.ForeignKeys)
		}
	})

	t.Run("drop_index_column_and_table", func(t *testing.T) {
		f := newLiveForms(t, driver, dsn)
		f.change(http.MethodDelete, "/ddl/index", `{"schema":"jd_api","table":"jobs","name":"tasks_effort_idx"}`)
		// The column's default constraint goes with it, in the same batch.
		got := f.change(http.MethodDelete, "/ddl/column", `{"schema":"jd_api","table":"jobs","name":"seen"}`)
		if !strings.Contains(got, "DROP CONSTRAINT") || !strings.Contains(got, "DROP COLUMN [seen]") {
			t.Errorf("drop column: %s", got)
		}
		liveExec(t, db, `INSERT INTO jd_api.jobs(id, caption, effort) VALUES (1, N'one', 1)`)
		f.change(http.MethodPost, "/ddl/truncate", `{"schema":"jd_api","table":"jobs"}`)
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM jd_api.jobs`).Scan(&n); err != nil || n != 0 {
			t.Errorf("rows after truncate = %d, %v", n, err)
		}
		f.change(http.MethodDelete, "/ddl/table", `{"schema":"jd_api","table":"jobs"}`)
		if code := getJSON(t, f.router, f.path("/table?schema=jd_api&table=jobs"), nil); code != http.StatusNotFound {
			t.Errorf("the dropped table = %d", code)
		}
	})

	t.Run("drop_schema", func(t *testing.T) {
		f := newLiveForms(t, driver, dsn)
		// Never a cascade: a schema that still holds a table is the engine's to refuse.
		if rec := do(t, f.router, http.MethodDelete, f.path("/ddl/schema"), `{"name":"jd_api"}`); rec.Code != http.StatusBadRequest {
			t.Errorf("dropping a schema that holds a table = %d %s", rec.Code, rec.Body.String())
		}
		f.change(http.MethodDelete, "/ddl/table", `{"schema":"jd_api","table":"owners"}`)
		if got := f.change(http.MethodDelete, "/ddl/schema", `{"name":"jd_api"}`); got != "DROP SCHEMA [jd_api]" {
			t.Errorf("drop schema: %s", got)
		}
	})
}

func TestLiveAPISchemaFormsOracle(t *testing.T) {
	const driver = dbx.DriverOracle
	dsn := os.Getenv("JD_TEST_ORACLE_DSN")
	if dsn == "" {
		t.Skip("set JD_TEST_ORACLE_DSN to run this")
	}
	info, err := dbx.ParseDSN(driver, dsn)
	if err != nil || info.User == "" {
		t.Fatalf("the DSN names no user: %v", err)
	}
	// A schema is a user, and an unquoted user name is stored in upper case.
	// The fixture's user is shared, so everything made here carries a prefix.
	schema := strings.ToUpper(info.User)
	db := liveOpen(t, driver, dsn)
	// A table dropped through the form goes to the recycle bin; PURGE takes
	// this test's own out of it and leaves anyone else's.
	drop := []string{
		`DROP VIEW "JD_API_OPEN"`,
		`DROP TABLE "JD_API_JOBS" CASCADE CONSTRAINTS PURGE`, `DROP TABLE "JD_API_TASKS" CASCADE CONSTRAINTS PURGE`,
		`DROP TABLE "JD_API_OWNERS" CASCADE CONSTRAINTS PURGE`,
		`PURGE TABLE "JD_API_JOBS"`, `PURGE TABLE "JD_API_OWNERS"`,
	}
	clean := func() {
		// No DROP … IF EXISTS before 23ai: a drop of what is not there fails,
		// and that is not a reason to stop.
		for _, s := range drop {
			_, _ = db.ExecContext(context.Background(), s)
		}
	}
	clean()
	t.Cleanup(clean)
	in := func(body string) string { return strings.ReplaceAll(body, "{S}", schema) }
	rel := func(name string) string { return `"` + schema + `"."` + name + `"` }

	t.Run("build", func(t *testing.T) {
		f := newLiveForms(t, driver, dsn)
		f.refused(http.MethodPost, "/ddl/schema", `{"name":"JD_API_S"}`, "a schema is a user")

		owners := in(`{"schema":"{S}","table":"JD_API_OWNERS","columns":[
			{"name":"ID","type":"NUMBER(10)","primaryKey":true,"notNull":true},{"name":"NAME","type":"VARCHAR2(100 CHAR)"}]}`)
		f.send(http.MethodPost, "/ddl/table?preview=1", owners)
		if code := getJSON(t, f.router, f.path("/table?schema="+schema+"&table=JD_API_OWNERS"), nil); code != http.StatusNotFound {
			t.Fatalf("the table a preview showed = %d, want 404", code)
		}
		f.change(http.MethodPost, "/ddl/table", owners)
		f.change(http.MethodPost, "/ddl/table", in(`{"schema":"{S}","table":"JD_API_TASKS","columns":[
			{"name":"ID","type":"NUMBER(10)","primaryKey":true,"notNull":true},
			{"name":"OWNER_ID","type":"NUMBER(10)"},
			{"name":"TITLE","type":"VARCHAR2(200)","notNull":true,"default":"'untitled'"},
			{"name":"EFFORT","type":"VARCHAR2(10)"}]}`))
		f.change(http.MethodPost, "/ddl/column", in(`{"schema":"{S}","table":"JD_API_TASKS",
			"column":{"name":"SEEN","type":"TIMESTAMP(6) WITH TIME ZONE","notNull":true,"default":"SYSTIMESTAMP"}}`))

		// MODIFY names what changes and leaves the rest.
		got := f.change(http.MethodPatch, "/ddl/column", in(`{"schema":"{S}","table":"JD_API_TASKS","name":"EFFORT","type":"NUMBER(5)","nullable":false,"default":"1"}`))
		if got != `ALTER TABLE `+rel("JD_API_TASKS")+` MODIFY ("EFFORT" NUMBER(5) DEFAULT 1 NOT NULL)` {
			t.Errorf("alter column: %s", got)
		}
		if got := f.change(http.MethodPatch, "/ddl/column", in(`{"schema":"{S}","table":"JD_API_TASKS","name":"EFFORT","dropDefault":true}`)); got != `ALTER TABLE `+rel("JD_API_TASKS")+` MODIFY ("EFFORT" DEFAULT NULL)` {
			t.Errorf("drop default: %s", got)
		}
		if got := f.change(http.MethodPatch, "/ddl/column", in(`{"schema":"{S}","table":"JD_API_TASKS","name":"TITLE","nullable":true}`)); got != `ALTER TABLE `+rel("JD_API_TASKS")+` MODIFY ("TITLE" NULL)` {
			t.Errorf("relax a column: %s", got)
		}

		// The index is named in the table's schema, not the session's.
		got = f.change(http.MethodPost, "/ddl/index", in(`{"schema":"{S}","table":"JD_API_TASKS","name":"JD_API_TASKS_EFFORT","fields":["EFFORT"],"concurrently":true}`))
		if got != `CREATE INDEX `+rel("JD_API_TASKS_EFFORT")+` ON `+rel("JD_API_TASKS")+` ("EFFORT") ONLINE` {
			t.Errorf("index: %s", got)
		}
		if got := f.change(http.MethodPost, "/ddl/rename", in(`{"schema":"{S}","table":"JD_API_TASKS","kind":"column","name":"TITLE","to":"CAPTION"}`)); got != `ALTER TABLE `+rel("JD_API_TASKS")+` RENAME COLUMN "TITLE" TO "CAPTION"` {
			t.Errorf("rename column: %s", got)
		}
		if got := f.change(http.MethodPost, "/ddl/rename", in(`{"schema":"{S}","table":"JD_API_TASKS","to":"JD_API_JOBS"}`)); got != `ALTER TABLE `+rel("JD_API_TASKS")+` RENAME TO "JD_API_JOBS"` {
			t.Errorf("rename table: %s", got)
		}

		// Oracle has no ON UPDATE action and its ON DELETE takes two.
		f.refused(http.MethodPost, "/ddl/foreign-key", in(`{"schema":"{S}","table":"JD_API_JOBS","columns":["OWNER_ID"],
			"refTable":"JD_API_OWNERS","refColumns":["ID"],"onUpdate":"cascade"}`), "Oracle has no ON UPDATE action")
		got = f.change(http.MethodPost, "/ddl/foreign-key", in(`{"schema":"{S}","table":"JD_API_JOBS","name":"JD_API_JOBS_OWNER","columns":["OWNER_ID"],
			"refTable":"JD_API_OWNERS","refColumns":["ID"],"onDelete":"set null"}`))
		if got != `ALTER TABLE `+rel("JD_API_JOBS")+` ADD CONSTRAINT "JD_API_JOBS_OWNER" FOREIGN KEY ("OWNER_ID") REFERENCES `+rel("JD_API_OWNERS")+` ("ID") ON DELETE SET NULL` {
			t.Errorf("foreign key: %s", got)
		}
		f.change(http.MethodPost, "/ddl/constraint", in(`{"schema":"{S}","table":"JD_API_JOBS","type":"unique","name":"JD_API_JOBS_UQ","columns":["OWNER_ID","CAPTION"]}`))
		f.change(http.MethodPost, "/ddl/constraint", in(`{"schema":"{S}","table":"JD_API_JOBS","type":"check","name":"JD_API_CAPTION_LEN","expression":"LENGTH(\"CAPTION\") > 0"}`))

		f.change(http.MethodPost, "/ddl/view", in(`{"schema":"{S}","name":"JD_API_OPEN","query":"SELECT \"ID\" FROM \"JD_API_JOBS\""}`))
		if got := f.change(http.MethodPost, "/ddl/view", in(`{"schema":"{S}","name":"JD_API_OPEN","query":"SELECT \"ID\", \"CAPTION\" FROM \"JD_API_JOBS\"","replace":true}`)); !strings.HasPrefix(got, `CREATE OR REPLACE VIEW `+rel("JD_API_OPEN")+` AS`) {
			t.Errorf("replace view: %s", got)
		}

		f.change(http.MethodPost, "/ddl/comment", in(`{"schema":"{S}","table":"JD_API_JOBS","comment":"What is left to do"}`))
		f.change(http.MethodPost, "/ddl/comment", in(`{"schema":"{S}","table":"JD_API_JOBS","column":"EFFORT","comment":"In hours, it's said"}`))

		f.refused(http.MethodPost, "/ddl/enum", in(`{"schema":"{S}","name":"JD_API_STATE","values":["new"]}`), "Oracle has no enum types")
		f.refused(http.MethodPost, "/ddl/view", in(`{"schema":"{S}","name":"JD_API_M","query":"SELECT 1 AS one FROM dual","materialized":true}`), "refresh options")
		f.refused(http.MethodPost, "/ddl/index", in(`{"schema":"{S}","table":"JD_API_JOBS","fields":["ID"],"where":"\"ID\" > 0"}`), "no partial indexes")
		f.refused(http.MethodPost, "/ddl/column", in(`{"schema":"{S}","table":"JD_API_JOBS","column":{"name":"X","type":"NUMBER, DROP COLUMN CAPTION"}}`), "not one this form can build")
	})

	t.Run("read", func(t *testing.T) {
		f := newLiveForms(t, driver, dsn)
		// No schema named: the user the session is.
		var catalog dbx.Catalog
		f.get("/catalog", &catalog)
		if catalog.Schema != schema || catalog.DefaultSchema != schema || catalog.Errors != nil ||
			liveObject(catalog.Objects[dbx.GroupTables], "JD_API_OWNERS") == nil ||
			liveObject(catalog.Objects[dbx.GroupViews], "JD_API_OPEN") == nil {
			t.Errorf("catalogue: schema %q, errors %v, %d tables, %d views", catalog.Schema, catalog.Errors,
				len(catalog.Objects[dbx.GroupTables]), len(catalog.Objects[dbx.GroupViews]))
		}
		if jobs := liveObject(catalog.Objects[dbx.GroupTables], "JD_API_JOBS"); jobs == nil || jobs.Comment != "What is left to do" {
			t.Errorf("jobs in the catalogue = %+v", jobs)
		}
		var view, table dbx.ObjectDefinition
		f.get("/object?kind=view&schema="+schema+"&name=JD_API_OPEN", &view)
		if !strings.Contains(view.Definition, `"CAPTION"`) || !strings.Contains(view.Definition, "JD_API_OPEN") {
			t.Errorf("view = %+v", view)
		}
		f.get("/object?kind=table&name=JD_API_JOBS", &table)
		if !strings.Contains(table.Definition, `"JD_API_JOBS"`) || table.Schema != schema {
			t.Errorf("table definition = %+v", table)
		}
		if code := getJSON(t, f.router, f.path("/object?kind=procedure&schema="+schema+"&name=JD_API_NOPE"), nil); code != http.StatusNotFound {
			t.Errorf("a procedure that is not there = %d", code)
		}

		var jobs, owners dbx.TableDetail
		f.get("/table?schema="+schema+"&table=JD_API_JOBS", &jobs)
		if jobs.Comment != "What is left to do" || liveColumn(t, jobs, "EFFORT").Comment != "In hours, it's said" ||
			liveColumn(t, jobs, "EFFORT").Type != "NUMBER(5,0)" || liveColumn(t, jobs, "EFFORT").Nullable || liveColumn(t, jobs, "EFFORT").Default != "" ||
			!liveColumn(t, jobs, "CAPTION").Nullable || liveColumn(t, jobs, "CAPTION").Default != "'untitled'" ||
			liveColumn(t, jobs, "SEEN").Nullable {
			t.Errorf("columns = %+v", jobs.Columns)
		}
		// EFFORT's NOT NULL is a check constraint to Oracle and nullability to a reader.
		if c := liveConstraint(jobs, "JD_API_CAPTION_LEN"); c == nil || c.Type != dbx.ConstraintCheck || len(jobs.Constraints) != 2 {
			t.Errorf("constraints = %+v", jobs.Constraints)
		}
		if c := liveConstraint(jobs, "JD_API_JOBS_UQ"); c == nil || c.Type != dbx.ConstraintUnique {
			t.Errorf("constraints = %+v", jobs.Constraints)
		}
		if liveIndex(jobs, "JD_API_TASKS_EFFORT") == nil {
			t.Errorf("indexes = %+v", jobs.Indexes)
		}
		if len(jobs.ForeignKeys) != 1 || jobs.ForeignKeys[0].Name != "JD_API_JOBS_OWNER" || jobs.ForeignKeys[0].OnDelete != "SET NULL" {
			t.Errorf("foreign keys = %+v", jobs.ForeignKeys)
		}
		f.get("/table?schema="+schema+"&table=JD_API_OWNERS", &owners)
		if len(owners.ReferencedBy) != 1 || owners.ReferencedBy[0].Table != "JD_API_JOBS" {
			t.Errorf("owners is referenced by %+v", owners.ReferencedBy)
		}
		var relations map[string][]dbx.ForeignKey
		f.get("/relations?schema="+schema, &relations)
		if len(relations[schema+".JD_API_JOBS"]) != 1 {
			t.Errorf("relations of jobs = %+v", relations[schema+".JD_API_JOBS"])
		}
	})

	t.Run("drop_constraints_and_view", func(t *testing.T) {
		f := newLiveForms(t, driver, dsn)
		f.change(http.MethodDelete, "/ddl/constraint", in(`{"schema":"{S}","table":"JD_API_JOBS","name":"JD_API_JOBS_UQ"}`))
		f.change(http.MethodDelete, "/ddl/constraint", in(`{"schema":"{S}","table":"JD_API_JOBS","name":"JD_API_CAPTION_LEN","type":"check"}`))
		f.change(http.MethodDelete, "/ddl/foreign-key", in(`{"schema":"{S}","table":"JD_API_JOBS","name":"JD_API_JOBS_OWNER"}`))
		f.change(http.MethodDelete, "/ddl/view", in(`{"schema":"{S}","name":"JD_API_OPEN"}`))
		var jobs dbx.TableDetail
		f.get("/table?schema="+schema+"&table=JD_API_JOBS", &jobs)
		if len(jobs.Constraints) != 0 || len(jobs.ForeignKeys) != 0 {
			t.Errorf("after the drops: constraints %+v, foreign keys %+v", jobs.Constraints, jobs.ForeignKeys)
		}
	})

	t.Run("drop_index_column_and_tables", func(t *testing.T) {
		f := newLiveForms(t, driver, dsn)
		if got := f.change(http.MethodDelete, "/ddl/index", in(`{"schema":"{S}","table":"JD_API_JOBS","name":"JD_API_TASKS_EFFORT"}`)); got != `DROP INDEX `+rel("JD_API_TASKS_EFFORT") {
			t.Errorf("drop index: %s", got)
		}
		f.change(http.MethodDelete, "/ddl/column", in(`{"schema":"{S}","table":"JD_API_JOBS","name":"SEEN"}`))
		liveExec(t, db, `INSERT INTO "JD_API_JOBS" ("ID", "CAPTION", "EFFORT") VALUES (1, 'one', 1)`)
		f.change(http.MethodPost, "/ddl/truncate", in(`{"schema":"{S}","table":"JD_API_JOBS"}`))
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM "JD_API_JOBS"`).Scan(&n); err != nil || n != 0 {
			t.Errorf("rows after truncate = %d, %v", n, err)
		}
		f.change(http.MethodDelete, "/ddl/table", in(`{"schema":"{S}","table":"JD_API_JOBS"}`))
		f.change(http.MethodDelete, "/ddl/table", in(`{"schema":"{S}","table":"JD_API_OWNERS"}`))
		if code := getJSON(t, f.router, f.path("/table?schema="+schema+"&table=JD_API_JOBS"), nil); code != http.StatusNotFound {
			t.Errorf("the dropped table = %d", code)
		}
	})
}
