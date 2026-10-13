package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
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
