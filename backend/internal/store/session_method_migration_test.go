package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestUpgradePreservesSessionAccessWithoutInventingFactorProof(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, DatabaseFile))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE sessions (
		id TEXT PRIMARY KEY, user_id INTEGER NOT NULL, token_hash TEXT NOT NULL UNIQUE,
		twofa_passed INTEGER NOT NULL DEFAULT 0, ip TEXT NOT NULL DEFAULT '',
		user_agent TEXT NOT NULL DEFAULT '', created_at INTEGER NOT NULL,
		last_seen_at INTEGER NOT NULL, expires_at INTEGER NOT NULL
	);
	INSERT INTO sessions(id, user_id, token_hash, twofa_passed, created_at, last_seen_at, expires_at)
	VALUES ('complete', 1, 'hash', 1, 1, 1, 9999999999), ('pending', 1, 'pending-hash', 0, 1, 1, 9999999999)`)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		st, err := Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		for id, completed := range map[string]int{"complete": 1, "pending": 0} {
			var method string
			var passed int
			err := st.DB.QueryRow(`SELECT auth_method, twofa_passed FROM sessions WHERE id = ?`, id).Scan(&method, &passed)
			if err != nil || method != "unknown" || passed != completed {
				t.Fatalf("upgrade changed authentication evidence: method=%q passed=%d err=%v", method, passed, err)
			}
		}
		st.Close()
	}
}
