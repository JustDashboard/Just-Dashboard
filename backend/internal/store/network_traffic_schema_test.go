package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An interface history recorded before exact counters keeps its rows, and
// they read as unknown bytes rather than zero; the traffic tables exist on a
// fresh install and on an upgraded one.
func TestTrafficSchemaFreshAndUpgradeKeepHistory(t *testing.T) {
	upgraded := t.TempDir()
	script, e := os.ReadFile(filepath.Join("testdata", "0.6.6.sql"))
	if e != nil {
		t.Fatal(e)
	}
	db, e := sql.Open("sqlite", filepath.Join(upgraded, DatabaseFile))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(string(script)); e != nil {
		t.Fatal(e)
	}
	// The shipped shape of the interface samples, with a row in it.
	if _, e = db.Exec(`CREATE TABLE metric_interface_samples (
		ts INTEGER NOT NULL, iface TEXT NOT NULL, rx_rate REAL NOT NULL DEFAULT 0, tx_rate REAL NOT NULL DEFAULT 0,
		rx_peak REAL NOT NULL DEFAULT 0, tx_peak REAL NOT NULL DEFAULT 0, rx_errors INTEGER NOT NULL DEFAULT 0,
		tx_errors INTEGER NOT NULL DEFAULT 0, rx_dropped INTEGER NOT NULL DEFAULT 0, tx_dropped INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (iface, ts));
		INSERT INTO metric_interface_samples (ts, iface, rx_rate, tx_rate) VALUES (100, 'eth0', 1000, 500);`); e != nil {
		t.Fatal(e)
	}
	db.Close()
	for name, dir := range map[string]string{"fresh": t.TempDir(), "upgraded": upgraded} {
		t.Run(name, func(t *testing.T) {
			st, e := Open(dir)
			if e != nil {
				t.Fatal(e)
			}
			for _, statement := range []string{
				`INSERT INTO metric_interface_samples (ts, iface, rx_rate, tx_rate, rx_bytes, tx_bytes, rx_packets, tx_packets, span) VALUES (200, 'eth0', 1, 1, 15000, 7500, 10, 5, 15)`,
				`INSERT INTO network_interface_quotas (iface, period, direction, limit_bytes, created_by, created_at) VALUES ('eth0', 'month', 'both', 1073741824, 'ops', 1)`,
				`INSERT INTO network_address_blocks (id, address, reason, comment, created_at, expires_at) VALUES ('a', '198.51.100.23', 'scan', 'jd-block a', 1, 3601)`,
				`INSERT INTO network_shaping_applied (device, entry, kind, handle, options, applied_at) VALUES ('eth0', '{}', 'fq_codel', '8004:', '{"target":"4999"}', 1)`,
				`INSERT INTO network_congestion_snapshots (at, before_algorithm, after_algorithm, payload) VALUES (1, 'cubic', 'bbr', '{}')`,
			} {
				if _, e = st.DB.Exec(statement); e != nil {
					t.Fatal(statement, e)
				}
			}
			if _, e = st.DB.Exec(`INSERT INTO network_interface_quotas (iface, period) VALUES ('eth0', 'day')`); e == nil {
				t.Fatal("a device holds two budgets")
			}
			st.Close()
			st, e = Open(dir)
			if e != nil {
				t.Fatal(e)
			}
			defer st.Close()
			var state string
			if e = st.DB.QueryRow(`SELECT state FROM network_address_blocks WHERE id = 'a'`).Scan(&state); e != nil || state != "active" {
				t.Fatal("reopen lost the block", state, e)
			}
			if name == "upgraded" {
				var bytes, span sql.NullInt64
				if e = st.DB.QueryRow(`SELECT rx_bytes, span FROM metric_interface_samples WHERE ts = 100`).Scan(&bytes, &span); e != nil || bytes.Valid || span.Valid {
					t.Fatal("an old row's bytes read as known", bytes, span, e)
				}
			}
		})
	}
}

func TestTrafficSchemaOnlyAddsTablesAndIndexes(t *testing.T) {
	for _, statement := range strings.Split(sqlComment.ReplaceAllString(networkTrafficSchema, ""), ";") {
		words := strings.Fields(strings.ToUpper(statement))
		if len(words) == 0 {
			continue
		}
		text := strings.Join(words, " ")
		if words[0] != "CREATE" || !strings.Contains(text, " IF NOT EXISTS ") {
			t.Fatalf("non-additive statement: %s", text)
		}
	}
}
