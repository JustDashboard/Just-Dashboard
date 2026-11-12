package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

// ingressInspectJSON is `docker inspect` of a Caddy the lookup accepts.
const ingressInspectJSON = `[{"Id":"4f1c0ffee","Name":"/edge","State":{"Running":true},
"Config":{"Cmd":["caddy","run","--config","/etc/caddy/Caddyfile","--adapter","caddyfile"]},
"Mounts":[{"Type":"bind","Source":"/srv/edge/Caddyfile","Destination":"/etc/caddy/Caddyfile","RW":false},
{"Type":"volume","Source":"/var/lib/docker/volumes/edge-config/_data","Destination":"/config","RW":true},
{"Type":"volume","Source":"/var/lib/docker/volumes/edge-data/_data","Destination":"/data","RW":true}],
"NetworkSettings":{"Ports":{"80/tcp":[{"HostIp":"0.0.0.0","HostPort":"80"}],"443/tcp":[{"HostIp":"0.0.0.0","HostPort":"443"}]},"Networks":{}}}]`

// ingressLookupServer is a server with one nginx site, served over TLS too,
// whose proxy looks the Docker ingress up through a docker the test writes:
// setDocker replaces its body.
func ingressLookupServer(t *testing.T) (c *client, setDocker func(body string)) {
	t.Helper()
	c, s, root := vhostServer(t, func(string) string { return "exit 0" })
	site := filepath.Join(root, "sites-available", "app")
	if err := os.WriteFile(site, []byte("server {\n  listen 80;\n  listen 443 ssl;\n  server_name app.example.com;\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../sites-available/app", filepath.Join(root, "sites-enabled", "app")); err != nil {
		t.Fatal(err)
	}
	// The lookup through the docker CLI, which the file below stands in for,
	// whatever the server's own Docker client would have reached.
	s.modules.proxy = proxysvc.NewWithDockerIngress(root, filepath.Join(root, "Caddyfile"))
	return c, func(body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, "bin", "docker"), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

type ingressAnswer struct {
	Error struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		Reason    string `json:"reason"`
		Raw       string `json:"raw"`
		Retryable bool   `json:"retryable"`
	} `json:"error"`
}

// GET /proxy/vhosts answered 500 "internal error" whenever the Docker
// ingress lookup failed, and the Sites page was two words. On a host whose
// daemon does not answer it now lists nginx's sites, and the status the page
// reads beside it says what the lookup could not do and why.
func TestTheSiteListAnswersWhenDockerDoesNot(t *testing.T) {
	c, setDocker := ingressLookupServer(t)
	setDocker(`echo "Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?" >&2
exit 1`)

	w := c.do(http.MethodGet, "/api/v1/proxy/vhosts", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("the list answered %d: %s", w.Code, w.Body)
	}
	var sites []proxysvc.VHost
	if err := json.Unmarshal(w.Body.Bytes(), &sites); err != nil || len(sites) != 1 || sites[0].Name != "app" {
		t.Fatalf("got %s, want nginx's one site", w.Body)
	}

	w = c.do(http.MethodGet, "/api/v1/proxy/status", "", nil)
	var status struct {
		IngressState   string                  `json:"ingressState"`
		IngressProblem *proxysvc.IngressNotice `json:"ingressProblem"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil || w.Code != http.StatusOK {
		t.Fatalf("status answered %d: %s", w.Code, w.Body)
	}
	problem := status.IngressProblem
	if problem == nil || problem.Code != proxysvc.IngressUnreachable || problem.Reason == "" || !problem.Retryable ||
		!strings.Contains(problem.Raw, "Cannot connect to the Docker daemon") {
		t.Fatalf("status says %+v, want the lookup's problem with docker's own text as raw", problem)
	}
	if status.IngressState != "" {
		t.Fatalf("got ingress state %q for a lookup that did not answer", status.IngressState)
	}
}

// The failures that remain are answered as what they are: an ingress that
// is there and cannot be read is a 503 worth trying again, and containers
// that do not name one Caddy are a 409, each with its code, its reason and
// what Docker said. Neither is "internal error" or a bad request.
func TestAnIngressFailureIsAnsweredWithItsReason(t *testing.T) {
	c, setDocker := ingressLookupServer(t)
	inspect := filepath.Join(t.TempDir(), "inspect.json")
	if err := os.WriteFile(inspect, []byte(ingressInspectJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	setDocker(`case "$1" in
ps) echo 4f1c0ffee ;;
inspect) cat "` + inspect + `" ;;
exec) echo "Error response from daemon: container 4f1c0ffee is restarting, wait until the container is running" >&2; exit 1 ;;
esac`)

	w := c.do(http.MethodGet, "/api/v1/proxy/vhosts", "", nil)
	var answer ingressAnswer
	if err := json.Unmarshal(w.Body.Bytes(), &answer); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusServiceUnavailable || answer.Error.Code != proxysvc.IngressUnreadable || !answer.Error.Retryable ||
		answer.Error.Reason == "" || !strings.Contains(answer.Error.Raw, "is restarting") || strings.Contains(answer.Error.Message, "daemon") {
		t.Fatalf("got %d %s", w.Code, w.Body)
	}

	// Two public Caddies: no route can be written until one is named, and
	// every route that needs the ingress says so.
	two := strings.TrimSuffix(strings.TrimSpace(ingressInspectJSON), "]") + "," +
		strings.TrimPrefix(strings.Replace(strings.Replace(ingressInspectJSON, "4f1c0ffee", "5e2d0ffee", 1), "/edge", "/edge-two", 1), "[")
	if err := os.WriteFile(inspect, []byte(two), 0o644); err != nil {
		t.Fatal(err)
	}
	w = c.do(http.MethodPost, "/api/v1/proxy/test", `{"kind":"caddy-ingress"}`, nil)
	answer = ingressAnswer{}
	if err := json.Unmarshal(w.Body.Bytes(), &answer); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusConflict || answer.Error.Code != proxysvc.IngressAmbiguous || answer.Error.Retryable ||
		!strings.Contains(answer.Error.Reason, "JD_INGRESS_CONTAINER") {
		t.Fatalf("got %d %s", w.Code, w.Body)
	}
	// The list still answers, since nginx's site is not Docker's to hide.
	if w := c.do(http.MethodGet, "/api/v1/proxy/vhosts", "", nil); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"name":"app"`) {
		t.Fatalf("got %d %s", w.Code, w.Body)
	}
}

// The TLS fleet is the sites and the watched endpoints, read through the
// same list, and it answered 500 "internal error" with it: the report of
// every site was gone from a host whose Docker daemon did not answer, though
// nginx's sites owe Docker nothing. It lists them, and an ingress that is
// found and cannot be read fails it as what it is.
func TestTheTLSFleetAnswersWhenDockerDoesNot(t *testing.T) {
	c, setDocker := ingressLookupServer(t)
	setDocker(`echo "Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?" >&2
exit 1`)

	w := c.do(http.MethodGet, "/api/v1/certificates/scans/latest", "", nil)
	var fleet struct {
		Targets []struct {
			Host  string   `json:"host"`
			Port  int      `json:"port"`
			Sites []string `json:"sites"`
		} `json:"targets"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &fleet); err != nil || w.Code != http.StatusOK {
		t.Fatalf("the fleet answered %d: %s", w.Code, w.Body)
	}
	if len(fleet.Targets) != 1 || fleet.Targets[0].Host != "app.example.com" || fleet.Targets[0].Port != 443 ||
		len(fleet.Targets[0].Sites) != 1 || fleet.Targets[0].Sites[0] != "app" {
		t.Fatalf("got %s, want nginx's one TLS site", w.Body)
	}

	inspect := filepath.Join(t.TempDir(), "inspect.json")
	if err := os.WriteFile(inspect, []byte(ingressInspectJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	setDocker(`case "$1" in
ps) echo 4f1c0ffee ;;
inspect) cat "` + inspect + `" ;;
exec) echo "Error response from daemon: container 4f1c0ffee is restarting, wait until the container is running" >&2; exit 1 ;;
esac`)
	w = c.do(http.MethodGet, "/api/v1/certificates/scans/latest", "", nil)
	var answer ingressAnswer
	if err := json.Unmarshal(w.Body.Bytes(), &answer); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusServiceUnavailable || answer.Error.Code != proxysvc.IngressUnreadable || !answer.Error.Retryable ||
		answer.Error.Reason == "" || !strings.Contains(answer.Error.Raw, "is restarting") {
		t.Fatalf("got %d %s", w.Code, w.Body)
	}
}
