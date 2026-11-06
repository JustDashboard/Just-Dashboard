package auth

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/store/storetest"
)

// A hub session idles out after an hour without use even where the install
// lets its own sessions idle far longer (hub-link-protocol.md section 9.9: 60
// minutes idle), and checking a socket's session does not count as use.
func TestAHubSessionIdlesOutAfterAnHourWhateverTheInstallAllows(t *testing.T) {
	st, err := storetest.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	sealer, err := NewSealer(strings.Repeat("ab", 32))
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(st, sealer, 24*time.Hour, 12*time.Hour, false)
	svc.SetHubSessionCheck(func(context.Context, *Session) bool { return true })
	user, err := svc.CreateUser(t.Context(), "tester", testPassword, RoleAdmin, false)
	if err != nil {
		t.Fatal(err)
	}
	login, err := svc.Login(t.Context(), user.Username, testPassword, "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := st.DB.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	hub, err := svc.StartHubSessionTx(t.Context(), tx, user.ID, "owner", "127.0.0.1", "test", HubReplace{})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	// Thirty-one minutes without use: both stand. A check of the hub
	// session's socket leaves its idle time where it was.
	idleFor := func(d time.Duration) {
		t.Helper()
		if _, err := st.DB.Exec(`UPDATE sessions SET last_seen_at = ?`, time.Now().Add(-d).Unix()); err != nil {
			t.Fatal(err)
		}
	}
	idleFor(31 * time.Minute)
	if _, _, err := svc.CheckSession(t.Context(), hub.Token); err != nil {
		t.Fatalf("a hub session idle for 31 minutes = %v", err)
	}
	var seen int64
	st.DB.QueryRow(`SELECT last_seen_at FROM sessions WHERE id = ?`, hub.Session.ID).Scan(&seen)
	if time.Since(time.Unix(seen, 0)) < 30*time.Minute {
		t.Fatal("checking a socket's session counted as using it")
	}

	idleFor(61 * time.Minute)
	if _, _, err := svc.ResolveSession(t.Context(), hub.Token); err == nil {
		t.Fatal("a hub session idle for 61 minutes still resolves")
	}
	if _, _, err := svc.ResolveSession(t.Context(), login.Token); err != nil {
		t.Fatalf("a password session idle for 61 minutes, under a 12-hour idle limit = %v", err)
	}
}
