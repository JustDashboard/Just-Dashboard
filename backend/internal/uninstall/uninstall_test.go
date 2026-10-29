package uninstall

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
	"github.com/Wayy01/Just-Dashboard/backend/internal/selfupdate"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// Every way the sibling could fail to reach what it has to remove is a wrong
// flag here.
func TestSiblingArgv(t *testing.T) {
	s := New(Options{DataDir: "/var/lib/just-dashboard", DockerHost: "unix:///var/run/docker.sock", Log: quiet()})
	line := strings.Join(s.siblingArgs(&Run{Dir: "/opt/Just-Dashboard", Image: "just-dashboard-backend:latest"}), " ")
	for _, want := range []string{
		"run --detach --rm",
		"--name " + SiblingContainer,
		"--network host",
		"--pid host",
		"--privileged",
		"--restart no",
		"-v /var/run/docker.sock:/var/run/docker.sock",
		"-v /opt/Just-Dashboard:/opt/Just-Dashboard",
		"-v /var/lib/just-dashboard:/var/lib/just-dashboard",
		"-w /opt/Just-Dashboard",
		"--entrypoint /usr/local/bin/just-dashboard",
		"just-dashboard-backend:latest -self-uninstall -state-dir /var/lib/just-dashboard",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("argv is missing %q:\n%s", want, line)
		}
	}
	if strings.Contains(line, "JD_MASTER_KEY") {
		t.Fatal("the sibling was handed a secret it has no use for")
	}
}

func TestDeletableRefusesSystemDirectories(t *testing.T) {
	for _, path := range []string{"/", "/var", "/var/lib", "/etc", "/home", "relative", "/var/lib/../lib"} {
		if deletable(path) == nil {
			t.Fatalf("%s was allowed", path)
		}
	}
	for _, path := range []string{"/var/lib/just-dashboard", "/var/backups/just-dashboard", "/tmp/just-dashboard", "/data"} {
		if err := deletable(path); err != nil {
			t.Fatalf("%s was refused: %v", path, err)
		}
	}
}

// fakeDocker is a host's worth of Docker objects, and what was removed.
type fakeDocker struct {
	containers []dockerx.Container
	networks   []dockerx.Network
	volumes    []dockerx.Volume
	images     []dockerx.Image
	removed    []string
	failVolume string
}

func (f *fakeDocker) ListContainers(context.Context, bool) ([]dockerx.Container, error) {
	return f.containers, nil
}
func (f *fakeDocker) RemoveContainer(_ context.Context, id string, _, _ bool) error {
	f.removed = append(f.removed, "container:"+id)
	return nil
}
func (f *fakeDocker) ListNetworks(context.Context) ([]dockerx.Network, error) { return f.networks, nil }
func (f *fakeDocker) DisconnectNetwork(_ context.Context, network, container string, _ bool) error {
	f.removed = append(f.removed, "disconnect:"+network+":"+container)
	return nil
}
func (f *fakeDocker) RemoveNetwork(_ context.Context, id string) error {
	f.removed = append(f.removed, "network:"+id)
	return nil
}
func (f *fakeDocker) ListVolumes(context.Context) ([]dockerx.Volume, error) { return f.volumes, nil }
func (f *fakeDocker) RemoveVolume(_ context.Context, name string, _ bool) error {
	if name == f.failVolume {
		return errors.New("volume is in use")
	}
	f.removed = append(f.removed, "volume:"+name)
	return nil
}
func (f *fakeDocker) ListImages(context.Context, bool) ([]dockerx.Image, error) { return f.images, nil }
func (f *fakeDocker) RemoveImage(_ context.Context, id string, _, _ bool) ([]string, error) {
	f.removed = append(f.removed, "image:"+id)
	return nil, nil
}

func hostWithDeployments() *fakeDocker {
	managed := map[string]string{managedLabel: "true"}
	return &fakeDocker{
		containers: []dockerx.Container{
			{ID: "c1", Name: "jd-e1-r3", Labels: managed},
			{ID: "c2", Name: "jd-e2-web-1", Labels: map[string]string{managedLabel: "true", composeProjectLabel: "jd-e2"}},
			{ID: "c3", Name: ingressContainer, Labels: map[string]string{ingressLabel: "true"}},
			{ID: "own", Name: "just-dashboard-backend-1", Labels: map[string]string{managedLabel: "true", composeProjectLabel: "just-dashboard"}},
			{ID: "pg", Name: "jd-postgres"},
		},
		networks: []dockerx.Network{
			{ID: "n1", Name: "jd-e2_default", Labels: map[string]string{composeProjectLabel: "jd-e2"}},
			{ID: "n2", Name: "jd-e1-db-abc", Labels: managed, Endpoints: []dockerx.NetworkEndpoint{{Container: "pg", Name: "jd-postgres"}}},
			{ID: "n3", Name: "just-dashboard_internal", Labels: map[string]string{composeProjectLabel: "just-dashboard"}},
			{ID: "n4", Name: "bridge", Labels: map[string]string{}},
		},
		volumes: []dockerx.Volume{
			{Name: "jd-e2_data", Labels: map[string]string{composeProjectLabel: "jd-e2"}},
			{Name: "uploads-1a2b", Labels: map[string]string{}},
			{Name: "just-dashboard-ingress-data", Labels: map[string]string{}},
			{Name: "just-dashboard_caddy-data", Labels: map[string]string{composeProjectLabel: "just-dashboard"}},
			{Name: "jd-postgres-data", Labels: map[string]string{}},
		},
		images: []dockerx.Image{
			{ID: "sha256:built", RepoTags: []string{"just-dashboard/deployment-1:run-3"}},
			{ID: "sha256:pulled", RepoDigests: []string{"nginx@sha256:abc"}},
			{ID: "sha256:own", RepoTags: []string{"just-dashboard-backend:latest"}},
			{ID: "sha256:other", RepoTags: []string{"postgres:17"}},
		},
	}
}

func targets(context.Context) (int, []deploy.RemovalTarget, error) {
	return 2, []deploy.RemovalTarget{
		{Kind: "compose_stack", ResourceID: "jd-e2"},
		{Kind: "docker_volume", ResourceID: "uploads-1a2b"},
		{Kind: "bind_path", ResourceID: "/srv/app/uploads"},
		{Kind: "proxy_site", ResourceID: "just-dashboard-env-1"},
		{Kind: "docker_image", ResourceID: "sha256:pulled"},
	}, nil
}

// What a wipe takes is what the deployments own — by label, and by what the
// database says — and nothing of the dashboard's own stack or of the
// operator's.
func TestInventoryTakesOnlyWhatDeploymentsOwn(t *testing.T) {
	docker := hostWithDeployments()
	owned, err := Teardown{Docker: docker, Targets: targets, OwnProject: "just-dashboard"}.Inventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	names := func(items []Item) string {
		out := []string{}
		for _, item := range items {
			out = append(out, item.Name)
		}
		return strings.Join(out, ",")
	}
	if got := names(owned.Containers); got != "jd-e1-r3,jd-e2-web-1" {
		t.Fatalf("containers = %s", got)
	}
	if owned.Ingress == nil || owned.Ingress.Name != ingressContainer {
		t.Fatalf("ingress = %+v", owned.Ingress)
	}
	if got := names(owned.Networks); got != "jd-e1-db-abc,jd-e2_default" {
		t.Fatalf("networks = %s", got)
	}
	if got := names(owned.Volumes); got != "jd-e2_data,just-dashboard-ingress-data,uploads-1a2b" {
		t.Fatalf("volumes = %s", got)
	}
	if got := names(owned.Images); got != "just-dashboard/deployment-1:run-3,nginx@sha256:abc" {
		t.Fatalf("images = %s", got)
	}
	if got := names(owned.Paths); got != "/srv/app/uploads" {
		t.Fatalf("paths = %s", got)
	}
	if got := names(owned.Routes); got != "just-dashboard-env-1" {
		t.Fatalf("routes = %s", got)
	}
	if owned.Projects != 2 {
		t.Fatalf("projects = %d", owned.Projects)
	}
}

// A database a deployment was linked to is let go of rather than removed, and
// one volume that will not go is a warning, not the end of the run.
func TestRemoveDisconnectsOthersAndCarriesOn(t *testing.T) {
	docker := hostWithDeployments()
	docker.failVolume = "uploads-1a2b"
	var routes, paths []string
	teardown := Teardown{
		Docker: docker, Targets: targets, OwnProject: "just-dashboard",
		RemoveRoute: func(_ context.Context, name string) error { routes = append(routes, name); return nil },
		RemovePath:  func(path string) error { paths = append(paths, path); return nil },
	}
	owned, err := teardown.Inventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	warnings := teardown.Remove(context.Background(), owned, &out)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "uploads-1a2b") {
		t.Fatalf("warnings = %v", warnings)
	}
	got := strings.Join(docker.removed, " ")
	for _, want := range []string{"container:c1", "container:c2", "container:c3", "disconnect:n2:pg", "network:n1",
		"network:n2", "volume:jd-e2_data", "image:just-dashboard/deployment-1:run-3", "image:sha256:pulled"} {
		if !strings.Contains(got, want) {
			t.Fatalf("%s was not done:\n%s", want, got)
		}
	}
	for _, never := range []string{"container:own", "container:pg", "network:n3", "network:n4", "caddy-data", "jd-postgres-data", "sha256:own", "sha256:other"} {
		if strings.Contains(got, never) {
			t.Fatalf("%s was removed:\n%s", never, got)
		}
	}
	// Containers before the networks they hold.
	if strings.Index(got, "container:c2") > strings.Index(got, "network:n1") {
		t.Fatalf("a network went before its container:\n%s", got)
	}
	if strings.Join(routes, ",") != "just-dashboard-env-1" || strings.Join(paths, ",") != "/srv/app/uploads" {
		t.Fatalf("routes = %v, paths = %v", routes, paths)
	}
}

// fakeHost records what the runner did, with a temporary directory standing
// in for the host's root.
type fakeHost struct {
	root     string
	commands []string
	failDown bool
}

func (h *fakeHost) Docker(_ context.Context, _ string, out io.Writer, args ...string) error {
	line := strings.Join(args, " ")
	h.commands = append(h.commands, "docker "+line)
	if h.failDown && strings.Contains(line, " down ") {
		return errors.New("exit status 1")
	}
	return nil
}

func (h *fakeHost) DockerOutput(_ context.Context, _ string, args ...string) ([]byte, error) {
	h.commands = append(h.commands, "docker "+strings.Join(args, " "))
	return []byte(`{"services":{
		"backend":{"image":"just-dashboard-backend:latest","build":{"context":"./backend"}},
		"frontend":{"image":"just-dashboard-frontend:latest","build":{"context":"./frontend"}},
		"proxy":{"image":"caddy:2-alpine"}}}`), nil
}

func (h *fakeHost) Run(_ context.Context, _ io.Writer, name string, args ...string) error {
	h.commands = append(h.commands, name+" "+strings.Join(args, " "))
	return nil
}

func (h *fakeHost) Path(path string) string { return filepath.Join(h.root, path) }

// fixture lays out an install under a fake host root: a checkout with its
// .env, a data directory and a backup directory. The run names them by their
// host paths, as a real one does.
func fixture(t *testing.T, mode Mode, deleteBackups bool) (*Store, *Run, *fakeHost) {
	t.Helper()
	host := &fakeHost{root: t.TempDir()}
	for _, dir := range []string{"/opt/jd", "/var/lib/just-dashboard/nginx-cache/site", "/var/backups/just-dashboard", "/tmp/just-dashboard"} {
		if err := os.MkdirAll(host.Path(dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for path, body := range map[string]string{
		"/opt/jd/.env":                         "JD_MASTER_KEY=abc\n",
		"/opt/jd/.env.jd-previous":             "JD_MASTER_KEY=abc\n",
		"/opt/jd/.env.example":                 "JD_MASTER_KEY=\n",
		"/opt/jd/docker-compose.yml":           "name: just-dashboard\n",
		"/var/lib/just-dashboard/vpsd.db":      "db",
		"/var/backups/just-dashboard/a.tar.gz": "archive",
	} {
		if err := os.WriteFile(host.Path(path), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	store := NewStore(host.Path("/var/lib/just-dashboard"))
	run := &Run{
		ID: "20261012T120000Z", Status: StatusPending, Mode: mode, DeleteBackups: deleteBackups,
		Dir: "/opt/jd", Compose: "docker-compose.yml", Project: "just-dashboard", Image: "just-dashboard-backend:latest",
		EnvPath: "/opt/jd/.env", DataDir: "/var/lib/just-dashboard", BackupDir: "/var/backups/just-dashboard",
	}
	if err := store.Save(run); err != nil {
		t.Fatal(err)
	}
	return store, run, host
}

func exists(path string) bool { _, err := os.Stat(path); return err == nil }

// Keeping removes the program and nothing it made, and leaves the key to the
// data beside the data.
func TestKeepLeavesDataAndKeepsTheKey(t *testing.T) {
	store, run, host := fixture(t, ModeKeep, false)
	var out bytes.Buffer
	if err := carryOut(context.Background(), store, run, &out, host); err != nil {
		t.Fatal(err)
	}
	cmds := strings.Join(host.commands, "\n")
	for _, want := range []string{
		"systemctl stop jd-terminal-*",
		"docker compose -f docker-compose.yml -p just-dashboard down --remove-orphans --timeout 30",
		"docker image rm just-dashboard-frontend:latest",
		"systemd-run --unit just-dashboard-uninstall-20261012t120000z",
	} {
		if !strings.Contains(cmds, want) {
			t.Fatalf("missing %q:\n%s", want, cmds)
		}
	}
	for _, never := range []string{"--volumes", "\ndocker image rm just-dashboard-backend:latest\n", "caddy:2-alpine", "/var/lib/just-dashboard\n"} {
		if strings.Contains("\n"+cmds+"\n", never) {
			t.Fatalf("keep ran %q:\n%s", never, cmds)
		}
	}
	if !exists(host.Path("/var/lib/just-dashboard/vpsd.db")) || !exists(host.Path(run.EnvPath)) ||
		!exists(host.Path("/var/backups/just-dashboard/a.tar.gz")) {
		t.Fatal("keep deleted something it should have kept")
	}
	if exists(host.Path("/tmp/just-dashboard")) {
		t.Fatal("the terminal's temporary files outlived the dashboard")
	}
	if b, err := os.ReadFile(host.Path("/var/lib/just-dashboard/" + KeptEnv)); err != nil || string(b) != "JD_MASTER_KEY=abc\n" {
		t.Fatalf("kept env = %q, %v", b, err)
	}
	got, _ := store.Load()
	if got.Status != StatusSuccess || got.Phase != PhaseFinished {
		t.Fatalf("record = %s/%s", got.Status, got.Phase)
	}
}

// A wipe deletes the data and every copy of the key, keeps what a site still
// needs, and keeps the backups unless asked.
func TestWipeDeletesDataAndKeyButNotBackupsUnlessAsked(t *testing.T) {
	store, run, host := fixture(t, ModeWipe, false)
	run.Keep = []string{nginxCache}
	var out bytes.Buffer
	if err := carryOut(context.Background(), store, run, &out, host); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(host.commands, "\n"), "down --remove-orphans --timeout 30 --volumes") {
		t.Fatalf("wipe kept the proxy's volumes:\n%s", strings.Join(host.commands, "\n"))
	}
	if exists(host.Path("/var/lib/just-dashboard/vpsd.db")) {
		t.Fatal("the database survived a wipe")
	}
	if !exists(host.Path("/var/lib/just-dashboard/nginx-cache/site")) {
		t.Fatal("a cache a site still uses was deleted")
	}
	if exists(host.Path("/opt/jd/.env")) || exists(host.Path("/opt/jd/.env.jd-previous")) {
		t.Fatal("a copy of the key survived a wipe")
	}
	if !exists(host.Path("/opt/jd/.env.example")) || !exists(host.Path("/opt/jd/docker-compose.yml")) {
		t.Fatal("the wipe removed the checkout's own files")
	}
	if !exists(host.Path("/var/backups/just-dashboard/a.tar.gz")) {
		t.Fatal("the backups went without being asked")
	}

	store, run, host = fixture(t, ModeWipe, true)
	if err := carryOut(context.Background(), store, run, io.Discard, host); err != nil {
		t.Fatal(err)
	}
	if exists(host.Path("/var/backups/just-dashboard")) {
		t.Fatal("the backups survived being asked to go")
	}
	if exists(host.Path("/var/lib/just-dashboard")) {
		t.Fatal("the data directory survived with nothing to keep in it")
	}
}

// A stack that will not come down is put back up, and the run fails without
// deleting anything.
func TestFailedStopStartsTheDashboardAgain(t *testing.T) {
	store, run, host := fixture(t, ModeWipe, true)
	host.failDown = true
	err := carryOut(context.Background(), store, run, io.Discard, host)
	if err == nil {
		t.Fatal("a failed stop reported success")
	}
	if !strings.Contains(strings.Join(host.commands, "\n"), "docker compose -f docker-compose.yml up -d") {
		t.Fatal("the dashboard was not started again")
	}
	if !exists(host.Path("/var/lib/just-dashboard/vpsd.db")) || !exists(host.Path(run.EnvPath)) {
		t.Fatal("files were deleted after the stop failed")
	}
	got, _ := store.Load()
	if got.Status != StatusFailed || got.Phase != PhaseStack {
		t.Fatalf("record = %s/%s, want failed in the stack phase", got.Status, got.Phase)
	}
}

// service is a Service with every outside dependency replaced.
func service(t *testing.T, launch func(context.Context, *Run) error) (*Service, *calls) {
	t.Helper()
	dir := t.TempDir()
	c := &calls{}
	s := New(Options{
		DataDir:    dir,
		DurableLog: filepath.Join(dir, "durable.log"),
		Locate: func(context.Context) (*selfupdate.Location, error) {
			return &selfupdate.Location{Dir: "/opt/jd", Compose: "docker-compose.yml", Image: "just-dashboard-backend:latest", Project: "just-dashboard"}, nil
		},
		Teardown: Teardown{Docker: hostWithDeployments(), Targets: targets},
		Pause: func(context.Context, io.Writer) error {
			c.add("pause")
			return nil
		},
		Resume: func(context.Context) error {
			c.add("resume")
			return nil
		},
		Log: quiet(),
	})
	s.launch = func(ctx context.Context, run *Run) error {
		c.add("launch")
		return launch(ctx, run)
	}
	return s, c
}

type calls struct {
	mu   sync.Mutex
	list []string
}

func (c *calls) add(name string) {
	c.mu.Lock()
	c.list = append(c.list, name)
	c.mu.Unlock()
}

func (c *calls) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.Join(c.list, ",")
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out")
}

func TestBeginPausesThenHandsOver(t *testing.T) {
	s, c := service(t, func(context.Context, *Run) error { return nil })
	run, err := s.Begin(context.Background(), Request{Mode: ModeWipe, DeleteBackups: true}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != StatusPending || run.Container != SiblingContainer || !run.DeleteBackups {
		t.Fatalf("run = %+v", run)
	}
	waitFor(t, func() bool { return c.String() == "pause,launch" })
	got, _ := s.store.Load()
	if got.Phase != PhaseStack || got.Status != StatusRunning {
		t.Fatalf("after hand-over: %s/%s", got.Status, got.Phase)
	}
	if log := s.Transcript(); !strings.Contains(log, "removed container jd-e1-r3") {
		t.Fatalf("the deployments were not removed:\n%s", log)
	}
	// The copy that outlives a wipe says the same.
	if b, _ := os.ReadFile(s.o.DurableLog); !strings.Contains(string(b), "removed container jd-e1-r3") {
		t.Fatalf("the durable transcript is missing the removal:\n%s", b)
	}
	// While it runs, nothing else in the lifecycle is admitted.
	if _, err := s.Begin(context.Background(), Request{Mode: ModeKeep}, "admin"); !errors.Is(err, ErrInProgress) {
		t.Fatalf("second uninstall = %v", err)
	}
	if err := selfupdate.CheckLifecycleIdle(s.o.DataDir); !errors.Is(err, selfupdate.ErrInProgress) {
		t.Fatalf("an update would be admitted: %v", err)
	}
}

func TestKeepLeavesDeploymentsAlone(t *testing.T) {
	s, c := service(t, func(context.Context, *Run) error { return nil })
	if _, err := s.Begin(context.Background(), Request{Mode: ModeKeep, DeleteBackups: true}, "admin"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return c.String() == "pause,launch" })
	if log := s.Transcript(); strings.Contains(log, "removed container") {
		t.Fatalf("keep removed a deployment:\n%s", log)
	}
	got, _ := s.store.Load()
	if got.DeleteBackups {
		t.Fatal("keep recorded deleting the backups")
	}
}

// A sibling that never starts leaves the dashboard up with its deployments
// paused, so the run says so and the stack is restarted to bring them back.
func TestFailedHandOverResumes(t *testing.T) {
	s, c := service(t, func(context.Context, *Run) error { return fmt.Errorf("no such image") })
	if _, err := s.Begin(context.Background(), Request{Mode: ModeKeep}, "admin"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return c.String() == "pause,launch,resume" })
	got, _ := s.store.Load()
	if got.Status != StatusFailed || !strings.Contains(got.Error, "no such image") {
		t.Fatalf("record = %+v", got)
	}
	if err := selfupdate.CheckLifecycleIdle(s.o.DataDir); err != nil {
		t.Fatalf("a failed uninstall still blocks the restart that resumes: %v", err)
	}
}

func TestBeginRefusesWhileARestartRuns(t *testing.T) {
	s, _ := service(t, func(context.Context, *Run) error { return nil })
	if err := os.WriteFile(filepath.Join(s.o.DataDir, "self-config.json"), []byte(`{"status":"running"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Begin(context.Background(), Request{Mode: ModeKeep}, "admin"); !errors.Is(err, ErrInProgress) {
		t.Fatalf("err = %v", err)
	}
	if _, err := s.Begin(context.Background(), Request{Mode: "nuke"}, "admin"); !errors.Is(err, ErrMode) {
		t.Fatalf("err = %v", err)
	}
}

// The process that starts after a kept uninstall is the reinstall.
func TestStartClearsAKeptRunAndItsKey(t *testing.T) {
	s, _ := service(t, nil)
	s.o.MasterKey = "abc"
	if err := s.store.Save(&Run{ID: "1", Status: StatusSuccess, Mode: ModeKeep}); err != nil {
		t.Fatal(err)
	}
	kept := filepath.Join(s.o.DataDir, KeptEnv)
	if err := os.WriteFile(kept, []byte("# settings\nJD_MASTER_KEY=abc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.Start(context.Background())
	if run, _ := s.store.Load(); run != nil {
		t.Fatal("the record of the uninstall outlived the reinstall")
	}
	if exists(kept) {
		t.Fatal("the copy of the key outlived the reinstall that has it")
	}

	// Another key: the copy may be the only one that opens the data.
	if err := s.store.Save(&Run{ID: "2", Status: StatusSuccess, Mode: ModeKeep}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(kept, []byte("JD_MASTER_KEY=old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.Start(context.Background())
	if !exists(kept) {
		t.Fatal("the only copy of the old key was removed")
	}
}

func TestStartClosesARunWithNoSibling(t *testing.T) {
	s, _ := service(t, nil)
	s.o.List = func(context.Context) ([]selfupdate.Sibling, error) { return nil, nil }
	if err := s.store.Save(&Run{ID: "1", Status: StatusRunning, Phase: PhaseDeployments, StartedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	s.Start(context.Background())
	got, _ := s.store.Load()
	if got.Status != StatusFailed || !strings.Contains(got.Error, "deployments") {
		t.Fatalf("record = %+v", got)
	}
}

// The Caddy in front of tailnet-only deployments is the deployments' too: a
// wipe takes it, its two volumes and its Caddyfile, and nothing else of the
// operator's that happens to run Caddy.
func TestTeardownTakesTheTailnetGate(t *testing.T) {
	docker := hostWithDeployments()
	gate := map[string]string{tailnetGateLabel: "true"}
	docker.containers = append(docker.containers,
		dockerx.Container{ID: "gate", Name: "just-dashboard-tailnet", Labels: gate},
		dockerx.Container{ID: "theirs", Name: "my-caddy", Labels: map[string]string{}})
	docker.volumes = append(docker.volumes,
		dockerx.Volume{Name: "just-dashboard-tailnet-config", Labels: gate},
		dockerx.Volume{Name: "just-dashboard-tailnet-data", Labels: gate})
	removedDir := false
	teardown := Teardown{Docker: docker, Targets: targets, OwnProject: "just-dashboard",
		RemoveTailnetGateDir: func() error { removedDir = true; return nil }}
	owned, err := teardown.Inventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if owned.TailnetGate == nil || owned.TailnetGate.ID != "gate" {
		t.Fatalf("gate = %+v", owned.TailnetGate)
	}
	var out bytes.Buffer
	if warnings := teardown.Remove(context.Background(), owned, &out); len(warnings) != 0 {
		t.Fatalf("warnings = %v", warnings)
	}
	got := strings.Join(docker.removed, " ")
	for _, want := range []string{"container:gate", "volume:just-dashboard-tailnet-config", "volume:just-dashboard-tailnet-data"} {
		if !strings.Contains(got, want) {
			t.Fatalf("%s was not done:\n%s", want, got)
		}
	}
	if strings.Contains(got, "container:theirs") || !removedDir {
		t.Fatalf("removed %s, dir %v", got, removedDir)
	}
}
