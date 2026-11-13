package proxysvc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// dockerShim puts a docker first on PATH whose body is the given shell.
func dockerShim(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// daemonDown is the docker CLI on a host whose daemon does not answer: the
// image always ships the CLI, so this is every install without a socket.
const daemonDown = `echo "Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?" >&2
exit 1`

// ingressHost is a Debian nginx tree with one site, behind a service that
// looks for the Docker ingress as production does.
func ingressHost(t *testing.T) *Service {
	t.Helper()
	_, root := debianTree(t)
	writeFile(t, filepath.Join(root, "sites-available", "app"), "server {\n  listen 80;\n  server_name app.example.com;\n}\n")
	symlink(t, "../sites-available/app", filepath.Join(root, "sites-enabled", "app"))
	return NewWithDockerIngress(root, filepath.Join(root, "Caddyfile"))
}

// A Docker daemon that does not answer used to take nginx's sites with it:
// the list read them, then returned nothing and an error the handler turned
// into "internal error". A page now gets the sites and the reason beside
// them; a caller that needs the whole list still gets the reason as an error.
func TestSitesAreReadWhenTheIngressLookupFails(t *testing.T) {
	dockerShim(t, daemonDown)
	service := ingressHost(t)

	hosts, problem, err := service.ReadVHosts(context.Background())
	if err != nil {
		t.Fatalf("the read failed: %v", err)
	}
	if len(hosts) != 1 || hosts[0].Name != "app" {
		t.Fatalf("got %+v, want nginx's one site", hosts)
	}
	if problem == nil || problem.Code != IngressUnreachable || !problem.Retryable() {
		t.Fatalf("got problem %+v, want %s and worth trying again", problem, IngressUnreachable)
	}
	if !strings.Contains(problem.Raw(), "Cannot connect to the Docker daemon") {
		t.Fatalf("what docker said was not kept: %q", problem.Raw())
	}
	if problem.Advice == "" || strings.Contains(problem.Message, "Cannot connect") {
		t.Fatalf("the sentence and the advice are the page's, docker's text the details': %+v", problem)
	}

	if _, err := service.ListVHosts(context.Background()); !errors.As(err, new(*IngressProblem)) {
		t.Fatalf("a caller of the whole list was not told: %v", err)
	}

	status := service.Availability(context.Background())
	if status.IngressProblem == nil || status.IngressProblem.Code != IngressUnreachable ||
		status.IngressProblem.Reason != problem.Advice || status.IngressProblem.Raw != problem.Raw() || !status.IngressProblem.Retryable {
		t.Fatalf("status says %+v, want the lookup's problem", status.IngressProblem)
	}
	if status.IngressState != "" {
		t.Fatalf("a lookup that failed is neither running nor provisionable, got %q", status.IngressState)
	}
}

// A healthy host fails the lookup too, and each case has its own sentence
// and its own remedy: none of them is Docker being down.
func TestTheLookupSaysWhichCaddyItCouldNotShare(t *testing.T) {
	second := strings.Replace(strings.Replace(ingressInspect, `"ID":"4f1c0ffee"`, `"ID":"5e2d0ffee"`, 1), `"Name":"/edge"`, `"Name":"/edge-two"`, 1)
	both := strings.TrimSuffix(strings.TrimSpace(ingressInspect), "]") + "," + strings.TrimPrefix(strings.TrimSpace(second), "[")
	plain := strings.Replace(ingressInspect, `,"443/tcp":[{"HostIp":"0.0.0.0","HostPort":"443"}]`, "", 1)
	unsaved := strings.Replace(ingressInspect, `"Type":"bind","Source":"/srv/edge/Caddyfile","Destination":"/etc/caddy/Caddyfile"`, `"Type":"bind","Source":"/srv/edge/Caddyfile","Destination":"/etc/other"`, 1)
	for name, c := range map[string]struct {
		inspect, code, says string
	}{
		"two public Caddies": {both, IngressAmbiguous, "JD_INGRESS_CONTAINER"},
		"no HTTPS port":      {plain, IngressUnusable, "443"},
		"nothing persisted":  {unsaved, IngressUnusable, "/config"},
	} {
		t.Run(name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "inspect.json")
			if err := os.WriteFile(file, []byte(c.inspect), 0o644); err != nil {
				t.Fatal(err)
			}
			dockerShim(t, `case "$1" in ps) echo 4f1c0ffee ;; inspect) cat "`+file+`" ;; esac`)
			service := ingressHost(t)

			hosts, problem, err := service.ReadVHosts(context.Background())
			if err != nil || len(hosts) != 1 {
				t.Fatalf("got %+v, %v, want nginx's one site", hosts, err)
			}
			if problem == nil || problem.Code != c.code || problem.Retryable() {
				t.Fatalf("got %+v, want %s, which trying again does not mend", problem, c.code)
			}
			if !strings.Contains(problem.Message+" "+problem.Advice, c.says) {
				t.Fatalf("%q / %q does not say %q", problem.Message, problem.Advice, c.says)
			}
		})
	}
}

// An ingress that is there and does not let its routes be read is not a
// partial list: the page would show a host with an ingress and none of its
// routes. It is the read's failure, under its own code and with what the
// container said.
func TestAnIngressThatCannotBeReadFailsTheList(t *testing.T) {
	file := filepath.Join(t.TempDir(), "inspect.json")
	if err := os.WriteFile(file, []byte(ingressInspect), 0o644); err != nil {
		t.Fatal(err)
	}
	dockerShim(t, `case "$1" in
ps) echo 4f1c0ffee ;;
inspect) cat "`+file+`" ;;
exec) echo "Error response from daemon: container 4f1c0ffee is restarting, wait until the container is running" >&2; exit 1 ;;
esac`)
	service := ingressHost(t)

	_, _, err := service.ReadVHosts(context.Background())
	var problem *IngressProblem
	if !errors.As(err, &problem) || problem.Code != IngressUnreadable || !problem.Retryable() {
		t.Fatalf("got %v, want %s", err, IngressUnreadable)
	}
	if !strings.Contains(problem.Raw(), "is restarting") || !strings.Contains(problem.Message, "edge") {
		t.Fatalf("got %q / %q, want the container named and what it said kept", problem.Message, problem.Raw())
	}
	if status := service.Availability(context.Background()); status.IngressProblem != nil || status.IngressState != IngressRunning {
		t.Fatalf("the lookup itself answered, so status has no problem: %+v", status)
	}
}

// The named ingress is the only one used, so its absence is said as that.
func TestANamedIngressThatIsNotRunningSaysSo(t *testing.T) {
	dockerShim(t, `echo "Error response from daemon: No such container: edge" >&2
exit 1`)
	_, root := debianTree(t)
	service := NewWithIngressContainer(root, filepath.Join(root, "Caddyfile"), "edge")

	_, err := service.dockerCaddy(context.Background())
	var problem *IngressProblem
	if !errors.As(err, &problem) || problem.Code != IngressNotRunning {
		t.Fatalf("got %v, want %s", err, IngressNotRunning)
	}
	if !strings.Contains(problem.Raw(), "No such container") || !strings.Contains(problem.Advice, "JD_INGRESS_CONTAINER") {
		t.Fatalf("got %q / %q", problem.Advice, problem.Raw())
	}
}
