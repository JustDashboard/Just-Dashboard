package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGatewayTelemetrySchemaFreshAndUpgradedKeepsCounters(t *testing.T) {
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
			if _, err = st.DB.Exec(`INSERT INTO network_gateway_counters(key,packets,bytes,generation) VALUES('forward:1',40,4000,7)`); err != nil {
				t.Fatal(err)
			}
			if _, err = st.DB.Exec(`INSERT INTO network_protection_samples(ts,key,value) VALUES(1,'conntrack:count',12)`); err != nil {
				t.Fatal(err)
			}
			st.Close()
			st, err = Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			var packets, generation int64
			if err = st.DB.QueryRow(`SELECT packets, generation FROM network_gateway_counters WHERE key='forward:1'`).Scan(&packets, &generation); err != nil || packets != 40 || generation != 7 {
				t.Fatal("reopening lost the accumulated counter", packets, generation, err)
			}
		})
	}
}

func TestGatewayTelemetrySchemaOnlyAddsTablesAndIndexes(t *testing.T) {
	for _, statement := range strings.Split(sqlComment.ReplaceAllString(networkGatewaySchema, ""), ";") {
		words := strings.Fields(strings.ToUpper(statement))
		if len(words) == 0 {
			continue
		}
		if words[0] != "CREATE" || !strings.Contains(strings.Join(words, " "), " IF NOT EXISTS ") {
			t.Fatal("nonadditive migration", statement)
		}
	}
}
