package healthprobe

import (
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
)

func env(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func TestRunPassesOnlyOnTheLivenessRoutesOK(t *testing.T) {
	status := http.StatusOK
	var asked string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = r.URL.Path
		w.WriteHeader(status)
	}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")

	if err := Run(env(map[string]string{"JD_ADDR": addr})); err != nil {
		t.Fatalf("a serving backend is unhealthy: %v", err)
	}
	if asked != "/healthz" {
		t.Fatalf("the probe asked %q, want /healthz", asked)
	}
	status = http.StatusServiceUnavailable
	if err := Run(env(map[string]string{"JD_ADDR": addr})); err == nil {
		t.Fatal("a 503 passed the health check")
	}
	srv.Close()
	if err := Run(env(map[string]string{"JD_ADDR": addr})); err == nil {
		t.Fatal("a backend that is not listening passed the health check")
	}
}

func TestTargetFollowsTheConfiguredAddress(t *testing.T) {
	for name, tc := range map[string]struct {
		env  map[string]string
		want string
	}{
		"default":                {nil, "http://127.0.0.1:8080/healthz"},
		"configured":             {map[string]string{"JD_ADDR": "127.0.0.1:18080"}, "http://127.0.0.1:18080/healthz"},
		"wildcard bind":          {map[string]string{"JD_ADDR": "0.0.0.0:9000"}, "http://127.0.0.1:9000/healthz"},
		"name before the rename": {map[string]string{"VPSD_ADDR": "127.0.0.1:7000"}, "http://127.0.0.1:7000/healthz"},
		"agent mode serves TLS":  {map[string]string{"JD_ADDR": "127.0.0.1:8443", "JD_AGENT_MODE": "true"}, "https://127.0.0.1:8443/healthz"},
		"agent mode off":         {map[string]string{"JD_AGENT_MODE": "false"}, "http://127.0.0.1:8080/healthz"},
	} {
		if got, _ := Target(env(tc.env)); got != tc.want {
			t.Errorf("%s: target = %s, want %s", name, got, tc.want)
		}
	}
}

func TestAgentModeAcceptsTheSelfSignedCertificate(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "https://")
	if err := Run(env(map[string]string{"JD_ADDR": addr, "JD_AGENT_MODE": "true"})); err != nil {
		t.Fatalf("an agent serving TLS is unhealthy: %v", err)
	}
}

// The check is cheap because it links nothing of the server's. One import of
// an internal package that grows would quietly put the cost back.
func TestTheHealthCheckLinksOnlyTheStandardLibrary(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "-f", "{{if not .Standard}}{{.ImportPath}}{{end}}",
		"github.com/Wayy01/Just-Dashboard/backend/cmd/healthcheck").Output()
	if err != nil {
		t.Skipf("go list is not available here: %v", err)
	}
	allowed := map[string]bool{
		"github.com/Wayy01/Just-Dashboard/backend/cmd/healthcheck":      true,
		"github.com/Wayy01/Just-Dashboard/backend/internal/healthprobe": true,
	}
	for _, pkg := range strings.Fields(string(out)) {
		if !allowed[pkg] {
			t.Errorf("cmd/healthcheck links %s; it must stay on the standard library", pkg)
		}
	}
}
