package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/remotedesk"
)

func TestRemoteDesktopComputersRoundTripWithoutThePassword(t *testing.T) {
	c, _ := newClient(t)
	w := c.do(http.MethodPost, "/api/v1/remote-desktop/computers",
		`{"name":"office-pc","os":"windows","host":"100.101.12.7","username":"wayy","password":"hunter2"}`, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("add: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "hunter2") {
		t.Fatalf("the password came back: %s", w.Body.String())
	}
	var added remotedesk.Computer
	json.Unmarshal(w.Body.Bytes(), &added)
	if !added.HasPassword || added.Protocol != "rdp" || added.Port != 3389 {
		t.Fatalf("added %#v", added)
	}

	w = c.do(http.MethodGet, "/api/v1/remote-desktop/", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("overview: %d %s", w.Code, w.Body.String())
	}
	var o remotedesk.Overview
	json.Unmarshal(w.Body.Bytes(), &o)
	if len(o.Computers) != 1 || o.Computers[0].ID != added.ID || o.Engine.Image != remotedesk.GuacdImage {
		t.Fatalf("overview %s", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "hunter2") || w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("overview leaked or was cacheable")
	}

	w = c.do(http.MethodPut, "/api/v1/remote-desktop/computers/"+added.ID,
		`{"name":"Office PC","os":"windows","host":"office-pc","port":3390}`, nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"hasPassword":true`) {
		t.Fatalf("update kept the password? %d %s", w.Code, w.Body.String())
	}

	w = c.do(http.MethodPost, "/api/v1/remote-desktop/computers", `{"name":"x","os":"windows","host":"bad host"}`, nil)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "host must be") {
		t.Fatalf("bad host: %d %s", w.Code, w.Body.String())
	}

	w = c.do(http.MethodDelete, "/api/v1/remote-desktop/computers/"+added.ID, "", nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	if w = c.do(http.MethodGet, "/api/v1/remote-desktop/computers/"+added.ID, "", nil); w.Code != http.StatusNotFound {
		t.Fatalf("deleted computer still answers: %d", w.Code)
	}
	if w = c.do(http.MethodGet, "/api/v1/remote-desktop/computers/not-an-id", "", nil); w.Code != http.StatusNotFound {
		t.Fatalf("malformed id: %d", w.Code)
	}
}

func TestRemoteDesktopIsForAdministratorsOnly(t *testing.T) {
	s := testServer(t)
	limited := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "limited", auth.RoleLimited)}
	for _, probe := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/remote-desktop/"},
		{http.MethodPost, "/api/v1/remote-desktop/engine"},
		{http.MethodGet, "/api/v1/remote-desktop/computers/00000000000000000000000000000000/session"},
		{http.MethodPost, "/api/v1/remote-desktop/stream"},
		{http.MethodPost, "/api/v1/remote-desktop/computers/00000000000000000000000000000000/stream/pair"},
	} {
		if w := limited.do(probe.method, probe.path, "", nil); w.Code != http.StatusForbidden {
			t.Fatalf("%s %s answered %d for a limited account", probe.method, probe.path, w.Code)
		}
	}
}

func TestRemoteDesktopProbeChecksTheAddressFirst(t *testing.T) {
	c, _ := newClient(t)
	if w := c.do(http.MethodGet, "/api/v1/remote-desktop/probe?host=a%20b&port=3389", "", nil); w.Code != http.StatusBadRequest {
		t.Fatalf("bad host probed: %d", w.Code)
	}
	w := c.do(http.MethodGet, "/api/v1/remote-desktop/probe?host=127.0.0.1&port=1", "", nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"state":"closed"`) {
		t.Fatalf("closed port: %d %s", w.Code, w.Body.String())
	}
}

func TestRemoteDesktopInstallNeedsDocker(t *testing.T) {
	c, _ := newClient(t)
	w := c.do(http.MethodPost, "/api/v1/remote-desktop/engine", "", nil)
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "docker_unavailable") {
		t.Fatalf("install without Docker: %d %s", w.Code, w.Body.String())
	}
}

type stubEngine struct{ status remotedesk.EngineStatus }

func (e *stubEngine) Status(context.Context) remotedesk.EngineStatus { return e.status }
func (e *stubEngine) Install(context.Context, func(string)) error    { return nil }
func (e *stubEngine) Remove(context.Context) error                   { return nil }
func (e *stubEngine) Close() error                                   { return nil }

// The bridge is reached only through the dashboard: signed in, as the
// dashboard's one account whatever the browser claims, never with the
// dashboard's cookie, framed by this origin only, and posted to only from a
// page of this origin.
func TestRemoteDesktopBridgeIsProxiedAsTheDashboardsAccount(t *testing.T) {
	c, s := newClient(t)
	var seen *http.Request
	bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Clone(context.Background())
		w.Header().Set("Content-Security-Policy", "default-src *")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Set-Cookie", "mlSession=x")
		io.WriteString(w, "<!doctype html>")
	}))
	t.Cleanup(bridge.Close)
	s.modules.remoteDesktop = remotedesk.New(remotedesk.Options{
		DB: s.Store.DB, Seal: s.Sealer.Seal, Open: s.Sealer.Open, Engine: &stubEngine{},
		Bridge: &stubEngine{status: remotedesk.EngineStatus{State: remotedesk.EngineRunning, Address: strings.TrimPrefix(bridge.URL, "http://")}},
	})

	w := c.do(http.MethodGet, "/api/v1/remote-desktop/bridge/stream.html?hostId=41&appId=3", "",
		map[string]string{remotedesk.BridgeUserHeader: "someone-else"})
	if w.Code != http.StatusOK || seen == nil {
		t.Fatalf("stream page: %d %s", w.Code, w.Body.String())
	}
	if seen.URL.Path != remotedesk.BridgePrefix+"/stream.html" || seen.URL.RawQuery != "hostId=41&appId=3" {
		t.Fatalf("bridge asked for %s", seen.URL)
	}
	if seen.Header.Get(remotedesk.BridgeUserHeader) != remotedesk.BridgeAccount || seen.Header.Get("Cookie") != "" {
		t.Fatalf("bridge saw account %q and cookie %q", seen.Header.Get(remotedesk.BridgeUserHeader), seen.Header.Get("Cookie"))
	}
	h := w.Header()
	if csp := h.Values("Content-Security-Policy"); len(csp) != 1 || !strings.Contains(csp[0], "frame-ancestors 'self'") {
		t.Fatalf("csp %v", csp)
	}
	if h.Get("X-Frame-Options") != "SAMEORIGIN" || len(h.Values("X-Frame-Options")) != 1 || h.Get("Set-Cookie") != "" {
		t.Fatalf("headers %v", h)
	}

	seen = nil
	w = c.do(http.MethodPost, "/api/v1/remote-desktop/bridge/api/host/cancel", `{}`, nil)
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "cross_site") || seen != nil {
		t.Fatalf("a post with no Origin: %d %s", w.Code, w.Body.String())
	}
	w = c.do(http.MethodPost, "/api/v1/remote-desktop/bridge/api/host/cancel", `{}`,
		map[string]string{"Origin": "https://evil.example"})
	if w.Code != http.StatusForbidden || seen != nil {
		t.Fatalf("a post from another site: %d", w.Code)
	}
	// The bridge's own page cannot know the CSRF header; its Origin stands in.
	w = c.do(http.MethodPost, "/api/v1/remote-desktop/bridge/api/host/cancel", `{}`,
		map[string]string{"Origin": "https://example.com", httpx.CSRFHeader: ""})
	if w.Code != http.StatusOK || seen == nil {
		t.Fatalf("a post from this dashboard: %d %s", w.Code, w.Body.String())
	}

	limited := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "limited", auth.RoleLimited)}
	if w := limited.do(http.MethodGet, "/api/v1/remote-desktop/bridge/stream.html", "", nil); w.Code != http.StatusForbidden {
		t.Fatalf("a limited account: %d", w.Code)
	}
	anonymous := &client{t: t, h: s.Routes()}
	if w := anonymous.do(http.MethodGet, "/api/v1/remote-desktop/bridge/stream.html", "", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("no session: %d", w.Code)
	}
}

func TestRemoteDesktopBridgeThatIsNotRunningSaysSo(t *testing.T) {
	c, s := newClient(t)
	s.modules.remoteDesktop = remotedesk.New(remotedesk.Options{
		DB: s.Store.DB, Seal: s.Sealer.Seal, Open: s.Sealer.Open, Engine: &stubEngine{},
		Bridge: &stubEngine{status: remotedesk.EngineStatus{State: remotedesk.EngineMissing}},
	})
	if w := c.do(http.MethodGet, "/api/v1/remote-desktop/bridge/stream.html", "", nil); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("stopped bridge: %d %s", w.Code, w.Body.String())
	}
	w := c.do(http.MethodPost, "/api/v1/remote-desktop/computers", `{"name":"office-pc","os":"windows","host":"100.101.12.7"}`, nil)
	var added remotedesk.Computer
	json.Unmarshal(w.Body.Bytes(), &added)
	w = c.do(http.MethodPost, "/api/v1/remote-desktop/computers/"+added.ID+"/stream/pair", "", nil)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "stream_bridge_stopped") {
		t.Fatalf("pair without a bridge: %d %s", w.Code, w.Body.String())
	}
}
