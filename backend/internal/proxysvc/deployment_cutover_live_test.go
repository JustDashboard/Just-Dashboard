package proxysvc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// Cutover continuity is the product's strongest availability claim, and the
// audit refuses to accept it from a unit test: a reload that drops one request
// in a hundred still looks green to a test that only checks the rendered file.
// This exercises a real nginx, real release processes, and real client traffic
// across at least a hundred activations, and counts what an ordinary visitor
// would have seen.
//
// nginx renders `listen 80`, which an unprivileged host process cannot bind, so
// the proxy runs in its own container with port 80 published on loopback. The
// release processes bind the test network's gateway address rather than
// 0.0.0.0: containers on that network reach them, and nothing outside the host
// does.

const (
	cutoverDomain    = "cutover.example.test"
	cutoverRouteName = "jd-deployment-cutover"
	// The unrelated site proves a cutover reload is scoped: a hundred reloads
	// must not disturb configuration this feature does not own.
	cutoverNeighbour = "existing.example.test"
)

type cutoverCounter struct {
	total  atomic.Int64
	failed atomic.Int64
	// firstError keeps the first failure verbatim. A count alone cannot be
	// investigated later, and the transition loop must not stop on it.
	once       sync.Once
	firstError string
}

func (c *cutoverCounter) ok() { c.total.Add(1) }

func (c *cutoverCounter) fail(format string, args ...any) {
	c.total.Add(1)
	c.failed.Add(1)
	c.once.Do(func() { c.firstError = fmt.Sprintf(format, args...) })
}

func (c *cutoverCounter) report(t *testing.T, label string) {
	t.Helper()
	total, failed := c.total.Load(), c.failed.Load()
	t.Logf("%s: %d requests, %d failed", label, total, failed)
	if failed > 0 {
		t.Errorf("%s lost %d of %d requests during cutovers; first: %s", label, failed, total, c.firstError)
	}
}

func TestLiveCutoverTrafficContinuity(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 to run real nginx cutovers under traffic")
	}
	transitions := 100
	if raw := os.Getenv("JD_CUTOVER_TRANSITIONS"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			t.Fatalf("JD_CUTOVER_TRANSITIONS must be a positive integer, got %q", raw)
		}
		transitions = parsed
	}

	stamp := strconv.FormatInt(time.Now().UnixNano(), 10)
	network := liveCutoverNetwork(t, stamp)
	root := liveCutoverRoot(t)
	binary := buildCutoverRelease(t)
	blue := startCutoverRelease(t, network, binary, "blue", stamp)
	green := startCutoverRelease(t, network, binary, "green", stamp)
	proxy := liveCutoverNginx(t, root, network, stamp)
	endpoint := proxy.endpoint

	service := New(root, "")
	route := func(release cutoverRelease) DeploymentRoute {
		return DeploymentRoute{
			Name:     cutoverRouteName,
			Domains:  []string{cutoverDomain},
			Upstream: "http://" + net.JoinHostPort(release.name, "8080"),
		}
	}

	ctx := t.Context()
	first, err := service.ApplyDeploymentRoute(ctx, route(blue))
	if err != nil {
		t.Fatalf("initial route: %v", err)
	}
	if !first.Applied || !first.Verified {
		t.Fatalf("initial route was not applied and verified: %+v", first)
	}
	t.Cleanup(func() {
		_ = service.RemoveDeploymentRoute(context.WithoutCancel(ctx), cutoverRouteName)
	})
	if got := waitCutoverRelease(t, endpoint, "blue", 15*time.Second); got != "blue" {
		t.Fatalf("first release never served traffic, last body %q", got)
	}

	traffic, stopTraffic := startCutoverTraffic(t, endpoint)

	// Persistent WebSocket connections are opened before the first transition
	// and read continuously until the last one. nginx keeps a shutting-down
	// worker alive for its upgraded connections, so a break here is a real
	// availability defect rather than a client timeout.
	sockets := openCutoverSockets(t, endpoint, 4)

	observed := 0
	slowest := time.Duration(0)
	start := time.Now()
	for i := 1; i <= transitions; i++ {
		target, want := green, "green"
		if i%2 == 0 {
			target, want = blue, "blue"
		}
		applyStart := time.Now()
		result, err := service.ApplyDeploymentRoute(ctx, route(target))
		if err != nil {
			t.Fatalf("transition %d apply: %v", i, err)
		}
		if !result.Applied || !result.Verified {
			t.Fatalf("transition %d was not applied and verified: %+v", i, result)
		}
		if err := service.VerifyDeploymentRoute(ctx, route(target)); err != nil {
			t.Fatalf("transition %d verify: %v", i, err)
		}
		if got := waitCutoverRelease(t, endpoint, want, 15*time.Second); got != want {
			t.Fatalf("transition %d never reached %s, last body %q", i, want, got)
		}
		if elapsed := time.Since(applyStart); elapsed > slowest {
			slowest = elapsed
		}
		observed++
	}
	elapsed := time.Since(start)

	// Traffic stops only after every transition has landed, so the counters
	// cover the whole cutover window rather than a quiet tail.
	stopTraffic()
	sockets.close(t)

	if observed != transitions {
		t.Fatalf("observed %d of %d transitions", observed, transitions)
	}
	traffic.fast.report(t, "fast HTTP")
	traffic.long.report(t, "long request")
	traffic.socket.report(t, "new WebSocket")
	t.Logf("completed %d transitions in %s; slowest activation %s", observed, elapsed.Round(time.Millisecond), slowest.Round(time.Millisecond))

	// An unrelated site sharing the same nginx must be untouched by a hundred
	// deployment reloads.
	if body, status, err := cutoverGet(endpoint, cutoverNeighbour, "/", 5*time.Second); err != nil || status != http.StatusOK || body != "neighbour" {
		t.Fatalf("unrelated site changed across %d reloads: %q status %d err %v", transitions, body, status, err)
	}

	writeCutoverEvidence(t, cutoverEvidence{
		Transitions:        observed,
		Duration:           elapsed.Round(time.Millisecond).String(),
		SlowestActivation:  slowest.Round(time.Millisecond).String(),
		FastRequests:       traffic.fast.total.Load(),
		FastFailed:         traffic.fast.failed.Load(),
		LongRequests:       traffic.long.total.Load(),
		LongFailed:         traffic.long.failed.Load(),
		NewSockets:         traffic.socket.total.Load(),
		NewSocketsFailed:   traffic.socket.failed.Load(),
		PersistentSockets:  len(sockets.conns),
		PersistentMessages: sockets.messages.Load(),
		PersistentFailed:   sockets.failures.Load(),
	})
	if sockets.failures.Load() > 0 {
		t.Errorf("%d persistent WebSocket connections broke during cutovers", sockets.failures.Load())
	}
}

type cutoverRelease struct {
	id   string
	name string
}

// buildCutoverRelease compiles the fixture release server as a static binary.
// The releases have to run as containers rather than host processes: nginx
// reaches a container on its own network, and a host whose firewall drops
// container-to-gateway traffic would otherwise fail this test for a reason
// that has nothing to do with cutovers.
func buildCutoverRelease(t *testing.T) string {
	t.Helper()
	tool := filepath.Join(runtime.GOROOT(), "bin", "go")
	if _, err := os.Stat(tool); err != nil {
		found, lookErr := exec.LookPath("go")
		if lookErr != nil {
			t.Fatalf("no Go toolchain to build the release fixture: %v", err)
		}
		tool = found
	}
	binary := filepath.Join(t.TempDir(), "release")
	build := exec.Command(tool, "build", "-trimpath", "-o", binary, ".")
	build.Dir = filepath.Join("testdata", "cutover-release")
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOFLAGS=")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build release fixture: %v: %s", err, out)
	}
	if err := os.Chmod(binary, 0o755); err != nil {
		t.Fatal(err)
	}
	return binary
}

func startCutoverRelease(t *testing.T, network, binary, id, stamp string) cutoverRelease {
	t.Helper()
	name := "jd-cutover-" + id + "-" + stamp
	args := []string{
		"run", "--detach", "--name", name,
		"--network", network,
		"--env", "JD_RELEASE_ID=" + id,
		"--volume", binary + ":/srv/release:ro",
		"alpine:3.20", "/srv/release",
	}
	if out, err := exec.Command("docker", args...).CombinedOutput(); err != nil {
		t.Fatalf("start release %s: %v: %s", id, err, out)
	}
	t.Cleanup(func() {
		if t.Failed() {
			if logs, err := exec.Command("docker", "logs", "--tail", "20", name).CombinedOutput(); err == nil {
				t.Logf("release %s logs:\n%s", id, logs)
			}
		}
		_ = exec.Command("docker", "rm", "--force", name).Run()
	})
	return cutoverRelease{id: id, name: name}
}

// liveCutoverNetwork owns its own bridge so nginx resolves the release
// containers by name, and cleanup cannot remove a network something else uses.
func liveCutoverNetwork(t *testing.T, stamp string) string {
	t.Helper()
	name := "jd-cutover-" + stamp
	if out, err := exec.Command("docker", "network", "create", name).CombinedOutput(); err != nil {
		t.Fatalf("create network: %v: %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "network", "rm", name).Run() })
	return name
}

func liveCutoverRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	confd := filepath.Join(root, "conf.d")
	if err := os.MkdirAll(confd, 0o755); err != nil {
		t.Fatal(err)
	}
	neighbour := fmt.Sprintf(`server {
    listen 80 default_server;
    server_name %s;
    location / { default_type text/plain; return 200 "neighbour"; }
}
`, cutoverNeighbour)
	if err := os.WriteFile(filepath.Join(confd, "zz-neighbour.conf"), []byte(neighbour), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// cutoverProxy is the nginx the deployment code actually reloads. Tests that
// take it away mid-transition need its identity, not only its address.
type cutoverProxy struct {
	name     string
	endpoint string
}

func (p cutoverProxy) kill(t *testing.T) {
	t.Helper()
	if out, err := exec.Command("docker", "kill", "--signal", "SIGKILL", p.name).CombinedOutput(); err != nil {
		t.Fatalf("kill proxy: %v: %s", err, out)
	}
}

func (p cutoverProxy) restart(t *testing.T) {
	t.Helper()
	if out, err := exec.Command("docker", "start", p.name).CombinedOutput(); err != nil {
		t.Fatalf("restart proxy: %v: %s", err, out)
	}
	p.waitReady(t)
}

func (p cutoverProxy) waitReady(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		if body, status, err := cutoverGet(p.endpoint, cutoverNeighbour, "/", 2*time.Second); err == nil && status == http.StatusOK && body == "neighbour" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("nginx did not become ready")
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// liveCutoverNginx runs the proxy the deployment code actually reloads, and
// puts a shim on PATH so hostexec's `nginx -t` and `nginx -s reload` reach it.
func liveCutoverNginx(t *testing.T, root, network, stamp string) cutoverProxy {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	name := "jd-cutover-nginx-" + stamp
	args := []string{
		"run", "--detach", "--name", name,
		"--network", network,
		"--publish", "127.0.0.1:" + strconv.Itoa(port) + ":80",
		"--volume", filepath.Join(root, "conf.d") + ":/etc/nginx/conf.d:ro",
		"nginx:1.27-alpine",
	}
	if out, err := exec.Command("docker", append([]string{}, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("start nginx: %v: %s", err, out)
	}
	t.Cleanup(func() {
		if t.Failed() {
			if logs, err := exec.Command("docker", "logs", "--tail", "50", name).CombinedOutput(); err == nil {
				t.Logf("nginx logs:\n%s", logs)
			}
		}
		_ = exec.Command("docker", "rm", "--force", name).Run()
	})

	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	// The read-only conf.d mount is the container's view of the same bytes the
	// Service writes on the host, so a reload inside the container reads what
	// the apply just wrote.
	shim := "#!/bin/sh\nexec docker exec " + name + " nginx \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "nginx"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	proxy := cutoverProxy{name: name, endpoint: "http://127.0.0.1:" + strconv.Itoa(port)}
	proxy.waitReady(t)
	return proxy
}

func cutoverGet(endpoint, host, path string, timeout time.Duration) (string, int, error) {
	client := &http.Client{Timeout: timeout}
	request, err := http.NewRequest("GET", endpoint+path, nil)
	if err != nil {
		return "", 0, err
	}
	request.Host = host
	response, err := client.Do(request)
	if err != nil {
		return "", 0, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return "", response.StatusCode, err
	}
	return strings.TrimSpace(string(body)), response.StatusCode, nil
}

// waitCutoverRelease polls until the new release answers. `nginx -s reload`
// returns before the new workers accept, so the transition is confirmed by the
// response rather than by the reload's exit status.
func waitCutoverRelease(t *testing.T, endpoint, want string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	last := ""
	for {
		body, status, err := cutoverGet(endpoint, cutoverDomain, "/", 2*time.Second)
		if err == nil && status == http.StatusOK {
			last = body
			if body == want {
				return body
			}
		} else if err != nil {
			last = err.Error()
		}
		if time.Now().After(deadline) {
			return last
		}
		time.Sleep(20 * time.Millisecond)
	}
}

type cutoverTraffic struct {
	fast   *cutoverCounter
	long   *cutoverCounter
	socket *cutoverCounter
}

func startCutoverTraffic(t *testing.T, endpoint string) (*cutoverTraffic, func()) {
	t.Helper()
	traffic := &cutoverTraffic{fast: &cutoverCounter{}, long: &cutoverCounter{}, socket: &cutoverCounter{}}
	stop := make(chan struct{})
	var wait sync.WaitGroup
	served := func(body string) bool { return body == "blue" || body == "green" }

	for i := 0; i < 4; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			// Each worker keeps its own client so connection pools, and the
			// reuse races a reload creates, are exercised rather than shared.
			client := &http.Client{Timeout: 5 * time.Second}
			for {
				select {
				case <-stop:
					return
				default:
				}
				request, _ := http.NewRequest("GET", endpoint+"/", nil)
				request.Host = cutoverDomain
				response, err := client.Do(request)
				if err != nil {
					traffic.fast.fail("request error: %v", err)
					continue
				}
				body, readErr := io.ReadAll(response.Body)
				response.Body.Close()
				switch {
				case readErr != nil:
					traffic.fast.fail("body error: %v", readErr)
				case response.StatusCode != http.StatusOK:
					traffic.fast.fail("status %d", response.StatusCode)
				case !served(strings.TrimSpace(string(body))):
					traffic.fast.fail("unexpected body %q", strings.TrimSpace(string(body)))
				default:
					traffic.fast.ok()
				}
			}
		}()
	}

	for i := 0; i < 2; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			client := &http.Client{Timeout: 20 * time.Second}
			for {
				select {
				case <-stop:
					return
				default:
				}
				request, _ := http.NewRequest("GET", endpoint+"/slow", nil)
				request.Host = cutoverDomain
				response, err := client.Do(request)
				if err != nil {
					traffic.long.fail("long request error: %v", err)
					continue
				}
				body, readErr := io.ReadAll(response.Body)
				response.Body.Close()
				lines := strings.Fields(strings.TrimSpace(string(body)))
				switch {
				case readErr != nil:
					traffic.long.fail("long body error: %v", readErr)
				case response.StatusCode != http.StatusOK:
					traffic.long.fail("long status %d", response.StatusCode)
				case len(lines) != 8:
					// A truncated stream is the exact symptom of a worker
					// that stopped mid-response.
					traffic.long.fail("truncated long response: %d of 8 chunks", len(lines))
				case !served(lines[0]):
					traffic.long.fail("unexpected long body %q", lines[0])
				default:
					traffic.long.ok()
				}
			}
		}()
	}

	wait.Add(1)
	go func() {
		defer wait.Done()
		for {
			select {
			case <-stop:
				return
			case <-time.After(150 * time.Millisecond):
			}
			conn, err := dialCutoverSocket(endpoint)
			if err != nil {
				traffic.socket.fail("dial: %v", err)
				continue
			}
			if err := cutoverSocketEcho(conn, "probe"); err != nil {
				traffic.socket.fail("echo: %v", err)
			} else {
				traffic.socket.ok()
			}
			conn.Close()
		}
	}()

	return traffic, func() {
		close(stop)
		wait.Wait()
	}
}

func dialCutoverSocket(endpoint string) (*websocket.Conn, error) {
	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	url := "ws" + strings.TrimPrefix(endpoint, "http") + "/ws"
	header := http.Header{}
	header.Set("Host", cutoverDomain)
	conn, response, err := dialer.Dial(url, header)
	if response != nil && response.Body != nil {
		response.Body.Close()
	}
	return conn, err
}

func cutoverSocketEcho(conn *websocket.Conn, message string) error {
	if err := conn.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return err
	}
	if err := conn.WriteMessage(websocket.TextMessage, []byte(message)); err != nil {
		return err
	}
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return err
	}
	_, reply, err := conn.ReadMessage()
	if err != nil {
		return err
	}
	if !strings.HasSuffix(string(reply), ":"+message) {
		return fmt.Errorf("unexpected reply %q", reply)
	}
	return nil
}

type cutoverSockets struct {
	conns    []*websocket.Conn
	messages atomic.Int64
	failures atomic.Int64
	stop     chan struct{}
	wait     sync.WaitGroup
}

func openCutoverSockets(t *testing.T, endpoint string, count int) *cutoverSockets {
	t.Helper()
	sockets := &cutoverSockets{stop: make(chan struct{})}
	for i := 0; i < count; i++ {
		conn, err := dialCutoverSocket(endpoint)
		if err != nil {
			t.Fatalf("persistent socket %d: %v", i, err)
		}
		sockets.conns = append(sockets.conns, conn)
		sockets.wait.Add(1)
		go func(conn *websocket.Conn, index int) {
			defer sockets.wait.Done()
			for {
				select {
				case <-sockets.stop:
					return
				case <-time.After(100 * time.Millisecond):
				}
				if err := cutoverSocketEcho(conn, "keepalive"); err != nil {
					sockets.failures.Add(1)
					t.Logf("persistent socket %d broke: %v", index, err)
					return
				}
				sockets.messages.Add(1)
			}
		}(conn, i)
	}
	return sockets
}

func (s *cutoverSockets) close(t *testing.T) {
	t.Helper()
	close(s.stop)
	s.wait.Wait()
	for _, conn := range s.conns {
		_ = conn.Close()
	}
}

type cutoverEvidence struct {
	Transitions        int    `json:"transitions"`
	Duration           string `json:"duration"`
	SlowestActivation  string `json:"slowestActivation"`
	FastRequests       int64  `json:"fastRequests"`
	FastFailed         int64  `json:"fastFailed"`
	LongRequests       int64  `json:"longRequests"`
	LongFailed         int64  `json:"longFailed"`
	NewSockets         int64  `json:"newSockets"`
	NewSocketsFailed   int64  `json:"newSocketsFailed"`
	PersistentSockets  int    `json:"persistentSockets"`
	PersistentMessages int64  `json:"persistentMessages"`
	PersistentFailed   int64  `json:"persistentFailed"`
}

// writeCutoverEvidence records the measured run where the audit keeps its other
// evidence, so a claim about cutover loss can be checked against numbers.
func writeCutoverEvidence(t *testing.T, evidence cutoverEvidence) {
	t.Helper()
	path := os.Getenv("JD_CUTOVER_EVIDENCE")
	if path == "" {
		return
	}
	body, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(body, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("cutover evidence written to %s", path)
}

// TestLiveCutoverSurvivesProxyLoss is the proxy half of the fault matrix. A
// cutover that begins while the proxy is gone must not leave the new release
// half-activated: the previous release's bytes stay on disk, the failure is
// reported rather than swallowed, and the old release is what serves once the
// proxy is back.
func TestLiveCutoverSurvivesProxyLoss(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 to kill a real proxy during a cutover")
	}
	stamp := strconv.FormatInt(time.Now().UnixNano(), 10)
	network := liveCutoverNetwork(t, stamp)
	root := liveCutoverRoot(t)
	binary := buildCutoverRelease(t)
	blue := startCutoverRelease(t, network, binary, "blue", stamp)
	green := startCutoverRelease(t, network, binary, "green", stamp)
	proxy := liveCutoverNginx(t, root, network, stamp)

	service := New(root, "")
	route := func(release cutoverRelease) DeploymentRoute {
		return DeploymentRoute{
			Name: cutoverRouteName, Domains: []string{cutoverDomain},
			Upstream: "http://" + net.JoinHostPort(release.name, "8080"),
		}
	}
	ctx := t.Context()
	if _, err := service.ApplyDeploymentRoute(ctx, route(blue)); err != nil {
		t.Fatalf("initial route: %v", err)
	}
	t.Cleanup(func() {
		_ = service.RemoveDeploymentRoute(context.WithoutCancel(ctx), cutoverRouteName)
	})
	if got := waitCutoverRelease(t, proxy.endpoint, "blue", 15*time.Second); got != "blue" {
		t.Fatalf("first release never served traffic, last body %q", got)
	}
	routePath := filepath.Join(root, "conf.d", cutoverRouteName+".conf")
	before, err := os.ReadFile(routePath)
	if err != nil {
		t.Fatal(err)
	}

	proxy.kill(t)
	result, err := service.ApplyDeploymentRoute(ctx, route(green))
	if err == nil {
		t.Fatal("cutover reported success while the proxy was gone")
	}
	if result.Applied {
		t.Fatalf("cutover claimed the route was applied: %+v", result)
	}
	// The product cannot honestly claim recovery it could not verify: with no
	// proxy to reload, restoration is reported as incomplete even though the
	// bytes were put back.
	if result.Recovered {
		t.Fatalf("cutover claimed recovery it could not verify: %+v", result)
	}

	after, err := os.ReadFile(routePath)
	if err != nil {
		t.Fatalf("previous route file did not survive the failed cutover: %v", err)
	}
	if string(after) != string(before) {
		t.Fatalf("previous route was not restored:\n--- before ---\n%s\n--- after ---\n%s", before, after)
	}
	if strings.Contains(string(after), green.name) {
		t.Fatal("the failed candidate remained in the restored route")
	}

	proxy.restart(t)
	if got := waitCutoverRelease(t, proxy.endpoint, "blue", 30*time.Second); got != "blue" {
		t.Fatalf("previous release is not live after proxy recovery, last body %q", got)
	}
	if body, status, err := cutoverGet(proxy.endpoint, cutoverNeighbour, "/", 5*time.Second); err != nil || status != http.StatusOK || body != "neighbour" {
		t.Fatalf("unrelated site did not survive the failed cutover: %q status %d err %v", body, status, err)
	}

	// The same route applies cleanly once the proxy is back, so a proxy
	// outage costs a retry rather than a stuck deployment.
	recovered, err := service.ApplyDeploymentRoute(ctx, route(green))
	if err != nil {
		t.Fatalf("retry after proxy recovery: %v", err)
	}
	if !recovered.Applied || !recovered.Verified {
		t.Fatalf("retry did not apply and verify: %+v", recovered)
	}
	if got := waitCutoverRelease(t, proxy.endpoint, "green", 15*time.Second); got != "green" {
		t.Fatalf("retry did not reach the new release, last body %q", got)
	}
}
