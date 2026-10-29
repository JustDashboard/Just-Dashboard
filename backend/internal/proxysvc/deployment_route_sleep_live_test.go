package proxysvc

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
	"github.com/Wayy01/Just-Dashboard/backend/internal/sessioncookie"
	"golang.org/x/crypto/bcrypt"
)

func fixtureBcrypt(t *testing.T, password string) string {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	return string(hash)
}

func basicCredentials(user, password string) string {
	return base64.StdEncoding.EncodeToString([]byte(user + ":" + password))
}

// wakeFixture is the dashboard's wake as far as a proxy can tell: a Unix
// socket that answers GET /wake/<route>. Its answer is the test's to choose,
// and starting the container is what the real one (deploy/sleep.go) does
// before it answers 200.
type wakeFixture struct {
	mu      sync.Mutex
	calls   []string
	answer  func(route string) (int, string)
	server  *http.Server
	address string
}

func newWakeFixture(t *testing.T, path string, answer func(string) (int, string)) *wakeFixture {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(path)
	// A Docker volume's path is longer than a socket address carries; the
	// dashboard binds it through the directory's descriptor, and so does this.
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	address := fmt.Sprintf("/proc/self/fd/%d/%s", dir.Fd(), filepath.Base(path))
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: address, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	if err := os.Chmod(path, 0o666); err != nil {
		t.Fatal(err)
	}
	w := &wakeFixture{answer: answer, address: path}
	w.server = &http.Server{Handler: http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		route := strings.TrimPrefix(r.URL.Path, "/wake/")
		w.mu.Lock()
		w.calls = append(w.calls, r.Method+" "+route+" "+r.Header.Get("X-Forwarded-Host")+" cookie="+r.Header.Get("Cookie"))
		answer := w.answer
		w.mu.Unlock()
		status, upstream := answer(route)
		if upstream != "" {
			rw.Header().Set(WakeUpstreamHeader, upstream)
		}
		rw.WriteHeader(status)
	})}
	go func() { _ = w.server.Serve(listener) }()
	t.Cleanup(func() {
		w.server.Close()
		_ = os.Remove(path)
	})
	return w
}

func (w *wakeFixture) set(answer func(string) (int, string)) {
	w.mu.Lock()
	w.answer = answer
	w.mu.Unlock()
}

func (w *wakeFixture) count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.calls)
}

func (w *wakeFixture) last() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.calls) == 0 {
		return ""
	}
	return w.calls[len(w.calls)-1]
}

func sleepDocker(t *testing.T, ctx context.Context, args ...string) string {
	t.Helper()
	raw, err := hostexec.Command(ctx, "docker", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("docker %s: %v: %s", strings.Join(args, " "), err, raw)
	}
	return strings.TrimSpace(string(raw))
}

// waitPort waits until something accepts on address.
func waitPort(t *testing.T, address string) {
	t.Helper()
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if conn, err := net.DialTimeout("tcp", address, 500*time.Millisecond); err == nil {
			conn.Close()
			return
		}
	}
	t.Fatalf("nothing answered on %s", address)
}

type sleepAnswer struct {
	status  int
	body    string
	retry   string
	elapsed time.Duration
}

func sleepRequest(t *testing.T, endpoint, host, method, path, body string, header ...string) sleepAnswer {
	t.Helper()
	client := &http.Client{Timeout: 60 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	request, _ := http.NewRequest(method, endpoint+path, strings.NewReader(body))
	request.Host = host
	for i := 0; i+1 < len(header); i += 2 {
		request.Header.Set(header[i], header[i+1])
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	started := time.Now()
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	answer := sleepAnswer{status: response.StatusCode, body: string(raw), retry: response.Header.Get("Retry-After"), elapsed: time.Since(started)}
	line := strings.TrimSpace(strings.SplitN(answer.body, "\n", 2)[0])
	if title := strings.Index(answer.body, "<title>"); title >= 0 {
		line = answer.body[title : strings.Index(answer.body, "</title>")+len("</title>")]
	}
	t.Logf("%-46s → %d in %4dms  %s", method+" "+path, answer.status, answer.elapsed.Milliseconds(), line)
	return answer
}

// What the host's real nginx does with a route whose application sleeps.
// The application is a real container on a fixed loopback port, as a
// release's is; the wake is a socket of the test's that starts it with
// docker start, as the dashboard's does. Stopped with no switch, the first
// request is woken and answered with the application's page, not a 502;
// stopped with the switch on, a POST is woken and reaches the application
// with its body; a wake that outlasts the hold gets the waking page; a wake
// with nothing asleep gets the route's own 502; and a visitor without the
// route's password is asked for it and wakes nothing.
func TestLiveDeploymentRouteWakesOnNginx(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 on a Docker host, as root")
	}
	root := liveNginx(t)
	// nginx's workers run as nobody under a root master, and read the pages,
	// the password file and the socket from the test's own folder.
	for dir := root; dir != "/tmp" && dir != "/"; dir = filepath.Dir(dir) {
		_ = os.Chmod(dir, 0o755)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	appPort := freePort(t)
	app := sleepDocker(t, ctx, "run", "-d", "-p", fmt.Sprintf("127.0.0.1:%d:80", appPort), "traefik/whoami:v1.11")
	defer hostexec.Command(context.Background(), "docker", "rm", "-f", app).Run()
	waitPort(t, fmt.Sprintf("127.0.0.1:%d", appPort))

	service := New(root, filepath.Join(root, "Caddyfile"))
	hash := fixtureBcrypt(t, "s3cret")
	route := DeploymentRoute{
		Name: "just-dashboard-env-71.conf", Domains: []string{"sleepy.example.test"},
		Upstream:   fmt.Sprintf("http://127.0.0.1:%d", appPort),
		ErrorPages: []int{502}, Sleep: &DeploymentSleep{HoldSeconds: 20},
		BasicAuth: []BasicAuthUser{{Username: "visitor", Hash: hash}},
	}
	starts := 0
	wake := newWakeFixture(t, service.nginxWakeSocket(), func(string) (int, string) {
		starts++
		sleepDocker(t, ctx, "start", app)
		waitPort(t, fmt.Sprintf("127.0.0.1:%d", appPort))
		if _, err := service.SetDeploymentSleeping(ctx, route.Name, false); err != nil {
			t.Errorf("clearing the switch: %v", err)
		}
		return WakeAwake, ""
	})
	if err := service.writeDeploymentAuthFile(route); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(service.authDir(), 0o755)
	_ = os.Chmod(filepath.Join(service.authDir(), deploymentAuthFileName(route.Name)), 0o644)
	spec := service.deploymentSpec(route)
	spec.AccessLog = false
	if err := service.writeMissingPages(spec); err != nil {
		t.Fatal(err)
	}
	if _, err := service.WriteDeploymentPage(ctx, route.Name, "502", "<html><title>Our own 502</title></html>\n"); err != nil {
		t.Fatal(err)
	}
	port := freePort(t)
	installSite(t, root, spec, port)
	if result := runValidator(ctx, "nginx", "-t"); !result.Valid {
		t.Fatalf("nginx refused the sleeping route: %s", result.Output)
	}
	startNginx(t, root)
	endpoint := "http://127.0.0.1:" + strconv.Itoa(port)
	waitPort(t, fmt.Sprintf("127.0.0.1:%d", port))
	signedIn := []string{"Authorization", "Basic " + basicCredentials("visitor", "s3cret"), "Cookie", "session=private"}

	if a := sleepRequest(t, endpoint, "sleepy.example.test", http.MethodGet, "/awake", "", signedIn...); a.status != 200 || !strings.Contains(a.body, "GET /awake") {
		t.Fatalf("awake = %d %q", a.status, a.body)
	}
	if wake.count() != 0 {
		t.Fatal("an awake application was woken")
	}

	// Stopped, no switch: the application not answering is woken.
	sleepDocker(t, ctx, "stop", "-t", "1", app)
	if a := sleepRequest(t, endpoint, "sleepy.example.test", http.MethodGet, "/first-after-sleep?x=1", "", signedIn...); a.status != 200 || !strings.Contains(a.body, "GET /first-after-sleep?x=1") {
		t.Fatalf("the first request after sleep = %d %q", a.status, a.body)
	}
	if starts != 1 || !strings.HasPrefix(wake.last(), "GET just-dashboard-env-71.conf sleepy.example.test cookie=") || strings.HasSuffix(wake.last(), "private") {
		t.Fatalf("wake calls = %d, last %q", starts, wake.last())
	}

	// Asleep by design: the switch is on, so the old address is never
	// dialled, and a POST is woken with its body.
	sleepDocker(t, ctx, "stop", "-t", "1", app)
	if state, err := service.SetDeploymentSleeping(ctx, route.Name, true); err != nil || !state.Ready || !state.On {
		t.Fatalf("switch on = %+v, %v", state, err)
	}
	if a := sleepRequest(t, endpoint, "sleepy.example.test", http.MethodPost, "/form", "name=ada&lang=go", signedIn...); a.status != 200 || !strings.Contains(a.body, "POST /form") || !strings.Contains(a.body, "name=ada&lang=go") {
		t.Fatalf("a POST to a sleeping application = %d %q", a.status, a.body)
	}
	if state, _ := service.DeploymentSleeping(ctx, route.Name); state.On {
		t.Fatal("the switch is still on after the wake")
	}

	// A visitor without the password is asked for it, and nothing wakes.
	sleepDocker(t, ctx, "stop", "-t", "1", app)
	before := wake.count()
	if a := sleepRequest(t, endpoint, "sleepy.example.test", http.MethodGet, "/", ""); a.status != 401 {
		t.Fatalf("a visitor without the password = %d", a.status)
	}
	if wake.count() != before {
		t.Fatal("a visitor the route refused woke the application")
	}

	// Starting after what the visitor is held for: the waking page.
	wake.set(func(string) (int, string) { return WakeStarting, "" })
	a := sleepRequest(t, endpoint, "sleepy.example.test", http.MethodGet, "/", "", signedIn...)
	waking, _ := ShippedSitePage("waking")
	if a.status != 503 || a.retry != "5" || a.body != waking {
		t.Fatalf("the waking page = %d %q %q", a.status, a.retry, a.body)
	}
	// Nothing asleep to wake: the route's own 502.
	wake.set(func(string) (int, string) { return WakeNothing, "" })
	if a := sleepRequest(t, endpoint, "sleepy.example.test", http.MethodGet, "/", "", signedIn...); a.status != 502 || !strings.Contains(a.body, "Our own 502") {
		t.Fatalf("nothing to wake = %d %q", a.status, a.body)
	}
	// And the dashboard not answering at all is the same 502.
	wake.server.Close()
	if a := sleepRequest(t, endpoint, "sleepy.example.test", http.MethodGet, "/", "", signedIn...); a.status != 502 || !strings.Contains(a.body, "Our own 502") {
		t.Fatalf("the wake not answering = %d %q", a.status, a.body)
	}
}

// The same on a real Caddy, as the Docker ingress runs it: the wake socket
// is inside the ingress's own /config volume, which only it mounts; the
// switch is a file Caddy reads on every request; the woken request is sent
// to the address the wake answers with, since a started container may have
// another; and the password is asked for before anything wakes.
func TestLiveDockerCaddyRouteWakes(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 for isolated Docker ingress test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	dir := t.TempDir()
	source := filepath.Join(dir, "Caddyfile")
	if err := os.WriteFile(source, []byte("# fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	network := fmt.Sprintf("jd-ingress-sleep-%d", time.Now().UnixNano())
	sleepDocker(t, ctx, "network", "create", network)
	defer hostexec.Command(context.Background(), "docker", "network", "rm", network).Run()
	port := freePort(t)
	id := sleepDocker(t, ctx, "run", "-d", "--network", network, "-p", fmt.Sprintf("127.0.0.1:%d:80", port),
		"-v", source+":/etc/caddy/Caddyfile:ro", "-v", "/config", "-v", "/data", "caddy:2-alpine")
	defer hostexec.Command(context.Background(), "docker", "rm", "-f", "-v", id).Run()
	app := sleepDocker(t, ctx, "run", "-d", "-p", "127.0.0.1::80", "traefik/whoami:v1.11")
	defer hostexec.Command(context.Background(), "docker", "rm", "-f", app).Run()
	published := strings.TrimPrefix(sleepDocker(t, ctx, "port", app, "80/tcp"), "127.0.0.1:")
	waitPort(t, "127.0.0.1:"+published)

	var inspected []ingressContainer
	if err := json.Unmarshal([]byte(sleepDocker(t, ctx, "inspect", id)), &inspected); err != nil || len(inspected) != 1 {
		t.Fatalf("inspect ingress: %v", err)
	}
	edge, err := dockerCaddyFrom(inspected[0])
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 40 && edge.configurationSynced(ctx) != nil; i++ {
		time.Sleep(100 * time.Millisecond)
	}
	service := New(dir, source)
	hash := fixtureBcrypt(t, "s3cret")
	route := DeploymentRoute{
		Name: "just-dashboard-env-72.conf", Domains: []string{"sleepy.example.test"},
		Upstream: "http://127.0.0.1:" + published, AccessLog: true,
		ErrorPages: []int{502}, Sleep: &DeploymentSleep{HoldSeconds: 20},
		BasicAuth: []BasicAuthUser{{Username: "visitor", Hash: hash}},
		// As on the name the dashboard is reached by: its session cookie
		// is never the application's, asleep or awake.
		DropCookies: DashboardCookies(),
	}
	if result, err := service.applyDockerCaddyRoute(ctx, edge, route); err != nil || !result.Verified {
		t.Fatalf("apply = %+v, %v; %s", result, err, sleepDocker(t, ctx, "logs", id))
	}
	root, _ := hostVisiblePath(edge.ConfigSource)
	socket := filepath.Join(root, "just-dashboard", "wake", "wake.sock")
	starts := 0
	wake := newWakeFixture(t, socket, func(name string) (int, string) {
		starts++
		sleepDocker(t, ctx, "start", app)
		upstream, err := edge.refreshTarget(ctx, name)
		if err != nil {
			t.Errorf("refreshing the route: %v", err)
			return WakeNothing, ""
		}
		waitPort(t, "127.0.0.1:"+strings.TrimPrefix(sleepDocker(t, ctx, "port", app, "80/tcp"), "127.0.0.1:"))
		if _, err := edge.setSleeping(ctx, name, false); err != nil {
			t.Errorf("clearing the switch: %v", err)
		}
		return WakeAwake, upstream
	})
	endpoint := "http://127.0.0.1:" + strconv.Itoa(port)
	signedIn := []string{"Authorization", "Basic " + basicCredentials("visitor", "s3cret"), "Cookie", "session=private; " + sessioncookie.Name + "=dashboard-secret"}
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(200 * time.Millisecond) {
		if a := sleepRequest(t, endpoint, "sleepy.example.test", http.MethodGet, "/awake", "", signedIn...); a.status == 200 {
			break
		} else if time.Now().After(deadline) {
			t.Fatalf("awake = %d %q", a.status, a.body)
		}
	}

	sleepDocker(t, ctx, "stop", "-t", "1", app)
	if a := sleepRequest(t, endpoint, "sleepy.example.test", http.MethodGet, "/first-after-sleep?x=1", "", signedIn...); a.status != 200 || !strings.Contains(a.body, "GET /first-after-sleep?x=1") ||
		!strings.Contains(a.body, "session=private") || strings.Contains(a.body, "dashboard-secret") {
		t.Fatalf("the first request after sleep = %d %q", a.status, a.body)
	}
	if starts != 1 || strings.HasSuffix(wake.last(), "private") {
		t.Fatalf("wake calls = %d, last %q", starts, wake.last())
	}

	// Asleep by design, with another container given the old address in
	// between: the switch keeps the request off it, and the wake's answer
	// sends it to the application wherever it now is.
	sleepDocker(t, ctx, "stop", "-t", "1", app)
	if state, err := edge.setSleeping(ctx, route.Name, true); err != nil || !state.Ready || !state.On {
		t.Fatalf("switch on = %+v, %v", state, err)
	}
	squatter := sleepDocker(t, ctx, "run", "-d", "caddy:2-alpine", "caddy", "respond", "--listen", ":80", "somebody else's site")
	defer hostexec.Command(context.Background(), "docker", "rm", "-f", squatter).Run()
	if a := sleepRequest(t, endpoint, "sleepy.example.test", http.MethodPost, "/form", "name=ada&lang=go", signedIn...); a.status != 200 || !strings.Contains(a.body, "POST /form") || !strings.Contains(a.body, "name=ada&lang=go") {
		t.Fatalf("a POST to a sleeping application = %d %q", a.status, a.body)
	}
	if state, _ := edge.sleeping(ctx, route.Name); state.On {
		t.Fatal("the switch is still on after the wake")
	}
	if a := sleepRequest(t, endpoint, "sleepy.example.test", http.MethodGet, "/after", "", signedIn...); a.status != 200 || strings.Contains(a.body, "somebody else") {
		t.Fatalf("after the wake = %d %q", a.status, a.body)
	}

	// The password before the wake.
	sleepDocker(t, ctx, "stop", "-t", "1", app)
	before := wake.count()
	if a := sleepRequest(t, endpoint, "sleepy.example.test", http.MethodGet, "/", ""); a.status != 401 {
		t.Fatalf("a visitor without the password = %d", a.status)
	}
	if wake.count() != before {
		t.Fatal("a visitor the route refused woke the application")
	}
	wake.set(func(string) (int, string) { return WakeStarting, "" })
	waking, _ := ShippedSitePage("waking")
	if a := sleepRequest(t, endpoint, "sleepy.example.test", http.MethodGet, "/", "", signedIn...); a.status != 503 || a.retry != "5" || a.body != waking {
		t.Fatalf("the waking page = %d %q %q", a.status, a.retry, a.body)
	}
	wake.set(func(string) (int, string) { return WakeNothing, "" })
	if a := sleepRequest(t, endpoint, "sleepy.example.test", http.MethodGet, "/", "", signedIn...); a.status != 502 {
		t.Fatalf("nothing to wake = %d %q", a.status, a.body)
	}
}
