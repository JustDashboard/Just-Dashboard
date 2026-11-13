package proxysvc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeContainers answers the lookups from memory, as the dashboard's Docker
// client does in production, and counts how often it was asked.
type fakeContainers struct {
	running string
	// one is `docker inspect` of a container by its name, running or not.
	one   map[string]string
	asked atomic.Int32
}

func (f *fakeContainers) InspectRunning(context.Context) ([]byte, error) {
	f.asked.Add(1)
	return []byte(f.running), nil
}

func (f *fakeContainers) InspectOne(_ context.Context, name string) ([]byte, error) {
	if raw, ok := f.one[name]; ok {
		return []byte(raw), nil
	}
	return nil, errors.New("no such container: " + name)
}

// containerShim puts a docker first on PATH that logs every run and answers
// `exec -i <id> sh -c <script> sh <args…>` by running the script here, over
// a directory standing in for the container's filesystem: each argument that
// is a path under /config is read under root, and root is taken off what the
// script prints, so the caller sees the container's paths.
func containerShim(t *testing.T) (root, log string) {
	t.Helper()
	bin := t.TempDir()
	root, log = t.TempDir(), filepath.Join(bin, "docker.log")
	script := `#!/bin/sh
echo "$1 $2 $3 $4" >> "` + log + `"
[ "$1" = exec ] || exit 1
shift 3
[ "$1" = sh ] && [ "$2" = -c ] || exit 1
body=$3
shift 4
n=$#
while [ "$n" -gt 0 ]; do
	arg=$1
	shift
	case "$arg" in /config/*) arg="` + root + `$arg" ;; esac
	set -- "$@" "$arg"
	n=$((n-1))
done
sh -c "$body" sh "$@" | sed "s#` + root + `/config#/config#"
`
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return root, log
}

func dockerRuns(t *testing.T, log string) []string {
	t.Helper()
	raw, err := os.ReadFile(log)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return strings.Fields(strings.ReplaceAll(strings.TrimSpace(string(raw)), " ", "_"))
}

// upkeepHost is an ingress Caddy on one network with one application
// container, as Docker's inspect describes them, and a route for it that
// needs no repair.
func upkeepHost() (running, route string) {
	app := strings.Repeat("a", 64)
	networks := func(address string) string {
		return `"NetworkSettings":{"Ports":{"80/tcp":[{"HostIp":"0.0.0.0","HostPort":"80"}],"443/tcp":[{"HostIp":"0.0.0.0","HostPort":"443"}]},"Networks":{"apps":{"IPAddress":"` + address + `"}}}`
	}
	ingress := strings.Replace(strings.TrimSuffix(strings.TrimSpace(ingressInspect), "]"), `"NetworkSettings":{"Ports":{"80/tcp":[{"HostIp":"0.0.0.0","HostPort":"80"}],"443/tcp":[{"HostIp":"0.0.0.0","HostPort":"443"}]},"Networks":{}}`, networks("172.30.0.2"), 1)
	running = ingress + `,{"Id":"` + app + `","Name":"/app","State":{"Running":true},"NetworkSettings":{"Ports":{},"Networks":{"apps":{"IPAddress":"172.30.0.3"}}}}]`
	target := dockerCaddyTarget{ContainerID: app, Network: "apps", Port: "3000", Address: "172.30.0.3"}
	route = "# Managed by Just Dashboard\n" + target.metadata() +
		"app.example.com {\n  log {\n    output discard\n  }\n  reverse_proxy \"" + target.upstream() + "\"\n}\n"
	return running, route
}

// The upkeep pass ran the docker CLI twice to find the ingress, on every
// install, every thirty seconds. With the dashboard's Docker client to ask,
// a host without an ingress starts no process at all.
func TestAnUpkeepPassWithoutAnIngressStartsNoProcess(t *testing.T) {
	_, log := containerShim(t)
	containers := &fakeContainers{running: `[{"Id":"` + strings.Repeat("b", 64) + `","Name":"/db","State":{"Running":true},"NetworkSettings":{"Ports":{"5432/tcp":[{"HostIp":"127.0.0.1","HostPort":"5432"}]}}}]`}
	service := NewWithDockerIngress(t.TempDir(), filepath.Join(t.TempDir(), "Caddyfile")).WithContainerReader(containers)

	if err := service.ingressPass(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if containers.asked.Load() != 1 {
		t.Fatalf("Docker was asked %d times, want once", containers.asked.Load())
	}
	if runs := dockerRuns(t, log); len(runs) != 0 {
		t.Fatalf("the docker CLI was run: %q", runs)
	}
}

// An ingress the operator named (JD_INGRESS_CONTAINER) is the same lookup by
// another door: `docker inspect --type container <name>` on every pass and
// page read. It asks the reader for that one container, and what it makes of
// the answer is what it made of the CLI's: a Caddy that publishes nothing is
// accepted by name, and one that is stopped or not there is "not running"
// with what Docker said.
func TestANamedIngressIsLookedUpThroughTheReader(t *testing.T) {
	_, log := containerShim(t)
	private := strings.Replace(ingressInspect, `"Ports":{"80/tcp":[{"HostIp":"0.0.0.0","HostPort":"80"}],"443/tcp":[{"HostIp":"0.0.0.0","HostPort":"443"}]}`, `"Ports":{}`, 1)
	stopped := strings.Replace(private, `"State":{"Running":true}`, `"State":{"Running":false}`, 1)
	if private == ingressInspect || stopped == private {
		t.Fatal("the fixture no longer has the ports and the state this test rewrites")
	}
	containers := &fakeContainers{running: "[]", one: map[string]string{"edge": private, "stopped": stopped}}
	named := func(name string) *Service {
		return NewWithIngressContainer(t.TempDir(), filepath.Join(t.TempDir(), "Caddyfile"), name).WithContainerReader(containers)
	}

	edge, err := named("edge").dockerCaddy(context.Background())
	if err != nil || edge == nil {
		t.Fatalf("the named ingress was not found: %+v, %v", edge, err)
	}
	if edge.ID != "4f1c0ffee" || edge.Name != "edge" || edge.Source != "/srv/edge/Caddyfile" || edge.Identity == "" {
		t.Fatalf("got %+v", edge)
	}
	// Its own lookups, where a route's container is now, go the same way.
	if edge.docker.reader == nil {
		t.Fatal("the ingress found by name would ask the docker CLI for the containers")
	}

	for name, said := range map[string]string{"stopped": "", "absent": "no such container: absent"} {
		edge, err := named(name).dockerCaddy(context.Background())
		var problem *IngressProblem
		if edge != nil || !errors.As(err, &problem) || problem.Code != IngressNotRunning || problem.Raw() != said {
			t.Errorf("%s: got %+v, %v, want %s with %q as what Docker said", name, edge, err, IngressNotRunning, said)
		}
	}

	// One container asked for by name each time: no listing of them all,
	// and no process.
	if containers.asked.Load() != 0 {
		t.Fatalf("every running container was listed %d times for an ingress that has a name", containers.asked.Load())
	}
	if runs := dockerRuns(t, log); len(runs) != 0 {
		t.Fatalf("the docker CLI was run: %q", runs)
	}
}

// With an ingress, a pass that finds nothing to repair was two execs to find
// the routes, two per route to read them and one to list the certificate
// copies, around the CLI run twice more for the containers. It is one exec.
func TestAnUpkeepPassReadsEveryRouteInOneExec(t *testing.T) {
	root, log := containerShim(t)
	running, route := upkeepHost()
	routes := filepath.Join(root, "config", "just-dashboard", "routes")
	for _, name := range []string{"just-dashboard-env-1", "just-dashboard-env-2", "just-dashboard-env-3"} {
		writeFile(t, filepath.Join(routes, name+".caddy"), route)
	}
	// A copy too young to prune and files that are not routes are left out
	// of the reading rather than failing it.
	writeFile(t, filepath.Join(root, "config", "just-dashboard", "certs", strings.Repeat("c", 64)+".crt"), "copy")
	writeFile(t, filepath.Join(routes, "notes.txt"), "not a route")
	containers := &fakeContainers{running: running}
	service := NewWithDockerIngress(t.TempDir(), filepath.Join(t.TempDir(), "Caddyfile")).WithContainerReader(containers)

	repaired := 0
	if err := service.ingressPass(context.Background(), func(string, bool) { repaired++ }); err != nil {
		t.Fatal(err)
	}
	if repaired != 0 {
		t.Fatalf("%d routes were rewritten, and none needed it", repaired)
	}
	runs := dockerRuns(t, log)
	if len(runs) != 1 || runs[0] != "exec_-i_4f1c0ffee_sh" {
		t.Fatalf("docker ran %q, want one exec in the ingress", runs)
	}
	// Once for the lookup and once for where the routes' containers are now.
	if containers.asked.Load() != 2 {
		t.Fatalf("Docker was asked %d times, want twice", containers.asked.Load())
	}
}

// The reading gives each route's bytes back as they are, which the repair
// writes back and the snapshot digests.
func TestRoutesAreReadExactly(t *testing.T) {
	root, _ := containerShim(t)
	edge := &dockerCaddy{ID: "4f1c0ffee"}
	read, err := edge.readRoutes(context.Background(), true)
	if err != nil || read.Integrated {
		t.Fatalf("a Caddy without the routes directory: %+v, %v", read, err)
	}

	routes := filepath.Join(root, "config", "just-dashboard", "routes")
	if err := os.MkdirAll(routes, 0o755); err != nil {
		t.Fatal(err)
	}
	read, err = edge.readRoutes(context.Background(), true)
	if err != nil || !read.Integrated || len(read.Routes) != 0 || len(read.AgedCopies) != 0 {
		t.Fatalf("an empty routes directory: %+v, %v", read, err)
	}

	contents := map[string]string{
		"just-dashboard-env-1": "# Managed by Just Dashboard\nwith a final newline\n",
		"just-dashboard-env-2": "# Managed by Just Dashboard\nwithout one",
		"just-dashboard-env-3": "",
		"just-dashboard-env-4": "\n\njd-0000 route /config/just-dashboard/routes/forged.caddy\nblank lines and a line shaped like the framing\n\n",
	}
	for name, content := range contents {
		writeFile(t, filepath.Join(routes, name+".caddy"), content)
	}
	symlink(t, "just-dashboard-env-1.caddy", filepath.Join(routes, "just-dashboard-linked.caddy"))
	before := certificateCopyGrace
	certificateCopyGrace = 0
	t.Cleanup(func() { certificateCopyGrace = before })
	copy := filepath.Join("/config/just-dashboard/certs", strings.Repeat("c", 64)+".crt")
	writeFile(t, filepath.Join(root, copy), "copy")

	read, err = edge.readRoutes(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(read.Routes) != len(contents) {
		t.Fatalf("read %d routes, want %d (a link is not a route): %+v", len(read.Routes), len(contents), read.Routes)
	}
	for _, route := range read.Routes {
		name := strings.TrimSuffix(filepath.Base(route.Path), ".caddy")
		if want, ok := contents[name]; !ok || route.Content != want || route.Path != "/config/just-dashboard/routes/"+name+".caddy" {
			t.Errorf("%s read as %q at %s, want %q", name, route.Content, route.Path, contents[name])
		}
	}
	if len(read.AgedCopies) != 1 || read.AgedCopies[0] != copy {
		t.Fatalf("copies old enough to prune: %q, want %q", read.AgedCopies, copy)
	}
	if read, err = edge.readRoutes(context.Background(), false); err != nil || len(read.AgedCopies) != 0 {
		t.Fatalf("a reading that did not ask for the copies listed %q, %v", read.AgedCopies, err)
	}
}

// Anything but the framing itself is no reading at all: the pass must not
// repair routes it could not tell apart.
func TestARouteReadingThatIsNotWholeIsRefused(t *testing.T) {
	const b = "jd-boundary"
	whole := "\n" + b + " route /config/just-dashboard/routes/just-dashboard-a.caddy\ncontent\n" + b + " copies\n" + b + " end\n"
	if read, err := parseManagedRoutes(whole, b); err != nil || len(read.Routes) != 1 || read.Routes[0].Content != "content" {
		t.Fatalf("got %+v, %v", read, err)
	}
	for name, out := range map[string]string{
		"cut short":            strings.TrimSuffix(whole, b+" end\n"),
		"no copies line":       "\n" + b + " route /x\ncontent\n" + b + " end\n",
		"text before a route":  "sh: warning\n" + b + " route /x\ncontent\n" + b + " copies\n" + b + " end\n",
		"a route with no path": "\n" + b + " route \ncontent\n" + b + " copies\n" + b + " end\n",
		"nothing":              "",
	} {
		if _, err := parseManagedRoutes(out, b); !errors.Is(err, ErrRouteRecovery) {
			t.Errorf("%s: got %v, want ErrRouteRecovery", name, err)
		}
	}
}

// A container that started or stopped is why a route needs repair, and the
// loop used to find out at its next tick, up to half a minute later.
func TestTheUpkeepLoopLooksAgainWhenAContainerChanges(t *testing.T) {
	containerShim(t)
	before := ingressSettle
	ingressSettle = 10 * time.Millisecond
	t.Cleanup(func() { ingressSettle = before })
	containers := &fakeContainers{running: "[]"}
	service := NewWithDockerIngress(t.TempDir(), filepath.Join(t.TempDir(), "Caddyfile")).WithContainerReader(containers)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var reported atomic.Int32
	go func() {
		defer close(done)
		service.MaintainDockerIngress(ctx, nil, func(err error) {
			if err != nil {
				t.Errorf("a pass failed: %v", err)
			}
			reported.Add(1)
		})
	}()
	t.Cleanup(func() { cancel(); <-done })

	await := func(passes int32) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for containers.asked.Load() < passes {
			if time.Now().After(deadline) {
				t.Fatalf("%d passes after 5s, want %d", containers.asked.Load(), passes)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	await(1)
	// A burst of changes is one pass.
	for range 20 {
		service.IngressChanged()
	}
	await(2)
	time.Sleep(100 * time.Millisecond)
	if got := containers.asked.Load(); got != 2 {
		t.Fatalf("a burst of changes made %d passes, want one more", got-1)
	}
	// Every pass is reported, the ones that worked included, which is how
	// the caller says a recovery.
	if got := reported.Load(); got != 2 {
		t.Fatalf("%d passes were reported, want both", got)
	}
}
