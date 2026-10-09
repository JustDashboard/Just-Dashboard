package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netsec"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netx"
)

func TestFirewallMaturityRoutesKeepTheirCapabilities(t *testing.T) {
	s := testServer(t)
	for _, role := range []auth.Role{auth.RoleLimited, auth.RoleReadOnly} {
		c := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "fw-"+string(role), role)}
		for _, m := range []struct{ method, path, body string }{
			{http.MethodGet, "/api/v1/firewall/history", ""},
			{http.MethodGet, "/api/v1/firewall/preflight?op=enable", ""},
			{http.MethodPost, "/api/v1/firewall/plans/preview", `{"operations":[]}`},
			{http.MethodPost, "/api/v1/firewall/plans", `{"operations":[]}`},
			{http.MethodDelete, "/api/v1/firewall/rules/1?id=fw-000000000000", ""},
		} {
			if w := c.do(m.method, m.path, m.body, nil); w.Code != http.StatusForbidden {
				t.Errorf("%s may call %s %s: %d %s", role, m.method, m.path, w.Code, strings.TrimSpace(w.Body.String()))
			}
		}
	}
}

func TestFirewallAccessIsReadableByAnyReader(t *testing.T) {
	s := testServer(t)
	c := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "fw-reader", auth.RoleReadOnly)}
	w := c.do(http.MethodGet, "/api/v1/firewall/access", "", nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"checks"`) {
		t.Fatalf("access = %d %s", w.Code, w.Body.String())
	}
}

func TestFirewallHistoryRefusesAMalformedIdentityBeforeReading(t *testing.T) {
	c, _ := newClient(t)
	if w := c.do(http.MethodGet, "/api/v1/firewall/history?rule=../../etc", "", nil); w.Code != http.StatusBadRequest {
		t.Fatalf("got %d: %s", w.Code, w.Body.String())
	}
	if w := c.do(http.MethodGet, "/api/v1/firewall/history", "", nil); w.Code != http.StatusOK {
		t.Fatalf("history = %d: %s", w.Code, w.Body.String())
	}
	if w := c.do(http.MethodPost, "/api/v1/firewall/plans/preview", `{"operations":[],"mystery":1}`, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("an unknown field = %d", w.Code)
	}
	if w := c.do(http.MethodGet, "/api/v1/firewall/preflight?op=add", "", nil); w.Code != http.StatusBadRequest {
		t.Fatalf("an add reviewed outside a plan = %d", w.Code)
	}
}

func TestHostFirewallChangesMayBeAppliedPending(t *testing.T) {
	for path, want := range map[string]bool{
		"/firewall/rules": true, "/firewall/rules/3": true, "/firewall/enabled": true, "/firewall/policy": true,
		"/firewall/reset": true, "/firewall/plans": true,
		"/firewall/logging": false, "/firewall/preflight": false, "/firewall/plans/preview": false, "/firewall/history": false,
	} {
		if got := supportsPendingNetworkApply(path); got != want {
			t.Errorf("%s = %v, want %v", path, got, want)
		}
	}
}

func TestFirewallErrorsKeepTheirMeaning(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{netsec.ErrRuleChanged, 409, "rule_changed"},
		{&netsec.AccessRefusal{Check: netsec.AccessCheck{Name: "SSH", Port: 22}}, 409, "would_lock_you_out"},
		{errors.Join(errors.New("x"), netsec.ErrLockout), 409, "would_lock_you_out"},
		{&netx.ConfirmationError{Reason: "Pending apply requires an independent host recovery watchdog"}, 409, "network_confirmation"},
		{&netx.ReadOnlyError{Reason: "An earlier network change needs confirmation or recovery"}, 409, "network_read_only"},
		{netsec.ErrReadOnly, 501, "firewall_read_only"},
		{netsec.ErrNoFirewall, 503, "no_firewall"},
		{errors.New("port must be a number"), 400, "bad_request"},
	}
	for _, c := range cases {
		var api *httpx.APIError
		if !errors.As(mapFirewallError(c.err), &api) || api.Status != c.status || api.Code != c.code {
			t.Errorf("%v = %+v, want %d %s", c.err, api, c.status, c.code)
		}
	}
}

func TestFirewallAccessNamesTheDashboardPortAndSSH(t *testing.T) {
	s := testServer(t)
	origNet, origSec := s.modules.network, s.modules.netsec
	t.Cleanup(func() { s.modules.network, s.modules.netsec = origNet, origSec })
	s.modules.network, s.modules.netsec = nil, nil
	r := httptest.NewRequest(http.MethodGet, "/api/v1/firewall/", nil)
	r.Host = "box.tailnet.ts.net:8443"
	r.RemoteAddr = "100.110.34.9:51000"
	a := s.firewallAccess(r)
	if len(a.Dashboard) != 1 || a.Dashboard[0] != 8443 || len(a.SSH) != 1 || a.SSH[0] != 22 || len(a.Ingress) != 2 {
		t.Fatalf("access = %+v", a)
	}
	r = httptest.NewRequest(http.MethodGet, "/api/v1/firewall/", nil)
	r.Host = "dash.example.com"
	r.Header.Set("X-Forwarded-Proto", "http")
	if a := s.firewallAccess(r); a.Dashboard[0] != 80 {
		t.Fatalf("a plain-HTTP dashboard without a port = %+v", a.Dashboard)
	}
}

func TestOwnedFirewallAdapterCarriesRulesBothWays(t *testing.T) {
	dir := t.TempDir()
	net := netx.New(netx.Options{Paths: netx.Paths{Dir: dir + "/network", Sysctl: dir + "/sysctl.conf", Unit: dir + "/unit"}})
	owned := ownedFirewall{net: net}
	table, err := owned.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if table.Enabled || table.Incoming != "accept" || len(table.Rules) != 0 {
		t.Fatalf("an unused owned table = %+v", table)
	}
	if err := owned.Change(context.Background(), netsec.OwnedChange{Op: "add", Rule: &netsec.OwnedRule{Action: "allow"}}); err == nil {
		t.Fatal("a rule selecting nothing reached the network module")
	}
}
