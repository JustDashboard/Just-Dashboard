package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestUpgradeAddsReplayCounterWithoutChangingEnrollment(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, DatabaseFile))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE users (
		id INTEGER PRIMARY KEY AUTOINCREMENT, username TEXT NOT NULL UNIQUE,
		password_hash TEXT NOT NULL, role TEXT NOT NULL,
		totp_secret TEXT NOT NULL DEFAULT '', totp_enabled INTEGER NOT NULL DEFAULT 0,
		disabled INTEGER NOT NULL DEFAULT 0, must_change_pw INTEGER NOT NULL DEFAULT 0,
		failed_count INTEGER NOT NULL DEFAULT 0, locked_until INTEGER NOT NULL DEFAULT 0,
		last_login_at INTEGER NOT NULL DEFAULT 0, created_at INTEGER NOT NULL
	);
	INSERT INTO users(username, password_hash, role, totp_secret, totp_enabled, created_at)
	VALUES ('existing', 'existing-hash', 'admin', 'existing-sealed-secret', 1, 1)`)
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
		var hash, secret string
		var enabled, step int64
		err = st.DB.QueryRow(`SELECT password_hash, totp_secret, totp_enabled, totp_last_step
			FROM users WHERE username = 'existing'`).Scan(&hash, &secret, &enabled, &step)
		st.Close()
		if err != nil || hash != "existing-hash" || secret != "existing-sealed-secret" || enabled != 1 || step != -1 {
			t.Fatalf("existing enrollment changed during migration: enabled=%d step=%d err=%v", enabled, step, err)
		}
	}
}

// The first-run question arrived after accounts did. Every account that
// existed before it is already past it, with nothing hidden, so an upgrade
// leaves every sidebar as it was.
func TestUpgradeTreatsExistingAccountsAsPastOnboarding(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, DatabaseFile))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE users (
		id INTEGER PRIMARY KEY AUTOINCREMENT, username TEXT NOT NULL UNIQUE,
		password_hash TEXT NOT NULL, role TEXT NOT NULL,
		totp_secret TEXT NOT NULL DEFAULT '', totp_enabled INTEGER NOT NULL DEFAULT 0,
		disabled INTEGER NOT NULL DEFAULT 0, must_change_pw INTEGER NOT NULL DEFAULT 0,
		failed_count INTEGER NOT NULL DEFAULT 0, locked_until INTEGER NOT NULL DEFAULT 0,
		last_login_at INTEGER NOT NULL DEFAULT 0, created_at INTEGER NOT NULL
	);
	INSERT INTO users(username, password_hash, role, created_at) VALUES ('existing', 'hash', 'admin', 1)`)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var onboarding int
	var hidden string
	if err := st.DB.QueryRow(`SELECT needs_onboarding, hidden_sections FROM users WHERE username = 'existing'`).Scan(&onboarding, &hidden); err != nil {
		t.Fatal(err)
	}
	if onboarding != 0 || hidden != "[]" {
		t.Fatalf("existing account after upgrade: needs_onboarding=%d hidden_sections=%q", onboarding, hidden)
	}
}

// Every key made before MCP keys existed opens the whole API its role allows,
// as it did, and reaches no database through the MCP endpoint: the scope
// arrives empty, and api_token_connections has nothing for it.
func TestUpgradeKeepsExistingKeysForTheWholeAPI(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, DatabaseFile))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE api_tokens (
		id INTEGER PRIMARY KEY AUTOINCREMENT, user_id INTEGER NOT NULL, name TEXT NOT NULL,
		prefix TEXT NOT NULL, token_hash TEXT NOT NULL UNIQUE, role TEXT NOT NULL,
		created_at INTEGER NOT NULL, expires_at INTEGER NOT NULL DEFAULT 0,
		last_used_at INTEGER NOT NULL DEFAULT 0, revoked INTEGER NOT NULL DEFAULT 0
	);
	INSERT INTO api_tokens(user_id, name, prefix, token_hash, role, created_at)
	VALUES (1, 'github-actions', 'vpsd_ab12cd34', 'existing-hash', 'limited', 1)`)
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
		var name, hash, role, scope string
		var grants int
		err = st.DB.QueryRow(`SELECT name, token_hash, role, scope FROM api_tokens WHERE id = 1`).
			Scan(&name, &hash, &role, &scope)
		if err == nil {
			err = st.DB.QueryRow(`SELECT COUNT(*) FROM api_token_connections`).Scan(&grants)
		}
		st.Close()
		if err != nil || name != "github-actions" || hash != "existing-hash" || role != "limited" || scope != "" || grants != 0 {
			t.Fatalf("existing key after upgrade: %q %q %q scope=%q grants=%d err=%v", name, hash, role, scope, grants, err)
		}
	}
}
