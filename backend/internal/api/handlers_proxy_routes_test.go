package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
	"github.com/go-chi/chi/v5"
)

// proxyAccess is the gate in front of a proxy route.
type proxyAccess int

const (
	// Any signed-in account.
	proxyRead proxyAccess = iota
	// system.admin.
	proxyAdmin
	// system.admin, then destructive and its tighter rate budget.
	proxyDestructive
)

// proxyRoutes is the proxy surface as it stood before the routes were split
// into a file per area, walked from the router of the time. Each area may add
// routes of its own; none of these may move, change method or change gate.
var proxyRoutes = []struct {
	method, path string
	access       proxyAccess
}{
	{http.MethodGet, "/api/v1/proxy/status", proxyRead},
	{http.MethodGet, "/api/v1/proxy/vhosts", proxyRead},
	{http.MethodGet, "/api/v1/proxy/config", proxyRead},
	{http.MethodPost, "/api/v1/proxy/validate", proxyAdmin},
	{http.MethodPut, "/api/v1/proxy/config", proxyAdmin},
	{http.MethodPost, "/api/v1/proxy/test", proxyAdmin},
	{http.MethodPost, "/api/v1/proxy/reload", proxyAdmin},
	{http.MethodPost, "/api/v1/proxy/vhosts/{name}/enabled", proxyDestructive},

	{http.MethodGet, "/api/v1/proxy/sites/{name}", proxyRead},
	{http.MethodPost, "/api/v1/proxy/sites/preview", proxyAdmin},
	{http.MethodPost, "/api/v1/proxy/sites/", proxyAdmin},
	{http.MethodDelete, "/api/v1/proxy/sites/{name}", proxyDestructive},

	{http.MethodGet, "/api/v1/proxy/streams/", proxyRead},
	{http.MethodPost, "/api/v1/proxy/streams/preview", proxyAdmin},
	{http.MethodPost, "/api/v1/proxy/streams/", proxyAdmin},
	{http.MethodDelete, "/api/v1/proxy/streams/{name}", proxyDestructive},

	{http.MethodGet, "/api/v1/proxy/auth-files/", proxyAdmin},
	{http.MethodPost, "/api/v1/proxy/auth-files/", proxyAdmin},
	{http.MethodDelete, "/api/v1/proxy/auth-files/{file}", proxyDestructive},
	{http.MethodDelete, "/api/v1/proxy/auth-files/{file}/users/{user}", proxyDestructive},

	{http.MethodGet, "/api/v1/certificates/", proxyRead},
	{http.MethodGet, "/api/v1/certificates/certbot", proxyRead},
	{http.MethodGet, "/api/v1/certificates/dns-providers", proxyRead},
	{http.MethodGet, "/api/v1/certificates/watched", proxyRead},
	{http.MethodGet, "/api/v1/certificates/check", proxyAdmin},
	{http.MethodGet, "/api/v1/certificates/scan", proxyAdmin},
	{http.MethodGet, "/api/v1/certificates/dns", proxyAdmin},
	{http.MethodPut, "/api/v1/certificates/dns/resolvers", proxyAdmin},
	{http.MethodPost, "/api/v1/certificates/watched", proxyAdmin},
	{http.MethodDelete, "/api/v1/certificates/watched/{id}", proxyAdmin},
	{http.MethodPost, "/api/v1/certificates/issue", proxyAdmin},
	{http.MethodPost, "/api/v1/certificates/import", proxyAdmin},
	{http.MethodPost, "/api/v1/certificates/dns-credentials", proxyAdmin},
	{http.MethodPost, "/api/v1/certificates/renew", proxyAdmin},
	{http.MethodDelete, "/api/v1/certificates/dns-credentials/{provider}", proxyDestructive},
	{http.MethodPost, "/api/v1/certificates/revoke", proxyDestructive},

	{http.MethodGet, "/api/v1/ports", proxyRead},
}

// routeGates counts, for each walked route, the capability checks and the rate
// budgets in front of it. chi.Walk hands over the middlewares but not their
// arguments, so they are told apart by the function that made them; the
// capability names are then proven by read-only and limited sessions being
// refused. The limited role holds file.write and service.control, so a route
// gated on either of those instead of system.admin lets it through.
func routeGates(t *testing.T, h http.Handler) map[string][2]int {
	t.Helper()
	gates := map[string][2]int{}
	err := chi.Walk(h.(chi.Routes), func(method, pattern string, _ http.Handler, mws ...func(http.Handler) http.Handler) error {
		var capabilities, budgets int
		for _, mw := range mws {
			name := runtime.FuncForPC(reflect.ValueOf(mw).Pointer()).Name()
			if strings.Contains(name, "RequireCapability") {
				capabilities++
			}
			if strings.Contains(name, "(*Limiter).ByPrincipal") {
				budgets++
			}
		}
		// A subrouter's index is walked as "/x/" whether the route was
		// written "/x" or mounted as "/" inside Route("/x"), and chi serves
		// both spellings for the second; the request below settles /ports.
		gates[method+" "+strings.TrimSuffix(pattern, "/")] = [2]int{capabilities, budgets}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return gates
}

func TestProxyRoutesKeepTheirPaths(t *testing.T) {
	s := testServer(t)
	h := s.Routes()
	gates := routeGates(t, h)
	want := map[proxyAccess][2]int{
		proxyRead:        {0, 1},
		proxyAdmin:       {1, 1},
		proxyDestructive: {2, 2},
	}
	refused := map[auth.Role]*client{
		auth.RoleReadOnly: {t: t, h: h, cookie: signInAs(t, s, "reader", auth.RoleReadOnly)},
		auth.RoleLimited:  {t: t, h: h, cookie: signInAs(t, s, "limited", auth.RoleLimited)},
	}
	for _, rt := range proxyRoutes {
		key := rt.method + " " + strings.TrimSuffix(rt.path, "/")
		got, ok := gates[key]
		if !ok {
			t.Errorf("%s %s is no longer mounted", rt.method, rt.path)
			continue
		}
		if got != want[rt.access] {
			t.Errorf("%s %s: %d capability checks and %d rate budgets, want %d and %d",
				rt.method, rt.path, got[0], got[1], want[rt.access][0], want[rt.access][1])
		}
		if rt.access == proxyRead {
			continue
		}
		for role, c := range refused {
			w := c.do(rt.method, routeParam.ReplaceAllString(rt.path, "1"), "", nil)
			if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), `"forbidden"`) {
				t.Errorf("%s %s: a %s account got %d %s, want 403 forbidden",
					rt.method, rt.path, role, w.Code, strings.TrimSpace(w.Body.String()))
			}
		}
	}
}

// The ports list moved into a subrouter of its own. The address the page asks
// for has to keep answering, with or without the trailing slash.
func TestPortListAnswersAtItsOldAddress(t *testing.T) {
	c, _ := newClient(t)
	for _, path := range []string{"/api/v1/ports", "/api/v1/ports/"} {
		w := c.do(http.MethodGet, path, "", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s = %d: %s", path, w.Code, strings.TrimSpace(w.Body.String()))
		}
		var listeners []map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &listeners); err != nil {
			t.Fatalf("GET %s is not a list of listeners: %v", path, err)
		}
	}
}

type actorLog struct{ actors []string }

func (l *actorLog) Record(_ context.Context, c proxysvc.Change) error {
	l.actors = append(l.actors, c.Actor)
	return nil
}

// A change made through the proxy routes is recorded as the account that made
// it, which is what a configuration history is read for after an outage.
func TestProxyChangesAreRecordedAsTheSignedInAccount(t *testing.T) {
	s := testServer(t)
	dir := t.TempDir()
	for _, sub := range []string{"sites-available", "bin"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "bin", "nginx"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Join(dir, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	s.Cfg.NginxDir = dir
	s.initModules()
	log := &actorLog{}
	s.modules.proxy.SetRecorder(log)

	c := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}
	body, _ := json.Marshal(map[string]any{
		"kind": "nginx", "path": filepath.Join(dir, "sites-available", "app"), "content": "server {}\n",
	})
	if w := c.do(http.MethodPut, "/api/v1/proxy/config", string(body), nil); w.Code != http.StatusOK {
		t.Fatalf("got %d: %s", w.Code, w.Body.String())
	}
	if !reflect.DeepEqual(log.actors, []string{"tester"}) {
		t.Fatalf("recorded actors %q, want the signed-in account", log.actors)
	}
}
