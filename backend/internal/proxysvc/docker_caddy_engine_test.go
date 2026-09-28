package proxysvc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ingressInspect is `docker inspect` of a Caddy that discovery accepts: it
// publishes 80 and 443 on every address, runs the stock command and persists
// its Caddyfile, /config and /data.
const ingressInspect = `[{"ID":"4f1c0ffee","Name":"/edge","State":{"Running":true},
"Config":{"Cmd":["caddy","run","--config","/etc/caddy/Caddyfile","--adapter","caddyfile"]},
"Mounts":[{"Type":"bind","Source":"/srv/edge/Caddyfile","Destination":"/etc/caddy/Caddyfile","RW":false},
{"Type":"volume","Source":"/var/lib/docker/volumes/edge-config/_data","Destination":"/config","RW":true},
{"Type":"volume","Source":"/var/lib/docker/volumes/edge-data/_data","Destination":"/data","RW":true}],
"NetworkSettings":{"Ports":{"80/tcp":[{"HostIp":"0.0.0.0","HostPort":"80"}],"443/tcp":[{"HostIp":"0.0.0.0","HostPort":"443"}]},"Networks":{}}}]`

// fakeDocker puts a docker first on PATH that lists one running container
// when JD_TEST_INGRESS is set (none otherwise), inspects it as ingressInspect,
// and answers `exec … caddy validate` with Caddy's own lines — failing when
// JD_TEST_CADDY_FAIL is set. Every call's argv goes to the returned log, and
// a caddy beside it records being run at all.
func fakeDocker(t *testing.T) (dockerLog, hostCaddyLog string) {
	t.Helper()
	dir := t.TempDir()
	dockerLog, hostCaddyLog = filepath.Join(dir, "docker.log"), filepath.Join(dir, "caddy.log")
	inspect := filepath.Join(dir, "inspect.json")
	if err := os.WriteFile(inspect, []byte(ingressInspect), 0o644); err != nil {
		t.Fatal(err)
	}
	docker := `#!/bin/sh
echo "$*" >> "` + dockerLog + `"
case "$1" in
ps) if [ -n "$JD_TEST_INGRESS" ]; then echo 4f1c0ffee; fi ;;
inspect) cat "` + inspect + `" ;;
exec)
	case "$3 $4" in
	"caddy validate")
		if [ -n "$JD_TEST_CADDY_FAIL" ]; then
			echo "Error: adapting config using caddyfile: /etc/caddy/Caddyfile:4: unrecognized directive: frob" >&2
			exit 1
		fi
		echo '{"level":"warn","ts":1790526498.09,"msg":"Caddyfile input is not formatted","adapter":"caddyfile","file":"/etc/caddy/Caddyfile","line":2}' >&2
		echo "Valid configuration" ;;
	"caddy reload") ;;
	esac ;;
esac
`
	caddy := "#!/bin/sh\necho \"$*\" >> \"" + hostCaddyLog + "\"\n"
	for name, body := range map[string]string{"docker": docker, "caddy": caddy} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dockerLog, hostCaddyLog
}

func readLog(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(b)
}

// Test config and Reload for the Docker ingress used to run the host's own
// caddy against /etc/caddy/Caddyfile — a binary an ingress host usually does
// not have, and a file that is not what the container serves. They run
// inside the container now, with the same arguments the deployment routes'
// own reload uses, and the test's lines come back placed.
func TestIngressIsTestedAndReloadedInsideItsContainer(t *testing.T) {
	dockerLog, hostCaddyLog := fakeDocker(t)
	t.Setenv("JD_TEST_INGRESS", "1")
	service := NewWithDockerIngress(t.TempDir(), filepath.Join(t.TempDir(), "Caddyfile"))

	res, err := service.Test(context.Background(), KindCaddyIngress)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Valid || res.Warnings != 1 || res.Diagnostics[0].File != "/etc/caddy/Caddyfile" || res.Diagnostics[0].Line != 2 ||
		res.Command != "docker exec edge caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile" {
		t.Fatalf("got %+v", res)
	}
	reload, err := service.Reload(context.Background(), KindCaddyIngress)
	if err != nil || !reload.Reloaded || !reload.Validation.Valid {
		t.Fatalf("got %+v, %v", reload, err)
	}
	var execs []string
	for _, line := range strings.Split(strings.TrimSpace(readLog(t, dockerLog)), "\n") {
		if strings.HasPrefix(line, "exec ") {
			execs = append(execs, line)
		}
	}
	want := []string{
		"exec 4f1c0ffee caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile",
		"exec 4f1c0ffee caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile",
		"exec 4f1c0ffee caddy reload --config /etc/caddy/Caddyfile --adapter caddyfile",
	}
	if strings.Join(execs, "\n") != strings.Join(want, "\n") {
		t.Fatalf("docker ran\n%s\nwant\n%s", strings.Join(execs, "\n"), strings.Join(want, "\n"))
	}
	if got := readLog(t, hostCaddyLog); got != "" {
		t.Fatalf("the host's caddy was run: %q", got)
	}
}

// A container whose Caddyfile fails its test is not reloaded, and the reason
// is Caddy's, placed at its line.
func TestIngressReloadIsRefusedWhenItsTestFails(t *testing.T) {
	dockerLog, _ := fakeDocker(t)
	t.Setenv("JD_TEST_INGRESS", "1")
	t.Setenv("JD_TEST_CADDY_FAIL", "1")
	service := NewWithDockerIngress(t.TempDir(), filepath.Join(t.TempDir(), "Caddyfile"))

	res, err := service.Reload(context.Background(), KindCaddyIngress)
	if !errors.Is(err, ErrInvalidConf) || res.Reloaded {
		t.Fatalf("got %+v, %v; want ErrInvalidConf and no reload", res, err)
	}
	if d := res.Validation.Diagnostics; len(d) != 1 || d[0].Line != 4 || !strings.Contains(d[0].Message, "unrecognized directive: frob") {
		t.Fatalf("the refusal is not placed: %+v", d)
	}
	if strings.Contains(readLog(t, dockerLog), "caddy reload") {
		t.Fatal("a Caddyfile that failed its test was reloaded")
	}
}

// With no ingress running — including a host where the first deployment would
// start one — there is nothing to test or reload, and nothing is run instead.
func TestIngressControlsNeedARunningIngress(t *testing.T) {
	dockerLog, hostCaddyLog := fakeDocker(t)
	service := NewWithDockerIngress(t.TempDir(), filepath.Join(t.TempDir(), "Caddyfile"))

	if _, err := service.Test(context.Background(), KindCaddyIngress); !errors.Is(err, ErrNoIngress) {
		t.Fatalf("test: %v, want ErrNoIngress", err)
	}
	if _, err := service.Reload(context.Background(), KindCaddyIngress); !errors.Is(err, ErrNoIngress) {
		t.Fatalf("reload: %v, want ErrNoIngress", err)
	}
	if strings.Contains(readLog(t, dockerLog), "exec") || readLog(t, hostCaddyLog) != "" {
		t.Fatalf("something ran without an ingress: docker %q, caddy %q", readLog(t, dockerLog), readLog(t, hostCaddyLog))
	}
}

// The status names an ingress only once it runs. One the first deployment
// would start is neither Caddy nor a container: the overview drew Caddy
// "running as a container" with Test and Reload for a container that did not
// exist.
func TestAvailabilityNamesOnlyARunningIngress(t *testing.T) {
	fakeDocker(t)
	t.Setenv("JD_TEST_INGRESS", "1")
	service := NewWithDockerIngress(t.TempDir(), filepath.Join(t.TempDir(), "Caddyfile"))
	a := service.Availability(context.Background())
	if !a.Caddy || a.IngressContainer != "edge" || a.IngressState != IngressRunning {
		t.Fatalf("a running ingress: %+v", a)
	}

	var later Availability
	later.setIngress(nil, true)
	if later.Caddy || later.IngressContainer != "" || later.IngressState != IngressProvisionable {
		t.Fatalf("an ingress the first deployment would start: %+v", later)
	}
	var none Availability
	none.setIngress(nil, false)
	if none.Caddy || none.IngressContainer != "" || none.IngressState != "" {
		t.Fatalf("no ingress at all: %+v", none)
	}
}
