package proxysvc

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
	"golang.org/x/crypto/bcrypt"
)

// A route with a service domain behind a real Caddy: the release's name is
// answered by the release's container and the service domain by the
// service's, each found by its loopback publication; the route's password,
// response header and maintenance switch hold on both names; the inventory
// lists both containers; and a service container that moved, or a Caddy
// taken off its network, is repaired by one maintenance pass while the
// release's name keeps answering. Every container and network is the test's
// own, published on loopback, and removed with it. Run it against a Docker
// daemon of its own (Docker-in-Docker), never one with a real ingress.
func TestLiveDockerCaddySendsServiceDomainsToTheirOwnContainers(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 for isolated Docker ingress test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
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
	if err := os.WriteFile(source, []byte("# fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().UnixNano()
	ingressNetwork := fmt.Sprintf("jd-ingress-services-%d", stamp)
	docker("network", "create", ingressNetwork)
	defer hostexec.Command(context.Background(), "docker", "network", "rm", ingressNetwork).Run()
	// The release's network and the service's own, as a stack whose
	// services are on networks the release is not: Caddy has to join both.
	appNetwork := fmt.Sprintf("jd-services-app-%d", stamp)
	docker("network", "create", appNetwork)
	defer hostexec.Command(context.Background(), "docker", "network", "rm", appNetwork).Run()
	serviceNetwork := fmt.Sprintf("jd-services-svc-%d", stamp)
	docker("network", "create", "--subnet", "10.213.7.0/24", serviceNetwork)
	defer hostexec.Command(context.Background(), "docker", "network", "rm", serviceNetwork).Run()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ingressPort := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	id := docker("run", "-d", "--network", ingressNetwork, "-p", fmt.Sprintf("127.0.0.1:%d:80", ingressPort),
		"-v", source+":/etc/caddy/Caddyfile:ro", "-v", "/config", "-v", "/data", "caddy:2-alpine")
	defer hostexec.Command(context.Background(), "docker", "rm", "-f", "-v", id).Run()
	edge := &dockerCaddy{ID: id, Name: "fixture", Source: source, Identity: "fixture-persistent-storage"}
	for i := 0; i < 40; i++ {
		if err := edge.configurationSynced(ctx); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	app := func(network, answer string) (string, string) {
		container := docker("run", "-d", "--network", network, "-p", "127.0.0.1::80", "caddy:2-alpine",
			"caddy", "respond", "--listen", ":80", "--header", "X-Route: application", answer)
		published := "http://127.0.0.1:" + strings.TrimPrefix(docker("port", container, "80/tcp"), "127.0.0.1:")
		return container, published
	}
	release, releaseUpstream := app(appNetwork, "release answer")
	defer hostexec.Command(context.Background(), "docker", "rm", "-f", "-v", release).Run()
	seerr, seerrUpstream := app(serviceNetwork, "seerr answer")
	defer hostexec.Command(context.Background(), "docker", "rm", "-f", "-v", seerr).Run()

	hash, err := bcrypt.GenerateFromPassword([]byte("fixture-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	route := DeploymentRoute{
		Name: "just-dashboard-env-45.conf", Domains: []string{"media-requests.example.test", "media.example.test"},
		Upstream: releaseUpstream, AccessLog: true, Trace: true,
		BasicAuth:       []BasicAuthUser{{Username: "admin", Hash: string(hash)}},
		ResponseHeaders: []HeaderValue{{Name: "X-Route", Value: "route"}},
		HostUpstreams:   []HostUpstream{{Hostnames: []string{"media-requests.example.test"}, Upstream: seerrUpstream}},
	}
	service := New(dir, source)
	result, err := service.applyDockerCaddyRoute(ctx, edge, route)
	if err != nil || !result.Verified {
		t.Fatalf("apply=%+v error=%v; fixture log: %s", result, err, docker("logs", id))
	}
	snapshot, err := edge.snapshot(ctx, route.Name)
	if err != nil {
		t.Fatal(err)
	}
	targets, ok := parseDockerCaddyTargets(snapshot.Content)
	if !ok || len(targets) != 2 || targets[0].ContainerID != fullID(t, docker, release) || targets[1].ContainerID != fullID(t, docker, seerr) ||
		targets[1].Network != serviceNetwork {
		t.Fatalf("written route targets %+v:\n%s", targets, snapshot.Content)
	}
	if want, _, err := edge.routeContent(ctx, route); err != nil || want != snapshot.Content {
		t.Fatalf("rebuilt route differs (%v):\n%s", err, want)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	ask := func(host string, signedIn bool) (int, string, string) {
		t.Helper()
		req, _ := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("http://127.0.0.1:%d/a%%2Fb?q=1", ingressPort), nil)
		req.Host = host
		if signedIn {
			req.SetBasicAuth("admin", "fixture-password")
		}
		res, err := client.Do(req)
		if err != nil {
			return 0, err.Error(), ""
		}
		defer res.Body.Close()
		body, _ := io.ReadAll(res.Body)
		return res.StatusCode, strings.TrimSpace(string(body)), strings.Join(res.Header.Values("X-Route"), ",")
	}
	expect := func(host string, signedIn bool, status int, body string) {
		t.Helper()
		gotStatus, gotBody, header := ask(host, signedIn)
		if gotStatus != status || (body != "" && gotBody != body) {
			t.Fatalf("%s (signed in %v) answered %d %q, want %d %q; ingress log: %s", host, signedIn, gotStatus, gotBody, status, body, docker("logs", "--tail", "20", id))
		}
		if status == http.StatusOK && header != "route" {
			t.Fatalf("%s answered with X-Route %q, want the route's own in place of the application's", host, header)
		}
	}
	expect("media.example.test", true, http.StatusOK, "release answer")
	expect("media-requests.example.test", true, http.StatusOK, "seerr answer")
	expect("MEDIA-REQUESTS.example.test", true, http.StatusOK, "seerr answer")
	// The password is the route's, on the service domain too.
	expect("media.example.test", false, http.StatusUnauthorized, "")
	expect("media-requests.example.test", false, http.StatusUnauthorized, "")

	// The request record carries both names.
	logPath, err := dockerCaddyAccessLogPath(route.Name)
	if err != nil {
		t.Fatal(err)
	}
	record := docker("exec", id, "cat", logPath)
	if !strings.Contains(record, `"host":"media-requests.example.test"`) || !strings.Contains(record, `"host":"media.example.test"`) {
		t.Fatalf("the request record does not list both names:\n%s", record)
	}

	inventory, err := edge.vhosts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	listed := false
	for _, site := range inventory {
		if site.Name == route.Name {
			listed = len(site.Upstreams) == 2 && len(site.ServerNames) == 2 && site.Upstreams[1] == targets[1].upstream()
		}
	}
	if !listed {
		t.Fatalf("the inventory does not list the route's service: %+v", inventory)
	}

	// Maintenance is the route's, so both names get its page.
	if _, err := edge.setMaintenance(ctx, route.Name, true); err != nil {
		t.Fatal(err)
	}
	expect("media.example.test", true, http.StatusServiceUnavailable, "")
	expect("media-requests.example.test", true, http.StatusServiceUnavailable, "")
	if _, err := edge.setMaintenance(ctx, route.Name, false); err != nil {
		t.Fatal(err)
	}
	expect("media-requests.example.test", true, http.StatusOK, "seerr answer")

	// The service's container comes back at another address. The route
	// still names the old one, so the service domain fails while the
	// release's name answers; one pass moves the route to the new address.
	docker("network", "disconnect", serviceNetwork, seerr)
	docker("network", "connect", "--ip", "10.213.7.200", serviceNetwork, seerr)
	if status, _, _ := ask("media-requests.example.test", true); status == http.StatusOK {
		t.Fatal("the service domain answered before the route was repaired")
	}
	expect("media.example.test", true, http.StatusOK, "release answer")
	repaired := map[string]bool{}
	if _, err := edge.reconcile(ctx, func(name string, ok bool) { repaired[name] = ok }); err != nil || !repaired[route.Name] {
		t.Fatalf("maintenance pass = %v, repaired %v", err, repaired)
	}
	expect("media-requests.example.test", true, http.StatusOK, "seerr answer")
	expect("media.example.test", true, http.StatusOK, "release answer")
	path, err := dockerCaddyRoutePath(route.Name)
	if err != nil {
		t.Fatal(err)
	}
	if content, _, err := edge.read(ctx, path); err != nil || !strings.Contains(content, `reverse_proxy "http://10.213.7.200:80"`) {
		t.Fatalf("the repaired route (%v):\n%s", err, content)
	}

	// A Caddy taken off the service's network, as a recreated one is, is
	// joined to it again by the next pass.
	docker("network", "disconnect", serviceNetwork, id)
	if status, _, _ := ask("media-requests.example.test", true); status == http.StatusOK {
		t.Fatal("the service domain answered with the ingress off its network")
	}
	repaired = map[string]bool{}
	if _, err := edge.reconcile(ctx, func(name string, ok bool) { repaired[name] = ok }); err != nil || !repaired[route.Name] {
		t.Fatalf("maintenance pass = %v, repaired %v", err, repaired)
	}
	expect("media-requests.example.test", true, http.StatusOK, "seerr answer")
	// And a pass with nothing to repair leaves the route alone.
	repaired = map[string]bool{}
	if _, err := edge.reconcile(ctx, func(name string, ok bool) { repaired[name] = ok }); err != nil || len(repaired) != 0 {
		t.Fatalf("a quiet pass = %v, repaired %v", err, repaired)
	}
}

// fullID is a container's whole id, which a route's target line records.
func fullID(t *testing.T, docker func(...string) string, container string) string {
	t.Helper()
	return docker("inspect", "--format", "{{.Id}}", container)
}
