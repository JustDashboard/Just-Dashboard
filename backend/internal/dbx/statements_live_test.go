package dbx

import (
	"strings"
	"testing"
)

// The setup against a server that loads the module already, which is what
// the fixture is: in a database where the extension has not been made, the
// answer is the one statement that makes it and nothing is offered to store;
// and a preload stores nothing, since the list the next start loads names the
// module. Nothing here writes to the server's configuration: the test runs
// only where the preload is sure to be a no-op.
func TestLivePostgresStatementsSetupWhereTheModuleIsLoaded(t *testing.T) {
	db, dsn := opsLive(t, DriverPostgres, "JD_TEST_POSTGRES_DSN")
	ctx := t.Context()
	preload, err := readPgPreload(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if !hasLibrary(preload.loaded, pgStatementsLibrary) || !hasLibrary(preload.next, pgStatementsLibrary) {
		t.Skipf("this server does not load pg_stat_statements at every start: %q, next %q", preload.loaded, preload.next)
	}
	if preload.source == "" {
		t.Error("the setting's source was not read")
	}

	const name = "jdt_pg_stat_statements_fresh"
	_, _ = db.Exec(`DROP DATABASE IF EXISTS ` + name)
	opsMustExec(t, db, `CREATE DATABASE `+name)
	t.Cleanup(func() { _, _ = db.Exec(`DROP DATABASE IF EXISTS ` + name) })
	fresh, err := OpenDatabase(ctx, DriverPostgres, dsn, name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fresh.Close() })

	report, err := TopStatements(ctx, fresh, DriverPostgres, StatementsOptions{})
	if err != nil {
		t.Fatal(err)
	}
	enable := report.Enable
	if report.Supported || enable == nil {
		t.Fatalf("a database without the extension: %+v", report)
	}
	if !enable.Available || !enable.Preloaded || enable.Configured || enable.Preload != "" || enable.Blocked != "" ||
		enable.SQL != "CREATE EXTENSION IF NOT EXISTS pg_stat_statements;" {
		t.Errorf("enable = %+v", enable)
	}

	change, err := PreloadStatements(ctx, fresh, DriverPostgres)
	if err != nil {
		t.Fatal(err)
	}
	if len(change.Statements) != 0 || change.RestartRequired || !change.Persisted ||
		change.Name != "shared_preload_libraries" || !strings.Contains(change.Value, pgStatementsLibrary) {
		t.Errorf("a preload of a list that names the module = %+v", change)
	}
}
