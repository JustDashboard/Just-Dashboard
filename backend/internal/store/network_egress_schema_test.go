package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNetworkEgressSchemaOnlyAddsTablesAndIndexes(t *testing.T) {
	for _, statement := range strings.Split(sqlComment.ReplaceAllString(networkEgressSchema, ""), ";") {
		words := strings.Fields(strings.ToUpper(statement))
		if len(words) == 0 {
			continue
		}
		if words[0] != "CREATE" || !strings.Contains(strings.Join(words, " "), " IF NOT EXISTS ") {
			t.Fatal("nonadditive migration", statement)
		}
	}
}

func TestNetworkEgressSchemaReachesFreshAndUpgradedInstalls(t *testing.T) {
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
			if _, err = st.DB.Exec(`INSERT INTO network_egress_events(group_id,at,kind,outcome,reason) VALUES(3,10,'switch','applied','m1 is down')`); err != nil {
				t.Fatal(err)
			}
			if _, err = st.DB.Exec(`INSERT INTO network_egress_samples(group_id,member_id,at,ok,total,good) VALUES(3,1,10,1,2,0)`); err != nil {
				t.Fatal(err)
			}
			if _, err = st.DB.Exec(`INSERT INTO network_egress_simulations(id,group_id,status) VALUES('s1',3,'passed')`); err != nil {
				t.Fatal(err)
			}
			st.Close()
			st, err = Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			var reason, status string
			var samples int
			if err = st.DB.QueryRow(`SELECT reason FROM network_egress_events`).Scan(&reason); err != nil || reason != "m1 is down" {
				t.Fatal("reopening lost the egress decision", reason, err)
			}
			if err = st.DB.QueryRow(`SELECT COUNT(*) FROM network_egress_samples`).Scan(&samples); err != nil || samples != 1 {
				t.Fatal("reopening lost the egress samples", samples, err)
			}
			if err = st.DB.QueryRow(`SELECT status FROM network_egress_simulations`).Scan(&status); err != nil || status != "passed" {
				t.Fatal("reopening lost the simulation", status, err)
			}
		})
	}
}
