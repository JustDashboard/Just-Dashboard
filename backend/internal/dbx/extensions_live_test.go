package dbx

import (
	"context"
	"database/sql"
	"os"
	"sort"
	"strings"
	"testing"
	"time"
)

// Extensions against a real PostgreSQL, in a database of the test's own: an
// extension is created in one database, and the shared server's others are
// other people's. Nothing here changes the server's configuration — a
// preload is planned and never run; the container test in the api package
// runs one on a server of its own.

const extensionsLiveDatabase = "jdt_extension_manager_live"

func liveExtensionsDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("JD_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("JD_TEST_POSTGRES_DSN is not set")
	}
	d := postgresDialect{}
	open := func(dsn string) *sql.DB {
		db, err := sql.Open(d.SQLDriverName(), d.NormaliseDSN(dsn))
		if err != nil {
			t.Fatal(err)
		}
		return db
	}
	server := open(dsn)
	t.Cleanup(func() { server.Close() })
	if err := server.Ping(); err != nil {
		t.Skipf("JD_TEST_POSTGRES_DSN is not reachable: %v", err)
	}
	drop := func() {
		_, _ = server.Exec(`DROP DATABASE IF EXISTS ` + extensionsLiveDatabase + ` WITH (FORCE)`)
	}
	drop()
	if _, err := server.Exec(`CREATE DATABASE ` + extensionsLiveDatabase); err != nil {
		t.Skipf("the server will not make a database for the test: %v", err)
	}
	db := open(dsnForDatabase(DriverPostgres, dsn, extensionsLiveDatabase))
	// Closed before the database is dropped: cleanups run last-added first.
	t.Cleanup(drop)
	t.Cleanup(func() { db.Close() })
	return db
}

func TestLivePostgresExtensions(t *testing.T) {
	db := liveExtensionsDB(t)
	d := postgresDialect{}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	planned := func(t *testing.T, plan *DDLPlan, err error, want string) *DDLPlan {
		t.Helper()
		if err != nil {
			t.Fatalf("plan: %v", err)
		}
		if got := plan.String(); got != want {
			t.Fatalf("statement = %q, want %q", got, want)
		}
		return plan
	}
	run := func(t *testing.T, plan *DDLPlan, err error, want string) {
		t.Helper()
		if err := planned(t, plan, err, want).Exec(ctx, db); err != nil {
			t.Fatalf("%s: %v", want, err)
		}
	}
	refused := func(t *testing.T, err error, words string) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), words) {
			t.Errorf("err = %v, want one saying %q", err, words)
		}
	}
	installed := func(t *testing.T) map[string]Extension {
		t.Helper()
		list, err := d.Extensions(ctx, db)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]Extension{}
		for _, e := range list {
			out[e.Name] = e
		}
		return out
	}

	t.Run("the list carries the catalogue", func(t *testing.T) {
		list := installed(t)
		if e := list["plpgsql"]; !e.Installed || e.Category != ExtensionLanguages {
			t.Errorf("plpgsql = %+v", e)
		}
		if e := list["hstore"]; e.Installed || e.AvailableVersion == "" || e.Category != ExtensionDataTypes || e.About == "" {
			t.Errorf("hstore = %+v", e)
		}
		if e, ok := list["pg_stat_statements"]; ok && e.Library != "pg_stat_statements" {
			t.Errorf("pg_stat_statements = %+v", e)
		}
		// The test signs in as a superuser on the fixtures, so the library
		// directory is read and auto_explain, which every build ships, is a row.
		if e, ok := list["auto_explain"]; ok && (!e.Module || e.Unavailable) {
			t.Errorf("auto_explain = %+v", e)
		}
		if e, ok := list["vector"]; ok && !e.Unavailable && !e.Installed && e.AvailableVersion == "" {
			t.Errorf("vector = %+v", e)
		}
	})

	t.Run("a version is created and updated along the path the server ships", func(t *testing.T) {
		plan, err := d.PlanCreateExtension(ctx, db, ExtensionSpec{Name: "hstore", Version: "1.4"})
		run(t, plan, err, `CREATE EXTENSION IF NOT EXISTS "hstore" VERSION '1.4'`)
		detail, err := d.ExtensionDetail(ctx, db, "hstore")
		if err != nil {
			t.Fatal(err)
		}
		if detail.Version != "1.4" || len(detail.Versions) < 2 || detail.Versions[0].Version != detail.AvailableVersion ||
			!containsString(detail.Updates, "1.5") || !containsString(detail.Updates, detail.AvailableVersion) ||
			detail.Updates[0] != detail.AvailableVersion {
			t.Fatalf("detail = version %s, versions %+v, updates %v", detail.Version, detail.Versions, detail.Updates)
		}
		if !detail.Superuser || detail.DefaultSchema != "public" || !containsString(detail.Schemas, "public") {
			t.Errorf("detail = superuser %v, schema %q of %v", detail.Superuser, detail.DefaultSchema, detail.Schemas)
		}
		plan, err = d.PlanUpdateExtension(ctx, db, "hstore", "1.5")
		run(t, plan, err, `ALTER EXTENSION "hstore" UPDATE TO '1.5'`)
		_, err = d.PlanUpdateExtension(ctx, db, "hstore", "1.5")
		refused(t, err, "already at version 1.5")
		_, err = d.PlanUpdateExtension(ctx, db, "hstore", "9.9")
		refused(t, err, "no update for hstore from 1.5 to 9.9")
		plan, err = d.PlanUpdateExtension(ctx, db, "hstore", "")
		run(t, plan, err, `ALTER EXTENSION "hstore" UPDATE`)
		if e := installed(t)["hstore"]; e.Version != e.AvailableVersion {
			t.Errorf("after the update to the default: %+v", e)
		}
		_, err = d.PlanCreateExtension(ctx, db, ExtensionSpec{Name: "hstore"})
		refused(t, err, "already enabled in this database")
	})

	t.Run("a schema is one that exists, and the one a control file fixes", func(t *testing.T) {
		if _, err := db.ExecContext(ctx, `CREATE SCHEMA jdt_extension_manager_ext`); err != nil {
			t.Fatal(err)
		}
		plan, err := d.PlanCreateExtension(ctx, db, ExtensionSpec{Name: "pg_trgm", Schema: "jdt_extension_manager_ext"})
		run(t, plan, err, `CREATE EXTENSION IF NOT EXISTS "pg_trgm" WITH SCHEMA "jdt_extension_manager_ext"`)
		if e := installed(t)["pg_trgm"]; !e.Installed || e.Schema != "jdt_extension_manager_ext" {
			t.Errorf("pg_trgm = %+v", e)
		}
		_, err = d.PlanCreateExtension(ctx, db, ExtensionSpec{Name: "citext", Schema: "jdt_extension_manager_nowhere"})
		refused(t, err, "there is no schema jdt_extension_manager_nowhere")
		_, err = d.PlanCreateExtension(ctx, db, ExtensionSpec{Name: "pltcl", Schema: "public"})
		refused(t, err, "pltcl is always created in the schema pg_catalog")
		_, err = d.PlanCreateExtension(ctx, db, ExtensionSpec{Name: "citext", Version: "0.0"})
		refused(t, err, "citext has no version 0.0 on this server")
		_, err = d.PlanCreateExtension(ctx, db, ExtensionSpec{Name: "jdt_extension_manager_none"})
		refused(t, err, "not an extension this server ships")
	})

	t.Run("CASCADE creates what it needs", func(t *testing.T) {
		detail, err := d.ExtensionDetail(ctx, db, "earthdistance")
		if err != nil {
			t.Fatal(err)
		}
		if len(detail.Versions) == 0 || strings.Join(detail.Versions[0].Missing, ",") != "cube" {
			t.Fatalf("earthdistance versions = %+v", detail.Versions)
		}
		plan, err := d.PlanCreateExtension(ctx, db, ExtensionSpec{Name: "earthdistance"})
		if err := planned(t, plan, err, `CREATE EXTENSION IF NOT EXISTS "earthdistance"`).Exec(ctx, db); err == nil ||
			!strings.Contains(err.Error(), "cube") {
			t.Fatalf("without CASCADE: %v", err)
		}
		plan, err = d.PlanCreateExtension(ctx, db, ExtensionSpec{Name: "earthdistance", Cascade: true})
		run(t, plan, err, `CREATE EXTENSION IF NOT EXISTS "earthdistance" CASCADE`)
		if e := installed(t)["cube"]; !e.Installed {
			t.Errorf("cube was not created with it: %+v", e)
		}
	})

	t.Run("a drop lists what goes with it, and CASCADE is the reader's choice", func(t *testing.T) {
		for _, stmt := range []string{
			`CREATE TABLE jdt_extension_manager_items (id int PRIMARY KEY, tags hstore, many hstore[], name text)`,
			`CREATE VIEW jdt_extension_manager_tags AS SELECT id, tags FROM jdt_extension_manager_items`,
			`CREATE VIEW jdt_extension_manager_names AS SELECT id, name FROM jdt_extension_manager_items`,
			`CREATE FUNCTION jdt_extension_manager_count(h hstore) RETURNS int LANGUAGE sql AS 'SELECT 1'`,
			`CREATE TABLE jdt_extension_manager_other (id int, d text DEFAULT (hstore('a', 'b'))::text)`,
			`CREATE INDEX jdt_extension_manager_trgm ON jdt_extension_manager_items
			   USING gin (name jdt_extension_manager_ext.gin_trgm_ops)`,
		} {
			if _, err := db.ExecContext(ctx, stmt); err != nil {
				t.Fatalf("%s: %v", stmt, err)
			}
		}
		objects := func(name string) []string {
			t.Helper()
			detail, err := d.ExtensionDetail(ctx, db, name)
			if err != nil {
				t.Fatal(err)
			}
			if detail.DependentsTotal != len(detail.Dependents) {
				t.Errorf("%s: %d dependents of %d", name, len(detail.Dependents), detail.DependentsTotal)
			}
			out := []string{}
			for _, dep := range detail.Dependents {
				out = append(out, dep.Object)
			}
			sort.Strings(out)
			return out
		}
		// The engine's own refusal names these five, and nothing else: not
		// the view of another column, not the columns of the view.
		want := []string{
			"column many of table jdt_extension_manager_items",
			"column tags of table jdt_extension_manager_items",
			"default value for column d of table jdt_extension_manager_other",
			"function jdt_extension_manager_count(hstore)",
			"view jdt_extension_manager_tags",
		}
		if got := objects("hstore"); strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Errorf("hstore's dependents =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
		// Another extension is named as itself, not as its members.
		if got := objects("cube"); strings.Join(got, ",") != "extension earthdistance" {
			t.Errorf("cube's dependents = %q", got)
		}
		if got := objects("pg_trgm"); strings.Join(got, ",") != "index jdt_extension_manager_trgm" {
			t.Errorf("pg_trgm's dependents = %q", got)
		}
		if got := objects("earthdistance"); len(got) != 0 {
			t.Errorf("earthdistance's dependents = %q", got)
		}

		detail, err := d.ExtensionDetail(ctx, db, "hstore")
		if err != nil {
			t.Fatal(err)
		}
		if len(detail.Members) == 0 || detail.Members[0].Kind != "function" || detail.Members[0].Count < 10 ||
			detail.Drop != `DROP EXTENSION "hstore"` || detail.DropCascade != `DROP EXTENSION "hstore" CASCADE` {
			t.Errorf("hstore: members %+v, drop %q / %q", detail.Members, detail.Drop, detail.DropCascade)
		}
		plan, err := d.PlanDropExtension(ctx, db, "hstore", false)
		if err := planned(t, plan, err, `DROP EXTENSION "hstore"`).Exec(ctx, db); err == nil ||
			!strings.Contains(err.Error(), "depend") {
			t.Fatalf("a drop without CASCADE while things depend on it: %v", err)
		}
		plan, err = d.PlanDropExtension(ctx, db, "hstore", true)
		run(t, plan, err, `DROP EXTENSION "hstore" CASCADE`)
		var columns int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM information_schema.columns
		  WHERE table_name = 'jdt_extension_manager_items'`).Scan(&columns); err != nil || columns != 2 {
			t.Errorf("the table kept %d columns (%v), want id and name", columns, err)
		}
		_, err = d.PlanDropExtension(ctx, db, "hstore", false)
		refused(t, err, "not enabled in this database")
	})

	t.Run("a preload is planned against what the server loads and never written here", func(t *testing.T) {
		state, err := d.ReadPreload(ctx, db)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("loaded %v, configured %v, settable %v (%s)", state.Loaded, state.Configured, state.Settable, state.Reason)
		for _, lib := range state.Loaded {
			if _, known := PreloadLibraryFor(lib); !known {
				continue
			}
			_, err := d.PlanPreload(ctx, db, lib)
			refused(t, err, "already loaded when the server starts")
		}
		_, err = d.PlanPreload(ctx, db, "jdt_extension_manager_evil")
		refused(t, err, "not a library the dashboard loads at start")
		if !state.Settable {
			if state.Reason == "" {
				t.Error("a list that cannot be changed says nothing about why")
			}
			_, err := d.PlanPreload(ctx, db, "passwordcheck")
			refused(t, err, state.Reason)
		}
	})
}
