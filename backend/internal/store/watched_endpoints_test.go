package store_test

import (
	"database/sql"
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
