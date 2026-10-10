package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNetworkIncidentSchemaOnlyAddsTablesAndIndexes(t *testing.T) {
	for _, statement := range strings.Split(sqlComment.ReplaceAllString(networkIncidentSchema, ""), ";") {
		words := strings.Fields(strings.ToUpper(statement))
		if len(words) == 0 {
			continue
		}
		if words[0] != "CREATE" || !strings.Contains(strings.Join(words, " "), " IF NOT EXISTS ") {
			t.Fatal("nonadditive migration", statement)
		}
	}
}

func TestNetworkIncidentSchemaReachesFreshAndUpgradedInstalls(t *testing.T) {
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
			if _, err = st.DB.Exec(`INSERT INTO network_incidents(finding_id,source,level,opened_at,last_seen_at) VALUES('firewall.off','firewall','warning',10,20)`); err != nil {
				t.Fatal(err)
			}
			st.Close()
			st, err = Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			var finding string
			var resolved, unobserved int64
			if err = st.DB.QueryRow(`SELECT finding_id,resolved_at,unobserved_since FROM network_incidents`).Scan(&finding, &resolved, &unobserved); err != nil || finding != "firewall.off" || resolved != 0 || unobserved != 0 {
				t.Fatal("reopening lost incident evidence", finding, resolved, unobserved, err)
			}
		})
	}
}
