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
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/accesslog"
	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
	"github.com/Wayy01/Just-Dashboard/backend/internal/tailnetgate"
)

// The tailnet gate's live acceptance, on a real nginx and a real Caddy. What
// stands in for the tailnet is an httptest server on a unix socket speaking
// tailscaled's local API whois, and a client that sends what tailscaled's
// serve sends — X-Forwarded-For with the tailnet address, the tailnet name in
// Host. The route the proxy asks is the dashboard's real whois handler over
// the real local API client. No real tailnet is involved, and the host's own
// tailscaled is never asked anything.

type gateFixture struct {
	whois    *httptest.Server
	app      *httptest.Server
	down     atomic.Bool
	refusals atomic.Int32
}

func newGateFixture(t *testing.T) *gateFixture {
	t.Helper()
	f := &gateFixture{}
	socket := filepath.Join(t.TempDir(), "tailscaled.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	tailscaled := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if f.down.Load() {
			http.Error(w, "tailscaled is restarting", http.StatusServiceUnavailable)
			return
		}
		if r.Host != "local-tailscaled.sock" || r.URL.Path != "/localapi/v0/whois" {
			http.Error(w, "invalid localapi request", http.StatusBadRequest)
			return
		}
		switch r.URL.Query().Get("addr") {
		case "100.101.102.103":
			fmt.Fprint(w, `{"Node":{"Name":"alice-laptop.tail1234.ts.net.","Addresses":["100.101.102.103/32"]},"UserProfile":{"LoginName":"alice@example.com","DisplayName":"Alice Example"}}`)
		case "100.101.102.104":
			fmt.Fprint(w, `{"Node":{"Name":"bob-laptop.tail1234.ts.net.","Addresses":["100.101.102.104/32"]},"UserProfile":{"LoginName":"bob@example.com","DisplayName":"Bob"}}`)
		case "100.64.0.9":
			fmt.Fprint(w, `{"Node":{"Name":"ci.tail1234.ts.net.","Tags":["tag:ci"],"ComputedName":"ci"},"UserProfile":{"LoginName":"tagged-devices","DisplayName":"Tagged Devices"}}`)
		default:
			http.Error(w, "no match for IP:port", http.StatusNotFound)
		}
	}))
	tailscaled.Listener = listener
	tailscaled.Start()
	t.Cleanup(tailscaled.Close)

	handler := &tailnetgate.Handler{
		WhoIs: tailnetgate.NewLocalAPI(nil, socket),
		Access: func(_ context.Context, environmentID int64) ([]string, error) {
			if environmentID == 7 {
				return []string{"Alice@Example.com", "tag:ci"}, nil
			}
			return nil, nil
		},
		Refused: func(context.Context, tailnetgate.Refusal) { f.refusals.Add(1) },
		// Every request asks tailscaled again, so the outage below is met
		// at once rather than after the few seconds an answer is kept.
		TTL: time.Nanosecond,
	}
	mux := http.NewServeMux()
	mux.Handle(tailnetgate.Path, handler)
	f.whois = httptest.NewServer(mux)
	t.Cleanup(f.whois.Close)
	f.app = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		fmt.Fprintf(w, "login=%s|user=%s|for=%s|host=%s|method=%s|body=%s",
			r.Header.Get("X-Tailnet-Login"), r.Header.Get("X-Tailnet-User"), r.Header.Get("X-Forwarded-For"),
			r.Host, r.Method, body)
	}))
	t.Cleanup(f.app.Close)
	return f
}

func (f *gateFixture) route(listen int) TailnetGateRoute {
	return TailnetGateRoute{
		Name: "just-dashboard-env-7.conf", Listen: listen, Upstream: f.app.URL,
		Check: f.whois.URL + tailnetgate.Path + "?environment=7",
	}
}

// visit is a request as tailscale serve forwards one: from loopback, with the
// tailnet address it came from in X-Forwarded-For and the node's name in Host.
// The visitor's own claims ride along, and must never reach the application.
func visit(t *testing.T, port int, from, method, body string) (int, string) {
	t.Helper()
	client := &http.Client{Timeout: 5 * time.Second}
	var response *http.Response
	var err error
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		request, _ := http.NewRequest(method, "http://127.0.0.1:"+strconv.Itoa(port)+"/report?q=1", strings.NewReader(body))
		request.Host = "box.tail1234.ts.net:21003"
		request.Header.Set("X-Forwarded-For", from)
		request.Header.Set("X-Forwarded-Proto", "https")
		request.Header.Set("X-Tailnet-Login", "mallory@example.com")
		request.Header.Set("X-Tailnet-User", "Mallory")
		if response, err = client.Do(request); err == nil || time.Now().After(deadline) {
			break
		}
	}
	if err != nil {
		t.Fatalf("%s from %s: %v", method, from, err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	return response.StatusCode, string(raw)
}

// The four answers every gate must give, whichever proxy holds it.
func (f *gateFixture) exercise(t *testing.T, port int) {
	t.Helper()
	status, body := visit(t, port, "100.101.102.103", http.MethodGet, "")
	if status != http.StatusOK || !strings.Contains(body, "login=alice@example.com|user=Alice Example|for=100.101.102.103|host=box.tail1234.ts.net:21003") ||
		strings.Contains(body, "mallory") || strings.Contains(body, "Mallory") {
		t.Fatalf("alice = %d %s", status, body)
	}
	t.Logf("the application was told: %s", body)
	if status, body := visit(t, port, "100.101.102.103", http.MethodPost, "payload"); status != http.StatusOK ||
		!strings.Contains(body, "method=POST|body=payload") {
		t.Fatalf("alice's POST = %d %s", status, body)
	}
	if status, body := visit(t, port, "100.64.0.9", http.MethodGet, ""); status != http.StatusOK || !strings.Contains(body, "login=tag:ci|user=ci|") {
		t.Fatalf("the CI machine = %d %s", status, body)
	}
	if status, body := visit(t, port, "100.101.102.104", http.MethodGet, ""); status != http.StatusForbidden || strings.Contains(body, "login=") {
		t.Fatalf("bob, not on the list = %d %s", status, body)
	}
	if status, body := visit(t, port, "100.70.0.1", http.MethodGet, ""); status != http.StatusForbidden || strings.Contains(body, "login=") {
		t.Fatalf("an address nobody holds = %d %s", status, body)
	}
	if f.refusals.Load() == 0 {
		t.Fatal("the refusals were not recorded")
	}
	f.down.Store(true)
	if status, body := visit(t, port, "100.101.102.103", http.MethodGet, ""); status != http.StatusServiceUnavailable || strings.Contains(body, "login=") {
		t.Fatalf("with tailscaled down = %d %s", status, body)
	}
	f.down.Store(false)
	if status, _ := visit(t, port, "100.101.102.103", http.MethodGet, ""); status != http.StatusOK {
		t.Fatalf("with tailscaled back = %d", status)
	}
	f.whois.Close()
	if status, body := visit(t, port, "100.101.102.103", http.MethodGet, ""); status != http.StatusServiceUnavailable || strings.Contains(body, "login=") {
		t.Fatalf("with the dashboard down = %d %s", status, body)
	}
}

// The host's real nginx, from a private prefix: needs only the nginx binary.
func TestLiveTailnetGateOnNginx(t *testing.T) {
	root := liveNginx(t)
	f := newGateFixture(t)
	port := freePort(t)
	accessLog := filepath.Join(root, "logs", "gate.access.log")
	content, err := renderNginxTailnetGate(f.route(port), accessLog, filepath.Join(root, "logs", "gate.error.log"))
	if err != nil {
		t.Fatal(err)
	}
	available := filepath.Join(root, "sites-available", "just-dashboard-env-7.conf")
	if err := os.WriteFile(available, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(available, filepath.Join(root, "sites-enabled", "just-dashboard-env-7.conf")); err != nil {
		t.Fatal(err)
	}
	if result := runValidator(context.Background(), "nginx", "-t"); !result.Valid {
		t.Fatalf("nginx refused the gate: %s", result.Output)
	}
	startNginx(t, root)
	f.exercise(t, port)

	raw, err := os.ReadFile(accessLog)
	if err != nil {
		t.Fatal(err)
	}
	logins := map[string]int{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		entry, ok := accesslog.Parse(line)
		if !ok {
			t.Fatalf("an unreadable record line: %q", line)
		}
		logins[entry.RemoteIP+" "+entry.User]++
	}
	if logins["100.101.102.103 alice@example.com"] == 0 || logins["100.64.0.9 tag:ci"] == 0 || logins["100.101.102.104 "] == 0 {
		t.Fatalf("the record = %v\n%s", logins, raw)
	}
}

// A real caddy:2-alpine, started as the dashboard starts its gate Caddy: on
// the host's network, every site bound to 127.0.0.1, its admin API a socket
// in its own volume.
func TestLiveTailnetGateOnCaddy(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 for the Docker tailnet gate test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	name := fmt.Sprintf("jdtest-tailnet-gate-%d", time.Now().UnixNano())
	service := New(t.TempDir(), filepath.Join(t.TempDir(), "Caddyfile"))
	service.tailnetGateName = name
	service.tailnetGateSource = filepath.Join(t.TempDir(), "gate", "Caddyfile")
	t.Cleanup(func() {
		_ = hostexec.Command(context.Background(), "docker", "rm", "-f", "-v", name).Run()
		_ = hostexec.Command(context.Background(), "docker", "volume", "rm", name+"-config", name+"-data").Run()
	})
	service.mu.Lock()
	edge, err := service.ensureGateContainer(ctx)
	service.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if edge.Driver != TailnetGateDriver {
		t.Fatalf("driver = %q", edge.Driver)
	}
	f := newGateFixture(t)
	port := freePort(t)
	route := f.route(port)
	result, err := service.applyCaddyTailnetGate(ctx, edge, route)
	if err != nil || !result.Verified || result.Snapshot.Driver != TailnetGateDriver {
		logs, _ := hostexec.Command(ctx, "docker", "logs", "--tail", "30", name).CombinedOutput()
		t.Fatalf("apply = %+v %v\n%s", result, err, logs)
	}
	// Bound to loopback alone: every listening socket on the port, by its
	// local address column.
	listening, _ := hostexec.Command(ctx, "ss", "-ltnH", "sport = :"+strconv.Itoa(port)).Output()
	sockets := strings.Split(strings.TrimSpace(string(listening)), "\n")
	for _, socket := range sockets {
		if fields := strings.Fields(socket); len(fields) < 4 || fields[3] != "127.0.0.1:"+strconv.Itoa(port) {
			t.Fatalf("the gate listens on: %s", listening)
		}
	}
	f.exercise(t, port)

	// The record names who the gate let in, read the way the Logs page does.
	reader := &caddyAccessLog{edge: *edge, path: mustAccessPath(t, route.Name)}
	logins := map[string]int{}
	_, _, _, err = reader.Read(ctx, "", 0, 1<<20, func(line string) {
		if entry, ok := accesslog.Parse(line); ok {
			logins[entry.RemoteIP+" "+entry.User]++
		}
	})
	if err != nil || logins["100.101.102.103 alice@example.com"] == 0 || logins["100.64.0.9 tag:ci"] == 0 {
		t.Fatalf("the record = %v %v", logins, err)
	}

	// A second ensure finds the running gate rather than starting another,
	// and removing the route takes it and its record away.
	service.mu.Lock()
	again, err := service.ensureGateContainer(ctx)
	service.mu.Unlock()
	if err != nil || again.ID != edge.ID {
		t.Fatalf("second ensure = %+v %v", again, err)
	}
	if err := service.removeGateContainerRoute(ctx, route.Name); err != nil {
		t.Fatal(err)
	}
	if snapshot, err := edge.snapshot(ctx, route.Name); err != nil || snapshot.Existed {
		t.Fatalf("after removal = %+v %v", snapshot, err)
	}
}

func mustAccessPath(t *testing.T, name string) string {
	t.Helper()
	path, err := dockerCaddyAccessLogPath(name)
	if err != nil {
		t.Fatal(err)
	}
	return path
}
