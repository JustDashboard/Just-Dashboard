package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHostAlertSchemaOnlyAddsTables(t *testing.T) {
	for _, statement := range strings.Split(sqlComment.ReplaceAllString(hostAlertSchema, ""), ";") {
		words := strings.Fields(strings.ToUpper(statement))
		if len(words) == 0 {
			continue
		}
		if words[0] != "CREATE" || !strings.Contains(strings.Join(words, " "), " IF NOT EXISTS ") {
			t.Fatal("nonadditive migration", statement)
		}
	}
}

// The tables arrive on a new install and on one upgraded from 0.6.6, and a
// rule with its state survives the next boot, since Open runs on every one.
func TestHostAlertSchemaReachesFreshAndUpgradedInstalls(t *testing.T) {
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
			if _, err = st.DB.Exec(`INSERT INTO host_alert_rules(kind, created_at, updated_at) VALUES('disk_filling', 1, 1)`); err != nil {
				t.Fatal(err)
			}
			if _, err = st.DB.Exec(`INSERT INTO host_alert_state(rule_id, subject, firing_since) VALUES(1, '/', 5)`); err != nil {
				t.Fatal(err)
			}
			st.Close()
			st, err = Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			var since int64
			if err = st.DB.QueryRow(`SELECT firing_since FROM host_alert_state WHERE rule_id=1 AND subject='/'`).Scan(&since); err != nil || since != 5 {
				t.Fatal("reopening lost a host alert's state", since, err)
			}
			// A rule's state goes with the rule.
			if _, err = st.DB.Exec(`DELETE FROM host_alert_rules WHERE id=1`); err != nil {
				t.Fatal(err)
			}
			var left int
			if err = st.DB.QueryRow(`SELECT COUNT(*) FROM host_alert_state`).Scan(&left); err != nil || left != 0 {
				t.Fatal("a removed rule left its state behind", left, err)
			}
		})
	}
}
