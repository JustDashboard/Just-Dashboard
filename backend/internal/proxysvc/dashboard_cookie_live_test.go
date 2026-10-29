package proxysvc

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// What a browser signed in to the dashboard sends a deployment on the
// dashboard's own name: the dashboard's session, current and legacy, between
// the application's own cookies — and what the application must be sent.
const (
	routeVisitorCookies = "theme=dark; __Host-jd_session=dashboard-secret; app_session=keep; vpsd_session=legacy-secret"
	routeKeptCookies    = "theme=dark; app_session=keep"
)

// The host's real nginx, from a private prefix, with a deployment route on
// the dashboard's own name and one on another: the first forwards every
// cookie but the dashboard's, to the application and to its sign-in check
// alike; the second forwards the request untouched.
func TestLiveNginxRouteOnTheDashboardsNameDropsItsSession(t *testing.T) {
	root := liveNginx(t)
	seen := make(chan string, 16)
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "cookie=%s", strings.Join(r.Header.Values("Cookie"), "+"))
	}))
	defer app.Close()
	check := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- strings.Join(r.Header.Values("Cookie"), "+")
		w.Header().Set("Remote-User", "alice")
		w.WriteHeader(http.StatusOK)
	}))
	defer check.Close()
	service := New(root, filepath.Join(root, "Caddyfile")).WithDashboardHost("dash.example.test")
	port := freePort(t)
	own := service.onDashboardName(DeploymentRoute{Name: "just-dashboard-env-51.conf", Domains: []string{"dash.example.test"}, Upstream: app.URL,
		ForwardAuth: &SiteForwardAuth{Provider: "authelia", Verify: check.URL + "/api/authz/auth-request", SignIn: "https://auth.example.test/"}})
	other := service.onDashboardName(DeploymentRoute{Name: "just-dashboard-env-52.conf", Domains: []string{"app.example.test"}, Upstream: app.URL})
	if len(own.DropCookies) == 0 || len(other.DropCookies) != 0 {
		t.Fatalf("drops: own %v, other %v", own.DropCookies, other.DropCookies)
	}
	for _, route := range []DeploymentRoute{own, other} {
		spec := deploymentSiteSpec(route, "")
		spec.Maintenance, spec.AccessLog = nil, false
		installSite(t, root, spec, port)
	}
	if result := runValidator(context.Background(), "nginx", "-t"); !result.Valid {
		t.Fatalf("nginx refused the routes: %s", result.Output)
	}
	startNginx(t, root)

	response, body := siteGet(t, port, "dash.example.test", "/", "Cookie", routeVisitorCookies)
	if response.StatusCode != http.StatusOK || body != "cookie="+routeKeptCookies {
		t.Fatalf("the route on the dashboard's name = %d %q", response.StatusCode, body)
	}
	select {
	case got := <-seen:
		if got != routeKeptCookies {
			t.Fatalf("the sign-in check was sent %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the sign-in check was never asked")
	}
	t.Logf("nginx forwarded %q as %q", routeVisitorCookies, body)
	if response, body := siteGet(t, port, "dash.example.test", "/", "Cookie", "__Host-jd_session=dashboard-secret"); response.StatusCode != http.StatusOK || body != "cookie=" {
		t.Fatalf("the dashboard's cookie alone = %d %q", response.StatusCode, body)
	}
	<-seen
	if response, body := siteGet(t, port, "app.example.test", "/", "Cookie", routeVisitorCookies); response.StatusCode != http.StatusOK || body != "cookie="+routeVisitorCookies {
		t.Fatalf("the route on another name = %d %q", response.StatusCode, body)
	}
}

// A real Caddy ingress, as TestLiveDockerCaddyServesRouteRules runs one: a
// route on the dashboard's own name, in front of an application that echoes
// what it was sent, forwards every cookie but the dashboard's, and a route on
// another name forwards them all.
func TestLiveDockerCaddyRouteOnTheDashboardsNameDropsItsSession(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 for isolated Docker ingress test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	docker := func(args ...string) string {
		t.Helper()
		raw, err := hostexec.Command(ctx, "docker", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("Docker fixture command failed: %v: %s", err, raw)
		}
		return strings.TrimSpace(string(raw))
	}
	dir := t.TempDir()
	source := filepath.Join(dir, "Caddyfile")
	if err := os.WriteFile(source, []byte("# fixture\n"), 0644); err != nil {
		t.Fatal(err)
	}
	network := fmt.Sprintf("jd-ingress-cookie-%d", time.Now().UnixNano())
	docker("network", "create", network)
	defer hostexec.Command(context.Background(), "docker", "network", "rm", network).Run()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	httpPort := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	id := docker("run", "-d", "--network", network, "-p", fmt.Sprintf("127.0.0.1:%d:80", httpPort),
		"-v", source+":/etc/caddy/Caddyfile:ro", "-v", "/config", "-v", "/data", "caddy:2-alpine")
	defer hostexec.Command(context.Background(), "docker", "rm", "-f", "-v", id).Run()
	// traefik/whoami answers with every header it was sent.
	app := docker("run", "-d", "-p", "127.0.0.1::80", "traefik/whoami:v1.11")
	defer hostexec.Command(context.Background(), "docker", "rm", "-f", "-v", app).Run()
	upstream := "http://" + docker("port", app, "80/tcp")
	edge := &dockerCaddy{ID: id, Name: "fixture", Source: source, Identity: "fixture-persistent-storage"}
	for i := 0; i < 40; i++ {
		if err := edge.configurationSynced(ctx); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	service := New(dir, source).WithDashboardHost("dash.example.test")
	for _, route := range []DeploymentRoute{
		{Name: "just-dashboard-env-53.conf", Domains: []string{"dash.example.test"}, Upstream: upstream},
		{Name: "just-dashboard-env-54.conf", Domains: []string{"app.example.test"}, Upstream: upstream},
	} {
		if result, err := service.applyDockerCaddyRoute(ctx, edge, route); err != nil || !result.Verified {
			t.Fatalf("apply %s = %+v %v; fixture log: %s", route.Name, result, err, docker("logs", id))
		}
	}
	client := &http.Client{Timeout: 10 * time.Second}
	sent := func(host string) string {
		t.Helper()
		var body string
		for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(200 * time.Millisecond) {
			request, _ := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/", httpPort), nil)
			request.Host = host
			request.Header.Set("Cookie", routeVisitorCookies)
			response, err := client.Do(request)
			if err == nil {
				raw, _ := io.ReadAll(response.Body)
				response.Body.Close()
				body = string(raw)
				if response.StatusCode == http.StatusOK {
					break
				}
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s did not answer: %v %s", host, err, body)
			}
		}
		for _, line := range strings.Split(body, "\n") {
			if value, ok := strings.CutPrefix(strings.TrimSpace(line), "Cookie: "); ok {
				return value
			}
		}
		return ""
	}
	if got := sent("dash.example.test"); got != routeKeptCookies {
		t.Fatalf("the route on the dashboard's name sent the application %q", got)
	}
	t.Logf("Caddy forwarded %q as %q", routeVisitorCookies, routeKeptCookies)
	if got := sent("app.example.test"); got != routeVisitorCookies {
		t.Fatalf("the route on another name sent the application %q", got)
	}
}
