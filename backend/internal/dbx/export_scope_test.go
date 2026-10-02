package dbx

import (
	"bytes"
	"context"
	"database/sql"
	"testing"
)

// An export of a statement runs where a run of it does: inside the engine's
// read scope. The statements here write and return rows, which is what a
// statement the classifier mistook for a read would do, and ExportQuery does
// not classify — so what happens to them is the scope's doing alone. Each is
// either refused by the engine or undone; none is kept, as every one of them
// was on these engines while only PostgreSQL and MySQL were given a scope.

func exportScopeRows(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestExportQueryRunsInsideTheReadScopeOnSQLite(t *testing.T) {
	db, _ := openTestDB(t)
	before := exportScopeRows(t, db, "users")
	var file bytes.Buffer
	_, _, err := ExportQuery(t.Context(), db, DriverSQLite,
		`INSERT INTO users(id, email, name) VALUES (90, 'kept@x.io', 'Kept') RETURNING id`, ExportOptions{Format: ExportCSV}, &file)
	if err == nil {
		t.Errorf("a writing statement was exported: %q", file.String())
	}
	if after := exportScopeRows(t, db, "users"); after != before {
		t.Errorf("the export kept a row: %d rows before, %d after", before, after)
	}
	// The connection goes back to the pool able to write.
	if _, err := db.ExecContext(t.Context(), `INSERT INTO users(id, email, name) VALUES (91, 'next@x.io', 'Next')`); err != nil {
		t.Errorf("the next statement on the pool was refused: %v", err)
	}
	// And a read is exported as it was.
	file.Reset()
	if n, _, err := ExportQuery(t.Context(), db, DriverSQLite, `SELECT id, email FROM users ORDER BY id`, ExportOptions{Format: ExportCSV}, &file); err != nil || n != before+1 {
		t.Errorf("a read exported %d rows (%v), want %d", n, err, before+1)
	}
}

func TestLiveExportQueryRunsInsideTheReadScopeOnSQLServer(t *testing.T) {
	db, _ := liveOwnMSSQL(t, "jd_export_scope")
	execAll(t, db, `CREATE TABLE dbo.queue_like (id int NOT NULL PRIMARY KEY, body nvarchar(40))`,
		`INSERT INTO dbo.queue_like VALUES (1, N'first'), (2, N'second')`)
	var file bytes.Buffer
	// What RECEIVE does to a queue: hand the rows back and take them away.
	n, _, err := ExportQuery(t.Context(), db, DriverMSSQL,
		`DELETE FROM dbo.queue_like OUTPUT deleted.id, deleted.body`, ExportOptions{Format: ExportCSV}, &file)
	if err != nil {
		t.Fatalf("export = %v", err)
	}
	if n != 2 {
		t.Errorf("exported %d rows, want the two the statement returned", n)
	}
	if left := exportScopeRows(t, db, "dbo.queue_like"); left != 2 {
		t.Errorf("%d rows are left: what the exported statement did was kept", left)
	}
}

func TestLiveExportQueryRunsInsideTheReadScopeOnClickHouse(t *testing.T) {
	db := liveSQL(t, DriverClickHouse, "JD_TEST_CLICKHOUSE_DSN", "clickhouse://default@127.0.0.1:9000/default")
	execAll(t, db, `DROP TABLE IF EXISTS jd_export_scope`,
		`CREATE TABLE jd_export_scope (id UInt32) ENGINE = MergeTree ORDER BY id`)
	t.Cleanup(func() { _, _ = db.Exec(`DROP TABLE IF EXISTS jd_export_scope`) })
	var file bytes.Buffer
	if _, _, err := ExportQuery(t.Context(), db, DriverClickHouse,
		`INSERT INTO jd_export_scope VALUES (1)`, ExportOptions{Format: ExportCSV}, &file); err == nil {
		t.Error("a writing statement was exported")
	}
	if n := exportScopeRows(t, db, "jd_export_scope"); n != 0 {
		t.Errorf("the export kept %d rows", n)
	}
}

func TestLiveExportQueryRunsInsideTheReadScopeOnOracle(t *testing.T) {
	db := liveSQL(t, DriverOracle, "JD_TEST_ORACLE_DSN", "oracle://jdtest:jdtest@127.0.0.1:1521/FREEPDB1")
	_, _ = db.Exec(`DROP TABLE JD_EXPORT_SCOPE`)
	execAll(t, db, `CREATE TABLE JD_EXPORT_SCOPE (ID NUMBER PRIMARY KEY)`)
	t.Cleanup(func() { _, _ = db.Exec(`DROP TABLE JD_EXPORT_SCOPE`) })
	var file bytes.Buffer
	if _, _, err := ExportQuery(t.Context(), db, DriverOracle,
		`INSERT INTO JD_EXPORT_SCOPE VALUES (1)`, ExportOptions{Format: ExportCSV}, &file); err == nil {
		t.Error("a writing statement was exported")
	}
	if n := exportScopeRows(t, db, "JD_EXPORT_SCOPE"); n != 0 {
		t.Errorf("the export kept %d rows", n)
	}
	file.Reset()
	if n, _, err := ExportQuery(t.Context(), db, DriverOracle, `SELECT 1 AS n FROM dual`, ExportOptions{Format: ExportCSV}, &file); err != nil || n != 1 {
		t.Errorf("a read exported %d rows (%v), want 1", n, err)
	}
}
