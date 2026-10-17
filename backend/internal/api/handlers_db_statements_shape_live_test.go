package api

import (
	"database/sql"
	"net/http"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
)

// A password an administrator set, as an account that may only read is shown
// the server's statement statistics. pg_stat_statements keeps a CREATE or
// ALTER ROLE as it was sent, and /statements is on the read surface: neither
// the role form's password nor one typed into a console may reach that list.
//
// The database is the test's own, with the extension created in it, so the
// list is of what ran here; the statistics are the whole server's and are
// never reset.
func TestLiveAPIPostgresStatementsShowAViewerNoPassword(t *testing.T) {
	dsn := opsLiveDSN(t, "JD_TEST_POSTGRES_DSN")
	server, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Close() })
	var super bool
	var preload, tracked sql.NullString
	if err := server.QueryRow(`
	  SELECT rolsuper, current_setting('shared_preload_libraries', true),
	         current_setting('pg_stat_statements.track_utility', true)
	  FROM pg_roles WHERE rolname = current_user`).Scan(&super, &preload, &tracked); err != nil {
		t.Skipf("postgres unreachable: %v", err)
	}
	if !super || !strings.Contains(preload.String, "pg_stat_statements") || tracked.String != "on" {
		t.Skipf("needs a superuser on a server that preloads pg_stat_statements and tracks utility statements (superuser %v, preload %q, track_utility %q)",
			super, preload.String, tracked.String)
	}
	const database, role = "jdt_statement_masking_api", "jdt_statement_masking_api"
	cleanup := func() {
		server.Exec(`DROP DATABASE IF EXISTS ` + database + ` WITH (FORCE)`)
		server.Exec(`DROP ROLE IF EXISTS ` + role)
	}
	cleanup()
	t.Cleanup(cleanup)
	if _, err := server.Exec(`CREATE DATABASE ` + database); err != nil {
		t.Fatal(err)
	}
	own := dbx.DSNForDatabase(dbx.DriverPostgres, dsn, database)
	direct, err := sql.Open("pgx", own)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { direct.Close() })
	if _, err := direct.Exec(`CREATE EXTENSION pg_stat_statements`); err != nil {
		t.Fatal(err)
	}
	s, admin, id := liveAPIRouter(t, dbx.DriverPostgres, own)
	viewer := asRole(s, auth.RoleReadOnly)
	base := pathf("/databases/%d", id)

	mustStatus(t, admin, http.MethodPost, base+"/server/roles",
		`{"name":"`+role+`","password":"jdt-sm-api-formsecret-a","login":true}`, http.StatusCreated)
	mustStatus(t, admin, http.MethodPut, base+"/server/roles/"+role, `{"password":"jdt-sm-api-formsecret-b"}`, http.StatusOK)
	if _, err := direct.Exec(`ALTER ROLE ` + role + ` PASSWORD 'jdt-sm-api-secret-c'`); err != nil {
		t.Fatal(err)
	}

	body := mustStatus(t, viewer, http.MethodGet, base+"/statements?sort=calls&limit=200", "", http.StatusOK)
	if !strings.Contains(body, `"supported":true`) {
		t.Fatalf("statements: %s", body)
	}
	if strings.Contains(body, "secret") {
		t.Errorf("a password reached a viewer: %s", body)
	}
	// The console's statement is in the list as its shape.
	if !strings.Contains(body, `"ALTER ROLE `+role+` PASSWORD ?"`) {
		t.Errorf("the console's ALTER ROLE is not listed by its shape: %s", body)
	}
	// The extension kept the console's password in clear, and was told not to
	// keep the form's.
	kept := func(secret string) int {
		var n int
		if err := direct.QueryRow(`SELECT count(*) FROM pg_stat_statements
		  WHERE dbid = (SELECT oid FROM pg_database WHERE datname = current_database()) AND query LIKE '%' || $1 || '%'`,
			secret).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if kept("jdt-sm-api-secret-c") == 0 {
		t.Fatal("pg_stat_statements did not keep the ALTER ROLE as it was sent; this test proves nothing here")
	}
	for _, secret := range []string{"jdt-sm-api-formsecret-a", "jdt-sm-api-formsecret-b"} {
		if n := kept(secret); n != 0 {
			t.Errorf("the role form's password %s is in pg_stat_statements %d times", secret, n)
		}
	}
}
