package store_test

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

// watched_domains held one row per name, so watching a second port replaced
// the first. Its rows become endpoints on upgrade, a name may then be watched
// on several ports, and a removed endpoint stays removed across restarts even
// though the copy runs on every boot.
func TestWatchedEndpointsKeepTheOldListAndAllowSeveralPorts(t *testing.T) {
	dir := install066Fixture(t)
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	endpoints := func(db *sql.DB, port int) int {
		t.Helper()
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM watched_endpoints
			WHERE domain = 'fixture.example.test' AND port = ? AND ip = ''`, port).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if endpoints(st.DB, 443) != 1 {
		t.Fatal("the 0.6.6 watch list was not carried over")
	}
	if _, err := st.DB.Exec(`INSERT INTO watched_endpoints(domain, port, created_at)
		VALUES('fixture.example.test', 993, 1700000100)`); err != nil {
		t.Fatalf("a second port for the same name: %v", err)
	}
	if _, err := st.DB.Exec(`INSERT INTO watched_endpoints(domain, port, created_at)
		VALUES('fixture.example.test', 993, 1700000200)`); err == nil {
		t.Fatal("the same endpoint twice was accepted")
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	st, err = store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if endpoints(st.DB, 443) != 1 || endpoints(st.DB, 993) != 1 {
		t.Fatal("reopening duplicated or lost an endpoint")
	}
	// What removing one does (handleUnwatchDomain): both rows go.
	if _, err := st.DB.Exec(`DELETE FROM watched_endpoints WHERE domain = 'fixture.example.test' AND port = 443`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`DELETE FROM watched_domains WHERE domain = 'fixture.example.test' AND port = 443`); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	st, err = store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if endpoints(st.DB, 443) != 0 {
		t.Fatal("a removed endpoint came back on restart")
	}
	if endpoints(st.DB, 993) != 1 {
		t.Fatal("the other port was lost")
	}
}

// A watch list from before network probes keeps every row as a TLS watch and
// gains the probe's columns, and a probe's check keeps its connect time.
func TestWatchedEndpointsGainProbeColumns(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, store.DatabaseFile))
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE TABLE watched_endpoints (id INTEGER PRIMARY KEY AUTOINCREMENT, domain TEXT NOT NULL, port INTEGER NOT NULL DEFAULT 443,
		   ip TEXT NOT NULL DEFAULT '', created_at INTEGER NOT NULL, checked_at INTEGER NOT NULL DEFAULT 0,
		   certificate TEXT NOT NULL DEFAULT '', UNIQUE(domain, port, ip))`,
		`CREATE TABLE watched_checks (id INTEGER PRIMARY KEY AUTOINCREMENT,
		   endpoint_id INTEGER NOT NULL REFERENCES watched_endpoints(id) ON DELETE CASCADE, checked_at INTEGER NOT NULL,
		   days_left INTEGER, fingerprint TEXT NOT NULL DEFAULT '', error TEXT NOT NULL DEFAULT '')`,
		`INSERT INTO watched_endpoints(domain, port, created_at) VALUES('old.example.test', 443, 1)`,
		`INSERT INTO watched_checks(endpoint_id, checked_at, days_left) VALUES(1, 2, 30)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var kind, probe string
	if err := st.DB.QueryRow(`SELECT kind, probe FROM watched_endpoints WHERE domain = 'old.example.test'`).Scan(&kind, &probe); err != nil || kind != "tls" || probe != "" {
		t.Fatalf("old row = %q %q %v", kind, probe, err)
	}
	var ms int64
	if err := st.DB.QueryRow(`SELECT ms FROM watched_checks WHERE endpoint_id = 1`).Scan(&ms); err != nil || ms != 0 {
		t.Fatalf("old check = %d %v", ms, err)
	}
	if _, err := st.DB.Exec(`INSERT INTO watched_endpoints(domain, port, kind, created_at) VALUES('db.example.test', 5432, 'tcp', 3)`); err != nil {
		t.Fatalf("a probe: %v", err)
	}
}
