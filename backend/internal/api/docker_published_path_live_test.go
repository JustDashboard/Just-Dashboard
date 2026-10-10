package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/netpath"
	"github.com/go-chi/chi/v5"
)

// TestLivePublishedPathOnThisHost assembles the inbound path of the first
// published TCP port on the actual Engine, through the real handler and the
// host's real firewall, iptables and gateway readers. It only reads: no
// packet is sent, no rule or container changes. Run the compiled binary as
// root so iptables can be listed, as the dashboard's backend does.
func TestLivePublishedPathOnThisHost(t *testing.T) {
	if os.Getenv("JD_PUBLISHED_PATH_LIVE") != "1" {
		t.Skip("set JD_PUBLISHED_PATH_LIVE=1 to read a published port's path on this host")
	}
	s := testServer(t)
	original := s.modules.docker
	s.modules.docker = dockerx.New("unix:///var/run/docker.sock")
	t.Cleanup(func() { _ = s.modules.docker.Close(); s.modules.docker = original })
	containers, err := s.modules.docker.ListContainers(t.Context(), false)
	if err != nil {
		t.Fatal(err)
	}
	var target dockerx.Container
	var port dockerx.PortExposure
	for _, c := range containers {
		for _, p := range c.Exposure {
			if p.HostPort > 0 && p.Protocol == "tcp" && !p.IPv6 && (port.HostPort == 0 || p.Scope == dockerx.ScopeAll && port.Scope != dockerx.ScopeAll) {
				target, port = c, p
			}
		}
	}
	if port.HostPort == 0 {
		t.Skip("no container publishes a TCP port on this host")
	}
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := &httpx.Principal{User: &auth.User{ID: 1, Username: "tester"}, Role: auth.RoleAdmin, Kind: "session", IP: "127.0.0.1"}
			next.ServeHTTP(w, r.WithContext(httpx.WithPrincipal(r.Context(), p)))
		})
	})
	s.mountDockerRoutes(router)
	request := httptest.NewRequest(http.MethodGet, "/docker/containers/"+target.ID+"/published/"+strconv.Itoa(port.HostPort)+"?protocol=tcp&family=inet", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("path: %d %s", response.Code, response.Body.String())
	}
	var result netpath.Result
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	t.Logf("published port %s:%d of %s (euid %d)", port.HostIP, port.HostPort, target.Name, os.Geteuid())
	seen := map[string]bool{}
	for _, e := range result.Evidence {
		seen[e.ID] = true
		t.Logf("%-12s %-9s %-14s %s", e.ID, e.Basis, e.State, e.Summary)
		for _, f := range e.Facts {
			t.Logf("    %s: %s", f.Label, f.Value)
		}
	}
	for _, id := range []string{"publication", "listener", "dnat", "docker-user", "firewall", "gateway", "proxy", "provider", "external"} {
		if !seen[id] {
			t.Fatalf("layer %s missing", id)
		}
	}
	for _, e := range result.Evidence {
		if e.ID == "provider" && e.Basis != netpath.Unknown {
			t.Fatalf("provider policy must stay unknown: %+v", e)
		}
		if e.ID == "dnat" && os.Geteuid() == 0 && e.Basis != netpath.Observed {
			t.Fatalf("as root, Docker's nat chain is readable: %+v", e)
		}
	}
	t.Logf("comparison: %s", result.Comparison)
}
