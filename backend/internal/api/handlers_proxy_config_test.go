package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

// configHost is an nginx directory with a main file, a site enabled through a
// link, a snippet nothing includes and one of the dashboard's password files.
func configHost(t *testing.T, s *Server) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range map[string]string{
		"nginx.conf":               "events {}\nhttp {\n    include " + dir + "/sites-enabled/*;\n}\n",
		"sites-available/app":      "server {\n    listen 80;\n    server_name app.test;\n}\n",
		"snippets/unused.conf":     "gzip on;\n",
		"jd-auth/shop":             "operator:$2y$10$secret-hash-never-sent\n",
		"conf.d/app.conf.dpkg-old": "server {}\n",
	} {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "sites-enabled"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "sites-available/app"), filepath.Join(dir, "sites-enabled/app")); err != nil {
		t.Fatal(err)
	}
	s.Cfg.NginxDir = dir
	s.initModules()
	return dir
}

// The tree is a read, like the editor's own read route: a read-only account
// lists the directory, with a password file named and marked but its hashes
// never sent, and whether nginx reads each file worked out without running
// nginx at all.
func TestConfigFilesAreListedForEveryAccount(t *testing.T) {
	s, bin := engineHost(t, map[string]string{"nginx": "exit 1\n"})
	dir := configHost(t, s)
	gates := routeGates(t, s.Routes())
	if got := gates["GET /api/v1/proxy/files"]; got != [2]int{0, 1} {
		t.Fatalf("GET /proxy/files: %d capability checks and %d rate budgets, want a read", got[0], got[1])
	}
	reader := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "config-reader", auth.RoleReadOnly)}
	w := reader.do(http.MethodGet, "/api/v1/proxy/files", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "secret-hash") {
		t.Fatal("a password file's content was sent")
	}
	var files proxysvc.ConfigFiles
	if err := json.Unmarshal(w.Body.Bytes(), &files); err != nil {
		t.Fatal(err)
	}
	byName := map[string]proxysvc.ConfigEntry{}
	for _, e := range files.Files {
		rel, _ := filepath.Rel(dir, e.Path)
		byName[rel] = e
	}
	if len(byName) != 5 || !files.IncludesKnown {
		t.Fatalf("listed %v (includes known %v)", byName, files.IncludesKnown)
	}
	if e := byName["jd-auth/shop"]; !e.Protected || e.Kind != "password" {
		t.Fatalf("the password file: %+v", e)
	}
	if !byName["sites-available/app"].Included || !byName["sites-enabled/app"].Included || byName["snippets/unused.conf"].Included {
		t.Fatalf("read by nginx: %+v", byName)
	}
	if _, listed := byName["conf.d/app.conf.dpkg-old"]; listed {
		t.Fatal("a package manager's backup nginx does not read was listed")
	}
	if got := shimLog(t, bin, "nginx"); got != "" {
		t.Fatalf("listing the files ran nginx: %q", got)
	}

	// A directory that is not there says which, and is worth no retry.
	s.Cfg.NginxDir = filepath.Join(dir, "absent")
	s.initModules()
	w = reader.do(http.MethodGet, "/api/v1/proxy/files", "", nil)
	if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "absent") {
		t.Fatalf("a missing directory: %d %s", w.Code, w.Body.String())
	}
}

// What nginx loads runs the host's binary, so it is the administrator's, as
// the config test is. It answers the files in nginx's order with the file a
// link resolves to beside it, and every directive placed in its blocks.
func TestEffectiveConfigIsTheAdministrators(t *testing.T) {
	s, bin := engineHost(t, map[string]string{"nginx": ""})
	dir := configHost(t, s)
	dump := "nginx: the configuration file " + dir + "/nginx.conf syntax is ok\n" +
		"# configuration file " + dir + "/nginx.conf:\n" +
		"events {}\nhttp {\n    include " + dir + "/sites-enabled/*;\n}\n\n" +
		"# configuration file " + dir + "/sites-enabled/app:\n" +
		"server {\n    listen 80;\n    server_name app.test;\n}\n\n"
	if err := os.WriteFile(filepath.Join(bin, "nginx"),
		// Builtins only: the shims' PATH holds nothing else.
		[]byte("#!/bin/sh\necho \"$*\" >> '"+filepath.Join(bin, "nginx.log")+"'\nprintf '%s' '"+dump+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	h := s.Routes()
	if got := routeGates(t, h)["GET /api/v1/proxy/effective"]; got != [2]int{1, 1} {
		t.Fatalf("GET /proxy/effective: %d capability checks and %d rate budgets, want system.admin", got[0], got[1])
	}
	for _, role := range []auth.Role{auth.RoleReadOnly, auth.RoleLimited} {
		c := &client{t: t, h: h, cookie: signInAs(t, s, "user-"+string(role), role)}
		if w := c.do(http.MethodGet, "/api/v1/proxy/effective", "", nil); w.Code != http.StatusForbidden {
			t.Errorf("a %s account got %d", role, w.Code)
		}
	}
	if got := shimLog(t, bin, "nginx"); got != "" {
		t.Fatalf("a refused request ran nginx: %q", got)
	}

	admin := &client{t: t, h: h, cookie: signIn(t, s)}
	w := admin.do(http.MethodGet, "/api/v1/proxy/effective", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
	var body struct {
		Files []struct {
			Path, Target, Content string
		}
		Directives []proxysvc.PlacedDirective
		CheckedAt  string
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Files) != 2 || body.Files[1].Path != dir+"/sites-enabled/app" ||
		body.Files[1].Target != dir+"/sites-available/app" || body.Files[0].Target != "" ||
		!strings.Contains(body.Files[1].Content, "server_name app.test;") || body.CheckedAt == "" {
		t.Fatalf("files %+v at %q", body.Files, body.CheckedAt)
	}
	var serverName *proxysvc.PlacedDirective
	for i, d := range body.Directives {
		if d.Name == "server_name" {
			serverName = &body.Directives[i]
		}
	}
	if serverName == nil || serverName.Line != 3 || strings.Join(serverName.Within, "/") != "http/server app.test" {
		t.Fatalf("server_name placed as %+v in %+v", serverName, body.Directives)
	}
}

// A configuration nginx refuses has nothing loaded to show: a 422 carrying
// the test, its line placed at the file, as a refused reload answers.
func TestEffectiveConfigOfARefusedConfigurationCarriesTheTest(t *testing.T) {
	s, _ := engineHost(t, map[string]string{"nginx": brokenNginx})
	configHost(t, s)
	admin := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}
	w := admin.do(http.MethodGet, "/api/v1/proxy/effective", "", nil)
	var body struct {
		Error      struct{ Code, Message string }
		Validation proxysvc.ValidationResult
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusUnprocessableEntity || body.Error.Code != "invalid_config" ||
		!strings.Contains(body.Error.Message, "nothing loaded to show") {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
	d := body.Validation.Diagnostics
	if body.Validation.Valid || len(d) != 1 || d[0].File != "/etc/nginx/sites-enabled/app" || d[0].Line != 3 {
		t.Fatalf("validation %+v", body.Validation)
	}
}

// The editor put a refused save's output in a toast. The refusal now carries
// the test beside the error, so the editor places each line in the file.
func TestConfigSaveRefusalCarriesTheTest(t *testing.T) {
	s, _ := engineHost(t, map[string]string{"nginx": brokenNginx})
	dir := configHost(t, s)
	admin := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}
	file := filepath.Join(dir, "sites-available/app")
	body, _ := json.Marshal(map[string]any{"kind": "nginx", "path": file, "content": "server {\n    frobnicate on;\n}\n"})
	w := admin.do(http.MethodPut, "/api/v1/proxy/config", string(body), nil)
	var refusal struct {
		Error      struct{ Code, Message string }
		Validation proxysvc.ValidationResult
	}
	if err := json.Unmarshal(w.Body.Bytes(), &refusal); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusUnprocessableEntity || refusal.Error.Code != "invalid_config" ||
		!strings.Contains(refusal.Error.Message, `unknown directive "frobnicate"`) {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
	if d := refusal.Validation.Diagnostics; refusal.Validation.Valid || len(d) != 1 || d[0].Line != 3 {
		t.Fatalf("validation %+v", refusal.Validation)
	}
	if b, _ := os.ReadFile(file); strings.Contains(string(b), "frobnicate") {
		t.Fatal("a refused save was written")
	}
	var audited int
	if err := s.Store.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = 'proxy.config.write'
		AND detail LIKE '%rejected%'`).Scan(&audited); err != nil || audited != 1 {
		t.Fatalf("the refusal was audited %d times (%v)", audited, err)
	}
}
