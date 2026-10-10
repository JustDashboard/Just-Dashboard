package api

import (
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

// The egress routes, who may call each and which spend the destructive
// budget: everything that can withdraw a path some traffic is using.
var egressRoutes = map[string]string{
	"GET /network/egress":                      "read",
	"GET /network/egress/simulations/{sim}":    "read",
	"GET /network/egress/{id}/events":          "read",
	"GET /network/egress/{id}/simulations":     "read",
	"POST /network/egress":                     "system.admin",
	"PUT /network/egress/{id}":                 "system.admin",
	"POST /network/egress/{id}/simulate":       "system.admin",
	"POST /network/egress/{id}/automation/off": "system.admin",
	"DELETE /network/egress/{id}":              "destructive",
	"POST /network/egress/{id}/enable":         "destructive",
	"POST /network/egress/{id}/disable":        "destructive",
	"POST /network/egress/{id}/switch":         "destructive",
	"POST /network/egress/{id}/automation/on":  "destructive",
}

func egressRouter(t *testing.T, role auth.Role, withModule bool) (*Server, chi.Router) {
	t.Helper()
	s := testServer(t)
	orig := s.modules.network
	t.Cleanup(func() { s.modules.network = orig })
	s.modules.network = nil
	if withModule {
		dir := t.TempDir()
		s.modules.network = netx.New(netx.Options{Paths: netx.Paths{
			Dir: filepath.Join(dir, "network"), Sysctl: filepath.Join(dir, "sysctl.conf"), Unit: filepath.Join(dir, "unit"),
		}, DB: s.Store.DB})
	}
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			// The destructive budget is per principal; a test spends a
			// separate one per route by naming the user.
			name := req.Header.Get("X-Test-User")
			if name == "" {
				name = "operator"
			}
			p := &httpx.Principal{User: &auth.User{ID: 1, Username: name}, Role: role, Kind: "session", IP: "127.0.0.1"}
			next.ServeHTTP(w, req.WithContext(httpx.WithPrincipal(req.Context(), p)))
		})
	})
	r.Use(httpx.AuditMutations(s.Audit))
	r.Route("/network", s.mountNetworkEgressRoutes)
	return s, r
}

func egressPath(pattern string) string {
	return strings.NewReplacer("{id}", "41", "{sim}", "0123456789ab").Replace(pattern)
}

func TestEgressRoutesAreExactlyTheContract(t *testing.T) {
	_, r := egressRouter(t, auth.RoleAdmin, false)
	var got, want []string
	if err := chi.Walk(r, func(method, pattern string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		got = append(got, method+" "+pattern)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for k := range egressRoutes {
		want = append(want, k)
	}
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("routes:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestEveryEgressMutationIsRefusedBelowItsCapabilityBeforeTheModule(t *testing.T) {
	for _, role := range []auth.Role{auth.RoleReadOnly, auth.RoleLimited} {
		// Mutations must be refused before the module, which is absent
		// there: reaching it would panic.
		_, reads := egressRouter(t, role, true)
		_, writes := egressRouter(t, role, false)
		for route, need := range egressRoutes {
			method, pattern, _ := strings.Cut(route, " ")
			r := writes
			if need == "read" {
				r = reads
			}
			rec := gwDo(r, method, egressPath(pattern), `{}`)
			if need == "read" {
				if rec.Code == http.StatusForbidden {
					t.Errorf("%s as %s was refused: %s", route, role, rec.Body)
				}
				continue
			}
			if rec.Code != http.StatusForbidden {
				t.Errorf("%s as %s: %d %s", route, role, rec.Code, rec.Body)
			}
		}
	}
}

// Only the routes that can withdraw a path spend the destructive budget: a
// burst of them runs it out, while the administrator routes never do.
func TestEgressPathWithdrawalsAreDestructive(t *testing.T) {
	_, r := egressRouter(t, auth.RoleAdmin, true)
	for route, need := range egressRoutes {
		if need == "read" {
			continue
		}
		method, pattern, _ := strings.Cut(route, " ")
		limited := false
		for i := 0; i < 60 && !limited; i++ {
			req := httptest.NewRequest(method, egressPath(pattern), strings.NewReader(`{"action":"failover"}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Test-User", route)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if rec.Code == http.StatusTooManyRequests {
				limited = true
			}
		}
		if limited != (need == "destructive") {
			t.Errorf("%s: destructive budget spent = %v, want %v", route, limited, need == "destructive")
		}
	}
}

func TestEgressMutationsAreAuditedAndMapModuleRefusals(t *testing.T) {
	s, r := egressRouter(t, auth.RoleAdmin, true)
	rec := gwDo(r, http.MethodPost, "/network/egress", `{"name":"x","policy":{"kind":"all"},"members":[{"kind":"gateway","gateway":"192.0.2.1","device":"eth0"}],"probes":[{"kind":"icmp","target":"1.1.1.1"}]}`)
	if code, msg := gwErr(rec); rec.Code != http.StatusBadRequest || code != "bad_request" || !strings.Contains(msg, "2 to 8 members") {
		t.Fatalf("one member = %d %s %s", rec.Code, code, msg)
	}
	rec = gwDo(r, http.MethodPost, "/network/egress", `{"name":"x","policy":{"kind":"rule","fwmark":"0x200"},"members":[{"kind":"gateway","gateway":"192.0.2.1","device":"eth0"},{"kind":"gateway","gateway":"192.0.2.2","device":"eth1"}],"probes":[{"kind":"icmp","target":"1.1.1.1"}]}`)
	if code, _ := gwErr(rec); rec.Code != http.StatusConflict || code != "would_lock_you_out" {
		t.Fatalf("member mark = %d %s", rec.Code, rec.Body)
	}
	rec = gwDo(r, http.MethodPost, "/network/egress/41/switch", `{"action":"sideways"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing group = %d %s", rec.Code, rec.Body)
	}
	rec = gwDo(r, http.MethodPost, "/network/egress/41/automation/on", ``)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("automation of a missing group = %d %s", rec.Code, rec.Body)
	}
	rec = gwDo(r, http.MethodGet, "/network/egress", ``)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"groups":[]`) || !strings.Contains(rec.Body.String(), `"capacity":6`) {
		t.Fatalf("view = %d %s", rec.Code, rec.Body)
	}
	var actions string
	if err := s.Store.DB.QueryRow(`SELECT group_concat(action) FROM audit_log`).Scan(&actions); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"network.egress.create", "network.egress.switch", "network.egress.automation.on"} {
		if !strings.Contains(actions, want) {
			t.Errorf("%s was not audited: %s", want, actions)
		}
	}
}

func TestEgressChangesAcceptPendingApplyButSimulationAndAutomationDoNot(t *testing.T) {
	for path, want := range map[string]bool{
		"/network/egress":                 true,
		"/network/egress/3":               true,
		"/network/egress/3/enable":        true,
		"/network/egress/3/disable":       true,
		"/network/egress/3/switch":        true,
		"/network/egress/3/simulate":      false,
		"/network/egress/3/automation/on": false,
		"/network/egressx":                false,
	} {
		if got := supportsPendingNetworkApply(path); got != want {
			t.Errorf("%s: pending = %v, want %v", path, got, want)
		}
	}
}
