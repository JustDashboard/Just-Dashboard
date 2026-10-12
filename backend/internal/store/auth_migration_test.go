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
