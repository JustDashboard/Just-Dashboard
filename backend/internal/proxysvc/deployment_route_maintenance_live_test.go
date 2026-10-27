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
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// maintenanceAnswer is one request as the proof in a pull request reads it.
func maintenanceAnswer(t *testing.T, label string, response *http.Response, body string) {
	t.Helper()
	line := strings.TrimSpace(strings.SplitN(body, "\n", 2)[0])
	if title := strings.Index(body, "<title>"); title >= 0 {
		line = body[title:min(len(body), strings.Index(body, "</title>")+len("</title>"))]
	}
	retry := ""
	if value := response.Header.Get("Retry-After"); value != "" {
		retry = "  Retry-After: " + value
	}
	t.Logf("%-44s → %s%s  %s", label, response.Status, retry, line)
}

// What the host's real nginx does with a deployment route's maintenance and
// error pages, switched the way the Domains settings switch it: the file is
// written and removed under a running nginx that is never reloaded. A
// visitor gets the maintenance page with a 503 and the route's Retry-After
// while an address let past it reaches the application; off again, the
// application answers; and with the application down the route's own 502
// page answers, as its own 404 replaces the application's.
func TestLiveDeploymentRouteMaintenanceOnNginx(t *testing.T) {
	root := liveNginx(t)
	ctx := context.Background()
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			http.Error(w, "the application's own 404", http.StatusNotFound)
			return
		}
		fmt.Fprint(w, "application answer")
	}))
	defer app.Close()
	service := New(root, filepath.Join(root, "Caddyfile"))
	route := DeploymentRoute{
		Name: "just-dashboard-env-6.conf", Domains: []string{"app.example.test"}, Upstream: app.URL,
		// The test's own address; the visitor below comes from 127.0.0.2.
		Maintenance: DeploymentMaintenance{RetryAfter: 120, BypassFrom: []string{"127.0.0.1"}},
		ErrorPages:  []int{404, 502}, InterceptErrors: true,
	}
	port := freePort(t)
	// An unprivileged nginx cannot open the route's request record under
	// /var/log; the route is otherwise installed as activation renders it,
	// with the pages activation writes beside it.
	spec := service.deploymentSpec(route)
	spec.AccessLog = false
	if err := service.writeMissingPages(spec); err != nil {
		t.Fatal(err)
	}
	if _, err := service.WriteDeploymentPage(ctx, route.Name, "502", "<html><title>Our own 502: back in a minute</title></html>\n"); err != nil {
		t.Fatal(err)
	}
	installed := installSite(t, root, spec, port)
	// The ACME webroot is the dashboard's, which an unprivileged test cannot
	// write a token into; it gets one of its own, as certbot would.
	webroot := filepath.Join(root, "acme")
	if err := os.MkdirAll(filepath.Join(webroot, ".well-known", "acme-challenge"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(webroot, ".well-known", "acme-challenge", "token"), []byte("token.thumbprint"), 0o644); err != nil {
		t.Fatal(err)
	}
	installed = strings.ReplaceAll(installed, "root "+deploymentACMEWebroot+";", "root "+webroot+";")
	if err := os.WriteFile(filepath.Join(root, "sites-available", route.Name), []byte(installed), 0o644); err != nil {
		t.Fatal(err)
	}
	if result := runValidator(ctx, "nginx", "-t"); !result.Valid {
		t.Fatalf("nginx refused the route: %s", result.Output)
	}
	startNginx(t, root)

	get := func(from, method, path string) (*http.Response, string) {
		t.Helper()
		dialer := &net.Dialer{LocalAddr: &net.TCPAddr{IP: net.ParseIP(from)}, Timeout: 2 * time.Second}
		client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{DialContext: dialer.DialContext, DisableKeepAlives: true}}
		request, _ := http.NewRequest(method, "http://127.0.0.1:"+strconv.Itoa(port)+path, nil)
		request.Host = "app.example.test"
		var response *http.Response
		var err error
		for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(50 * time.Millisecond) {
			if response, err = client.Do(request); err == nil || time.Now().After(deadline) {
				break
			}
		}
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		maintenanceAnswer(t, method+" "+path+" from "+from, response, string(body))
		return response, string(body)
	}
	maintenancePage, _ := ShippedSitePage("maintenance")
	switchTo := func(on bool) {
		t.Helper()
		state, err := service.SetDeploymentMaintenance(ctx, route.Name, on)
		if err != nil || state.On != on || !state.Ready {
			t.Fatalf("switch %t = %+v, %v", on, state, err)
		}
		t.Logf("maintenance switched %s (no reload)", map[bool]string{true: "on", false: "off"}[on])
	}

	if response, body := get("127.0.0.2", http.MethodGet, "/"); response.StatusCode != 200 || body != "application answer" {
		t.Fatalf("before maintenance = %d %q", response.StatusCode, body)
	}
	switchTo(true)
	response, body := get("127.0.0.2", http.MethodGet, "/pricing")
	if response.StatusCode != 503 || response.Header.Get("Retry-After") != "120" || body != maintenancePage {
		t.Fatalf("a visitor in maintenance = %d %q %q", response.StatusCode, response.Header.Get("Retry-After"), body)
	}
	if response, _ := get("127.0.0.2", http.MethodPost, "/form"); response.StatusCode != 503 {
		t.Fatalf("a POST in maintenance = %d", response.StatusCode)
	}
	if response, body := get("127.0.0.1", http.MethodGet, "/"); response.StatusCode != 200 || body != "application answer" {
		t.Fatalf("an address let past = %d %q", response.StatusCode, body)
	}
	if response, body := get("127.0.0.2", http.MethodGet, "/.well-known/acme-challenge/token"); response.StatusCode != 200 || body != "token.thumbprint" {
		t.Fatalf("the ACME challenge in maintenance = %d %q", response.StatusCode, body)
	}
	switchTo(false)
	if response, body := get("127.0.0.2", http.MethodGet, "/"); response.StatusCode != 200 || body != "application answer" {
		t.Fatalf("after maintenance = %d %q", response.StatusCode, body)
	}
	notFound, _ := ShippedSitePage("404")
	if response, body := get("127.0.0.2", http.MethodGet, "/missing"); response.StatusCode != 404 || body != notFound {
		t.Fatalf("the application's 404 = %d %q", response.StatusCode, body)
	}
	app.Close()
	if response, body := get("127.0.0.2", http.MethodGet, "/"); response.StatusCode != 502 || !strings.Contains(body, "Our own 502") {
		t.Fatalf("with the application down = %d %q", response.StatusCode, body)
	}
	if current, _ := os.ReadFile(filepath.Join(root, "sites-available", route.Name)); string(current) != installed {
		t.Fatal("switching maintenance changed the route")
	}
}

// The same on a real Caddy, as the Docker ingress runs it: the switch is a
// file inside the ingress that Caddy reads on every request, so it acts on
// a running Caddy and survives the route being written again by the next
// release; an address let past reaches the application while a visitor
// from anywhere else gets the maintenance page; the application's own 404
// and Caddy's own 502 get the route's pages.
func TestLiveDockerCaddyServesMaintenanceAndErrorPages(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 for isolated Docker ingress test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
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
	network := fmt.Sprintf("jd-ingress-maintenance-%d", time.Now().UnixNano())
	docker("network", "create", network)
	defer hostexec.Command(context.Background(), "docker", "network", "rm", network).Run()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	binding := fmt.Sprintf("127.0.0.1:%d:80", listener.Addr().(*net.TCPAddr).Port)
	listener.Close()
	id := docker("run", "-d", "--network", network, "-p", binding, "-v", source+":/etc/caddy/Caddyfile:ro", "-v", "/config", "-v", "/data", "caddy:2-alpine")
	defer hostexec.Command(context.Background(), "docker", "rm", "-f", "-v", id).Run()
	// The application says who the ingress says asked, so the test knows
	// which address it reaches the ingress from, and has a 404 of its own.
	appConfig := ":80 {\n\trespond /missing \"the application's own 404\" 404\n\trespond \"application answer for {header.X-Forwarded-For}\"\n}\n"
	app := docker("run", "-d", "-p", "127.0.0.1::80", "caddy:2-alpine", "sh", "-c",
		"printf '%s' \"$0\" > /tmp/Caddyfile && exec caddy run --config /tmp/Caddyfile --adapter caddyfile", appConfig)
	defer hostexec.Command(context.Background(), "docker", "rm", "-f", "-v", app).Run()
	published := func(id string) string { return strings.TrimPrefix(docker("port", id, "80/tcp"), "127.0.0.1:") }
	edge := &dockerCaddy{ID: id, Name: "fixture", Source: source, Identity: "fixture-persistent-storage"}
	endpoint := "http://127.0.0.1:" + published(id)
	client := &http.Client{Timeout: 10 * time.Second}
	get := func(label, path string) (*http.Response, string) {
		t.Helper()
		var response *http.Response
		var err error
		for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(100 * time.Millisecond) {
			request, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+path, nil)
			request.Host = "maintenance.example.test"
			if response, err = client.Do(request); err == nil || time.Now().After(deadline) {
				break
			}
		}
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		maintenanceAnswer(t, "GET "+path+" "+label, response, string(body))
		return response, string(body)
	}
	for i := 0; i < 40; i++ {
		if err := edge.configurationSynced(ctx); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	service := New(dir, source)
	route := DeploymentRoute{
		Name: "just-dashboard-env-44.conf", Domains: []string{"maintenance.example.test"},
		Upstream:    "http://127.0.0.1:" + published(app),
		Maintenance: DeploymentMaintenance{RetryAfter: 120},
		ErrorPages:  []int{404, 502}, InterceptErrors: true,
	}
	apply := func() string {
		t.Helper()
		if result, err := service.applyDockerCaddyRoute(ctx, edge, route); err != nil || !result.Verified {
			t.Fatalf("apply=%+v error=%v; fixture log: %s", result, err, docker("logs", id))
		}
		snapshot, err := edge.snapshot(ctx, route.Name)
		if err != nil {
			t.Fatal(err)
		}
		return snapshot.Content
	}
	installed := apply()
	switchTo := func(on bool) {
		t.Helper()
		state, err := edge.setMaintenance(ctx, route.Name, on)
		if err != nil || state.On != on || !state.Ready || state.Driver != "caddy" {
			t.Fatalf("switch %t = %+v, %v", on, state, err)
		}
		t.Logf("maintenance switched %s (no reload)", map[bool]string{true: "on", false: "off"}[on])
	}

	response, body := get("from this host", "/")
	host, found := strings.CutPrefix(body, "application answer for ")
	if response.StatusCode != 200 || !found || net.ParseIP(host) == nil {
		t.Fatalf("before maintenance = %d %q", response.StatusCode, body)
	}
	switchTo(true)
	maintenancePage, _ := ShippedSitePage("maintenance")
	response, body = get("from this host", "/pricing")
	if response.StatusCode != 503 || response.Header.Get("Retry-After") != "120" || body != maintenancePage {
		t.Fatalf("a visitor in maintenance = %d %q %q", response.StatusCode, response.Header.Get("Retry-After"), body)
	}
	switchTo(false)
	if response, body := get("from this host", "/"); response.StatusCode != 200 || !strings.HasPrefix(body, "application answer") {
		t.Fatalf("after maintenance = %d %q", response.StatusCode, body)
	}

	// The next release lets this host past. The switch, turned on before it
	// is written, is still on after: it is not the route's.
	switchTo(true)
	route.Maintenance.BypassFrom = []string{host}
	if again := apply(); again == installed {
		t.Fatal("the route with an address let past is the route without one")
	}
	if state, err := edge.maintenance(ctx, route.Name); err != nil || !state.On {
		t.Fatalf("the switch after the route was written again = %+v, %v", state, err)
	}
	if response, body := get("from "+host+", let past", "/"); response.StatusCode != 200 || !strings.HasPrefix(body, "application answer") {
		t.Fatalf("an address let past = %d %q", response.StatusCode, body)
	}
	// A visitor from anywhere else: the application's own container, asking
	// the ingress over the network they share.
	ingress := docker("inspect", "-f", "{{range .NetworkSettings.Networks}}{{.IPAddress}} {{end}}", id)
	var answer string
	for _, address := range strings.Fields(ingress) {
		// The request as it goes on the wire: busybox's wget keeps the
		// headers of an answer it calls an error to itself.
		raw, _ := hostexec.Command(ctx, "docker", "exec", app, "sh", "-c",
			`printf 'GET / HTTP/1.0\r\nHost: maintenance.example.test\r\n\r\n' | nc -w 5 "$0" 80`, address).CombinedOutput()
		if strings.HasPrefix(string(raw), "HTTP/1.") {
			answer = string(raw)
			break
		}
	}
	status, _, _ := strings.Cut(answer, "\r\n")
	retry := ""
	if at := strings.Index(answer, "Retry-After: "); at >= 0 {
		retry, _, _ = strings.Cut(answer[at:], "\r\n")
	}
	t.Logf("%-44s → %s  %s", "GET / from the application's container", status, retry)
	if !strings.HasPrefix(status, "HTTP/1.0 503") || retry != "Retry-After: 120" || !strings.Contains(answer, "<title>Down for maintenance</title>") {
		t.Fatalf("a visitor not let past = %q", answer)
	}
	switchTo(false)

	notFound, _ := ShippedSitePage("404")
	if response, body := get("from this host", "/missing"); response.StatusCode != 404 || body != notFound {
		t.Fatalf("the application's 404 = %d %q", response.StatusCode, body)
	}
	pages, _ := dockerCaddyPagesDir(route.Name)
	if err := edge.write(ctx, pages+"/502.html", "<html><title>Our own 502: back in a minute</title></html>\n"); err != nil {
		t.Fatal(err)
	}
	docker("stop", app)
	if response, body := get("from this host", "/"); response.StatusCode != 502 || !strings.Contains(body, "Our own 502") {
		t.Fatalf("with the application down = %d %q", response.StatusCode, body)
	}
}
