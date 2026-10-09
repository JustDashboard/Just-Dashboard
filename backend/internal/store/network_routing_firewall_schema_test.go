package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRouteAndFirewallHistorySchemasAreAdditive(t *testing.T) {
	for name, schema := range map[string]string{"route": networkRouteHistorySchema, "firewall": firewallHistorySchema} {
		for _, statement := range strings.Split(sqlComment.ReplaceAllString(schema, ""), ";") {
			words := strings.Fields(strings.ToUpper(statement))
			if len(words) == 0 {
				continue
			}
			text := strings.Join(words, " ")
			switch {
			case words[0] == "CREATE" && !strings.Contains(text, " IF NOT EXISTS "):
				t.Errorf("%s: CREATE without IF NOT EXISTS: %s", name, text)
			case words[0] == "DROP" || words[0] == "ALTER" || strings.Contains(text, " RENAME "):
				t.Errorf("%s: not additive: %s", name, text)
			}
		}
	}
}

func TestRouteAndFirewallHistoryUpgradeAnExistingInstall(t *testing.T) {
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
	if _, err = db.Exec(`INSERT INTO watched_domains(domain,created_at) VALUES('history.example',1)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	for range 2 {
		st, err := Open(upgraded)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = st.DB.Exec(`INSERT INTO network_route_events(observed_at,object,change) VALUES(1,'route','added')`); err != nil {
			t.Fatal(err)
		}
		if _, err = st.DB.Exec(`INSERT INTO firewall_rule_events(at,operation,rule_id) VALUES(1,'add','fw-1')`); err != nil {
			t.Fatal(err)
		}
		if _, err = st.DB.Exec(`INSERT INTO network_route_snapshot(id,observed_at,snapshot_json) VALUES(1,1,'{}') ON CONFLICT(id) DO UPDATE SET observed_at=excluded.observed_at`); err != nil {
			t.Fatal(err)
		}
		var kept string
		if err = st.DB.QueryRow(`SELECT domain FROM watched_domains WHERE domain='history.example'`).Scan(&kept); err != nil || kept != "history.example" {
			t.Fatalf("existing data changed: %q %v", kept, err)
		}
		st.Close()
	}
	st, err := Open(upgraded)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var events int
	if err = st.DB.QueryRow(`SELECT count(*) FROM network_route_events`).Scan(&events); err != nil || events != 2 {
		t.Fatalf("route events survived reopening: %d %v", events, err)
	}
}
