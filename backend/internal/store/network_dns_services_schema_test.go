package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestDNSServicesSchemaUpgradeKeepsSealedStateAndDefaults(t *testing.T) {
	upgraded := t.TempDir()
	script, err := os.ReadFile(filepath.Join("testdata", "0.6.6.sql"))
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := sql.Open("sqlite", filepath.Join(upgraded, DatabaseFile))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = legacy.Exec(string(script)); err != nil {
		t.Fatal(err)
	}
	legacy.Close()
	for name, dir := range map[string]string{"fresh": t.TempDir(), "0.6.6": upgraded} {
		t.Run(name, func(t *testing.T) {
			st, err := Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = st.DB.Exec(`INSERT INTO network_dns_services(id,name,engine,endpoint,secret_enc,created_at,updated_at) VALUES('native','Retained engine','adguard','http://127.0.0.1:3000','sealed-native',1,1);
INSERT INTO network_dns_service_changes(id,connection_id,generation,request_json,before_json,state,created_at,expires_at) VALUES('review','native',1,'{}','{}','applying',1,2);
INSERT INTO network_dns_service_provisions(id,request_json,secret_enc,state,resources_json,created_at,expires_at) VALUES('owned','{}','sealed-bootstrap','applying','{"phase":"starting"}',1,2)`); err != nil {
				t.Fatal(err)
			}
			st.Close()
			st, err = Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			for range 2 {
				if err = InitializeNetworkDNSServices(t.Context(), st.DB); err != nil {
					t.Fatal(err)
				}
			}
			var management, generation int
			var ownership, secret, state, resources string
			if err = st.DB.QueryRow(`SELECT management,generation,ownership,secret_enc FROM network_dns_services WHERE id='native'`).Scan(&management, &generation, &ownership, &secret); err != nil || management != 0 || generation != 1 || ownership != "connected" || secret != "sealed-native" {
				t.Fatalf("connection defaults or sealed state changed: %d %d %s %s %v", management, generation, ownership, secret, err)
			}
			if err = st.DB.QueryRow(`SELECT state FROM network_dns_service_changes WHERE id='review'`).Scan(&state); err != nil || state != "applying" {
				t.Fatal("schema replayed a native change", state, err)
			}
			if err = st.DB.QueryRow(`SELECT state,resources_json,secret_enc FROM network_dns_service_provisions WHERE id='owned'`).Scan(&state, &resources, &secret); err != nil || state != "applying" || resources != `{"phase":"starting"}` || secret != "sealed-bootstrap" {
				t.Fatal("schema lost interrupted ownership or sealed bootstrap", state, resources, err)
			}
			if _, err = st.DB.Exec(`DELETE FROM network_dns_services WHERE id='native'`); err != nil {
				t.Fatal(err)
			}
			var changes, provisions int
			st.DB.QueryRow(`SELECT count(*) FROM network_dns_service_changes`).Scan(&changes)
			st.DB.QueryRow(`SELECT count(*) FROM network_dns_service_provisions`).Scan(&provisions)
			if changes != 0 || provisions != 1 {
				t.Fatal("connection cascade removed owned resource evidence or retained orphan changes", changes, provisions)
			}
		})
	}
}
