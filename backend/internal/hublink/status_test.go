package hublink

import (
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
	// Anyone else is told only that the hub is not reached.
	if r, _ := e.svc.Status(t.Context(), false); r.Admin != nil || r.Reachable == nil || *r.Reachable {
		t.Fatalf("reader status = %+v", r)
	}
}
