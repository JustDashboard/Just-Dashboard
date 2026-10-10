package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNetworkProbeSchemaFreshAndUpgradeKeepDurableIdentity(t *testing.T) {
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
	for name, dir := range map[string]string{"fresh": t.TempDir(), "upgraded": upgraded} {
		t.Run(name, func(t *testing.T) {
			st, e := Open(dir)
			if e != nil {
				t.Fatal(e)
			}
			_, e = st.DB.Exec(`INSERT INTO network_probe_vantages(id,enrollment_hash) VALUES('a','token-a'),('b','token-b')`)
			if e != nil {
				t.Fatal(e)
			}
			_, e = st.DB.Exec(`UPDATE network_probe_vantages SET enrollment_hash='used:a',public_key='key-a',sequence=7 WHERE id='a'`)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = st.DB.Exec(`UPDATE network_probe_vantages SET public_key='key-a' WHERE id='b'`); e == nil {
				t.Fatal("vantage identity reused")
			}
			_, e = st.DB.Exec(`INSERT INTO network_probe_checks(id,vantage_id,request,nonce) VALUES('check','a','{}','nonce')`)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = st.DB.Exec(`INSERT INTO network_probe_checks(id,vantage_id,request,nonce) VALUES('other','b','{}','nonce')`); e == nil {
				t.Fatal("job nonce reused")
			}
			st.Close()
			st, e = Open(dir)
			if e != nil {
				t.Fatal(e)
			}
			defer st.Close()
			var seq int
			var token, status string
			if e = st.DB.QueryRow(`SELECT sequence,enrollment_hash FROM network_probe_vantages WHERE id='a'`).Scan(&seq, &token); e != nil || seq != 7 || token != "used:a" {
				t.Fatal("reopen changed identity", seq, token, e)
			}
			if e = st.DB.QueryRow(`SELECT status FROM network_probe_checks WHERE id='check'`).Scan(&status); e != nil || status != "queued" {
				t.Fatal("reopen changed pending job", status, e)
			}
		})
	}
}

func TestNetworkProbeSchemaOnlyAddsTablesAndIndexes(t *testing.T) {
	for _, statement := range strings.Split(sqlComment.ReplaceAllString(networkProbeSchema, ""), ";") {
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
