package hublink

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accountlink"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
)

// The link's own events end hub sign-in with it (hub-link-protocol.md
// sections 2.2, 8.9 and 9.3). The API's tests drive a redeem end to end; these
// hold the link side: idling, clearing, and a reply that lands too late.

// signInEnv is newEnv with hub sign-in on and an account service, a link to
// the fake hub, and one account with a grant and a hub session.
func newSignInLink(t *testing.T) (*env, *auth.Service, int64, string) {
	t.Helper()
	var svc *auth.Service
	e := newEnv(t, func(o *Options) {
		svc = auth.NewService(o.Store, o.Sealer, 12*time.Hour, time.Hour, false)
		o.Auth, o.SignIn = svc, true
	})
	st := e.link(accountlink.ScopeObserve)
	u, err := svc.CreateUser(t.Context(), "ion", "Correct-Horse-Battery-9", auth.RoleAdmin, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.DB.Exec(`INSERT INTO hub_signin_grants(user_id, hub_user, server_id, created_at) VALUES(?, 'owner', ?, ?)`,
		u.ID, st.ServerID, time.Now().Add(-time.Minute).Unix()); err != nil {
		t.Fatal(err)
	}
	// The connector has applied the rule to this link, as it does at start.
	if err := e.svc.admit(t.Context(), st); err != nil {
		t.Fatal(err)
	}
	var token string
	if err := e.svc.writeTx(t.Context(), func(tx *sql.Tx) error {
		start, err := svc.StartHubSessionTx(t.Context(), tx, u.ID, "owner", "127.0.0.1", "test", "")
		if start != nil {
			token = start.Token
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.ResolveSession(t.Context(), token); err != nil {
		t.Fatalf("the hub session does not resolve before the event: %v", err)
	}
	return e, svc, u.ID, token
}

func hubSessions(t *testing.T, e *env) int {
	t.Helper()
	var n int
	if err := e.st.DB.QueryRow(`SELECT COUNT(*) FROM sessions WHERE auth_method = 'hub'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestTheLinkGoingIdleRevokesEveryHubSessionAndKeepsTheGrants(t *testing.T) {
	e, svc, _, token := newSignInLink(t)
	st, _, _ := e.svc.Linked(t.Context())
	st.URL = "https://hub.tailed39ba.ts.net"
	if _, err := e.st.DB.Exec(`UPDATE settings SET value = ? WHERE key = 'hub.link.state'`, mustJSON(t, st)); err != nil {
		t.Fatal(err)
	}
	e.tailnet.set(func(s *TailnetStatus) { s.Suffix = "other.ts.net"; s.Self = "box.other.ts.net" })
	if err := e.svc.Sync(t.Context()); err == nil {
		t.Fatal("an idle link synced")
	}
	if n := hubSessions(t, e); n != 0 {
		t.Fatalf("%d hub sessions survived the link going idle", n)
	}
	if _, _, err := svc.ResolveSession(t.Context(), token); err == nil {
		t.Fatal("a hub session resolved on an idle link")
	}
	var grants int
	e.st.DB.QueryRow(`SELECT COUNT(*) FROM hub_signin_grants`).Scan(&grants)
	if grants != 1 {
		t.Fatalf("grants = %d; they stay, unusable, until a person disconnects", grants)
	}
	rows := e.auditRows("hub.link.idle")
	if len(rows) != 1 || !strings.Contains(rows[0].Detail, `"sessionsRevoked":1`) {
		t.Fatalf("idle audit = %+v", rows)
	}
}

func TestEndingTheLinkDeletesGrantsTicketsAndHubSessions(t *testing.T) {
	e, svc, userID, token := newSignInLink(t)
	if _, err := e.st.DB.Exec(`INSERT INTO hub_signin_tickets(nonce, user_id, used_at, expires_at) VALUES('AAECAwQFBgcICQoLDA0ODw', ?, 1, 2)`, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Disconnect(t.Context()); err != nil {
		t.Fatal(err)
	}
	var grants, tickets int
	e.st.DB.QueryRow(`SELECT COUNT(*) FROM hub_signin_grants`).Scan(&grants)
	e.st.DB.QueryRow(`SELECT COUNT(*) FROM hub_signin_tickets`).Scan(&tickets)
	if grants != 0 || tickets != 0 || hubSessions(t, e) != 0 {
		t.Fatalf("after disconnect: %d grants, %d tickets, %d hub sessions", grants, tickets, hubSessions(t, e))
	}
	if _, _, err := svc.ResolveSession(t.Context(), token); err == nil {
		t.Fatal("a hub session resolved after a disconnect")
	}
	rows := e.auditRows("hub.signin.revoke")
	if len(rows) != 1 || !strings.Contains(rows[0].Detail, `"reason":"link_ended"`) ||
		!strings.Contains(rows[0].Detail, `"sessionsRevoked":1`) || rows[0].Actor != "system" {
		t.Fatalf("revoke audit = %+v", rows)
	}
}

// A heartbeat reply that lands after a disconnect writes nothing: no view for
// the old hub, and no grant ended or kept on its word (section 8.9).
func TestAReplyForALinkThatIsGoneWritesNothing(t *testing.T) {
	e, _, _, _ := newSignInLink(t)
	e.hub.setReply("signIn", map[string]any{"users": []string{"owner"}})
	arrived, release := e.hub.holdNextHeartbeat()
	done := make(chan error, 1)
	go func() { done <- e.svc.Sync(context.WithoutCancel(t.Context())) }()
	<-arrived
	if _, err := e.svc.Disconnect(t.Context()); err != nil {
		t.Fatal(err)
	}
	release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := e.st.Setting(t.Context(), keyView); ok {
		t.Fatal("a late reply brought the old hub's view back")
	}
}

// With hub sign-in on, every heartbeat names the grants for this link; off,
// the key is absent (section 9.3).
func TestTheHeartbeatNamesTheGrantsOnlyWhileSignInIsOn(t *testing.T) {
	e, _, _, _ := newSignInLink(t)
	e.hub.setReply("signIn", map[string]any{"users": []string{"owner"}})
	if err := e.svc.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	bodies := e.hub.heartbeatBodies()
	last := string(bodies[len(bodies)-1])
	if !strings.Contains(last, `"signIn":{"grants":[{"userId":1,"username":"ion","hubUser":"owner"}]}`) {
		t.Fatalf("heartbeat = %s", last)
	}
	e.svc.signIn = false
	e.advance(time.Minute)
	if err := e.svc.Sync(context.WithoutCancel(t.Context())); err != nil {
		t.Fatal(err)
	}
	bodies = e.hub.heartbeatBodies()
	if last := string(bodies[len(bodies)-1]); strings.Contains(last, `"signIn"`) {
		t.Fatalf("sign-in off still sends signIn: %s", last)
	}
}

// Another machine at the hub's name sets the link idle and revokes every hub
// session in that same write transaction (hub-link-protocol.md 2.4, 9.3).
func TestAnotherMachineAtTheHubsNameRevokesEveryHubSession(t *testing.T) {
	var svc *auth.Service
	e := newTailnetEnv(t, func(o *Options) {
		svc = auth.NewService(o.Store, o.Sealer, 12*time.Hour, time.Hour, false)
		o.Auth, o.SignIn = svc, true
	})
	st := e.link(accountlink.ScopeObserve)
	u, err := svc.CreateUser(t.Context(), "ion", "Correct-Horse-Battery-9", auth.RoleAdmin, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.DB.Exec(`INSERT INTO hub_signin_grants(user_id, hub_user, server_id, created_at) VALUES(?, 'owner', ?, ?)`,
		u.ID, st.ServerID, time.Now().Add(-time.Minute).Unix()); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.admit(t.Context(), st); err != nil {
		t.Fatal(err)
	}
	var token string
	if err := e.svc.writeTx(t.Context(), func(tx *sql.Tx) error {
		start, err := svc.StartHubSessionTx(t.Context(), tx, u.ID, "owner", "127.0.0.1", "test", "")
		if start != nil {
			token = start.Token
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.ResolveSession(t.Context(), token); err != nil {
		t.Fatalf("the hub session does not resolve before the change: %v", err)
	}

	e.whois.set(func(w *fakeWhoIs) { w.id = "nImpostor" })
	e.svc.gate.forget()
	if err := e.svc.Sync(t.Context()); err == nil {
		t.Fatal("a link to another machine synced")
	}
	if n := hubSessions(t, e); n != 0 {
		t.Fatalf("%d hub sessions survived the hub's machine changing", n)
	}
	if _, _, err := svc.ResolveSession(t.Context(), token); err == nil {
		t.Fatal("a hub session resolved on a link to another machine")
	}
	if rows := e.auditRows("hub.link.node_changed"); len(rows) != 1 || !strings.Contains(rows[0].Detail, `"sessionsRevoked":1`) {
		t.Fatalf("node_changed audit = %+v", rows)
	}
	if rows := e.auditRows("hub.session.revoke"); len(rows) != 1 || !strings.Contains(rows[0].Detail, `"reason":"link_idle"`) {
		t.Fatalf("session revoke audit = %+v", rows)
	}
}

// A key announced in a heartbeat reply is never pinned, and a pinned key is
// never ended by one (hub-link-protocol.md 4.3): a machine that answers at the
// hub's name could otherwise sign commands and sign-in tickets of its own.
func TestKeysAnnouncedInAReplyAreNeverPinned(t *testing.T) {
	e, _, _, _ := newSignInLink(t)
	before, _, _ := e.svc.Linked(t.Context())
	other := seedKey(t, vectorOtherSeed)
	e.hub.setReply("siteKeys", []map[string]any{
		{"kid": "hub-2027-01", "publicKey": pubB64(other)},
		{"kid": vectorKid, "publicKey": before.SiteKeys[0].PublicKey, "notAfter": vectorNow - 60},
	})
	if err := e.svc.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	after, _, _ := e.svc.Linked(t.Context())
	if mustJSON(t, after.SiteKeys) != mustJSON(t, before.SiteKeys) {
		t.Fatalf("pinned keys changed: %s -> %s", mustJSON(t, before.SiteKeys), mustJSON(t, after.SiteKeys))
	}
	if _, _, _, err := checkTicket("v1.hub-2027-01.e30.c2ln", ticketCheck{
		Keys: after.SiteKeys, Hub: after.URL, ServerID: after.ServerID, Now: e.clock(),
	}); err == nil || err.Code != codeBadTicket {
		t.Fatalf("a ticket under the announced key = %v", err)
	}
}
