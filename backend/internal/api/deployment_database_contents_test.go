package api

import (
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dbx"
)

// A build's seed may name another database on its connection's server
// (${{database.5.url.app_shadow}}): that is not the database the check
// counts, so it holds rows without the check dialling anything, while the
// connection's own database, by name or by default, is counted.
func TestDatabaseContentsCountsOnlyTheConnectionsOwnDatabase(t *testing.T) {
	s := testServer(t)
	t.Cleanup(s.Shutdown)
	// Nothing listens on port 1, so a database that is counted fails to dial.
	sealed, err := s.Sealer.Seal("postgres://app:secret@127.0.0.1:1/app?sslmode=disable&connect_timeout=2")
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.Store.DB.Exec(`INSERT INTO db_connections(name, driver, dsn_enc, created_at) VALUES(?,?,?,?)`,
		"contents", string(dbx.DriverPostgres), sealed, 0)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := result.LastInsertId()
	contents := deploymentDatabaseContents{server: s}
	present, err := contents.DatabaseHasRows(t.Context(), id, "app_shadow")
	if !present || err != nil {
		t.Fatalf("another database on the server: rows %v, err %v", present, err)
	}
	for _, database := range []string{"app", ""} {
		present, err := contents.DatabaseHasRows(t.Context(), id, database)
		if !present || err == nil {
			t.Fatalf("the connection's own database %q was not counted: rows %v, err %v", database, present, err)
		}
	}
}
