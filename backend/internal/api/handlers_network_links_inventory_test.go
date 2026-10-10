package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
)

// The detailed device, bridge, readiness and namespace reads are `read`: they
// change nothing and send nothing. The VLAN and flood-end replacements are
// administrator mutations, destructive when they take something away.

func TestNetworkInventoryMutationsNeedSystemAdmin(t *testing.T) {
	s := testServer(t)
	readOnly := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "reader", auth.RoleReadOnly)}
	for _, m := range []struct{ path, body string }{
		{"/api/v1/network/links/eth0.100/vlans", `{"vlans":[{"vid":10,"pvid":true,"untagged":true}]}`},
		{"/api/v1/network/links/vx42/remotes", `{"remotes":["198.51.100.8"]}`},
	} {
		if w := readOnly.do(http.MethodPut, m.path, m.body, nil); w.Code != http.StatusForbidden {
			t.Fatalf("%s: a read-only account got %d: %s", m.path, w.Code, strings.TrimSpace(w.Body.String()))
		}
	}
}

func TestNetworkInventoryReadsAreOpenToReadersAndValidated(t *testing.T) {
	s := testServer(t)
	reader := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "reader", auth.RoleReadOnly)}
	for _, path := range []string{
		"/api/v1/network/links/lo/detail",
		"/api/v1/network/links/lo/readiness",
		"/api/v1/network/links/lo/bridge",
		"/api/v1/network/links/lo/master/preview?master=",
		"/api/v1/network/namespaces/postgres?kind=container",
	} {
		w := reader.do(http.MethodGet, path, "", nil)
		if w.Code == http.StatusForbidden || w.Code == http.StatusUnauthorized || w.Code >= 500 && w.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s: a reader got %d: %s", path, w.Code, strings.TrimSpace(w.Body.String()))
		}
	}
	for _, tc := range []struct{ path, code string }{
		{"/api/v1/network/links/a%20b/detail", "bad_request"},
		{"/api/v1/network/links/a%20b/readiness", "bad_request"},
		{"/api/v1/network/namespaces/lab/lookup?kind=named", "bad_request"},
		{"/api/v1/network/namespaces/lab?kind=vm", "bad_request"},
		{"/api/v1/network/namespaces/postgres/lookup?kind=container&target=example.com", "bad_request"},
	} {
		w := reader.do(http.MethodGet, tc.path, "", nil)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), tc.code) {
			t.Fatalf("%s: got %d: %s", tc.path, w.Code, strings.TrimSpace(w.Body.String()))
		}
	}
}
