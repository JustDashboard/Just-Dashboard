package httpx

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

// Every entry a hub session causes says so, whichever writer records it: the
// mutation middleware, a read recorded before it runs, and a handler's own
// entry built from AuditEntry (a socket at open, an outcome after the
// response). The same account in a session of this server's own, and the
// assistant acting in either, read apart from it (hub-link-protocol.md
// section 9.10).
func TestAHubSessionIsAuditedApartFromTheSameAccountsOwnSession(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	log := audit.New(st, slog.New(slog.DiscardHandler))
	ion := &auth.User{ID: 3, Username: "ion", Role: auth.RoleAdmin}
	local := &Principal{User: ion, Role: auth.RoleAdmin, Kind: "session", SessionID: "here-1"}
	hub := &Principal{User: ion, Role: auth.RoleAdmin, Kind: "session", SessionID: "hub-1", HubSession: true, HubUser: "owner"}
	hubAssistant := *hub
	hubAssistant.Assistant = true
	localAssistant := *local
	localAssistant.Assistant = true

	write := AuditMutations(log)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		SetAudit(r, "thing.change", "it", nil)
		w.WriteHeader(http.StatusNoContent)
	}))
	as := func(method string, p *Principal) *http.Request {
		r := httptest.NewRequest(method, "/api/v1/thing", nil)
		return r.WithContext(WithPrincipal(r.Context(), p))
	}
	for _, p := range []*Principal{local, hub, &hubAssistant, &localAssistant} {
		write.ServeHTTP(httptest.NewRecorder(), as(http.MethodPost, p))
	}
	AuditRead(log, as(http.MethodGet, hub), "thing.read", "it")
	own := AuditEntry(as(http.MethodGet, hub))
	own.Action, own.Success = "thing.open", true
	log.Record(t.Context(), own)

	rows, _, err := log.List(t.Context(), audit.Filter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	type who struct{ actor, hubUser, session string }
	got := map[string][]who{}
	for _, r := range rows {
		if r.Username != "ion" {
			t.Errorf("%s was recorded as %q", r.Action, r.Username)
		}
		got[r.Action] = append(got[r.Action], who{r.Actor, r.HubUser, r.SessionID})
	}
	fromHub := who{"hub-session", "owner", "hub-1"}
	for _, action := range []string{"thing.read", "thing.open"} {
		if len(got[action]) != 1 || got[action][0] != fromHub {
			t.Errorf("%s = %+v, want %+v", action, got[action], fromHub)
		}
	}
	// List answers newest first.
	want := []who{
		{"assistant", "", ""},
		{"assistant", "owner", "hub-1"},
		fromHub,
		{"session", "", ""},
	}
	if changes := got["thing.change"]; len(changes) != len(want) {
		t.Fatalf("thing.change = %+v", changes)
	} else {
		for i := range want {
			if changes[i] != want[i] {
				t.Errorf("thing.change #%d = %+v, want %+v", len(want)-i, changes[i], want[i])
			}
		}
	}
}
