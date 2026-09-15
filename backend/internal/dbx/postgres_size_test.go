package dbx

import (
	"context"
	"crypto/rand"
	"database/sql"
	"strings"
	"testing"
	"time"
)

func TestLivePostgresSizesTolerateConcurrentRelationDrop(t *testing.T) {
	const fallback = "postgres://jdtest:jdtest@127.0.0.1:5432/jdtest?sslmode=disable"
	db := liveSQL(t, DriverPostgres, "JD_TEST_POSTGRES_DSN", fallback)
	other, err := sql.Open("pgx", liveDSN(t, "JD_TEST_POSTGRES_DSN", fallback))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { other.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	schema := "jd_sizes_" + strings.ToLower(rand.Text())
	if _, err := other.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = other.Exec("DROP SCHEMA " + schema + " CASCADE") })
	for _, table := range []string{"vanishing", "retained"} {
		if _, err := other.ExecContext(ctx, "CREATE TABLE "+schema+"."+table+" (id INTEGER PRIMARY KEY)"); err != nil {
			t.Fatal(err)
		}
		if _, err := other.ExecContext(ctx, "INSERT INTO "+schema+"."+table+" VALUES (1)"); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var oid int64
	// Reading the catalogue establishes a snapshot without locking the table.
	if err := tx.QueryRowContext(ctx, `SELECT c.oid FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = $1 AND c.relname = 'vanishing'`, schema).Scan(&oid); err != nil {
		t.Fatal(err)
	}
	if _, err := other.ExecContext(ctx, "DROP TABLE "+schema+".vanishing"); err != nil {
		t.Fatal(err)
	}
	var size sql.NullInt64
	if err := tx.QueryRowContext(ctx, "SELECT pg_total_relation_size($1::oid)", oid).Scan(&size); err != nil || size.Valid {
		t.Fatalf("fixture must expose PostgreSQL's missing-relation NULL: %+v (%v)", size, err)
	}

	// Execute the production catalogue queries and scanners in this held
	// snapshot, reproducing the drop race without relying on thread timing.
	rows, err := tx.QueryContext(ctx, postgresTablesQuery, schema)
	if err != nil {
		t.Fatal(err)
	}
	tables, err := scanTables(rows)
	if err != nil {
		t.Fatalf("Tables with stale catalogue entry: %v", err)
	}
	if len(tables) != 2 {
		t.Fatalf("expected snapshot to retain both catalogue entries, got %#v", tables)
	}
	for _, table := range tables {
		if table.Name == "vanishing" && table.Size != 0 {
			t.Errorf("vanished table bytes = %d, want zero", table.Size)
		}
		if table.Name == "retained" && table.Size <= 0 {
			t.Errorf("retained table lost its size: %+v", table)
		}
	}
	rows, err = tx.QueryContext(ctx, postgresTableSizesQuery, schema)
	if err != nil {
		t.Fatal(err)
	}
	sizes, err := scanSizes(rows, schema)
	if err != nil {
		t.Fatalf("TableSizes with stale catalogue entry: %v", err)
	}
	if len(sizes) != 2 {
		t.Fatalf("expected both catalogue entries, got %#v", sizes)
	}
	for _, size := range sizes {
		if size.Table == "vanishing" && (size.Bytes != 0 || size.DataBytes != 0 || size.IndexBytes != 0) {
			t.Errorf("vanished relation sizes = %+v, want zeros", size)
		}
		if size.Table == "retained" && (size.Bytes <= 0 || size.DataBytes <= 0 || size.IndexBytes <= 0) {
			t.Errorf("retained relation lost sizes: %+v", size)
		}
	}
}
