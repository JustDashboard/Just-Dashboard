package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
)

// engineHost is a host whose only binaries are the given shims, each of which
// appends its argv to <name>.log beside it before running its body.
func engineHost(t *testing.T, bodies map[string]string) (*Server, string) {
	t.Helper()
	bin := t.TempDir()
	for name, body := range bodies {
		script := "#!/bin/sh\necho \"$*\" >> \"" + filepath.Join(bin, name+".log") + "\"\n" + body
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	return testServer(t), bin
}

func shimLog(t *testing.T, bin, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(bin, name+".log"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(b)
}

// nginx refusing its configuration the way 1.26 does, unless JD_TEST_NGINX_OK
// is set.
const brokenNginx = `if [ -n "$JD_TEST_NGINX_OK" ]; then
	echo 'nginx: configuration file /etc/nginx/nginx.conf test is successful' >&2
	exit 0
fi
echo 'nginx: [emerg] unknown directive "frobnicate" in /etc/nginx/sites-enabled/app:3' >&2
echo 'nginx: configuration file /etc/nginx/nginx.conf test failed' >&2
exit 1
`

// The overview's Restart posted straight to systemctl. This host's
// nginx.service tests its configuration before it starts, so a restart over a
// broken file stopped nginx and could not bring it back. Start and restart now
// run the test first and are refused with nginx's own line, and systemctl is
// never asked; stop needs no test.
func TestEngineStartAndRestartAreRefusedOverABrokenConfiguration(t *testing.T) {
	s, bin := engineHost(t, map[string]string{"nginx": brokenNginx, "systemctl": ""})
	c := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}

	for _, action := range []string{"restart", "start"} {
		w := c.do(http.MethodPost, "/api/v1/proxy/engine/"+action, "", nil)
		if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), `"invalid_config"`) ||
			!strings.Contains(w.Body.String(), `unknown directive \"frobnicate\" in /etc/nginx/sites-enabled/app:3`) ||
			!strings.Contains(w.Body.String(), "nginx was not "+action+"ed") {
			t.Fatalf("%s over a broken file: %d %s", action, w.Code, w.Body.String())
		}
	}
	if got := shimLog(t, bin, "systemctl"); got != "" {
		t.Fatalf("systemctl was run over a configuration nginx refuses: %q", got)
	}

	t.Setenv("JD_TEST_NGINX_OK", "1")
	for _, action := range []string{"restart", "start", "stop"} {
		w := c.do(http.MethodPost, "/api/v1/proxy/engine/"+action, "", nil)
		var body struct{ Action, Unit string }
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &body) != nil ||
			body.Action != action || body.Unit != "nginx.service" {
			t.Fatalf("%s: %d %s", action, w.Code, w.Body.String())
		}
	}
	if got, want := shimLog(t, bin, "systemctl"), "restart nginx.service\nstart nginx.service\nstop nginx.service\n"; got != want {
		t.Fatalf("systemctl ran %q, want %q", got, want)
	}
	// Two refused tests and two passing ones: stop ran no test.
	if got := strings.Count(shimLog(t, bin, "nginx"), "-t"); got != 4 {
		t.Fatalf("nginx -t ran %d times, want 4", got)
	}
}

// The unit is the server's own choice, the gates are the ones the Services
// page puts on the same verbs plus system.admin, and only the three verbs
// exist.
func TestEngineRoutesAreGatedAndControlOnlyTheProxy(t *testing.T) {
	s, bin := engineHost(t, map[string]string{"nginx": "exit 0\n", "systemctl": ""})
	h := s.Routes()
	gates := routeGates(t, h)
	for path, want := range map[string][2]int{
		"POST /api/v1/proxy/engine/start":   {1, 1},
		"POST /api/v1/proxy/engine/restart": {2, 2},
		"POST /api/v1/proxy/engine/stop":    {2, 2},
	} {
		if got := gates[path]; got != want {
			t.Errorf("%s: %d capability checks and %d rate budgets, want %d and %d", path, got[0], got[1], want[0], want[1])
		}
	}
	for _, role := range []auth.Role{auth.RoleReadOnly, auth.RoleLimited} {
		c := &client{t: t, h: h, cookie: signInAs(t, s, "user-"+string(role), role)}
		for _, action := range []string{"start", "restart", "stop"} {
			if w := c.do(http.MethodPost, "/api/v1/proxy/engine/"+action, "", nil); w.Code != http.StatusForbidden {
				t.Errorf("a %s account got %d for %s", role, w.Code, action)
			}
		}
	}
	admin := &client{t: t, h: h, cookie: signIn(t, s)}
	for _, path := range []string{"/api/v1/proxy/engine/reload", "/api/v1/proxy/engine/ssh.service"} {
		if w := admin.do(http.MethodPost, path, "", nil); w.Code != http.StatusNotFound && w.Code != http.StatusMethodNotAllowed {
			t.Errorf("POST %s answered %d", path, w.Code)
		}
	}
	if got := shimLog(t, bin, "systemctl"); got != "" {
		t.Fatalf("systemctl was run by a refused request: %q", got)
	}
}

// A host whose proxy is only the Docker ingress has no unit to control, and
// says so rather than guessing one.
func TestEngineRoutesNeedAServiceUnit(t *testing.T) {
	s, bin := engineHost(t, map[string]string{"systemctl": ""})
	c := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}
	w := c.do(http.MethodPost, "/api/v1/proxy/engine/restart", "", nil)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), `"no_engine_unit"`) {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
	if got := shimLog(t, bin, "systemctl"); got != "" {
		t.Fatalf("systemctl was run with no engine: %q", got)
	}
}

// Test config and Reload name the engine they act on. An unknown one used to
// fall through to nginx; the ingress is refused where none is running rather
// than tested with the host's caddy.
//
// The config editor's validate and save took any kind as well: "apache" was
// validated by nginx -t, and "caddy-ingress" validated and wrote an nginx file
// and then reloaded the Docker Caddy. Both are refused before a file is
// touched, and the ingress, whose Caddyfile deployments write, is not the
// editor's to validate or save.
func TestEngineTestAndReloadTakeOnlyAKnownEngine(t *testing.T) {
	s, bin := engineHost(t, map[string]string{"nginx": "exit 0\n", "caddy": "exit 0\n"})
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "conf.d"), 0o755); err != nil {
		t.Fatal(err)
	}
	s.Cfg.NginxDir = dir
	s.initModules()
	c := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}
	for _, path := range []string{"/api/v1/proxy/test", "/api/v1/proxy/reload"} {
		if w := c.do(http.MethodPost, path, `{"kind":"apache"}`, nil); w.Code != http.StatusBadRequest {
			t.Errorf("%s with an unknown engine: %d %s", path, w.Code, w.Body.String())
		}
		w := c.do(http.MethodPost, path, `{"kind":"caddy-ingress"}`, nil)
		if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), `"no_ingress"`) {
			t.Errorf("%s for an ingress that is not running: %d %s", path, w.Code, w.Body.String())
		}
	}

	file := filepath.Join(dir, "conf.d", "zz-kind.conf")
	for _, kind := range []string{"apache", "caddy-ingress"} {
		body, _ := json.Marshal(map[string]any{
			"kind": kind, "path": file, "content": "server { listen 20001; return 200 ok; }\n", "reload": true,
		})
		for _, route := range [][2]string{
			{http.MethodPost, "/api/v1/proxy/validate"},
			{http.MethodPut, "/api/v1/proxy/config"},
		} {
			if w := c.do(route[0], route[1], string(body), nil); w.Code != http.StatusBadRequest {
				t.Errorf("%s %s with kind %q: %d %s", route[0], route[1], kind, w.Code, w.Body.String())
			}
		}
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("a refused request left %s on disk: %v", file, err)
	}
	if got := shimLog(t, bin, "nginx") + shimLog(t, bin, "caddy"); got != "" {
		t.Fatalf("an engine was run for a request naming another: %q", got)
	}

	// An unnamed engine is still nginx, as the editor has always sent it.
	body, _ := json.Marshal(map[string]any{"path": file, "content": "server { listen 20001; }\n"})
	if w := c.do(http.MethodPut, "/api/v1/proxy/config", string(body), nil); w.Code != http.StatusOK {
		t.Fatalf("a save naming no engine: %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(shimLog(t, bin, "nginx"), "-t") {
		t.Fatal("a save naming no engine was not tested by nginx")
	}
}

// The overview opens a reader's route file from a list read earlier. A file
// removed since was a 400 whose raw open error the editor put in a toast over
// an empty buffer, which then read as an empty site file. It is a 404 worth
// reading again, and a missing path outside the proxy's directories is still
// refused as outside them, saying nothing about whether it exists.
func TestConfigReadOfAFileRemovedSinceTheListIsNotFoundAndWorthRetrying(t *testing.T) {
	s := testServer(t)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sites-available"), 0o755); err != nil {
		t.Fatal(err)
	}
	s.Cfg.NginxDir = dir
	s.initModules()
	reader := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "config-reader", auth.RoleReadOnly)}
	read := func(path string) *httptest.ResponseRecorder {
		return reader.do(http.MethodGet, "/api/v1/proxy/config?path="+url.QueryEscape(path), "", nil)
	}

	file := filepath.Join(dir, "sites-available", "gone.example.com")
	w := read(file)
	var failed struct {
		Error struct {
			Code, Message, Reason, Raw string
			Retryable                  bool
		}
	}
	if w.Code != http.StatusNotFound || json.Unmarshal(w.Body.Bytes(), &failed) != nil ||
		failed.Error.Code != "not_found" || !failed.Error.Retryable ||
		failed.Error.Message != "That file is not on disk." ||
		!strings.Contains(failed.Error.Reason, "removed or renamed") ||
		!strings.Contains(failed.Error.Raw, "no such file or directory") {
		t.Fatalf("a file removed since the list: %d %s", w.Code, w.Body.String())
	}

	// Trying again once it is back reads it.
	if err := os.WriteFile(file, []byte("server { listen 80; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if w := read(file); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "listen 80;") {
		t.Fatalf("the file once it is back: %d %s", w.Code, w.Body.String())
	}

	outside := filepath.Join(t.TempDir(), "missing.conf")
	if w := read(outside); w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), `"outside_root"`) {
		t.Fatalf("a missing file outside the proxy's directories: %d %s", w.Code, w.Body.String())
	}
}
