package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

// nginxDumping puts an nginx on PATH whose -T prints nginx.conf and the one
// site under dir, as nginx prints its configuration.
func nginxDumping(t *testing.T, dir, site string) {
	t.Helper()
	for _, sub := range []string{"sites-enabled", "bin"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	conf := filepath.Join(dir, "nginx.conf")
	sitePath := filepath.Join(dir, "sites-enabled", "admin")
	if err := os.WriteFile(conf, []byte("events {}\nhttp {\n    include "+dir+"/sites-enabled/*;\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sitePath, []byte(site), 0o644); err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(`#!/bin/sh
if [ "$1" = "-T" ]; then
  echo "nginx: the configuration file %[1]s syntax is ok" >&2
  echo "nginx: configuration file %[1]s test is successful" >&2
  printf '# configuration file %[1]s:\n'; cat '%[1]s'; printf '\n'
  printf '# configuration file %[2]s:\n'; cat '%[2]s'; printf '\n'
fi
exit 0
`, conf, sitePath)
	if err := os.WriteFile(filepath.Join(dir, "bin", "nginx"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Join(dir, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// Who may reach a URL is an administrator's question, answered from the
// configuration with the host firewall judged for the same address, and a
// source that is not one address is a 400.
func TestRouteAccessExplainsLayersForAdmins(t *testing.T) {
	dir := t.TempDir()
	nginxDumping(t, dir, "server {\n    listen 80;\n    server_name admin.example.com;\n    allow 192.0.2.0/24;\n    deny all;\n    location / { proxy_pass http://127.0.0.1:3000; }\n}\n")
	s := testServer(t)
	s.Cfg.NginxDir = dir
	s.initModules()
	h := s.Routes()
	admin := &client{t: t, h: h, cookie: signIn(t, s)}
	reader := &client{t: t, h: h, cookie: signInAs(t, s, "reader", auth.RoleReadOnly)}
	path := "/api/v1/proxy/resolve/access?url=http://admin.example.com/&source="
	if w := reader.do(http.MethodGet, path+"192.0.2.9", "", nil); w.Code != http.StatusForbidden {
		t.Fatalf("reader: %d %s", w.Code, w.Body.String())
	}
	if w := admin.do(http.MethodGet, path+"everyone", "", nil); w.Code != http.StatusBadRequest {
		t.Fatalf("a source that is not an address: %d %s", w.Code, w.Body.String())
	}
	for source, want := range map[string]string{"198.51.100.4": proxysvc.LayerRefuses, "192.0.2.9": proxysvc.LayerAdmits} {
		w := admin.do(http.MethodGet, path+source, "", nil)
		var out proxysvc.AccessExplanation
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || w.Code != http.StatusOK {
			t.Fatalf("%s: %d %s %v", source, w.Code, w.Body.String(), err)
		}
		ids := []string{}
		var addresses proxysvc.AccessLayer
		for _, l := range out.Layers {
			ids = append(ids, l.ID)
			if l.ID == "addresses" {
				addresses = l
			}
		}
		if out.Source != source || addresses.Verdict != want || len(ids) < 3 || ids[0] != "outside" || ids[1] != "firewall" {
			t.Fatalf("%s: %+v", source, out)
		}
		if want == proxysvc.LayerRefuses && out.Verdict != proxysvc.AccessRefused {
			t.Fatalf("%s: verdict %s", source, out.Verdict)
		}
	}
}

// A stream's path is a read every account has, as its sessions are; a name
// that is no stream is a 404.
func TestStreamPathIsReadByEveryAccount(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "stream.d"), 0o755); err != nil {
		t.Fatal(err)
	}
	content, err := proxysvc.RenderStream(&proxysvc.StreamSpec{Name: "pg", Listen: 6432, Upstream: "10.0.0.5:5432"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stream.d", "pg.conf"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	s := testServer(t)
	s.Cfg.NginxDir = dir
	s.initModules()
	reader := &client{t: t, h: s.Routes(), cookie: signInAs(t, s, "reader", auth.RoleReadOnly)}
	w := reader.do(http.MethodGet, "/api/v1/proxy/streams/pg/path", "", nil)
	var path proxysvc.StreamPath
	if err := json.Unmarshal(w.Body.Bytes(), &path); err != nil || w.Code != http.StatusOK {
		t.Fatalf("%d %s %v", w.Code, w.Body.String(), err)
	}
	if path.Name != "pg" || len(path.Servers) != 1 || path.Servers[0].Address != "10.0.0.5:5432" {
		t.Fatalf("path = %+v", path)
	}
	if w := reader.do(http.MethodGet, "/api/v1/proxy/streams/none/path", "", nil); w.Code != http.StatusNotFound {
		t.Fatalf("no such stream: %d %s", w.Code, w.Body.String())
	}
}
