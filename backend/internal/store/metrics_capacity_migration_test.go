package store_test

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

// An install from before right-sizing gains the throttling counters as
// unknown, never as zero throttling, and the volume-size table.
func TestCapacityMigrationLeavesOldContainerHistoryUnmeasured(t *testing.T) {
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
	for range 2 {
		st, err := store.Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		var periods, throttled sql.NullInt64
		if err := st.DB.QueryRow(`SELECT cpu_periods, cpu_throttled FROM metric_container_samples WHERE name='web'`).
			Scan(&periods, &throttled); err != nil || periods.Valid || throttled.Valid {
			t.Fatalf("old history gained counters: %v %v %v", periods, throttled, err)
		}
		if _, err := st.DB.Exec(`INSERT INTO metric_volume_samples(ts, name, size_bytes) VALUES(1, 'v', 2)`); err != nil {
			t.Fatalf("volume samples table: %v", err)
		}
		if _, err := st.DB.Exec(`DELETE FROM metric_volume_samples`); err != nil {
			t.Fatal(err)
		}
		if err := st.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
