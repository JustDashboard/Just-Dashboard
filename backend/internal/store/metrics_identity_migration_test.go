package store_test

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

func TestContainerIdentityMigrationPreservesUnattributedHistory(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, store.DatabaseFile))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE metric_container_samples (
		ts INTEGER NOT NULL, name TEXT NOT NULL, cpu_percent REAL NOT NULL DEFAULT 0,
		mem_bytes INTEGER NOT NULL DEFAULT 0, mem_limit INTEGER NOT NULL DEFAULT 0,
		mem_percent REAL NOT NULL DEFAULT 0, net_rx INTEGER NOT NULL DEFAULT 0,
		net_tx INTEGER NOT NULL DEFAULT 0, pids INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY(name,ts));
		INSERT INTO metric_container_samples(ts,name,cpu_percent) VALUES(123,'web',42)`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		st, err := store.Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		var id string
		var cpu float64
		err = st.DB.QueryRow(`SELECT container_id,cpu_percent FROM metric_container_samples WHERE name='web' AND ts=123`).Scan(&id, &cpu)
		if err != nil || id != "" || cpu != 42 {
			t.Fatalf("migration attributed or lost old data: id=%q cpu=%v err=%v", id, cpu, err)
		}
		var indexes int
		if err := st.DB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_container_samples_identity_ts'`).Scan(&indexes); err != nil || indexes != 1 {
			t.Fatalf("identity index missing after migration: count=%d err=%v", indexes, err)
		}
		if err := st.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
