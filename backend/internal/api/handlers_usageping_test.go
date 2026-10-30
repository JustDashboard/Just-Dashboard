package api

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/usageping"
)

// usagePingServer is a test server whose count reports to a stand-in site.
func usagePingServer(t *testing.T, offBy string) (*Server, *atomic.Int32, *client) {
	t.Helper()
	s := testServer(t)
	var reports atomic.Int32
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reports.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(site.Close)
	s.modules.usagePing = usageping.New(usageping.Options{
		Store: s.Store, SiteURL: site.URL, Version: "0.8.2", OffBy: offBy,
	})
	admin := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "count-admin", auth.RoleAdmin)}
	return s, &reports, admin
}

func TestTheUsagePingRoutesFollowTheContractTheFrontendBuildsAgainst(t *testing.T) {
	s, reports, admin := usagePingServer(t, "")

	w := admin.do(http.MethodGet, "/api/v1/dashboard/usage-ping", "", nil)
	if w.Code != 200 {
		t.Fatalf("status: %d %s", w.Code, w.Body)
	}
	m := decodeMap(t, w.Body.String())
	if m["enabled"] != true || m["choice"] != "undecided" || m["nextBody"] == "" || m["siteUrl"] == "" {
		t.Fatalf("a server nobody has answered for reads as %s", w.Body)
	}
	if reports.Load() != 0 {
		t.Fatal("reading the status sent a report")
	}

	w = admin.do(http.MethodPut, "/api/v1/dashboard/usage-ping", `{}`, nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("an answer that says nothing: %d", w.Code)
	}

	w = admin.do(http.MethodPut, "/api/v1/dashboard/usage-ping", `{"on":false}`, nil)
	if w.Code != 200 || decodeMap(t, w.Body.String())["choice"] != "off" || reports.Load() != 0 {
		t.Fatalf("a no: %d %s, %d reports", w.Code, w.Body, reports.Load())
	}
	if len(linkAudit(t, s, "usage.ping.off")) != 1 {
		t.Fatal("a no was not audited")
	}

	// A yes answers with its first report already sent, so the page need not wait.
	w = admin.do(http.MethodPut, "/api/v1/dashboard/usage-ping", `{"on":true}`, nil)
	m = decodeMap(t, w.Body.String())
	if w.Code != 200 || m["choice"] != "on" || m["lastSentAt"] == nil || m["lastBody"] == nil {
		t.Fatalf("a yes: %d %s", w.Code, w.Body)
	}
	if reports.Load() != 1 {
		t.Fatalf("a yes sent %d reports", reports.Load())
	}
	if len(linkAudit(t, s, "usage.ping.on")) != 1 {
		t.Fatal("a yes was not audited")
	}
}

func TestOnlyAnAdministratorsSessionMayAnswerTheUsageCount(t *testing.T) {
	s, reports, _ := usagePingServer(t, "")
	for _, role := range []auth.Role{auth.RoleReadOnly, auth.RoleLimited} {
		c := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "count-"+string(role), role)}
		for _, method := range []string{http.MethodGet, http.MethodPut} {
			if w := c.do(method, "/api/v1/dashboard/usage-ping", `{"on":true}`, nil); w.Code != http.StatusForbidden {
				t.Errorf("%s as %s = %d", method, role, w.Code)
			}
		}
	}

	user, err := s.Auth.UserByID(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	_, secret, err := s.Auth.CreateAPIToken(t.Context(), user, "count", auth.RoleAdmin, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	key := &client{t: t, h: s.Routes()}
	bearer := map[string]string{"Authorization": "Bearer " + secret}
	w := key.do(http.MethodPut, "/api/v1/dashboard/usage-ping", `{"on":true}`, bearer)
	if w.Code != http.StatusForbidden || linkErrorCode(t, w.Body.String()) != "session_required" {
		t.Errorf("an answer with a key = %d %s", w.Code, w.Body)
	}
	if w := key.do(http.MethodGet, "/api/v1/dashboard/usage-ping", "", bearer); w.Code != 200 {
		t.Errorf("a key could not read the status: %d", w.Code)
	}
	if reports.Load() != 0 {
		t.Fatalf("%d reports without an administrator's yes", reports.Load())
	}
}

func TestTheEnvironmentSwitchRefusesAnAnswer(t *testing.T) {
	_, reports, admin := usagePingServer(t, "JD_USAGE_PING")

	w := admin.do(http.MethodGet, "/api/v1/dashboard/usage-ping", "", nil)
	m := decodeMap(t, w.Body.String())
	if m["enabled"] != false || m["offBy"] != "JD_USAGE_PING" {
		t.Fatalf("switched off reads as %s", w.Body)
	}
	w = admin.do(http.MethodPut, "/api/v1/dashboard/usage-ping", `{"on":true}`, nil)
	if w.Code != http.StatusForbidden || linkErrorCode(t, w.Body.String()) != "usage_ping_disabled" {
		t.Fatalf("an answer while switched off = %d %s", w.Code, w.Body)
	}
	if reports.Load() != 0 {
		t.Fatalf("%d reports while switched off", reports.Load())
	}
}

// JD_ACCOUNT_LINK=false tells an operator the server never contacts the site, so
// it has to turn this off too, and nobody on such a server gets asked.
func TestTurningOffLinkingTurnsOffTheCount(t *testing.T) {
	s := testServer(t)
	s.Cfg.AccountLink = false
	s.Cfg.UsagePing = true
	s.initUsagePing()
	st, err := s.modules.usagePing.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if st.Enabled || st.OffBy != "JD_ACCOUNT_LINK" {
		t.Fatalf("with linking off: %+v", st)
	}

	s.Cfg.AccountLink = true
	s.Cfg.UsagePing = true
	s.initUsagePing()
	if st, _ := s.modules.usagePing.Status(t.Context()); !st.Enabled {
		t.Fatalf("with both on: %+v", st)
	}
}

func TestAnAgentHasNoUsageCount(t *testing.T) {
	s := testServer(t)
	s.modules.usagePing = nil
	s.Cfg.AgentMode = true
	s.initUsagePing()
	if s.modules.usagePing != nil {
		t.Fatal("an agent made a usage count")
	}
}
