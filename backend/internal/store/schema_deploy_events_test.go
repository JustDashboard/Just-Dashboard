package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeployEventSchemaOnlyAddsTables(t *testing.T) {
	for _, statement := range strings.Split(sqlComment.ReplaceAllString(deployEventSchema, ""), ";") {
		words := strings.Fields(strings.ToUpper(statement))
		if len(words) == 0 {
			continue
		}
		if words[0] != "CREATE" || !strings.Contains(strings.Join(words, " "), " IF NOT EXISTS ") {
			t.Fatal("nonadditive migration", statement)
		}
	}
}

// The table arrives on a new install and on one upgraded from 0.6.6, and a
// row keyed the way the recorder writes one — the daemon's nanosecond
// timestamp, the environment it belongs to, the event whole — reads back.
func TestDeployEventsReachFreshAndUpgradedInstalls(t *testing.T) {
	upgraded := t.TempDir()
	script, err := os.ReadFile(filepath.Join("testdata", "0.6.6.sql"))
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(upgraded, DatabaseFile))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(script)); err != nil {
		t.Fatal(err)
	}
	db.Close()
	for name, dir := range map[string]string{"fresh": t.TempDir(), "0.6.6": upgraded} {
		t.Run(name, func(t *testing.T) {
			st, err := Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			columns, err := tableColumns(t.Context(), st.DB, "deploy_container_events")
			if err != nil {
				t.Fatal(err)
			}
			for _, column := range []string{"ts_nano", "environment_id", "release_id", "release_number", "run_id", "kind", "action", "name", "container_id", "exit_code", "signal", "requested", "source", "event_json"} {
				if !columns[column] {
					t.Fatalf("deploy_container_events lacks %s: %v", column, columns)
				}
			}
			if _, err := st.DB.Exec(`INSERT INTO deploy_container_events(ts_nano, environment_id, release_id, release_number, run_id, kind, action, name, container_id, exit_code, source, event_json)
				VALUES(1727430000123456789, 7, 21, 3, 42, 'container', 'die', 'api-production-r3', 'c0ffee', '137', 'daemon', '{"action":"die"}')`); err != nil {
				t.Fatal(err)
			}
			var ts int64
			var exitCode, body string
			if err := st.DB.QueryRow(`SELECT ts_nano, exit_code, event_json FROM deploy_container_events WHERE environment_id = 7`).Scan(&ts, &exitCode, &body); err != nil {
				t.Fatal(err)
			}
			if ts != 1727430000123456789 || exitCode != "137" || body != `{"action":"die"}` {
				t.Fatalf("the row came back changed: %d %q %q", ts, exitCode, body)
			}
		})
	}
}
