package api

import (
	"context"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accountlink"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/hublink"
	"github.com/Wayy01/Just-Dashboard/backend/internal/hublink/hubstub"
	"github.com/Wayy01/Just-Dashboard/backend/internal/tailnetgate"
)

// stubTailnet is a server on the tailnet tailed39ba.ts.net named box, so the
// URL rule never asks the real host's tailscale.
type stubTailnet struct{}

func (stubTailnet) Status(context.Context) (hublink.TailnetStatus, error) {
	return hublink.TailnetStatus{Running: true, Suffix: "tailed39ba.ts.net", Self: "box.tailed39ba.ts.net", Peers: map[string]netip.Addr{}}, nil
}

type stubWhoIs struct{}

func (stubWhoIs) WhoIs(context.Context, netip.Addr) (tailnetgate.Peer, error) {
	return tailnetgate.Peer{}, tailnetgate.ErrUnavailable
}

// hubServer is a test server whose hub link reaches a stand-in hub at the test
// origin.
func hubServer(t *testing.T, enabled bool) (*Server, *hubstub.Hub, *client) {
	t.Helper()
	s := testServer(t)
	hub := hubstub.New(t)
	origin, err := hublink.ParseTestOrigin(hub.URL)
	if err != nil {
		t.Fatal(err)
	}
	s.modules.hubLink = hublink.New(hublink.Options{
		Store: s.Store, Sealer: s.Sealer, Audit: s.Audit, Enabled: enabled, TestOrigin: origin,
		Host: newAccountHost(s), Version: "test", Tailnet: stubTailnet{}, WhoIs: stubWhoIs{},
	})
	admin := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "hub-admin", auth.RoleAdmin)}
	return s, hub, admin
}

func hubSession(t *testing.T, c *client) map[string]any {
	t.Helper()
	w := c.do(http.MethodGet, "/api/v1/auth/session", "", nil)
	if w.Code != 200 {
		t.Fatalf("session = %d %s", w.Code, w.Body)
	}
	hub, _ := decodeMap(t, w.Body.String())["hub"].(map[string]any)
	return hub
}

func hello(t *testing.T, s *Server) (int, string, string) {
	t.Helper()
	w := (&client{t: t, h: s.Routes()}).do(http.MethodGet, "/api/v1/hub/hello", "", nil)
	return w.Code, w.Body.String(), w.Header().Get("Cache-Control")
}

func TestTheHubRoutesFollowTheContractTheFrontendBuildsAgainst(t *testing.T) {
	s, hub, admin := hubServer(t, true)

	// Unlinked.
	w := admin.do(http.MethodGet, "/api/v1/dashboard/hub", "", nil)
	m := decodeMap(t, w.Body.String())
	if w.Code != 200 || keysOf(m) != "admin,enabled,signIn,state" || m["state"] != "unlinked" || m["enabled"] != true {
		t.Fatalf("unlinked status = %d %s", w.Code, w.Body)
	}
	if a := m["admin"].(map[string]any); keysOf(a) != "testOrigin" || a["testOrigin"] != hub.URL {
		t.Fatalf("unlinked admin = %v", a)
	}
	if sign := m["signIn"].(map[string]any); keysOf(sign) != "enabled,offered,users" || sign["enabled"] != false {
		t.Fatalf("signIn = %v", sign)
	}
	if code, body, cache := hello(t, s); code != 200 || body != `{"product":"just-dashboard","protocol":1,"hub":"available"}` || cache != "no-store" {
		t.Fatalf("hello = %d %s %q", code, body, cache)
	}
	if hubSession(t, admin) != nil {
		t.Fatal("the session carries hub before any link")
	}

	// Connect: pending, with the request the page shows.
	w = admin.do(http.MethodPost, "/api/v1/dashboard/hub", `{"url":"`+hub.URL+`","scope":"operate"}`, nil)
	if w.Code != 200 {
		t.Fatalf("connect = %d %s", w.Code, w.Body)
	}
	m = decodeMap(t, w.Body.String())
	a := m["admin"].(map[string]any)
	req, _ := a["request"].(map[string]any)
	if keysOf(m) != "admin,enabled,hubUrl,signIn,state" || m["state"] != "pending" || m["hubUrl"] != hub.URL ||
		keysOf(a) != "fingerprint,request,testOrigin" || keysOf(req) != "approveUrl,expiresAt,hubUrl,scope,userCode" ||
		req["userCode"] != "K7QM-29XD" || req["scope"] != "operate" {
		t.Fatalf("pending status = %s", w.Body)
	}
	if _, err := time.Parse(time.RFC3339, req["expiresAt"].(string)); err != nil {
		t.Fatalf("expiresAt is not RFC 3339: %v", err)
	}
	if fp, _ := a["fingerprint"].(string); len(fp) != 19 || strings.Count(fp, ":") != 3 {
		t.Fatalf("fingerprint = %q", fp)
	}
	if _, body, _ := hello(t, s); !strings.Contains(body, `"hub":"pending"`) {
		t.Fatalf("hello while pending = %s", body)
	}
	if body := hub.LastRequest(); body["token"] != nil || body["scope"] != "operate" {
		t.Fatalf("request body = %v", body)
	}
	if w := admin.do(http.MethodPost, "/api/v1/dashboard/hub", `{"url":"`+hub.URL+`","scope":"observe"}`, nil); w.Code != 409 || linkErrorCode(t, w.Body.String()) != "link_pending" {
		t.Fatalf("second connect = %d %s", w.Code, w.Body)
	}

	// Cancel: 204, and a new request can be made.
	if w := admin.do(http.MethodDelete, "/api/v1/dashboard/hub/request", "", nil); w.Code != 204 {
		t.Fatalf("cancel = %d %s", w.Code, w.Body)
	}
	if w := admin.do(http.MethodPost, "/api/v1/dashboard/hub", `{"url":"`+hub.URL+`","scope":"maintain"}`, nil); w.Code != 200 {
		t.Fatalf("connect after cancel = %d %s", w.Code, w.Body)
	}

	// The owner approves in the hub; the dashboard collects the approval.
	hub.Approve()
	if res, err := s.modules.hubLink.Poll(t.Context()); err != nil || res.State != accountlink.PollLinked {
		t.Fatalf("poll = %+v, %v", res, err)
	}
	w = admin.do(http.MethodGet, "/api/v1/dashboard/hub", "", nil)
	m = decodeMap(t, w.Body.String())
	link, _ := m["admin"].(map[string]any)["link"].(map[string]any)
	if m["state"] != "linked" || keysOf(m) != "admin,enabled,fleet,hubName,hubUrl,notices,reachable,scope,signIn,state" ||
		m["hubName"] != "hub" || m["scope"] != "maintain" || m["reachable"] != false ||
		len(m["fleet"].([]any)) != 0 || len(m["notices"].([]any)) != 0 ||
		keysOf(link) != "hubKeys,linkedAt,linkedBy,serverId" || link["linkedBy"] != "hub-admin" || link["serverId"] != hubstub.ServerID {
		t.Fatalf("linked status = %s", w.Body)
	}
	if w := admin.do(http.MethodPost, "/api/v1/dashboard/hub", `{"url":"`+hub.URL+`","scope":"observe"}`, nil); w.Code != 409 || linkErrorCode(t, w.Body.String()) != "already_linked" {
		t.Fatalf("connect while linked = %d %s", w.Code, w.Body)
	}

	// Sync: a heartbeat now, and the fleet and notices it brought back.
	w = admin.do(http.MethodPost, "/api/v1/dashboard/hub/sync", "", nil)
	if w.Code != 200 {
		t.Fatalf("sync = %d %s", w.Code, w.Body)
	}
	m = decodeMap(t, w.Body.String())
	fleet, notices := m["fleet"].([]any), m["notices"].([]any)
	if m["reachable"] != true || len(fleet) != 1 || len(notices) != 1 {
		t.Fatalf("after sync = %s", w.Body)
	}
	if f := fleet[0].(map[string]any); keysOf(f) != "endpoint,lastSeenAt,name,self,serverId,state,version" || f["self"] != true {
		t.Fatalf("fleet entry = %v", f)
	}
	if n := notices[0].(map[string]any); keysOf(n) != "at,body,href,id,level,title" || n["href"] != hub.URL+"/updates" {
		t.Fatalf("notice = %v", n)
	}
	for _, at := range []string{m["lastHeartbeatAt"].(string), fleet[0].(map[string]any)["lastSeenAt"].(string), notices[0].(map[string]any)["at"].(string)} {
		if _, err := time.Parse(time.RFC3339, at); err != nil {
			t.Fatalf("%q is not RFC 3339", at)
		}
	}
	snap, _ := m["admin"].(map[string]any)["link"].(map[string]any)["lastSnapshot"].(map[string]any)
	if snap["protocol"] != float64(1) || snap["server"].(map[string]any)["scope"] != "maintain" {
		t.Fatalf("lastSnapshot = %v", snap)
	}

	// The session tells the shell, and the probe says linked.
	if h := hubSession(t, admin); keysOf(h) != "linked,name,reachable,url" || h["linked"] != true || h["name"] != "hub" || h["url"] != hub.URL || h["reachable"] != true {
		t.Fatalf("session hub = %v", h)
	}
	if _, body, _ := hello(t, s); !strings.Contains(body, `"hub":"linked"`) {
		t.Fatalf("hello while linked = %s", body)
	}

	// Scope.
	w = admin.do(http.MethodPut, "/api/v1/dashboard/hub/scope", `{"scope":"observe"}`, nil)
	if w.Code != 200 || decodeMap(t, w.Body.String())["scope"] != "observe" {
		t.Fatalf("scope = %d %s", w.Code, w.Body)
	}
	if w := admin.do(http.MethodPut, "/api/v1/dashboard/hub/scope", `{"scope":"root"}`, nil); w.Code != 400 || linkErrorCode(t, w.Body.String()) != "bad_request" {
		t.Fatalf("bad scope = %d", w.Code)
	}

	// Disconnect: 204, the hub is told, then not linked.
	if w := admin.do(http.MethodDelete, "/api/v1/dashboard/hub", "", nil); w.Code != 204 {
		t.Fatalf("disconnect = %d %s", w.Code, w.Body)
	}
	if _, _, _, unlinks := hub.Count(); unlinks != 1 {
		t.Fatalf("the hub was told %d times", unlinks)
	}
	for _, c := range []struct{ method, path, body string }{
		{http.MethodDelete, "/api/v1/dashboard/hub", ""},
		{http.MethodPut, "/api/v1/dashboard/hub/scope", `{"scope":"observe"}`},
		{http.MethodPost, "/api/v1/dashboard/hub/sync", ""},
	} {
		if w := admin.do(c.method, c.path, c.body, nil); w.Code != 409 || linkErrorCode(t, w.Body.String()) != "not_linked" {
			t.Fatalf("%s %s unlinked = %d %s", c.method, c.path, w.Code, w.Body)
		}
	}
	if hubSession(t, admin) != nil {
		t.Fatal("the session still carries hub after the disconnect")
	}

	// Every change is in the audit log, with who made it.
	for _, action := range []string{"hub.link.begin", "hub.link.cancel", "hub.link.scope", "hub.unlink", "hub.link"} {
		rows := linkAudit(t, s, action)
		if len(rows) == 0 || rows[len(rows)-1].Username != "hub-admin" {
			t.Errorf("%s audit = %+v", action, rows)
		}
	}
	if rows := linkAudit(t, s, "hub.unlink"); !strings.Contains(rows[len(rows)-1].Detail, `"hubNotified":true`) {
		t.Errorf("hub.unlink detail = %s", rows[len(rows)-1].Detail)
	}
}

func TestOnlyAnAdministratorWithASessionMayChangeTheHubLink(t *testing.T) {
	s, hub, admin := hubServer(t, true)
	admin.do(http.MethodPost, "/api/v1/dashboard/hub", `{"url":"`+hub.URL+`","scope":"observe"}`, nil)
	hub.Approve()
	if _, err := s.modules.hubLink.Poll(t.Context()); err != nil {
		t.Fatal(err)
	}
	changes := []struct{ method, path, body string }{
		{http.MethodPost, "/api/v1/dashboard/hub", `{"url":"hub.tailed39ba.ts.net","scope":"observe"}`},
		{http.MethodDelete, "/api/v1/dashboard/hub/request", ""},
		{http.MethodPut, "/api/v1/dashboard/hub/scope", `{"scope":"maintain"}`},
		{http.MethodDelete, "/api/v1/dashboard/hub", ""},
		{http.MethodPost, "/api/v1/dashboard/hub/sync", ""},
	}
	for _, role := range []auth.Role{auth.RoleReadOnly, auth.RoleLimited} {
		c := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "hub-"+string(role), role)}
		for _, rt := range changes {
			if w := c.do(rt.method, rt.path, rt.body, nil); w.Code != http.StatusForbidden {
				t.Errorf("%s %s as %s = %d", rt.method, rt.path, role, w.Code)
			}
		}
		// Every role reads the status, the fleet and the session's hub, and
		// none but an administrator is sent the admin part.
		w := c.do(http.MethodGet, "/api/v1/dashboard/hub", "", nil)
		m := decodeMap(t, w.Body.String())
		if w.Code != 200 || m["state"] != "linked" || m["admin"] != nil || m["fleet"] == nil {
			t.Errorf("status as %s = %d %s", role, w.Code, w.Body)
		}
		if h := hubSession(t, c); h == nil || h["name"] != "hub" {
			t.Errorf("session hub as %s = %v", role, h)
		}
	}

	// An API key, even an administrator's, cannot hand the server to a hub,
	// re-scope the link or take it back. It can read the status and sync.
	user, err := s.Auth.UserByID(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	_, secret, err := s.Auth.CreateAPIToken(t.Context(), user, "hub", auth.RoleAdmin, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	key := &client{t: t, h: s.Routes()}
	bearer := map[string]string{"Authorization": "Bearer " + secret}
	for _, rt := range changes[:4] {
		w := key.do(rt.method, rt.path, rt.body, bearer)
		if w.Code != http.StatusForbidden || linkErrorCode(t, w.Body.String()) != "session_required" {
			t.Errorf("%s %s with a key = %d %s", rt.method, rt.path, w.Code, w.Body)
		}
	}
	if w := key.do(http.MethodGet, "/api/v1/dashboard/hub", "", bearer); w.Code != 200 {
		t.Errorf("a key could not read the status: %d", w.Code)
	}
	if w := key.do(http.MethodPost, "/api/v1/dashboard/hub/sync", "", bearer); w.Code != 200 {
		t.Errorf("a key could not sync: %d %s", w.Code, w.Body)
	}
	if st, linked, _ := s.modules.hubLink.Linked(t.Context()); !linked || st.Scope != accountlink.ScopeObserve {
		t.Fatalf("a key changed the link: %+v", st)
	}
}

// A session that still owes its second factor is never told about the hub.
func TestTheSessionCarriesHubOnlyWhenFullySignedIn(t *testing.T) {
	s, hub, admin := hubServer(t, true)
	admin.do(http.MethodPost, "/api/v1/dashboard/hub", `{"url":"`+hub.URL+`","scope":"observe"}`, nil)
	hub.Approve()
	if _, err := s.modules.hubLink.Poll(t.Context()); err != nil {
		t.Fatal(err)
	}
	// hub-admin has an authenticator; a fresh password sign-in owes its code.
	login, err := s.Auth.Login(t.Context(), "hub-admin", "Correct-Horse-Battery-9", "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	partial := &client{t: t, h: s.Routes(), cookie: httpx.SessionCookie + "=" + login.Token}
	w := partial.do(http.MethodGet, "/api/v1/auth/session", "", nil)
	m := decodeMap(t, w.Body.String())
	if w.Code != 200 || m["needsTotp"] != true || m["hub"] != nil {
		t.Fatalf("partial session = %d %s", w.Code, w.Body)
	}
	anon := &client{t: t, h: s.Routes()}
	if w := anon.do(http.MethodGet, "/api/v1/auth/session", "", nil); strings.Contains(w.Body.String(), `"hub"`) {
		t.Fatalf("anonymous session = %s", w.Body)
	}
}

func TestTheHubURLRuleAnswersWithItsOwnCodes(t *testing.T) {
	s, hub, admin := hubServer(t, true)
	for url, code := range map[string]string{
		"http://hub.tailed39ba.ts.net":  "hub_url_invalid",
		"https://100.101.102.103":       "hub_url_invalid",
		"hub.tailed39ba.ts.net/servers": "hub_url_invalid",
		"hub.example.com":               "hub_url_not_tailnet",
		"a.hub.tailed39ba.ts.net":       "hub_url_not_tailnet",
		"box.tailed39ba.ts.net":         "hub_url_same_host",
	} {
		w := admin.do(http.MethodPost, "/api/v1/dashboard/hub", `{"url":"`+url+`","scope":"observe"}`, nil)
		if w.Code != 400 || linkErrorCode(t, w.Body.String()) != code {
			t.Errorf("%s = %d %s, want %s", url, w.Code, w.Body, code)
		}
	}
	if w := admin.do(http.MethodPost, "/api/v1/dashboard/hub", `{"url":"`+hub.URL+`","scope":"everything"}`, nil); w.Code != 400 || linkErrorCode(t, w.Body.String()) != "bad_request" {
		t.Errorf("bad scope = %d %s", w.Code, w.Body)
	}
	if r, _, _, _ := hub.Count(); r != 0 {
		t.Fatalf("a refused address reached a hub %d times", r)
	}
	if rows := linkAudit(t, s, "hub.link.begin"); len(rows) != 6 || rows[0].Success {
		t.Fatalf("refused connects audited = %+v", rows)
	}
}

func TestSwitchedOffTheHubRoutesSayWhyAndTheProbeIsGone(t *testing.T) {
	s, hub, admin := hubServer(t, false)
	w := admin.do(http.MethodGet, "/api/v1/dashboard/hub", "", nil)
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"enabled":false,"state":"unlinked"}` {
		t.Fatalf("status = %d %s", w.Code, w.Body)
	}
	if code, _, _ := hello(t, s); code != http.StatusNotFound {
		t.Fatalf("hello while switched off = %d", code)
	}
	for _, c := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/v1/dashboard/hub", `{"url":"` + hub.URL + `","scope":"observe"}`},
		{http.MethodPut, "/api/v1/dashboard/hub/scope", `{"scope":"observe"}`},
		{http.MethodPost, "/api/v1/dashboard/hub/sync", ""},
	} {
		if w := admin.do(c.method, c.path, c.body, nil); w.Code != 403 || linkErrorCode(t, w.Body.String()) != "hub_link_disabled" {
			t.Errorf("%s %s = %d %s", c.method, c.path, w.Code, w.Body)
		}
	}
	if r, p, b, u := hub.Count(); r+p+b+u != 0 {
		t.Fatal("a switched-off dashboard called the hub")
	}
}

func TestAnUnreachableHubIsABadGatewayThatSaysWhatFailed(t *testing.T) {
	s, hub, admin := hubServer(t, true)
	hub.Close()
	w := admin.do(http.MethodPost, "/api/v1/dashboard/hub", `{"url":"`+hub.URL+`","scope":"observe"}`, nil)
	if w.Code != http.StatusBadGateway || linkErrorCode(t, w.Body.String()) != "hub_unreachable" || !strings.Contains(w.Body.String(), hub.URL) {
		t.Fatalf("connect = %d %s", w.Code, w.Body)
	}
	if rows := linkAudit(t, s, "hub.link.begin"); len(rows) != 1 || rows[0].Success {
		t.Fatalf("the failed attempt must be audited as failed: %+v", rows)
	}
}

// peerTailnet is stubTailnet with the hub's machine on it.
type peerTailnet struct{}

func (peerTailnet) Status(context.Context) (hublink.TailnetStatus, error) {
	return hublink.TailnetStatus{Running: true, Suffix: "tailed39ba.ts.net", Self: "box.tailed39ba.ts.net",
		Peers: map[string]netip.Addr{"hub.tailed39ba.ts.net": netip.MustParseAddr("100.101.102.103")}}, nil
}

// hubWhoIs is tailscaled naming the hub's machine (or the machine id, when
// set, holding the hub's name), or failing with err.
type hubWhoIs struct {
	err error
	id  string
}

func (w hubWhoIs) WhoIs(_ context.Context, a netip.Addr) (tailnetgate.Peer, error) {
	if w.err != nil {
		return tailnetgate.Peer{}, w.err
	}
	id := w.id
	if id == "" {
		id = "nK7f2QbPaa21CNTRL"
	}
	return tailnetgate.Peer{Address: a, Node: "hub.tailed39ba.ts.net.", StableID: id, Login: "wayy01@github"}, nil
}

// GET /dashboard/hub/check applies the URL rule without starting anything and
// shows the hub's machine before Connect (protocol 8.2, HL-11): an
// administrator's, in a session, not audited, with Connect's error codes.
func TestTheHubCheckShowsTheMachineBeforeConnect(t *testing.T) {
	s, hub, admin := hubServer(t, true)
	check := func(c *client, url string, headers map[string]string) (int, string) {
		w := c.do(http.MethodGet, "/api/v1/dashboard/hub/check?url="+url, "", headers)
		return w.Code, strings.TrimSpace(w.Body.String())
	}
	if code, body := check(admin, hub.URL, nil); code != 200 || body != `{"hubUrl":"`+hub.URL+`","testOrigin":true}` {
		t.Fatalf("the test origin = %d %s", code, body)
	}
	// stubWhoIs cannot be asked: no machine, and the page says not checked.
	if code, body := check(admin, "hub.tailed39ba.ts.net", nil); code != 200 || body != `{"hubUrl":"https://hub.tailed39ba.ts.net","testOrigin":false}` {
		t.Fatalf("unchecked = %d %s", code, body)
	}
	for url, want := range map[string]string{
		"http://hub.tailed39ba.ts.net": "hub_url_invalid",
		"hub.example.com":              "hub_url_not_tailnet",
		"box.tailed39ba.ts.net":        "hub_url_same_host",
	} {
		if code, body := check(admin, url, nil); code != 400 || linkErrorCode(t, body) != want {
			t.Errorf("%s = %d %s, want %s", url, code, body, want)
		}
	}
	// Only an administrator, and never with an API key.
	for _, role := range []auth.Role{auth.RoleReadOnly, auth.RoleLimited} {
		c := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "hub-check-"+string(role), role)}
		if code, _ := check(c, "hub.tailed39ba.ts.net", nil); code != http.StatusForbidden {
			t.Errorf("check as %s = %d", role, code)
		}
	}
	user, err := s.Auth.UserByID(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	_, secret, err := s.Auth.CreateAPIToken(t.Context(), user, "hub-check", auth.RoleAdmin, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if code, body := check(&client{t: t, h: s.Routes()}, "hub.tailed39ba.ts.net", map[string]string{"Authorization": "Bearer " + secret}); code != 403 || linkErrorCode(t, body) != "session_required" {
		t.Errorf("check with a key = %d %s", code, body)
	}
	if r, _, _, _ := hub.Count(); r != 0 || len(linkAudit(t, s, "hub.link")) != 0 {
		t.Fatal("check sent a request or wrote an audit entry")
	}

	// With tailscaled answering, the machine and its owner.
	s.modules.hubLink = hublink.New(hublink.Options{
		Store: s.Store, Sealer: s.Sealer, Audit: s.Audit, Enabled: true, Host: newAccountHost(s), Version: "test",
		Tailnet: peerTailnet{}, WhoIs: hubWhoIs{},
	})
	admin = &client{t: t, h: s.Routes(), cookie: admin.cookie}
	want := `{"hubUrl":"https://hub.tailed39ba.ts.net","machine":{"id":"nK7f2QbPaa21CNTRL","name":"hub.tailed39ba.ts.net","owner":"wayy01@github","tags":[]},"testOrigin":false}`
	if code, body := check(admin, "https://HUB.tailed39ba.ts.net.:443", nil); code != 200 || body != want {
		t.Fatalf("checked = %d %s", code, body)
	}
	s.modules.hubLink = hublink.New(hublink.Options{
		Store: s.Store, Sealer: s.Sealer, Audit: s.Audit, Enabled: true, Host: newAccountHost(s), Version: "test",
		Tailnet: peerTailnet{}, WhoIs: hubWhoIs{err: tailnetgate.ErrNotAPeer},
	})
	admin = &client{t: t, h: s.Routes(), cookie: admin.cookie}
	if code, body := check(admin, "hub.tailed39ba.ts.net", nil); code != 400 || linkErrorCode(t, body) != "hub_not_on_tailnet" {
		t.Fatalf("no machine at the address = %d %s", code, body)
	}
	s.modules.hubLink = hublink.New(hublink.Options{Store: s.Store, Sealer: s.Sealer, Enabled: false})
	admin = &client{t: t, h: s.Routes(), cookie: admin.cookie}
	if code, body := check(admin, "hub.tailed39ba.ts.net", nil); code != 403 || linkErrorCode(t, body) != "hub_link_disabled" {
		t.Fatalf("switched off = %d %s", code, body)
	}
}

// A sync whose check of the hub's machine fails this time, with no machine at
// the hub's address (ErrNotAPeer), is a heartbeat that failed: 200 and the
// status saying why. Only an idle link, here one whose hub name another
// machine holds now, is refused, with 409 hub_url_refused (protocol 2.4, 8.2).
func TestASyncThatCannotCheckTheHubsMachineAnswersTheStatus(t *testing.T) {
	s := testServer(t)
	hub := hubstub.New(t)
	toHub := hub.At("https://hub.tailed39ba.ts.net")
	cookie := signInAs(t, s, "hub-sync", auth.RoleAdmin)
	// Each start of the dashboard checks the hub's machine before it sends.
	start := func(who hubWhoIs) *client {
		s.modules.hubLink = hublink.New(hublink.Options{
			Store: s.Store, Sealer: s.Sealer, Audit: s.Audit, Enabled: true, Host: newAccountHost(s), Version: "test",
			Tailnet: peerTailnet{}, WhoIs: who, HTTP: toHub,
		})
		return &client{t: t, h: s.Routes(), cookie: cookie}
	}
	admin := start(hubWhoIs{})
	if w := admin.do(http.MethodPost, "/api/v1/dashboard/hub", `{"url":"hub.tailed39ba.ts.net","scope":"observe"}`, nil); w.Code != 200 {
		t.Fatalf("connect = %d %s", w.Code, w.Body)
	}
	hub.Approve()
	if res, err := s.modules.hubLink.Poll(t.Context()); err != nil || res.State != accountlink.PollLinked {
		t.Fatalf("poll = %+v, %v", res, err)
	}

	admin = start(hubWhoIs{err: tailnetgate.ErrNotAPeer})
	w := admin.do(http.MethodPost, "/api/v1/dashboard/hub/sync", "", nil)
	if w.Code != 200 {
		t.Fatalf("sync with no machine at the address = %d %s", w.Code, w.Body)
	}
	link, _ := decodeMap(t, w.Body.String())["admin"].(map[string]any)["link"].(map[string]any)
	if last, _ := link["lastError"].(string); !strings.Contains(last, "the hub's machine is not on this tailnet") {
		t.Fatalf("status after sync = %s", w.Body)
	}
	if _, _, beats, _ := hub.Count(); beats != 0 {
		t.Fatalf("%d heartbeats with no machine at the address", beats)
	}

	admin = start(hubWhoIs{id: "nImpostorCNTRL"})
	if w := admin.do(http.MethodPost, "/api/v1/dashboard/hub/sync", "", nil); w.Code != 409 || linkErrorCode(t, w.Body.String()) != "hub_url_refused" ||
		!strings.Contains(w.Body.String(), "the hub's machine changed") {
		t.Fatalf("sync with another machine at the name = %d %s", w.Code, w.Body)
	}
	if _, _, beats, _ := hub.Count(); beats != 0 {
		t.Fatalf("%d heartbeats reached another machine", beats)
	}
}

// Connect records the hub node it found, or why it did not, in the
// hub.link.begin audit entry (protocol 2.2 step 8).
func TestTheBeginAuditNamesTheHubsMachine(t *testing.T) {
	s, hub, admin := hubServer(t, true)
	if w := admin.do(http.MethodPost, "/api/v1/dashboard/hub", `{"url":"`+hub.URL+`","scope":"observe"}`, nil); w.Code != 200 {
		t.Fatalf("connect = %d %s", w.Code, w.Body)
	}
	if rows := linkAudit(t, s, "hub.link.begin"); len(rows) != 1 || !strings.Contains(rows[0].Detail, `"hubNode":"not checked: the test origin"`) {
		t.Fatalf("begin audit = %+v", rows)
	}
}

func TestHubRoutesDoNotExistInAgentMode(t *testing.T) {
	s := testServer(t)
	s.Cfg.AgentMode = true
	s.modules.hubLink = nil
	s.initHubLink()
	if s.modules.hubLink != nil {
		t.Fatal("agent mode built a hub link")
	}
}
