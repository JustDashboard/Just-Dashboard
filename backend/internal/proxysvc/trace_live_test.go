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
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accesslog"
	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

var traceparentRe = regexp.MustCompile(`(?i)traceparent: (00-([0-9a-f]{32})-([0-9a-f]{16})-01)`)

const clientTrace, clientSpan = "4bf92f3577b34da6a3ce929d0e0e4736", "00f067aa0ba902b7"

// A real Caddy ingress sends the application a traceparent and records the
// same ids beside the request: a new trace for a request that brought none,
// the client's trace continued — its span recorded as the parent — for one
// that did. traefik/whoami answers with the headers it received, which is
// what the application sees.
func TestLiveDockerCaddySendsAndRecordsATraceparent(t *testing.T) {
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
	network := fmt.Sprintf("jd-ingress-trace-%d", time.Now().UnixNano())
	docker("network", "create", network)
	defer hostexec.Command(context.Background(), "docker", "network", "rm", network).Run()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	id := docker("run", "-d", "--network", network, "-p", fmt.Sprintf("127.0.0.1:%d:80", port),
		"-v", source+":/etc/caddy/Caddyfile:ro", "-v", "/config", "-v", "/data", "caddy:2-alpine")
	defer hostexec.Command(context.Background(), "docker", "rm", "-f", "-v", id).Run()
	app := docker("run", "-d", "-p", "127.0.0.1::80", "traefik/whoami:v1.11")
	defer hostexec.Command(context.Background(), "docker", "rm", "-f", "-v", app).Run()
	edge := &dockerCaddy{ID: id, Name: "fixture", Source: source, Identity: "fixture-persistent-storage"}
	for i := 0; i < 40; i++ {
		if err := edge.configurationSynced(ctx); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !edge.recordsTraces(ctx) {
		t.Fatalf("caddy:2-alpine is %s, which has no log_append", docker("exec", id, "caddy", "version"))
	}
	service := New(dir, source)
	route := DeploymentRoute{
		Name: "just-dashboard-env-44.conf", Domains: []string{"trace.example.test"},
		Upstream:  "http://127.0.0.1:" + strings.TrimPrefix(docker("port", app, "80/tcp"), "127.0.0.1:"),
		AccessLog: true, Trace: true,
	}
	if result, err := service.applyDockerCaddyRoute(ctx, edge, route); err != nil || !result.Verified {
		t.Fatalf("apply=%+v error=%v; fixture log: %s", result, err, docker("logs", id))
	}
	seen := func(traceparent string) []string {
		t.Helper()
		req, _ := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("http://127.0.0.1:%d/orders/7", port), nil)
		req.Host = "trace.example.test"
		if traceparent != "" {
			req.Header.Set("Traceparent", traceparent)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		body, _ := io.ReadAll(res.Body)
		match := traceparentRe.FindStringSubmatch(string(body))
		if res.StatusCode != 200 || match == nil {
			t.Fatalf("the application saw no traceparent: %s\n%s", res.Status, body)
		}
		return match
	}
	fresh := seen("")
	continued := seen("00-" + clientTrace + "-" + clientSpan + "-01")
	if continued[2] != clientTrace || continued[3] == clientSpan {
		t.Fatalf("the client's trace was not continued with a span of the ingress's: %s", continued[1])
	}
	if fresh[2] == clientTrace || fresh[3] != fresh[2][16:] {
		t.Fatalf("a new trace = %s", fresh[1])
	}

	var entries []accesslog.Entry
	for deadline := time.Now().Add(10 * time.Second); len(entries) < 2 && time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		entries = entries[:0]
		for _, line := range strings.Split(docker("exec", id, "cat", "/config/just-dashboard/access/just-dashboard-env-44.log"), "\n") {
			if entry, ok := accesslog.Parse(line); ok {
				entries = append(entries, entry)
			}
		}
	}
	if len(entries) != 2 {
		t.Fatalf("the record holds %d requests", len(entries))
	}
	if entries[0].TraceID != fresh[2] || entries[0].SpanID != fresh[3] || entries[0].ParentSpanID != "" {
		t.Fatalf("the new trace was recorded as %+v, sent as %s", entries[0], fresh[1])
	}
	if entries[1].TraceID != clientTrace || entries[1].SpanID != continued[3] || entries[1].ParentSpanID != clientSpan {
		t.Fatalf("the continued trace was recorded as %+v, sent as %s", entries[1], continued[1])
	}
}

// The host's nginx does the same from the route activation renders, its
// request record redirected to the test's folder because an unprivileged
// nginx cannot open /var/log.
func TestLiveDeploymentRouteTracesOnNginx(t *testing.T) {
	root := liveNginx(t)
	ctx := context.Background()
	received := make(chan string, 4)
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received <- r.Header.Get("Traceparent")
		fmt.Fprint(w, "application answer")
	}))
	defer app.Close()
	service := New(root, filepath.Join(root, "Caddyfile"))
	route := DeploymentRoute{Name: "just-dashboard-env-45.conf", Domains: []string{"trace.example.test"}, Upstream: app.URL, AccessLog: true, Trace: true}
	spec := service.deploymentSpec(route)
	spec.HTTP2 = false
	if err := service.writeMissingPages(spec); err != nil {
		t.Fatal(err)
	}
	content, err := RenderNginx(spec)
	if err != nil {
		t.Fatal(err)
	}
	record := filepath.Join(root, "logs", "trace.access.log")
	port := freePort(t)
	for own, instead := range map[string]string{
		"    listen 80;\n":                   fmt.Sprintf("    listen 127.0.0.1:%d;\n", port),
		"    listen [::]:80;\n":              "",
		nginxAccessLogPath(route.Name) + " ": record + " ",
		nginxErrorLogPath(route.Name):        filepath.Join(root, "logs", "trace.error.log"),
	} {
		content = strings.ReplaceAll(content, own, instead)
	}
	available := filepath.Join(root, "sites-available", route.Name)
	if err := os.WriteFile(available, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(available, filepath.Join(root, "sites-enabled", route.Name)); err != nil {
		t.Fatal(err)
	}
	if result := runValidator(ctx, "nginx", "-t"); !result.Valid {
		t.Fatalf("nginx refused the route: %s\n%s", result.Output, content)
	}
	startNginx(t, root)
	send := func(traceparent string) []string {
		t.Helper()
		request, _ := http.NewRequest("GET", "http://127.0.0.1:"+strconv.Itoa(port)+"/orders/7", nil)
		request.Host = "trace.example.test"
		if traceparent != "" {
			request.Header.Set("Traceparent", traceparent)
		}
		var response *http.Response
		for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(50 * time.Millisecond) {
			if response, err = http.DefaultClient.Do(request); err == nil || time.Now().After(deadline) {
				break
			}
		}
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		got := <-received
		match := traceparentRe.FindStringSubmatch("traceparent: " + got)
		if match == nil {
			t.Fatalf("the application saw traceparent %q", got)
		}
		return match
	}
	fresh := send("")
	continued := send("00-" + clientTrace + "-" + clientSpan + "-01")
	if continued[2] != clientTrace || continued[3] == clientSpan || fresh[2] == clientTrace || fresh[3] != fresh[2][16:] {
		t.Fatalf("sent %s and %s", fresh[1], continued[1])
	}
	var entries []accesslog.Entry
	for deadline := time.Now().Add(5 * time.Second); len(entries) < 2 && time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		raw, _ := os.ReadFile(record)
		entries = entries[:0]
		for _, line := range strings.Split(string(raw), "\n") {
			if entry, ok := accesslog.Parse(line); ok {
				entries = append(entries, entry)
			}
		}
	}
	if len(entries) != 2 || entries[0].TraceID != fresh[2] || entries[0].SpanID != fresh[3] ||
		entries[1].TraceID != clientTrace || entries[1].SpanID != continued[3] || entries[1].ParentSpanID != clientSpan {
		raw, _ := os.ReadFile(record)
		t.Fatalf("the record = %+v from:\n%s", entries, raw)
	}
}
