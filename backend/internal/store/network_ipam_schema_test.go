package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIPAMSchemaFreshAndExistingInstallKeepHeldAllocations(t *testing.T) {
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
	db.Close()
	for name, dir := range map[string]string{"fresh": t.TempDir(), "0.6.6": upgraded} {
		t.Run(name, func(t *testing.T) {
			st, e := Open(dir)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = st.DB.Exec(`INSERT INTO network_ipam_pools(id,name,prefix,allocation_bits) VALUES('pool','shared','fd48:abcd::/48',64)`); e != nil {
				t.Fatal(e)
			}
			if _, e = st.DB.Exec(`INSERT INTO network_ipam_reservations(id,pool_id,prefix,state,handoff_id,unknown_sources) VALUES('reservation','pool','fd48:abcd::/64','handing_off','attempt','["provider:unknown"]')`); e != nil {
				t.Fatal(e)
			}
			st.Close()
			st, e = Open(dir)
			if e != nil {
				t.Fatal(e)
			}
			defer st.Close()
			var state, handoff, coverage string
			if e = st.DB.QueryRow(`SELECT state,handoff_id,unknown_sources FROM network_ipam_reservations WHERE id='reservation'`).Scan(&state, &handoff, &coverage); e != nil || state != "handing_off" || handoff != "attempt" || coverage != `["provider:unknown"]` {
				t.Fatal("schema reopen lost planning evidence", state, handoff, coverage, e)
			}
			if _, e = st.DB.Exec(`DELETE FROM network_ipam_pools WHERE id='pool'`); e == nil {
				t.Fatal("pool with retained reservation was deleted")
			}
		})
	}
}
func TestIPAMSchemaOnlyAddsTablesAndIndexes(t *testing.T) {
	for _, statement := range strings.Split(sqlComment.ReplaceAllString(networkIPAMSchema, ""), ";") {
		words := strings.Fields(strings.ToUpper(statement))
		if len(words) == 0 {
			continue
		}
		if words[0] != "CREATE" || !strings.Contains(strings.Join(words, " "), " IF NOT EXISTS ") {
			t.Fatal("nonadditive migration", statement)
		}
	}
}
