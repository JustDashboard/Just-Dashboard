package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netx"
	"github.com/go-chi/chi/v5"
)

// These tests stop at the handler. Anything that would reach the network
// module's host commands — a request that gets past decoding and validation —
// is left to netx, whose tests put a recorder and, behind JD_NETNS_LIVE, a
// throwaway namespace in place of the host. Here the module is either absent
// (so a request that gets that far panics rather than touching the machine
// running the tests) or a real one pointed at a temporary directory and given
// only requests it refuses before it runs anything.

func gatewayRouter(t *testing.T, role auth.Role, withModule bool) (*Server, chi.Router) {
	t.Helper()
	s := testServer(t)
	// Shutdown, which runs after this test's own cleanups, stops the module it
	// was built with.
	origNet, origSec := s.modules.network, s.modules.netsec
	t.Cleanup(func() { s.modules.network, s.modules.netsec = origNet, origSec })
	s.modules.netsec = nil // protectedPorts would run sshd -T
	s.modules.network = nil
	if withModule {
		dir := t.TempDir()
		s.modules.network = netx.New(netx.Options{Paths: netx.Paths{
			Dir: filepath.Join(dir, "network"), Sysctl: filepath.Join(dir, "sysctl.conf"), Unit: filepath.Join(dir, "unit"),
		}})
	}
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			p := &httpx.Principal{User: &auth.User{ID: 1, Username: "tester"}, Role: role, Kind: "session", IP: "127.0.0.1"}
			next.ServeHTTP(w, req.WithContext(httpx.WithPrincipal(req.Context(), p)))
		})
	})
	r.Route("/network", func(r chi.Router) {
		s.mountNetworkGatewayRoutes(r)
		s.mountNetworkShapingRoutes(r)
	})
	return s, r
}

func gwDo(r http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func gwErr(rec *httptest.ResponseRecorder) (code, message string) {
	var body struct {
		Error struct{ Code, Message string } `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return body.Error.Code, body.Error.Message
}

// The contract the pages are written from: every route, who may call it, and
// which of them are destructive.
var gatewayRoutes = map[string]string{
	"GET /network/gateway":                             "read",
	"GET /network/protection":                          "read",
	"GET /network/shaping":                             "read",
	"POST /network/gateway/forwards":                   "system.admin",
	"PUT /network/gateway/forwards/{id}":               "system.admin",
	"DELETE /network/gateway/forwards/{id}":            "destructive",
	"POST /network/gateway/nat":                        "system.admin",
	"PUT /network/gateway/nat/{id}":                    "system.admin",
	"DELETE /network/gateway/nat/{id}":                 "destructive",
	"POST /network/protection/limits":                  "system.admin",
	"PUT /network/protection/limits/{id}":              "system.admin",
	"DELETE /network/protection/limits/{id}":           "destructive",
	"POST /network/protection/blocklists":              "system.admin",
	"PUT /network/protection/blocklists/{id}":          "system.admin",
	"DELETE /network/protection/blocklists/{id}":       "destructive",
	"POST /network/protection/blocklists/{id}/refresh": "system.admin",
	"POST /network/protection/settings":                "system.admin",
	"DELETE /network/protection/settings/{key}":        "destructive",
	"DELETE /network/protection/trusted":               "destructive",
	"POST /network/shaping/bbr":                        "system.admin",
	"POST /network/shaping/{device}":                   "system.admin",
	"DELETE /network/shaping/{device}":                 "destructive",
}

func TestGatewayRoutesAreExactlyTheContract(t *testing.T) {
	_, r := gatewayRouter(t, auth.RoleAdmin, false)
	var got []string
	if err := chi.Walk(r, func(method, pattern string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		got = append(got, method+" "+pattern)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var want []string
	for k := range gatewayRoutes {
		want = append(want, k)
	}
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("routes:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestEveryGatewayMutationIsRefusedBelowItsCapabilityBeforeTheModuleIsReached(t *testing.T) {
	for route, need := range gatewayRoutes {
		method, pattern, _ := strings.Cut(route, " ")
		if method == http.MethodGet {
			continue
		}
		path := gwPath(pattern)
		for _, role := range []auth.Role{auth.RoleReadOnly, auth.RoleLimited} {
			_, r := gatewayRouter(t, role, false) // no module: reaching it panics
			rec := gwDo(r, method, path, `{}`)
			if rec.Code != http.StatusForbidden {
				t.Errorf("%s as %s: %d %s", route, role, rec.Code, rec.Body)
			}
		}
		if need == "destructive" {
			// A role with system.admin but without destructive would be
			// refused by the group the route is in; the roles that exist
			// today have both or neither, so the by-hand check is exercised
			// directly in TestRequireDestructive.
			continue
		}
	}
}

func gwPath(pattern string) string {
	out := pattern
	for _, p := range []string{"{id}", "{key}", "{device}"} {
		out = strings.ReplaceAll(out, p, map[string]string{"{id}": "1", "{key}": "net.ipv4.tcp_syncookies", "{device}": "eth0"}[p])
	}
	return out
}

func TestGatewayHandlersRefuseWhatIsMalformedBeforeAnythingRuns(t *testing.T) {
	cases := []struct {
		name         string
		method, path string
		body         string
		status       int
		code         string
		contains     string
	}{
		{"an id that is not a number", "DELETE", "/network/gateway/forwards/abc", ``, 400, "bad_request", "invalid id"},
		{"an id of zero", "DELETE", "/network/protection/limits/0", ``, 400, "bad_request", "invalid id"},
		{"a negative id on an update", "PUT", "/network/gateway/nat/-3", `{}`, 400, "bad_request", "invalid id"},
		{"a refresh with a bad id", "POST", "/network/protection/blocklists/x/refresh", ``, 400, "bad_request", "invalid id"},
		{"trusted removal with no address", "DELETE", "/network/protection/trusted", ``, 400, "bad_request", "address is required"},
		{"an unknown field", "POST", "/network/gateway/forwards", `{"name":"x","bogus":1}`, 400, "", ""},
		{"a body that is not JSON", "POST", "/network/protection/limits", `{`, 400, "", ""},
		{"a forward with no ports", "POST", "/network/gateway/forwards", `{"name":"web","protocol":"tcp","target":"10.0.0.5"}`, 400, "bad_request", "port"},
		{"a forward to loopback", "POST", "/network/gateway/forwards", `{"name":"web","protocol":"tcp","ports":"80","target":"127.0.0.1"}`, 400, "bad_request", "route_localnet"},
		{"a forward whose range does not fit", "POST", "/network/gateway/forwards", `{"name":"web","protocol":"tcp","ports":"8000-8010","target":"10.0.0.5","targetPort":"9000-9004"}`, 400, "bad_request", "same size"},
		{"a NAT entry for every source", "POST", "/network/gateway/nat", `{"name":"x","source":"0.0.0.0/0","interface":"eth0"}`, 400, "bad_request", "everything this server sends"},
		{"a limit with neither a rate nor connections", "POST", "/network/protection/limits", `{"name":"x","protocol":"tcp","ports":"22"}`, 400, "bad_request", "needs a rate"},
		{"a limit action that admits", "POST", "/network/protection/limits", `{"name":"x","protocol":"tcp","ports":"22","rate":5,"per":"second","action":"accept"}`, 400, "bad_request", "drop or reject"},
		{"a manual list with nothing in it", "POST", "/network/protection/blocklists", `{"name":"x","kind":"manual"}`, 400, "bad_request", "at least one"},
		{"a feed on a scheme the server must not fetch", "POST", "/network/protection/blocklists", `{"name":"x","kind":"feed","url":"file:///etc/passwd"}`, 400, "bad_request", "http or https"},
		{"a country that is a word", "POST", "/network/protection/blocklists", `{"name":"x","kind":"country","countries":["china"]}`, 400, "bad_request", "two-letter"},
		{"a setting outside the closed list", "POST", "/network/protection/settings", `{"values":{"net.ipv4.ip_forward":"0"}}`, 400, "bad_request", "not a setting the dashboard manages"},
		{"a setting value out of range", "POST", "/network/protection/settings", `{"values":{"net.ipv4.tcp_synack_retries":"9"}}`, 400, "bad_request", "from 1 to 5"},
		{"no settings", "POST", "/network/protection/settings", `{"values":{}}`, 400, "bad_request", "no settings"},
		{"resetting a key outside the list", "DELETE", "/network/protection/settings/net.ipv4.ip_forward", ``, 400, "bad_request", "not a setting"},
		{"shaping with nothing asked", "POST", "/network/shaping/eth0", `{}`, 400, "bad_request", "set a queue discipline"},
		{"shaping with an unknown discipline", "POST", "/network/shaping/eth0", `{"qdisc":"htb"}`, 400, "bad_request", "queue discipline"},
		{"shaping a device with a space in its name", "POST", "/network/shaping/eth%200", `{"egressKbit":5000}`, 400, "bad_request", "interface name"},
		{"clearing a device with a bad name", "DELETE", "/network/shaping/a%2Fb", ``, 400, "bad_request", "interface name"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, r := gatewayRouter(t, auth.RoleAdmin, true)
			rec := gwDo(r, c.method, c.path, c.body)
			if rec.Code != c.status {
				t.Fatalf("status %d, want %d: %s", rec.Code, c.status, rec.Body)
			}
			code, msg := gwErr(rec)
			if c.code != "" && code != c.code {
				t.Errorf("code %q, want %q", code, c.code)
			}
			if !strings.Contains(msg, c.contains) {
				t.Errorf("message %q lacks %q", msg, c.contains)
			}
		})
	}
}

func TestCreatingAForwardWithoutAContentTypeIsRefused(t *testing.T) {
	_, r := gatewayRouter(t, auth.RoleAdmin, true)
	req := httptest.NewRequest(http.MethodPost, "/network/gateway/forwards", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
}

func TestMapGatewayError(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"forwarding off", &netx.ForwardingRequiredError{Family: "4"}, 409, "forwarding_off"},
		{"forwarding off, wrapped", errors.Join(errors.New("x"), &netx.ForwardingRequiredError{Family: "6"}), 409, "forwarding_off"},
		{"a guard", &netx.GuardError{Reason: "no"}, 409, "would_lock_you_out"},
		{"read-only", &netx.ReadOnlyError{Reason: "firewalld"}, 501, "network_read_only"},
		{"a missing tool", &netx.UnavailableError{Tool: "nft", Package: "nftables"}, 503, "tool_unavailable"},
		{"not found", netx.ErrNotFound, 404, "not_found"},
		{"exists", netx.ErrExists, 409, "exists"},
		{"not managed", netx.ErrNotManaged, 409, "not_managed"},
		{"anything else is a sentence about the request", errors.New("bad port"), 400, "bad_request"},
	}
	for _, c := range cases {
		var api *httpx.APIError
		if err := mapGatewayError(c.err); !errors.As(err, &api) || api.Status != c.status || api.Code != c.code {
			t.Errorf("%s: got %v", c.name, err)
		}
	}
	var api *httpx.APIError
	if err := mapGatewayError(&netx.ForwardingRequiredError{Family: "4"}); !errors.As(err, &api) || !strings.Contains(api.Message, "Routing page") {
		t.Errorf("forwarding_off must say where to fix it: %v", err)
	}
}

func TestAuditChangeRecordsAGuardsRefusalAsOne(t *testing.T) {
	// SetAudit with no audit context is a no-op; the point here is only that
	// neither branch panics on a nil detail.
	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	auditChange(req, "network.forward.add", "web", nil, &netx.GuardError{Reason: "port 22"})
	auditChange(req, "network.forward.add", "web", map[string]any{"a": 1}, nil)
}

func TestRequireDestructive(t *testing.T) {
	s := testServer(t)
	for role, want := range map[auth.Role]int{auth.RoleAdmin: 0, auth.RoleLimited: http.StatusForbidden, auth.RoleReadOnly: http.StatusForbidden} {
		req := httptest.NewRequest(http.MethodPut, "/x", nil)
		p := &httpx.Principal{User: &auth.User{ID: 1, Username: "u-" + string(role)}, Role: role, Kind: "session"}
		req = req.WithContext(httpx.WithPrincipal(req.Context(), p))
		err := s.requireDestructive(req, "test", "turning a thing off")
		var api *httpx.APIError
		switch {
		case want == 0 && err != nil:
			t.Errorf("%s: %v", role, err)
		case want != 0 && (!errors.As(err, &api) || api.Status != want || !strings.Contains(api.Message, "turning a thing off")):
			t.Errorf("%s: err = %v", role, err)
		}
	}
	// The budget is the destructive limiter's: it runs out.
	req := httptest.NewRequest(http.MethodPut, "/x", nil)
	p := &httpx.Principal{User: &auth.User{ID: 1, Username: "burst"}, Role: auth.RoleAdmin, Kind: "session"}
	req = req.WithContext(httpx.WithPrincipal(req.Context(), p))
	var limited bool
	for i := 0; i < 100 && !limited; i++ {
		var api *httpx.APIError
		if err := s.requireDestructive(req, "test", "x"); errors.As(err, &api) && api.Status == http.StatusTooManyRequests {
			limited = true
		}
	}
	if !limited {
		t.Error("the destructive limiter never ran out")
	}
}

func TestEntryID(t *testing.T) {
	for raw, want := range map[string]int{"": 0, "7": 7} {
		r := chi.NewRouteContext()
		if raw != "" {
			r.URLParams.Add("id", raw)
		}
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, r))
		if got, err := entryID(req); err != nil || got != want {
			t.Errorf("entryID(%q) = %d, %v", raw, got, err)
		}
	}
	for _, raw := range []string{"0", "-1", "x", "1.5"} {
		r := chi.NewRouteContext()
		r.URLParams.Add("id", raw)
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, r))
		if _, err := entryID(req); err == nil {
			t.Errorf("entryID(%q) accepted", raw)
		}
	}
}

func TestProtectedPortsAreSSHTheWebPortsAndTheOneTheRequestCameTo(t *testing.T) {
	s := testServer(t)
	origSec := s.modules.netsec
	t.Cleanup(func() { s.modules.netsec = origSec })
	s.modules.netsec = nil
	s.Cfg.Addr = "127.0.0.1:8080"
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Host = "dash.example.com:9443"
	got := s.protectedPorts(req)
	sort.Ints(got)
	want := []int{22, 80, 443, 8080, 9443}
	if len(got) != len(want) {
		t.Fatalf("ports = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ports = %v, want %v", got, want)
		}
	}
	req.Host = "dash.example.com" // no port
	if got := s.protectedPorts(req); len(got) != 4 {
		t.Fatalf("ports = %v", got)
	}
}
