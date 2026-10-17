package api

import (
	"database/sql"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
)

// TestLivePostgresFullCarriesItsExtensions holds a server started from the
// full template to what the template says of it: the libraries it loads at
// start are loaded, pg_cron works in the database the server was created
// for, and every extension it carries can be created — the two that need
// preloading included, which is the part a wrong command line breaks.
//
// JD_TEST_POSTGRES_FULL_DSN is an account that may create databases on such a
// server, connected to the database it was created with. The test makes a
// database of its own for the extensions that are not pg_cron, removes it,
// and leaves pg_cron as it found it.
func TestLivePostgresFullCarriesItsExtensions(t *testing.T) {
	dsn := os.Getenv("JD_TEST_POSTGRES_FULL_DSN")
	if dsn == "" {
		t.Skip("JD_TEST_POSTGRES_FULL_DSN unset")
	}
	tmpl := provisionTemplates["postgres-full"]
	open := func(dsn string) *sql.DB {
		t.Helper()
		d, err := dbx.DialectFor(dbx.DriverPostgres)
		if err != nil {
			t.Fatal(err)
		}
		db, err := sql.Open(d.SQLDriverName(), d.NormaliseDSN(dsn))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		if err := db.PingContext(t.Context()); err != nil {
			t.Fatalf("the full server does not answer: %v", err)
		}
		return db
	}
	db := open(dsn)
	ctx := t.Context()

	var preloaded, cronDatabase, current string
	if err := db.QueryRowContext(ctx, `SELECT current_setting('shared_preload_libraries'), current_setting('cron.database_name'), current_database()`).
		Scan(&preloaded, &cronDatabase, &current); err != nil {
		t.Fatal(err)
	}
	loaded := strings.Split(strings.ReplaceAll(preloaded, " ", ""), ",")
	for _, library := range tmpl.preload {
		if !slices.Contains(loaded, library) {
			t.Errorf("%s is not loaded: shared_preload_libraries = %q", library, preloaded)
		}
	}
	if cronDatabase != current {
		t.Errorf("pg_cron schedules in %q, and the server was created for %q", cronDatabase, current)
	}
	for _, extension := range tmpl.extensions {
		var version string
		if err := db.QueryRowContext(ctx, `SELECT default_version FROM pg_available_extensions WHERE name = $1`, extension).Scan(&version); err != nil {
			t.Errorf("%s is not available: %v", extension, err)
			continue
		}
		t.Logf("%s %s", extension, version)
	}

	// pg_cron can be created only in the database its scheduler works in.
	var had bool
	_ = db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_cron')`).Scan(&had)
	if !had {
		if _, err := db.ExecContext(ctx, `CREATE EXTENSION pg_cron`); err != nil {
			t.Fatalf("pg_cron: %v", err)
		}
		t.Cleanup(func() { _, _ = db.Exec(`DROP EXTENSION IF EXISTS pg_cron`) })
	}
	if _, err := db.ExecContext(ctx, `SELECT cron.schedule('jdt-full-recipe-check', '0 3 * * *', 'SELECT 1')`); err != nil {
		t.Errorf("pg_cron does not schedule: %v", err)
	} else if _, err := db.ExecContext(ctx, `SELECT cron.unschedule('jdt-full-recipe-check')`); err != nil {
		t.Errorf("pg_cron does not unschedule: %v", err)
	}

	const scratch = "jdt_full_recipe_check"
	_, _ = db.ExecContext(ctx, `DROP DATABASE IF EXISTS `+scratch)
	if _, err := db.ExecContext(ctx, `CREATE DATABASE `+scratch); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = db.Exec(`DROP DATABASE IF EXISTS ` + scratch + ` WITH (FORCE)`) })
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + scratch
	other := open(u.String())
	for _, statement := range []string{
		`CREATE EXTENSION vector`,
		`SELECT '[1,2,3]'::vector <-> '[1,2,4]'::vector`,
		`CREATE EXTENSION postgis`,
		`SELECT ST_AsText(ST_MakePoint(28.83, 47.01))`,
		`CREATE SCHEMA partman`,
		`CREATE EXTENSION pg_partman SCHEMA partman`,
		`CREATE EXTENSION pgaudit`,
		`CREATE EXTENSION hypopg`,
		`CREATE TABLE jdt_hypo (id int)`,
		`SELECT indexrelid FROM hypopg_create_index('CREATE INDEX ON jdt_hypo (id)')`,
		`CREATE EXTENSION pg_repack`,
	} {
		if _, err := other.ExecContext(ctx, statement); err != nil {
			t.Errorf("%s: %v", statement, err)
		}
	}
}
