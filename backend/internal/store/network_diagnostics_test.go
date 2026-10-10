package store

import "testing"

func TestOpenAddsDiagnosticRecordsToExistingInstall(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`DROP TABLE network_diagnostic_runs; INSERT INTO settings(key,value) VALUES('existing-setting','preserved')`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var count int
	if err := s.DB.QueryRow(`SELECT count(*) FROM network_diagnostic_runs`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("new table=%d, %v", count, err)
	}
	var value string
	if err := s.DB.QueryRow(`SELECT value FROM settings WHERE key='existing-setting'`).Scan(&value); err != nil || value != "preserved" {
		t.Fatalf("existing setting=%q, %v", value, err)
	}
}
