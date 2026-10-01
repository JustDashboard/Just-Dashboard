package api

import (
	"compress/gzip"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

// Protection is a mark on one saved connection, and its middleware stands in
// front of that connection's routes. A request addressed to another connection
// to the same server can still reach what the protected one stands for: its
// database, by naming it, and its account, which is the server's. These are
// those requests, each asked through an unprotected neighbour.
//
// Nothing listens on the addresses used, so a request that is let through ends
// in a refused dial and never in a change.

func neighbourConnection(t *testing.T, s *Server, name string, driver dbx.Driver, dsn string, readOnly bool) int64 {
	t.Helper()
	sealed, err := s.Sealer.Seal(dsn)
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Store.DB.Exec(
		`INSERT INTO db_connections(name, driver, dsn_enc, created_at, read_only) VALUES(?,?,?,0,?)`,
		name, string(driver), sealed, readOnly)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

// redisArchive writes the head of a Redis dump of those numbered databases:
// the header is all a restore reads to learn where the keys will go.
func redisArchive(t *testing.T, path, databases string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	fmt.Fprintf(gz, `{"format":"jd-redis","version":1,"driver":"redis","database":%q}`+"\n", databases)
	if err := errors.Join(gz.Close(), f.Close()); err != nil {
		t.Fatal(err)
	}
}

func isProtectedRefusal(code int, body string) bool {
	return code == http.StatusConflict && strings.Contains(body, "connection_read_only")
}

func TestAProtectedDatabaseIsNotReplacedOrDroppedThroughANeighbour(t *testing.T) {
	s := newConnHarness(t).s
	t.Cleanup(s.Shutdown)
	neighbourConnection(t, s, "prod", dbx.DriverPostgres, "postgres://app:pw@127.0.0.1:1/prod?sslmode=disable", true)
	// The same server by another spelling of its address, as another account.
	staging := neighbourConnection(t, s, "staging", dbx.DriverPostgres, "postgres://admin:pw@localhost:1/staging?sslmode=disable", false)
	dump := filepath.Join(s.dbDumpDir("staging"), "old.sql")
	if err := os.MkdirAll(filepath.Dir(dump), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dump, []byte("SELECT 1;\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}
	base := fmt.Sprintf("/api/v1/databases/%d", staging)

	rec := c.do(http.MethodPost, base+"/restore", `{"file":"old.sql","database":"prod"}`, nil)
	if !isProtectedRefusal(rec.Code, rec.Body.String()) {
		t.Errorf("restore into a protected connection's database through a neighbour = %d %s, want 409 connection_read_only", rec.Code, rec.Body.String())
	}
	confirm := map[string]string{httpx.ConfirmHeader: "prod"}
	rec = c.do(http.MethodDelete, base+"/database", `{"database":"prod"}`, confirm)
	if !isProtectedRefusal(rec.Code, rec.Body.String()) {
		t.Errorf("drop of a protected connection's database through a neighbour = %d %s, want 409 connection_read_only", rec.Code, rec.Body.String())
	}
	// The container is every database of the server at once.
	rec = c.do(http.MethodDelete, base+"/database", `{"removeContainer":true}`, map[string]string{httpx.ConfirmHeader: "staging"})
	if !isProtectedRefusal(rec.Code, rec.Body.String()) {
		t.Errorf("removing the server a protected connection is on = %d %s, want 409 connection_read_only", rec.Code, rec.Body.String())
	}
	rec = c.do(http.MethodPost, "/api/v1/backups/runs/1/restore-database", fmt.Sprintf(`{"connectionId":%d,"database":"prod"}`, staging), nil)
	if !isProtectedRefusal(rec.Code, rec.Body.String()) {
		t.Errorf("a backup run restored into a protected connection's database = %d %s, want 409 connection_read_only", rec.Code, rec.Body.String())
	}
	dumper := &backupDatabaseDumper{server: s}
	if _, err := dumper.RestoreDatabase(t.Context(), staging, "prod", dump); err == nil || !strings.Contains(err.Error(), "is protected") {
		t.Errorf("the backups adapter restoring into a protected connection's database = %v, want a refusal that says it is protected", err)
	}

	// A database nobody protects is not held back: the drop goes as far as the
	// server, which is not there.
	rec = c.do(http.MethodDelete, base+"/database", `{"database":"scratch"}`, map[string]string{httpx.ConfirmHeader: "scratch"})
	if rec.Code != http.StatusBadGateway {
		t.Errorf("drop of an unprotected database = %d %s, want it to reach the server and fail there", rec.Code, rec.Body.String())
	}
	through, _, err := s.dbConnRow(t.Context(), staging)
	if err != nil {
		t.Fatal(err)
	}
	for _, database := range []string{"staging", "scratch", ""} {
		if err := s.refuseDatabaseOfProtected(t.Context(), through, database); err != nil {
			t.Errorf("database %q is not a protected connection's and was refused: %v", database, err)
		}
	}
	// Another server, and another engine at the same address, are not this one.
	for name, dsn := range map[string]string{
		"elsewhere": "postgres://admin:pw@127.0.0.1:2/staging?sslmode=disable",
		"mysql":     "admin:pw@tcp(127.0.0.1:1)/staging",
	} {
		driver := dbx.DriverPostgres
		if name == "mysql" {
			driver = dbx.DriverMySQL
		}
		other, _, err := s.dbConnRow(t.Context(), neighbourConnection(t, s, name, driver, dsn, false))
		if err != nil {
			t.Fatal(err)
		}
		if err := s.refuseDatabaseOfProtected(t.Context(), other, "prod"); err != nil {
			t.Errorf("%s is not on the protected connection's server and was refused: %v", name, err)
		}
		if err := s.refuseServerOfProtected(t.Context(), other); err != nil {
			t.Errorf("%s is not the protected connection's server and its removal was refused: %v", name, err)
		}
	}
	if _, err := s.Store.DB.Exec(`UPDATE db_connections SET read_only = 0`); err != nil {
		t.Fatal(err)
	}
	if err := s.refuseDatabaseOfProtected(t.Context(), through, "prod"); err != nil {
		t.Errorf("with protection off the database was still refused: %v", err)
	}
}

// A Redis archive restored with no database named puts each key back in the
// numbered database it came from, whichever one the connection is on. So the
// archive is what says where a restore writes: for the refusal, and for the
// database the job reports, which used to be the connection's own.
func TestARedisRestoreIsHeldToTheDatabasesItsArchiveNames(t *testing.T) {
	s := newConnHarness(t).s
	t.Cleanup(s.Shutdown)
	neighbourConnection(t, s, "sessions", dbx.DriverRedis, "redis://:pw@127.0.0.1:1/8", true)
	cache := neighbourConnection(t, s, "cache", dbx.DriverRedis, "redis://:pw@localhost:1/0", false)
	dir := s.dbDumpDir("cache")
	redisArchive(t, filepath.Join(dir, "redis-db8.jsonl.gz"), "8")
	redisArchive(t, filepath.Join(dir, "redis-db0_3.jsonl.gz"), "0,3")
	through, _, err := s.dbConnRow(t.Context(), cache)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		file, target string
		want         []string
	}{
		{"redis-db8.jsonl.gz", "", []string{"8"}},
		{"redis-db0_3.jsonl.gz", "", []string{"0", "3"}},
		{"redis-db8.jsonl.gz", "5", []string{"5"}},
		// Not an archive: the connection's own, as before.
		{"missing.jsonl.gz", "", []string{"0"}},
	} {
		if got := restoreDestinations(through, c.target, filepath.Join(dir, c.file)); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s restored with database %q writes into %v, want %v", c.file, c.target, got, c.want)
		}
	}

	c := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}
	base := fmt.Sprintf("/api/v1/databases/%d", cache)
	rec := c.do(http.MethodPost, base+"/restore", `{"file":"redis-db8.jsonl.gz"}`, nil)
	if !isProtectedRefusal(rec.Code, rec.Body.String()) {
		t.Errorf("an archive of db8 restored through a connection on db0 = %d %s, want 409 connection_read_only", rec.Code, rec.Body.String())
	}
	rec = c.do(http.MethodDelete, base+"/database", `{"database":"8"}`, map[string]string{httpx.ConfirmHeader: "db8"})
	if !isProtectedRefusal(rec.Code, rec.Body.String()) {
		t.Errorf("flushing db8 through a connection on db0 = %d %s, want 409 connection_read_only", rec.Code, rec.Body.String())
	}
	dumper := &backupDatabaseDumper{server: s}
	if _, err := dumper.RestoreDatabase(t.Context(), cache, "", filepath.Join(dir, "redis-db8.jsonl.gz")); err == nil || !strings.Contains(err.Error(), "is protected") {
		t.Errorf("the backups adapter restoring an archive of db8 = %v, want a refusal that says it is protected", err)
	}
	for _, database := range []string{"0", "", "3", "db3"} {
		if err := s.refuseDatabaseOfProtected(t.Context(), through, database); err != nil {
			t.Errorf("database %q is not the protected connection's and was refused: %v", database, err)
		}
	}
	if err := s.refuseDatabaseOfProtected(t.Context(), through, "db8"); err == nil {
		t.Error("db8 is the protected connection's database under Redis's own spelling, and was let through")
	}
}

// An account is the server's. The one a protected connection signs in with is
// not given a new password, locked or dropped through another connection to
// the same server, whichever route the engine's accounts are edited by.
func TestAProtectedConnectionsAccountIsNotChangedThroughANeighbour(t *testing.T) {
	s := newConnHarness(t).s
	t.Cleanup(s.Shutdown)
	neighbourConnection(t, s, "prod", dbx.DriverPostgres, "postgres://app:pw@127.0.0.1:1/prod?sslmode=disable", true)
	staging := neighbourConnection(t, s, "staging", dbx.DriverPostgres, "postgres://admin:pw@localhost:1/staging?sslmode=disable", false)
	neighbourConnection(t, s, "sessions", dbx.DriverRedis, "redis://127.0.0.1:1/8", true)
	cache := neighbourConnection(t, s, "cache", dbx.DriverRedis, "redis://127.0.0.1:1/0", false)
	neighbourConnection(t, s, "orders", dbx.DriverMongo, "mongodb://app:pw@127.0.0.1:1/orders", true)
	events := neighbourConnection(t, s, "events", dbx.DriverMongo, "mongodb://root:pw@127.0.0.1:1/events", false)
	c := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}

	sql := fmt.Sprintf("/api/v1/databases/%d/server/roles/", staging)
	for _, rq := range []struct{ method, path, body string }{
		{http.MethodPut, sql + "app", `{"password":"Another-Passw0rd-9"}`},
		{http.MethodDelete, sql + "app", ``},
		// A Redis connection that names no user signs in as default.
		{http.MethodPut, fmt.Sprintf("/api/v1/databases/%d/server/roles/default", cache), `{"password":"Another-Passw0rd-9"}`},
		{http.MethodPut, fmt.Sprintf("/api/v1/databases/%d/redis/acl/default", cache), `{"commands":["+@read"]}`},
		{http.MethodDelete, fmt.Sprintf("/api/v1/databases/%d/redis/acl/default", cache), ``},
		{http.MethodPut, fmt.Sprintf("/api/v1/databases/%d/mongo/users", events), `{"user":"app","password":"Another-Passw0rd-9"}`},
		{http.MethodDelete, fmt.Sprintf("/api/v1/databases/%d/mongo/users", events), `{"user":"app"}`},
	} {
		rec := c.do(rq.method, rq.path, rq.body, nil)
		if !isProtectedRefusal(rec.Code, rec.Body.String()) {
			t.Errorf("%s %s = %d %s, want 409 connection_read_only", rq.method, rq.path, rec.Code, rec.Body.String())
		}
	}
	// Another account on the same server goes as far as the server.
	for _, rq := range []struct{ method, path, body string }{
		{http.MethodPut, sql + "reports", `{"password":"Another-Passw0rd-9"}`},
		{http.MethodDelete, sql + "reports", ``},
	} {
		rec := c.do(rq.method, rq.path, rq.body, nil)
		if isProtectedRefusal(rec.Code, rec.Body.String()) || rec.Code < http.StatusBadRequest {
			t.Errorf("%s %s = %d %s, want it to reach the server and fail there", rq.method, rq.path, rec.Code, rec.Body.String())
		}
	}
}
