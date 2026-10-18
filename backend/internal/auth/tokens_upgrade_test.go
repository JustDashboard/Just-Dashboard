package auth

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

// A key made before keys could be scoped has no scope column. After the
// upgrade it must read as unscoped and keep working as the ordinary key it was,
// not as a key narrowed to nothing or widened to anything else.
func TestKeyMadeBeforeScopesStillWorksAsAnOrdinaryKey(t *testing.T) {
	dir := t.TempDir()
	old, err := sql.Open("sqlite", filepath.Join(dir, store.DatabaseFile))
	if err != nil {
		t.Fatal(err)
	}
	secret := tokenPrefix + strings.Repeat("a", 43)
	_, err = old.Exec(`CREATE TABLE users (
		id INTEGER PRIMARY KEY AUTOINCREMENT, username TEXT NOT NULL UNIQUE,
		password_hash TEXT NOT NULL, role TEXT NOT NULL,
		totp_secret TEXT NOT NULL DEFAULT '', totp_enabled INTEGER NOT NULL DEFAULT 0,
		disabled INTEGER NOT NULL DEFAULT 0, must_change_pw INTEGER NOT NULL DEFAULT 0,
		failed_count INTEGER NOT NULL DEFAULT 0, locked_until INTEGER NOT NULL DEFAULT 0,
		last_login_at INTEGER NOT NULL DEFAULT 0, created_at INTEGER NOT NULL
	);
	INSERT INTO users(id, username, password_hash, role, created_at) VALUES (1, 'owner', 'hash', 'admin', 1);
	CREATE TABLE api_tokens (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		name TEXT NOT NULL, prefix TEXT NOT NULL, token_hash TEXT NOT NULL UNIQUE,
		role TEXT NOT NULL, created_at INTEGER NOT NULL,
		expires_at INTEGER NOT NULL DEFAULT 0, last_used_at INTEGER NOT NULL DEFAULT 0,
		revoked INTEGER NOT NULL DEFAULT 0
	);`)
	if err == nil {
		_, err = old.Exec(`INSERT INTO api_tokens(user_id, name, prefix, token_hash, role, created_at)
			VALUES (1, 'github-actions', ?, ?, 'limited', 1)`, secret[:13], HashToken(secret))
	}
	if err != nil {
		old.Close()
		t.Fatal(err)
	}
	old.Close()

	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("Open on a database from before key scopes: %v", err)
	}
	defer st.Close()
	sealer, err := NewSealer(strings.Repeat("ab", 32))
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(st, sealer, time.Hour, time.Minute, false)

	token, _, role, err := svc.ResolveAPIToken(t.Context(), secret)
	if err != nil {
		t.Fatalf("the existing key stopped working: %v", err)
	}
	if token.Scope != "" || role != RoleLimited {
		t.Fatalf("existing key resolved as scope %q, role %q", token.Scope, role)
	}
	listed, err := svc.ListAPITokens(t.Context(), 1, true)
	if err != nil || len(listed) != 1 || listed[0].Scope != "" {
		t.Fatalf("listing after upgrade: %+v, %v", listed, err)
	}
}
