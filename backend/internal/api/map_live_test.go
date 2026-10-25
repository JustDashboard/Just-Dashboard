package api

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/hostexec"
	"github.com/Wayy01/Just-Dashboard/backend/internal/httpx"
)

// The server map read from a real daemon: a container started with a volume
// is drawn running and wired to that volume, its compose-less siblings are
// still boxes, and a container that exited on its own is drawn as the
// failure it is — the reading every other source on the map is joined to.
func TestLiveServerMapReadsTheRealDaemon(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 for the Docker map test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	docker := func(args ...string) string {
		t.Helper()
		raw, err := hostexec.Command(ctx, "docker", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("docker %v: %v: %s", args, err, raw)
		}
		return strings.TrimSpace(string(raw))
	}
	cleanup := func() {
		_ = hostexec.Command(context.Background(), "docker", "rm", "-f", "jd-map-live-web", "jd-map-live-crash").Run()
		_ = hostexec.Command(context.Background(), "docker", "volume", "rm", "-f", "jd-map-live-data").Run()
	}
	cleanup()
	t.Cleanup(cleanup)
	web := docker("run", "-d", "--name", "jd-map-live-web", "-p", "127.0.0.1::80",
		"-v", "jd-map-live-data:/data", "nginx:alpine")
	docker("run", "-d", "--name", "jd-map-live-crash", "alpine", "sh", "-c", "exit 3")
	docker("wait", "jd-map-live-crash")

	s := testServer(t)
	// The test server is built for no host; this one reads the real daemon.
	s.modules.docker = dockerx.New("unix:///var/run/docker.sock")
	admin := &httpx.Principal{User: &auth.User{ID: 1, Username: "live"}, Role: auth.RoleAdmin, Kind: "session"}
	var m mapResponse
	deadline := time.Now().Add(time.Minute)
	for {
		m = filterServerMap(buildServerMap(s.readServerMap(admin)), admin)
		if k := kindOf(t, m, "containers"); k.State == "read" && k.Total > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the containers were never read: %+v", kindOf(t, m, "containers"))
		}
		time.Sleep(500 * time.Millisecond)
	}

	running := itemOf(t, m, "containers", "container:"+web)
	if running.State != "running" || running.Tone != mapOK || running.Product != "nginx" {
		t.Errorf("the running container = %+v", running)
	}
	var crashed mapItem
	for _, item := range kindOf(t, m, "containers").Items {
		if item.Name == "jd-map-live-crash" {
			crashed = item
		}
	}
	if crashed.State != "exited (3)" || crashed.Tone != mapDanger {
		t.Errorf("a container that exited 3 on its own = %+v", crashed)
	}
	// Volumes come from Docker's disk-usage walk, which a cold daemon may
	// still be making: wait for it rather than call the line missing.
	for time.Now().Before(deadline) {
		if k := kindOf(t, m, "volumes"); k.State == "read" {
			break
		}
		time.Sleep(500 * time.Millisecond)
		m = filterServerMap(buildServerMap(s.readServerMap(admin)), admin)
	}
	if !hasLink(m, "container:"+web, "volume:jd-map-live-data", "mount") {
		t.Errorf("no mount line from the container to its volume: %v", m.Links)
	}
	if v := itemOf(t, m, "volumes", "volume:jd-map-live-data"); v.State != "in use" {
		t.Errorf("the mounted volume = %+v", v)
	}
	for _, k := range m.Kinds {
		if k.State == "unread" && k.Reason == "" {
			t.Errorf("%s is unread without a reason", k.Key)
		}
		t.Logf("%-12s %-7s %3d %s", k.Key, k.State, k.Total, k.Reason)
	}
}
