package api

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accountlink"
	"github.com/Wayy01/Just-Dashboard/backend/internal/accountlink/linkstub"
	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/go-chi/chi/v5"
)

// linkedServer is a test server whose connector talks to a stand-in site.
func linkedServer(t *testing.T, enabled bool) (*Server, *linkstub.Site, *client) {
	t.Helper()
	s := testServer(t)
	site := linkstub.New(t)
	s.modules.accountLink = accountlink.New(accountlink.Options{
		Store: s.Store, Sealer: s.Sealer, Audit: s.Audit, SiteURL: site.URL, Enabled: enabled,
		Host: newAccountHost(s), Version: "test",
	})
	admin := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "link-admin", auth.RoleAdmin)}
	return s, site, admin
}

func decodeMap(t *testing.T, body string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("not JSON: %s", body)
	}
	return m
}

func keysOf(m map[string]any) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

func linkErrorCode(t *testing.T, body string) string {
	t.Helper()
	m := decodeMap(t, body)
	e, _ := m["error"].(map[string]any)
	code, _ := e["code"].(string)
	return code
}

func linkAudit(t *testing.T, s *Server, action string) []audit.Entry {
	t.Helper()
	rows, _, err := s.Audit.List(t.Context(), audit.Filter{Action: action, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestTheLinkRoutesFollowTheContractTheFrontendBuildsAgainst(t *testing.T) {
	s, site, admin := linkedServer(t, true)

	// Unlinked.
	w := admin.do(http.MethodGet, "/api/v1/dashboard/link", "", nil)
	if w.Code != 200 {
		t.Fatalf("status = %d %s", w.Code, w.Body)
	}
	m := decodeMap(t, w.Body.String())
	if keysOf(m) != "enabled,siteUrl,state" || m["state"] != "unlinked" || m["enabled"] != true || m["siteUrl"] != site.URL {
		t.Fatalf("unlinked status = %s", w.Body)
	}

	// Begin: pending, with the request the page shows.
	w = admin.do(http.MethodPost, "/api/v1/dashboard/link", `{"scope":"operate"}`, nil)
	if w.Code != 200 {
		t.Fatalf("begin = %d %s", w.Code, w.Body)
	}
	m = decodeMap(t, w.Body.String())
	req, _ := m["request"].(map[string]any)
	if keysOf(m) != "enabled,fingerprint,request,siteUrl,state" || m["state"] != "pending" ||
		keysOf(req) != "approveUrl,expiresAt,scope,userCode" || req["userCode"] != "ABCD-2345" || req["scope"] != "operate" ||
		!strings.HasPrefix(req["approveUrl"].(string), site.URL+"/link?code=") {
		t.Fatalf("pending status = %s", w.Body)
	}
	if _, err := time.Parse(time.RFC3339, req["expiresAt"].(string)); err != nil {
		t.Fatalf("expiresAt is not RFC 3339: %v", err)
	}
	if fp, _ := m["fingerprint"].(string); len(fp) != 19 || strings.Count(fp, ":") != 3 {
		t.Fatalf("fingerprint = %q", fp)
	}
	if w := admin.do(http.MethodPost, "/api/v1/dashboard/link", `{"scope":"observe"}`, nil); w.Code != 409 || linkErrorCode(t, w.Body.String()) != "link_pending" {
		t.Fatalf("second begin = %d %s", w.Code, w.Body)
	}

	// Cancel: 204, and a new request can be made.
	if w := admin.do(http.MethodDelete, "/api/v1/dashboard/link/request", "", nil); w.Code != 204 {
		t.Fatalf("cancel = %d %s", w.Code, w.Body)
	}
	if w := admin.do(http.MethodPost, "/api/v1/dashboard/link", `{"scope":"maintain"}`, nil); w.Code != 200 {
		t.Fatalf("begin after cancel = %d %s", w.Code, w.Body)
	}

	// The person approves on the site; the dashboard collects the approval.
	site.Approve("ion@example.com")
	if res, err := s.modules.accountLink.PollLink(t.Context()); err != nil || res.State != accountlink.PollLinked {
		t.Fatalf("poll = %+v, %v", res, err)
	}
	w = admin.do(http.MethodGet, "/api/v1/dashboard/link", "", nil)
	m = decodeMap(t, w.Body.String())
	link, _ := m["link"].(map[string]any)
	if m["state"] != "linked" || keysOf(m) != "enabled,fingerprint,link,siteUrl,state" ||
		keysOf(link) != "linkedAt,linkedBy,owner,scope,serverId,siteKeys" ||
		link["owner"] != "ion@example.com" || link["scope"] != "maintain" || link["linkedBy"] != "link-admin" {
		t.Fatalf("linked status = %s", w.Body)
	}
	keys, _ := link["siteKeys"].([]any)
	if len(keys) != 1 || keysOf(keys[0].(map[string]any)) != "fingerprint,kid" {
		t.Fatalf("siteKeys = %v", link["siteKeys"])
	}
	if w := admin.do(http.MethodPost, "/api/v1/dashboard/link", `{"scope":"observe"}`, nil); w.Code != 409 || linkErrorCode(t, w.Body.String()) != "already_linked" {
		t.Fatalf("begin while linked = %d %s", w.Code, w.Body)
	}

	// Scope.
	w = admin.do(http.MethodPut, "/api/v1/dashboard/link/scope", `{"scope":"observe"}`, nil)
	if w.Code != 200 || decodeMap(t, w.Body.String())["link"].(map[string]any)["scope"] != "observe" {
		t.Fatalf("scope = %d %s", w.Code, w.Body)
	}
	if w := admin.do(http.MethodPut, "/api/v1/dashboard/link/scope", `{"scope":"root"}`, nil); w.Code != 400 {
		t.Fatalf("bad scope = %d", w.Code)
	}

	// Sync: a heartbeat now, whose exact body the page then shows.
	w = admin.do(http.MethodPost, "/api/v1/dashboard/link/sync", "", nil)
	if w.Code != 200 {
		t.Fatalf("sync = %d %s", w.Code, w.Body)
	}
	link = decodeMap(t, w.Body.String())["link"].(map[string]any)
	if _, err := time.Parse(time.RFC3339, link["lastHeartbeatAt"].(string)); err != nil {
		t.Fatalf("lastHeartbeatAt = %v", link["lastHeartbeatAt"])
	}
	snap, _ := link["lastSnapshot"].(map[string]any)
	server, _ := snap["server"].(map[string]any)
	if snap["protocol"] != float64(1) || server["scope"] != "observe" || server["version"] == "" || server["name"] == "" {
		t.Fatalf("lastSnapshot = %v", link["lastSnapshot"])
	}
	if _, ok := snap["containers"]; ok {
		if _, listed := snap["containers"].(map[string]any)["list"]; listed {
			t.Fatal("container names in an observe snapshot")
		}
	}

	// Unlink: 204, then not linked.
	if w := admin.do(http.MethodDelete, "/api/v1/dashboard/link", "", nil); w.Code != 204 {
		t.Fatalf("unlink = %d %s", w.Code, w.Body)
	}
	for _, c := range []struct{ method, path, body string }{
		{http.MethodDelete, "/api/v1/dashboard/link", ""},
		{http.MethodPut, "/api/v1/dashboard/link/scope", `{"scope":"observe"}`},
		{http.MethodPost, "/api/v1/dashboard/link/sync", ""},
	} {
		if w := admin.do(c.method, c.path, c.body, nil); w.Code != 409 || linkErrorCode(t, w.Body.String()) != "not_linked" {
			t.Fatalf("%s %s unlinked = %d %s", c.method, c.path, w.Code, w.Body)
		}
	}

	// Every change is in the audit log, with who made it.
	for _, action := range []string{"account.link.begin", "account.link.cancel", "account.link.scope", "account.unlink"} {
		rows := linkAudit(t, s, action)
		if len(rows) == 0 || rows[len(rows)-1].Username != "link-admin" {
			t.Errorf("%s audit = %+v", action, rows)
		}
	}
	if rows := linkAudit(t, s, "account.link.begin"); len(rows) != 4 {
		// two that began, one refused as pending, one refused as already linked
		t.Errorf("begin audit has %d rows, want 4: %+v", len(rows), rows)
	}
}

func TestOnlyAnAdministratorWithASessionMayChangeTheLink(t *testing.T) {
	s, _, admin := linkedServer(t, true)
	routes := []struct{ method, path, body string }{
		{http.MethodGet, "/api/v1/dashboard/link", ""},
		{http.MethodPost, "/api/v1/dashboard/link", `{"scope":"observe"}`},
		{http.MethodDelete, "/api/v1/dashboard/link/request", ""},
		{http.MethodPut, "/api/v1/dashboard/link/scope", `{"scope":"observe"}`},
		{http.MethodDelete, "/api/v1/dashboard/link", ""},
		{http.MethodPost, "/api/v1/dashboard/link/sync", ""},
	}
	for _, role := range []auth.Role{auth.RoleReadOnly, auth.RoleLimited} {
		c := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "link-"+string(role), role)}
		for _, rt := range routes {
			if w := c.do(rt.method, rt.path, rt.body, nil); w.Code != http.StatusForbidden {
				t.Errorf("%s %s as %s = %d", rt.method, rt.path, role, w.Code)
			}
		}
	}

	// An API key, even an administrator's, cannot hand the server to an
	// account, narrow or widen the link, or take it back. It can read the
	// status and ask for a heartbeat.
	user, err := s.Auth.UserByID(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	_, secret, err := s.Auth.CreateAPIToken(t.Context(), user, "link", auth.RoleAdmin, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	key := &client{t: t, h: s.Routes()}
	bearer := map[string]string{"Authorization": "Bearer " + secret}
	for _, rt := range routes[1:5] {
		w := key.do(rt.method, rt.path, rt.body, bearer)
		if w.Code != http.StatusForbidden || linkErrorCode(t, w.Body.String()) != "session_required" {
			t.Errorf("%s %s with a key = %d %s", rt.method, rt.path, w.Code, w.Body)
		}
	}
	if w := key.do(http.MethodGet, "/api/v1/dashboard/link", "", bearer); w.Code != 200 {
		t.Errorf("a key could not read the status: %d", w.Code)
	}
	if st, _ := s.modules.accountLink.Status(t.Context()); st.State != accountlink.StateUnlinked || st.Request != nil {
		t.Fatalf("a key changed the link: %+v", st)
	}
	_ = admin
}

func TestSwitchedOffTheRoutesSayWhyAndOfferNothing(t *testing.T) {
	_, site, admin := linkedServer(t, false)
	w := admin.do(http.MethodGet, "/api/v1/dashboard/link", "", nil)
	if m := decodeMap(t, w.Body.String()); w.Code != 200 || m["enabled"] != false || m["state"] != "unlinked" {
		t.Fatalf("status = %d %s", w.Code, w.Body)
	}
	for _, c := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/v1/dashboard/link", `{"scope":"observe"}`},
		{http.MethodPut, "/api/v1/dashboard/link/scope", `{"scope":"observe"}`},
		{http.MethodPost, "/api/v1/dashboard/link/sync", ""},
	} {
		if w := admin.do(c.method, c.path, c.body, nil); w.Code != 403 || linkErrorCode(t, w.Body.String()) != "account_link_disabled" {
			t.Errorf("%s %s = %d %s", c.method, c.path, w.Code, w.Body)
		}
	}
	if _, _, _, _, hits := site.Count(); hits != 0 {
		t.Fatalf("a switched-off dashboard called the site %d times", hits)
	}
}

func TestAnUnreachableSiteIsABadGatewayThatSaysWhatFailed(t *testing.T) {
	s, site, admin := linkedServer(t, true)
	site.Close()
	w := admin.do(http.MethodPost, "/api/v1/dashboard/link", `{"scope":"observe"}`, nil)
	if w.Code != http.StatusBadGateway || linkErrorCode(t, w.Body.String()) != "site_unreachable" || !strings.Contains(w.Body.String(), site.URL) {
		t.Fatalf("begin = %d %s", w.Code, w.Body)
	}
	if rows := linkAudit(t, s, "account.link.begin"); len(rows) != 1 || rows[0].Success {
		t.Fatalf("the failed attempt must be audited as failed: %+v", rows)
	}
}

func TestLinkRoutesAreNotMountedWhereTheConnectorDoesNotExist(t *testing.T) {
	s := testServer(t)
	routes := func() []string {
		r := chi.NewRouter()
		s.mountSelfUpdateRoutes(r)
		var out []string
		_ = chi.Walk(r, func(method, pattern string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
			if strings.Contains(pattern, "/link") {
				out = append(out, method+" "+pattern)
			}
			return nil
		})
		return out
	}
	if got := routes(); len(got) != 6 {
		t.Fatalf("link routes = %v", got)
	}
	s.modules.accountLink = nil
	if got := routes(); len(got) != 0 {
		t.Fatalf("agent mode has link routes: %v", got)
	}
	s.Cfg.AgentMode = true
	s.initAccountLink()
	if s.modules.accountLink != nil {
		t.Fatal("agent mode built a connector")
	}
}

func TestALinkToAnotherSiteIsNotSyncedAndCanStillBeUnlinked(t *testing.T) {
	s, site, admin := linkedServer(t, true)
	admin.do(http.MethodPost, "/api/v1/dashboard/link", `{"scope":"observe"}`, nil)
	site.Approve("ion@example.com")
	if res, err := s.modules.accountLink.PollLink(t.Context()); err != nil || res.State != accountlink.PollLinked {
		t.Fatalf("poll = %+v, %v", res, err)
	}
	// JD_ACCOUNT_URL now names another site.
	elsewhere := linkstub.New(t)
	s.modules.accountLink = accountlink.New(accountlink.Options{
		Store: s.Store, Sealer: s.Sealer, Audit: s.Audit, SiteURL: elsewhere.URL, Enabled: true,
		Host: newAccountHost(s), Version: "test",
	})
	w := admin.do(http.MethodPost, "/api/v1/dashboard/link/sync", "", nil)
	if w.Code != http.StatusConflict || linkErrorCode(t, w.Body.String()) != "site_changed" {
		t.Fatalf("sync = %d %s", w.Code, w.Body)
	}
	if w := admin.do(http.MethodDelete, "/api/v1/dashboard/link", "", nil); w.Code != 204 {
		t.Fatalf("unlink = %d %s", w.Code, w.Body)
	}
	if _, _, beats, unlinks, hits := elsewhere.Count(); beats != 0 || unlinks != 0 || hits != 0 {
		t.Fatalf("the other site was called: %d heartbeats, %d unlinks, %d requests", beats, unlinks, hits)
	}
	if _, _, beats, unlinks, _ := site.Count(); beats != 0 || unlinks != 0 {
		t.Fatalf("the site the link was made with was told by a service configured for another: %d %d", beats, unlinks)
	}
}
