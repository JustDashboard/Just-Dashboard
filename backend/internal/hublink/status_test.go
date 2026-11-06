package hublink

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accountlink"
)

// The status has no field of its own for an idle link: its reason is the
// link's last error, in ErrURLRefused's words. Settings → Hub reads the reason
// back out of exactly this shape (idleReason in frontend/src/lib/hub-link.ts)
// to draw the link as idle, so a change to the words is a change to the page.
func TestAnIdleLinksLastErrorIsTheShapeThePageReads(t *testing.T) {
	e := newEnv(t)
	st := e.link(accountlink.ScopeObserve)
	st.Idle = &accountlink.Idle{Code: CodeURLRefused, Reason: "the hub's machine changed: a; b", At: time.Now().UTC()}
	if _, err := e.st.DB.Exec(`UPDATE settings SET value = ? WHERE key = 'hub.link.state'`, mustJSON(t, st)); err != nil {
		t.Fatal(err)
	}
	s, err := e.svc.Status(t.Context(), true)
	if err != nil {
		t.Fatal(err)
	}
	want := "this server no longer accepts the hub's address: the hub's machine changed: a; b; disconnect, then connect again"
	if s.State != StateLinked || s.Admin == nil || s.Admin.Link == nil || s.Admin.Link.LastError != want {
		t.Fatalf("status = %+v, link = %+v", s, s.Admin)
	}
	if s.Reachable == nil || *s.Reachable {
		t.Fatalf("an idle link reads as reachable: %v", s.Reachable)
	}
	// Anyone else is told that it is idle, so no page offers the hub's
	// address as a link, and not why.
	if r, _ := e.svc.Status(t.Context(), false); r.Admin != nil || r.Reachable == nil || *r.Reachable || !r.Idle {
		t.Fatalf("reader status = %+v", r)
	}
	if !s.Idle {
		t.Fatal("an idle link's status does not say idle")
	}
	// So is the shell, from the session, before its first paint (protocol 8.3).
	raw, _ := json.Marshal(e.svc.Session(t.Context()))
	if !strings.Contains(string(raw), `"reachable":false,"idle":true}`) {
		t.Fatalf("session hub = %s", raw)
	}
}

// A link in use says nothing about idling, and says the interval the hub set,
// as the heartbeat loop clamps it.
func TestALinkInUseIsNotIdleAndSaysItsInterval(t *testing.T) {
	e := newEnv(t)
	st := e.link(accountlink.ScopeObserve)
	for _, c := range []struct{ stored, want int }{{60, 60}, {0, 10}, {3600, 600}} {
		st.HeartbeatSeconds = c.stored
		if _, err := e.st.DB.Exec(`UPDATE settings SET value = ? WHERE key = 'hub.link.state'`, mustJSON(t, st)); err != nil {
			t.Fatal(err)
		}
		s, err := e.svc.Status(t.Context(), false)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(s)
		if s.HeartbeatSeconds != c.want || strings.Contains(string(raw), `"idle"`) {
			t.Fatalf("stored %d: status = %s", c.stored, raw)
		}
	}
	raw, _ := json.Marshal(e.svc.Session(t.Context()))
	if strings.Contains(string(raw), `"idle"`) {
		t.Fatalf("session hub = %s", raw)
	}
	// Nothing about an interval while nothing is linked.
	if _, err := e.st.DB.Exec(`DELETE FROM settings WHERE key = 'hub.link.state'`); err != nil {
		t.Fatal(err)
	}
	if s, _ := e.svc.Status(t.Context(), false); s.HeartbeatSeconds != 0 || s.Idle {
		t.Fatalf("unlinked status = %+v", s)
	}
}
