package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWireGuardRecordSchemaFreshAndUpgradeKeepHistory(t *testing.T) {
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
			for _, statement := range []string{
				`INSERT INTO network_wg_samples(iface,public_key,ts,rx,tx,rx_delta,tx_delta,handshake) VALUES('wg0','key',100,10,20,10,20,90)`,
				`INSERT INTO network_wg_usage(iface,public_key,day,rx,tx) VALUES('wg0','key',20000,10,20)`,
				`INSERT INTO network_wg_endpoints(iface,public_key,endpoint,first_seen,last_seen) VALUES('wg0','key','198.51.100.4:41641',90,100)`,
				`INSERT INTO network_wg_events(iface,ts,kind,detail) VALUES('wg0',100,'created','udp 51820')`,
				`INSERT INTO network_wg_quotas(iface,public_key,period,limit_bytes) VALUES('wg0','key','month',1000)`,
				`INSERT INTO network_vpn_clients(iface,public_key,name,created_at,client_routes) VALUES('wg0','key','phone',1,'10.8.0.0/24')`,
			} {
				if _, e = st.DB.Exec(statement); e != nil {
					t.Fatal(statement, e)
				}
			}
			if _, e = st.DB.Exec(`INSERT INTO network_wg_samples(iface,public_key,ts) VALUES('wg0','key',100)`); e == nil {
				t.Fatal("a peer sampled twice at one instant")
			}
			st.Close()
			st, e = Open(dir)
			if e != nil {
				t.Fatal(e)
			}
			defer st.Close()
			var samples, events int
			var limit int64
			if e = st.DB.QueryRow(`SELECT count(*) FROM network_wg_samples`).Scan(&samples); e != nil || samples != 1 {
				t.Fatal("reopen lost samples", samples, e)
			}
			if e = st.DB.QueryRow(`SELECT count(*) FROM network_wg_events`).Scan(&events); e != nil || events != 1 {
				t.Fatal("reopen lost lifecycle history", events, e)
			}
			if e = st.DB.QueryRow(`SELECT limit_bytes FROM network_wg_quotas WHERE public_key='key'`).Scan(&limit); e != nil || limit != 1000 {
				t.Fatal("reopen lost the usage budget", limit, e)
			}
		})
	}
}

func TestWireGuardRecordSchemaOnlyAddsTablesAndIndexes(t *testing.T) {
	for _, statement := range strings.Split(sqlComment.ReplaceAllString(networkWireGuardSchema, ""), ";") {
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
