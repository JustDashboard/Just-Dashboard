package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
)

// vhostServer is a server whose proxy is a Debian-layout nginx directory in
// a temporary directory, behind an nginx that runs script.
func vhostServer(t *testing.T, script func(root string) string) (*client, *Server, string) {
	t.Helper()
	s := testServer(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"sites-available", "sites-enabled", "bin"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	shim := "#!/bin/sh\n" + script(root) + "\n"
	if err := os.WriteFile(filepath.Join(root, "bin", "nginx"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Join(root, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	s.Cfg.NginxDir = root
	s.initModules()
	return &client{t: t, h: s.Routes(), cookie: signIn(t, s)}, s, root
}

type linkAnswer struct {
	Name        string `json:"name"`
	Enabled     bool   `json:"enabled"`
	Reloaded    bool   `json:"reloaded"`
	ReloadError string `json:"reloadError"`
	Error       struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Raw     string `json:"raw"`
	} `json:"error"`
}

func decodeLink(t *testing.T, body []byte) linkAnswer {
	t.Helper()
	var out linkAnswer
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	return out
}

func lastAudit(t *testing.T, s *Server, action string) audit.Entry {
	t.Helper()
	entries, _, err := s.Audit.List(t.Context(), audit.Filter{Action: action, Limit: 1})
	if err != nil || len(entries) == 0 {
		t.Fatalf("no %s audit entry (%v)", action, err)
	}
	return entries[0]
}

// The switch answered a refused enable with 502 "configuration failed
// validation" and left the link in place. It is a 422 now, with nginx's own
// reason as the message and the test output behind it, and nothing linked.
func TestVHostToggleSaysWhyNginxRefusedTheSite(t *testing.T) {
	c, s, root := vhostServer(t, func(root string) string {
		link := filepath.Join(root, "sites-enabled", "broken")
		return fmt.Sprintf(`if [ -e '%[1]s' ]; then echo 'nginx: [emerg] unknown directive "foo" in %[1]s:3' >&2; echo 'nginx: configuration file test failed' >&2; exit 1; fi; exit 0`, link)
	})
	site := filepath.Join(root, "sites-available", "broken")
	if err := os.WriteFile(site, []byte("server { foo bar; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := c.do(http.MethodPost, "/api/v1/proxy/vhosts/broken/enabled", `{"enabled":true,"reload":true}`, nil)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("got %d: %s", w.Code, w.Body.String())
	}
	answer := decodeLink(t, w.Body.Bytes())
	if answer.Error.Code != "invalid_config" || answer.Error.Message != `unknown directive "foo" in `+site+":3" {
		t.Errorf("error = %+v", answer.Error)
	}
	if !strings.Contains(answer.Error.Raw, "test failed") {
		t.Errorf("the test output is not kept: %q", answer.Error.Raw)
	}
	if _, err := os.Lstat(filepath.Join(root, "sites-enabled", "broken")); !os.IsNotExist(err) {
		t.Fatalf("the refused link is still there: %v", err)
	}
	if entry := lastAudit(t, s, "proxy.vhost.toggle"); entry.Success || !strings.Contains(entry.Detail, "refused") {
		t.Errorf("audit = %+v", entry)
	}
}

// A disable is refused when another site needs the one going out, and
// nginx's error is then in that other site's file. The answer says the
// configuration was refused without this site, and so does the audit entry.
func TestVHostToggleSaysARefusedDisableWasNeededElsewhere(t *testing.T) {
	c, s, root := vhostServer(t, func(root string) string {
		return fmt.Sprintf(`if [ ! -e '%s' ]; then echo 'nginx: [emerg] host not found in upstream "pool" in %s:4' >&2; exit 1; fi; exit 0`,
			filepath.Join(root, "sites-enabled", "pool"), filepath.Join(root, "sites-available", "app"))
	})
	if err := os.WriteFile(filepath.Join(root, "sites-available", "pool"), []byte("upstream pool { server 127.0.0.1:3000; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../sites-available/pool", filepath.Join(root, "sites-enabled", "pool")); err != nil {
		t.Fatal(err)
	}
	w := c.do(http.MethodPost, "/api/v1/proxy/vhosts/pool/enabled", `{"enabled":false,"reload":true}`, nil)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("got %d: %s", w.Code, w.Body.String())
	}
	want := `nginx refuses the configuration without pool: host not found in upstream "pool" in ` + filepath.Join(root, "sites-available", "app") + ":4"
	if answer := decodeLink(t, w.Body.Bytes()); answer.Error.Code != "invalid_config" || answer.Error.Message != want {
		t.Errorf("error = %+v\nwant %q", answer.Error, want)
	}
	if target, err := os.Readlink(filepath.Join(root, "sites-enabled", "pool")); err != nil || target != "../sites-available/pool" {
		t.Errorf("link = %q (%v), want it back", target, err)
	}
	if entry := lastAudit(t, s, "proxy.vhost.toggle"); entry.Success || !strings.Contains(entry.Detail, "without pool") {
		t.Errorf("audit = %+v", entry)
	}
}

// A reload that fails after a clean test is reported, not returned as a
// failure of the toggle: the link is in place and nginx accepted it.
func TestVHostToggleReportsAReloadThatFailed(t *testing.T) {
	c, s, root := vhostServer(t, func(string) string {
		return `if [ "$1" = "-s" ]; then echo 'nginx: [error] invalid PID number "" in "/run/nginx.pid"' >&2; exit 1; fi; exit 0`
	})
	if err := os.WriteFile(filepath.Join(root, "sites-available", "app"), []byte("server {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := c.do(http.MethodPost, "/api/v1/proxy/vhosts/app/enabled", `{"enabled":true,"reload":true}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("got %d: %s", w.Code, w.Body.String())
	}
	answer := decodeLink(t, w.Body.Bytes())
	if !answer.Enabled || answer.Reloaded || !strings.Contains(answer.ReloadError, "invalid PID number") {
		t.Errorf("answer = %+v", answer)
	}
	if _, err := os.Lstat(filepath.Join(root, "sites-enabled", "app")); err != nil {
		t.Errorf("the tested link was taken out over a reload failure: %v", err)
	}
	if entry := lastAudit(t, s, "proxy.vhost.toggle"); !entry.Success || !strings.Contains(entry.Detail, "invalid PID number") {
		t.Errorf("audit = %+v", entry)
	}

	w = c.do(http.MethodPost, "/api/v1/proxy/vhosts/app/enabled", `{"enabled":false,"reload":false}`, nil)
	if answer := decodeLink(t, w.Body.Bytes()); w.Code != http.StatusOK || answer.Enabled || answer.Reloaded || answer.ReloadError != "" {
		t.Errorf("a disable without reload: %d %+v", w.Code, answer)
	}
}

// A reload refused by the test — a disable out of a configuration broken
// elsewhere stands, and the reload after it cannot — says nginx's reason
// rather than "configuration failed validation".
func TestVHostToggleNamesWhyTheReloadWasRefused(t *testing.T) {
	c, _, root := vhostServer(t, func(root string) string {
		return fmt.Sprintf(`echo 'nginx: [emerg] open() "%[1]s/sites-enabled/ghost" failed (2: No such file or directory) in %[1]s/nginx.conf:20' >&2; exit 1`, root)
	})
	if err := os.WriteFile(filepath.Join(root, "sites-available", "app"), []byte("server {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "sites-available", "app"), filepath.Join(root, "sites-enabled", "app")); err != nil {
		t.Fatal(err)
	}
	w := c.do(http.MethodPost, "/api/v1/proxy/vhosts/app/enabled", `{"enabled":false,"reload":true}`, nil)
	answer := decodeLink(t, w.Body.Bytes())
	if w.Code != http.StatusOK || answer.Enabled || answer.Reloaded {
		t.Fatalf("got %d %+v", w.Code, answer)
	}
	if want := "nginx -t failed: open() \"" + root + "/sites-enabled/ghost\" failed (2: No such file or directory) in " + root + "/nginx.conf:20"; answer.ReloadError != want {
		t.Errorf("reloadError = %q\nwant %q", answer.ReloadError, want)
	}
}

func TestVHostUnlinkRemovesOnlyALinkNoSiteOwns(t *testing.T) {
	c, s, root := vhostServer(t, func(string) string { return "exit 0" })
	enabled := func(name string) string { return filepath.Join(root, "sites-enabled", name) }
	if err := os.Symlink("/nonexistent/jd-test/ghost", enabled("ghost")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(enabled("copy"), []byte("server {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := c.do(http.MethodDelete, "/api/v1/proxy/vhosts/ghost/link", "", nil)
	if answer := decodeLink(t, w.Body.Bytes()); w.Code != http.StatusOK || !answer.Reloaded {
		t.Fatalf("got %d %+v", w.Code, answer)
	}
	if _, err := os.Lstat(enabled("ghost")); !os.IsNotExist(err) {
		t.Errorf("the dangling link is still there: %v", err)
	}
	if entry := lastAudit(t, s, "proxy.vhost.unlink"); !entry.Success || entry.Target != "ghost" {
		t.Errorf("audit = %+v", entry)
	}
	w = c.do(http.MethodDelete, "/api/v1/proxy/vhosts/copy/link", "", nil)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "is a file") {
		t.Fatalf("a file in sites-enabled: %d %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(enabled("copy")); err != nil {
		t.Errorf("the file was removed: %v", err)
	}
}

// Removing a link takes configuration out of nginx: system.admin, the
// destructive gate and its budget, and a plain confirmation rather than a
// typed phrase.
func TestVHostUnlinkIsAdminOnlyAndDestructive(t *testing.T) {
	c, s, _ := vhostServer(t, func(string) string { return "exit 0" })
	gates := routeGates(t, c.h)
	if got := gates["DELETE /api/v1/proxy/vhosts/{name}/link"]; got != [2]int{2, 2} {
		t.Errorf("gates = %v, want two capability checks and two budgets", got)
	}
	reader := &client{t: t, h: c.h, cookie: signInAs(t, s, "reader", auth.RoleReadOnly)}
	if w := reader.do(http.MethodDelete, "/api/v1/proxy/vhosts/ghost/link", "", nil); w.Code != http.StatusForbidden {
		t.Errorf("a read-only account got %d", w.Code)
	}
	if w := c.do(http.MethodDelete, "/api/v1/proxy/vhosts/ghost/link", "", nil); w.Code == http.StatusPreconditionRequired || w.Code == http.StatusPreconditionFailed {
		t.Errorf("the route asked for a typed phrase: %d", w.Code)
	}
}

// chi hands back a path parameter as the client escaped it, and the page
// escapes every name, so a link called vb:8080 — which stopped every reload —
// could not be removed: "sites-enabled has nothing called vb%3A8080".
func TestVHostRoutesTakeTheNameUnescaped(t *testing.T) {
	c, _, root := vhostServer(t, func(string) string { return "exit 0" })
	enabled := func(name string) string { return filepath.Join(root, "sites-enabled", name) }
	if err := os.Symlink("/nonexistent/jd-test/a:b", enabled("a:b")); err != nil {
		t.Fatal(err)
	}
	w := c.do(http.MethodDelete, "/api/v1/proxy/vhosts/a%3Ab/link", "", nil)
	if answer := decodeLink(t, w.Body.Bytes()); w.Code != http.StatusOK || answer.Name != "a:b" || !answer.Reloaded {
		t.Fatalf("unlink: %d %s", w.Code, w.Body.String())
	}
	if _, err := os.Lstat(enabled("a:b")); !os.IsNotExist(err) {
		t.Errorf("sites-enabled/a:b is still there: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "sites-available", "c:d"), []byte("server {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w = c.do(http.MethodPost, "/api/v1/proxy/vhosts/c%3Ad/enabled", `{"enabled":true,"reload":true}`, nil)
	if answer := decodeLink(t, w.Body.Bytes()); w.Code != http.StatusOK || answer.Name != "c:d" || !answer.Enabled {
		t.Fatalf("toggle: %d %s", w.Code, w.Body.String())
	}
	if _, err := os.Lstat(enabled("c:d")); err != nil {
		t.Errorf("sites-enabled/c:d was not made: %v", err)
	}
}

// A site copied into sites-enabled is what nginx serves under its name, and
// the delete took it with no copy kept.
func TestSiteDeleteLeavesAServedCopyAlone(t *testing.T) {
	c, _, root := vhostServer(t, func(string) string { return "exit 0" })
	copied := filepath.Join(root, "sites-enabled", "copy.test")
	if err := os.WriteFile(filepath.Join(root, "sites-available", "copy.test"), []byte("server { return 204; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(copied, []byte("server { return 200; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := c.do(http.MethodDelete, "/api/v1/proxy/sites/copy.test", "", nil)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "is a file of its own") {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
	if b, err := os.ReadFile(copied); err != nil || string(b) != "server { return 200; }\n" {
		t.Errorf("the served copy is gone or changed: %q %v", b, err)
	}
}
