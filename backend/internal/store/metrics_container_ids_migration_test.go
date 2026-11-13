package store_test

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

// An install that recorded its containers' ids spelled out in every sample
// gains the place to write an id once and the column for its number, and
// nothing it recorded is touched: the rows keep the ids they spell, hold no
// number, and the index that finds them is still there beside the new one.
func TestContainerIdNumbersArriveBesideSpelledOutHistory(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, store.DatabaseFile))
	if err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("a", 64)
	// The table as 0.8.5 left it.
	_, err = db.Exec(`CREATE TABLE metric_container_samples (
		ts INTEGER NOT NULL, name TEXT NOT NULL, cpu_percent REAL NOT NULL DEFAULT 0,
		mem_bytes INTEGER NOT NULL DEFAULT 0, mem_limit INTEGER NOT NULL DEFAULT 0,
		mem_percent REAL NOT NULL DEFAULT 0, net_rx INTEGER NOT NULL DEFAULT 0,
		net_tx INTEGER NOT NULL DEFAULT 0, pids INTEGER NOT NULL DEFAULT 0,
		block_read INTEGER NOT NULL DEFAULT 0, block_write INTEGER NOT NULL DEFAULT 0,
		size_rw INTEGER NOT NULL DEFAULT 0, container_id TEXT NOT NULL DEFAULT '',
		network_available INTEGER DEFAULT NULL, block_available INTEGER DEFAULT NULL,
		mem_limited INTEGER DEFAULT NULL, cpu_total INTEGER DEFAULT NULL, sample_time REAL DEFAULT NULL,
		cpu_periods INTEGER DEFAULT NULL, cpu_throttled INTEGER DEFAULT NULL,
		PRIMARY KEY(name,ts));
		CREATE INDEX idx_container_samples_identity_ts ON metric_container_samples(container_id, ts);
		INSERT INTO metric_container_samples(ts,name,cpu_percent,container_id,cpu_total) VALUES(123,'web',42,?,7)`, id)
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
		var spelled string
		var ref sql.NullInt64
		var cpu float64
		var total int64
		if err := st.DB.QueryRow(`SELECT container_id, container_ref, cpu_percent, cpu_total
			FROM metric_container_samples WHERE name='web' AND ts=123`).Scan(&spelled, &ref, &cpu, &total); err != nil {
			t.Fatal(err)
		}
		if spelled != id || ref.Valid || cpu != 42 || total != 7 {
			t.Fatalf("open %d rewrote recorded history: id=%q number=%v cpu=%v total=%v", i+1, spelled, ref, cpu, total)
		}
		var written int
		if err := st.DB.QueryRow(`SELECT COUNT(*) FROM metric_container_ids`).Scan(&written); err != nil || written != 0 {
			t.Fatalf("open %d: %d ids written down, %v; the store moves nothing, the recorder writes an id when it next samples", i+1, written, err)
		}
		for _, index := range []string{"idx_container_samples_identity_ts", "idx_container_samples_ref_ts"} {
			var found int
			if err := st.DB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`, index).Scan(&found); err != nil || found != 1 {
				t.Fatalf("open %d: index %s: count=%d err=%v", i+1, index, found, err)
			}
		}
		if err := st.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
