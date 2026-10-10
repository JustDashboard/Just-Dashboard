package store

import (
	"testing"
)

func TestNetworkDNSEvidenceSchemaIsAdditiveAndIdempotent(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	if _, err = s.DB.Exec(`INSERT INTO settings(key,value) VALUES('dns.fixture','preserved')`); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = InitializeNetworkDNSEvidence(t.Context(), s.DB); err != nil {
			t.Fatal(err)
		}
	}
	var value string
	if err = s.DB.QueryRow(`SELECT value FROM settings WHERE key='dns.fixture'`).Scan(&value); err != nil || value != "preserved" {
		t.Fatalf("schema changed existing data %s %v", value, err)
	}
}
