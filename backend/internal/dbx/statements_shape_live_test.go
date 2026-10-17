package dbx

import (
	"database/sql"
	"net/url"
	"strings"
	"testing"
)

// A PostgreSQL's statement statistics, read the way the Statements list reads
// them, after statements the extension keeps as they were sent.
//
// The database is the test's own and the extension is created in it, so the
// list holds what ran here and none of what the fixture's other users run; the
// statistics are never reset, because they are the whole server's.
func TestLivePostgresStatementsShowAShapeAndNoValue(t *testing.T) {
	server, dsn := opsLive(t, DriverPostgres, "JD_TEST_POSTGRES_DSN")
	ctx := t.Context()
	var super bool
	var preload, tracked sql.NullString
	if err := server.QueryRowContext(ctx, `
	  SELECT rolsuper, current_setting('shared_preload_libraries', true),
	         current_setting('pg_stat_statements.track_utility', true)
	  FROM pg_roles WHERE rolname = current_user`).Scan(&super, &preload, &tracked); err != nil {
		t.Fatal(err)
	}
	if !super || !strings.Contains(preload.String, "pg_stat_statements") || tracked.String != "on" {
		t.Skipf("needs a superuser on a server that preloads pg_stat_statements and tracks utility statements (superuser %v, preload %q, track_utility %q)",
			super, preload.String, tracked.String)
	}
	const database = "jdt_statement_masking_db"
	roles := []string{"jdt_statement_masking_r", "jdt_statement_masking_form"}
	cleanup := func() {
		server.Exec(`DROP DATABASE IF EXISTS ` + database + ` WITH (FORCE)`)
		for _, role := range roles {
			server.Exec(`DROP ROLE IF EXISTS ` + role)
		}
	}
	cleanup()
	t.Cleanup(cleanup)
	opsMustExec(t, server, `CREATE DATABASE `+database)
	own := DSNForDatabase(DriverPostgres, dsn, database)
	db := opsOpen(t, DriverPostgres, own, "the test's own database")
	opsMustExec(t, db, `CREATE EXTENSION pg_stat_statements`)

	// As a console sends them: every one is kept with its value in it.
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE ROLE jdt_statement_masking_r PASSWORD 'jdt-sm-secret-1'`,
		`ALTER ROLE jdt_statement_masking_r WITH ENCRYPTED PASSWORD E'jdt-sm-\'secret-2'`,
		`ALTER ROLE jdt_statement_masking_r PASSWORD $pw$jdt-sm-secret-3$pw$`,
		`ALTER ROLE jdt_statement_masking_r SET jdt_sm.token = jdtsmsecret4`,
		`SET jdt_sm.other TO jdtsmsecret5`,
		`COMMENT ON ROLE jdt_statement_masking_r IS 'jdt-sm-secret-6'`,
		`DO $$ BEGIN PERFORM 'jdt-sm-secret-7'; END $$`,
		`SET standard_conforming_strings = off`,
		`COMMENT ON ROLE jdt_statement_masking_r IS 'it\'s jdt-sm-secret-8'`,
		`SET standard_conforming_strings = on`,
		// Planned: the extension has the value out already, and its marker stays.
		`SELECT count(*) FROM pg_class WHERE relname = 'jdt-sm-secret-9'`,
	} {
		if _, err := conn.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	conn.Close()

	// As the dashboard's role form sends them: with the account a superuser,
	// the extension is told not to keep them at all.
	admin, err := AdminFor(DriverPostgres)
	if err != nil {
		t.Fatal(err)
	}
	if err := admin.CreateRole(ctx, db, RoleSpec{Name: roles[1], Password: "jdt-sm-formsecret-a", SetPassword: true, Login: true}); err != nil {
		t.Fatal(err)
	}
	if err := admin.AlterRole(ctx, db, RoleSpec{Name: roles[1], Password: "jdt-sm-formsecret-b", SetPassword: true}); err != nil {
		t.Fatal(err)
	}
	// What the form set is the account's password: it signs in with it.
	signIn, err := url.Parse(own)
	if err != nil {
		t.Fatal(err)
	}
	signIn.User = url.UserPassword(roles[1], "jdt-sm-formsecret-b")
	formed, err := sql.Open("pgx", signIn.String())
	if err != nil {
		t.Fatal(err)
	}
	if err := formed.PingContext(ctx); err != nil {
		t.Errorf("the role the form made does not sign in with the password it set: %v", err)
	}
	formed.Close()

	kept := func(secret string) int {
		t.Helper()
		var n int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM pg_stat_statements
		  WHERE dbid = (SELECT oid FROM pg_database WHERE datname = current_database()) AND query LIKE '%' || $1 || '%'`,
			secret).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	// What this guards against is there to be guarded against: the extension
	// keeps the console's password in clear.
	if kept("jdt-sm-secret-1") == 0 {
		t.Fatal("pg_stat_statements did not keep the CREATE ROLE as it was sent; this test proves nothing here")
	}
	for _, secret := range []string{"jdt-sm-formsecret-a", "jdt-sm-formsecret-b"} {
		if n := kept(secret); n != 0 {
			t.Errorf("the role form's password %s is in pg_stat_statements %d times", secret, n)
		}
	}

	report, err := TopStatements(ctx, db, DriverPostgres, StatementsOptions{Sort: StatementsByCalls, Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Supported {
		t.Fatalf("statements: %+v", report)
	}
	listed := map[string]bool{}
	queries := []string{}
	for _, s := range report.Statements {
		listed[s.Query] = true
		queries = append(queries, s.Query)
		if strings.Contains(s.Query, "secret") {
			t.Errorf("a value reached the list: %q", s.Query)
		}
	}
	for _, want := range []string{
		`CREATE ROLE jdt_statement_masking_r PASSWORD ?`,
		`ALTER ROLE jdt_statement_masking_r WITH ENCRYPTED PASSWORD ?`,
		`ALTER ROLE jdt_statement_masking_r PASSWORD ?`,
		`ALTER ROLE jdt_statement_masking_r SET jdt_sm.token = ?`,
		`SET jdt_sm.other TO ?`,
		`COMMENT ON ROLE jdt_statement_masking_r IS ?`,
		`DO ?`,
		`SELECT count(*) FROM pg_class WHERE relname = $1`,
	} {
		if !listed[want] {
			t.Errorf("%q is not in the list: %q", want, queries)
		}
	}
}
