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
	"sync"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
)

// A replicated release behind a real Caddy: the route shares requests between
// every replica, the inventory lists each of them, a replica that dies costs a
// visitor nothing, and a Caddy that lost the replicas' network is put back on
// it by one maintenance pass. The ingress and the replicas are the test's own,
// published on loopback, and removed with it.
func TestLiveDockerCaddyBalancesReplicas(t *testing.T) {
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
	stamp := time.Now().UnixNano()
	ingressNetwork := fmt.Sprintf("jd-ingress-replicas-%d", stamp)
	docker("network", "create", ingressNetwork)
	defer hostexec.Command(context.Background(), "docker", "network", "rm", ingressNetwork).Run()
	// The replicas' own network, as a deployment's are on one the ingress
	// joins, so taking the ingress off it is something the test can do.
	appNetwork := fmt.Sprintf("jd-replicas-%d", stamp)
	docker("network", "create", appNetwork)
	defer hostexec.Command(context.Background(), "docker", "network", "rm", appNetwork).Run()
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

	replicas := []string{}
	upstreams := []string{}
	for replica := 1; replica <= 3; replica++ {
		app := docker("run", "-d", "--network", appNetwork, "-p", "127.0.0.1::80", "caddy:2-alpine",
			"caddy", "respond", "--listen", ":80", "--header", fmt.Sprintf("X-Replica: %d", replica), "release answer")
		defer hostexec.Command(context.Background(), "docker", "rm", "-f", "-v", app).Run()
		replicas = append(replicas, app)
		upstreams = append(upstreams, "http://127.0.0.1:"+strings.TrimPrefix(docker("port", app, "80/tcp"), "127.0.0.1:"))
	}

	client := &http.Client{Timeout: 10 * time.Second}
	ask := func() (string, error) {
		req, _ := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("http://127.0.0.1:%d/", ingressPort), nil)
		req.Host = "replicas.example.test"
		res, err := client.Do(req)
		if err != nil {
			return "", err
		}
		defer res.Body.Close()
		body, _ := io.ReadAll(res.Body)
		if res.StatusCode != http.StatusOK || strings.TrimSpace(string(body)) != "release answer" {
			return "", fmt.Errorf("answered %d %q", res.StatusCode, body)
		}
		return res.Header.Get("X-Replica"), nil
	}
	shared := func(want int) map[string]int {
		t.Helper()
		answered := map[string]int{}
		for i := 0; i < 60; i++ {
			replica, err := ask()
			if err != nil {
				t.Fatalf("request %d: %v; ingress log: %s", i, err, docker("logs", "--tail", "20", id))
			}
			answered[replica]++
		}
		if len(answered) != want {
			t.Fatalf("requests were shared between %v, want %d replicas", answered, want)
		}
		return answered
	}

	service := New(dir, source)
	route := DeploymentRoute{Name: "just-dashboard-env-44.conf", Domains: []string{"replicas.example.test"}, Upstreams: upstreams}
	result, err := service.applyDockerCaddyRoute(ctx, edge, route)
	if err != nil || !result.Verified {
		t.Fatalf("apply=%+v error=%v; fixture log: %s", result, err, docker("logs", id))
	}
	snapshot, err := edge.snapshot(ctx, route.Name)
	if err != nil {
		t.Fatal(err)
	}
	targets, ok := parseDockerCaddyTargets(snapshot.Content)
	if !ok || len(targets) != 3 || !strings.Contains(snapshot.Content, "lb_policy least_conn") {
		t.Fatalf("written route targets %+v:\n%s", targets, snapshot.Content)
	}
	// What verification compares against: the route rebuilt from every
	// replica's publication, which must be what was written.
	if want, _, err := edge.routeContent(ctx, route); err != nil || want != snapshot.Content {
		t.Fatalf("rebuilt route differs (%v):\n%s", err, want)
	}
	t.Logf("three replicas answered %v", shared(3))
	inventory, err := edge.vhosts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	listed := false
	for _, site := range inventory {
		if site.Name == route.Name {
			listed = len(site.Upstreams) == 3
		}
	}
	if !listed {
		t.Fatalf("the inventory does not list the route's three replicas: %+v", inventory)
	}

	// A replica dies under steady traffic. Caddy tries the next replica for
	// any request it could not connect for, so nobody notices.
	var failures []string
	var mu sync.Mutex
	stop := make(chan struct{})
	var wait sync.WaitGroup
	total := 0
	for worker := 0; worker < 4; worker++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for {
				select {
				case <-stop:
					return
				case <-time.After(20 * time.Millisecond):
				}
				_, err := ask()
				mu.Lock()
				total++
				if err != nil {
					failures = append(failures, err.Error())
				}
				mu.Unlock()
			}
		}()
	}
	time.Sleep(2 * time.Second)
	docker("kill", "--signal", "SIGKILL", replicas[1])
	time.Sleep(14 * time.Second)
	close(stop)
	wait.Wait()
	if len(failures) > 0 {
		t.Fatalf("a replica's death lost %d of %d requests; first: %s", len(failures), total, failures[0])
	}
	t.Logf("replica loss: %d requests, none lost", total)
	t.Logf("the two left answered %v", shared(2))

	// The ingress loses the replicas' network, as a recreated Caddy does: one
	// maintenance pass joins it again for every replica, and they answer.
	docker("network", "disconnect", appNetwork, id)
	if _, err := ask(); err == nil {
		t.Fatal("the route still answered with the ingress off the replicas' network")
	}
	repaired := map[string]bool{}
	if _, err := edge.reconcile(ctx, func(name string, ok bool) { repaired[name] = ok }); err != nil || !repaired[route.Name] {
		t.Fatalf("maintenance pass = %v, repaired %v", err, repaired)
	}
	shared(2)
}
