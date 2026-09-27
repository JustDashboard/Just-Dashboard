package proxysvc

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

// liveNginx gives a test the host's real nginx against a private prefix under
// t.TempDir(): an nginx.conf including conf.d/*.conf and sites-enabled/*
// inside http, and a shim first on PATH so the Service's `nginx -t` and
// `nginx -T` run the real binary there. nginx's startup log goes into the
// prefix as well, which is what makes an unprivileged test print what root
// prints on the host. Skipped where nginx is not installed.
func liveNginx(t *testing.T) string {
	t.Helper()
	binary, err := exec.LookPath("nginx")
	if err != nil {
		t.Skip("nginx is not installed")
	}
	root := t.TempDir()
	for _, dir := range []string{"sites-available", "sites-enabled", "conf.d", "logs", "bin"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// conf.d is included by a relative path, the way Debian's nginx.conf
	// includes mime.types, so the tree is tested resolving one.
	conf := fmt.Sprintf(`pid %[1]s/nginx.pid;
error_log %[1]s/logs/error.log;
events {}
http {
    access_log off;
    client_body_temp_path %[1]s/tmp-body;
    proxy_temp_path %[1]s/tmp-proxy;
    fastcgi_temp_path %[1]s/tmp-fastcgi;
    uwsgi_temp_path %[1]s/tmp-uwsgi;
    scgi_temp_path %[1]s/tmp-scgi;
    include conf.d/*.conf;
    include %[1]s/sites-enabled/*;
}
`, root)
	if err := os.WriteFile(filepath.Join(root, "nginx.conf"), []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}
	shim := fmt.Sprintf("#!/bin/sh\nexec '%s' -e '%s/logs/startup.log' -p '%s' -c '%s/nginx.conf' \"$@\"\n",
		binary, root, root, root)
	if err := os.WriteFile(filepath.Join(root, "bin", "nginx"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Join(root, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	return root
}

func TestParseEffective(t *testing.T) {
	// The shape nginx -T prints: the verdict, then each file behind its
	// marker followed by one line feed of nginx's own.
	output := "nginx: the configuration file /etc/nginx/nginx.conf syntax is ok\n" +
		"nginx: configuration file /etc/nginx/nginx.conf test is successful\n" +
		"# configuration file /etc/nginx/nginx.conf:\n" +
		"events {}\nhttp {\n    include /etc/nginx/conf.d/*.conf;\n}\n" +
		"\n" +
		"# configuration file /etc/nginx/conf.d/no-newline.conf:\n" +
		"server {}" +
		"\n" +
		"# configuration file /etc/nginx/conf.d/blank-lines.conf:\n" +
		"\n\nserver {}\n\n" +
		"\n"
	want := []ConfigFile{
		{Path: "/etc/nginx/nginx.conf", Content: "events {}\nhttp {\n    include /etc/nginx/conf.d/*.conf;\n}\n"},
		{Path: "/etc/nginx/conf.d/no-newline.conf", Content: "server {}"},
		{Path: "/etc/nginx/conf.d/blank-lines.conf", Content: "\n\nserver {}\n\n"},
	}
	if got := ParseEffective(output); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v\nwant %#v", got, want)
	}
}

// The real `nginx -T`: every file nginx reads comes back byte for byte, a
// password file does not come back at all, and the cache is forgotten by the
// service's own writes but not by a change it did not make.
func TestEffectiveConfigFromARealDump(t *testing.T) {
	root := liveNginx(t)
	service := New(root, filepath.Join(root, "Caddyfile"))
	site := filepath.Join(root, "conf.d", "app.conf")
	siteContent := "server {\n    listen 127.0.0.1:18090;\n    server_name app.example.test;\n    location / {\n        return 204;\n    }\n}"
	if err := os.WriteFile(site, []byte(siteContent), 0o644); err != nil {
		t.Fatal(err)
	}
	// Named like a password file and included as configuration: exactly the
	// mistake that would otherwise print hashes to every reader.
	secret := filepath.Join(root, "sites-enabled", "team.htpasswd")
	if err := os.WriteFile(secret, []byte("# operator:$2y$10$abcdefghijklmnopqrstuv\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	files, err := service.EffectiveConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]string{}
	for _, f := range files {
		byPath[f.Path] = f.Content
	}
	if len(files) == 0 || files[0].Path != filepath.Join(root, "nginx.conf") {
		t.Fatalf("the main configuration is not first: %+v", files)
	}
	if byPath[site] != siteContent {
		t.Fatalf("site came back as %q", byPath[site])
	}
	if _, ok := byPath[secret]; ok {
		t.Fatal("a password file was returned")
	}

	tree, err := NginxTree(files)
	if err != nil {
		t.Fatal(err)
	}
	ret := findDirective(tree, "return")
	if ret == nil || ret.File != site || ret.Line != 5 ||
		!reflect.DeepEqual(ret.Context, []string{"http", "server", "location"}) {
		t.Fatalf("the relative include was not expanded in place: %+v", ret)
	}

	if err := os.WriteFile(site, []byte("server {\n    listen 127.0.0.1:18091;\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cached, err := service.EffectiveConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cached, files) {
		t.Fatal("a second read inside the TTL ran nginx again")
	}
	if _, err := service.WriteConfig(context.Background(), KindNginx, site, "server {\n    listen 127.0.0.1:18092;\n}\n"); err != nil {
		t.Fatal(err)
	}
	fresh, err := service.EffectiveConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range fresh {
		if f.Path == site && f.Content != "server {\n    listen 127.0.0.1:18092;\n}\n" {
			t.Fatalf("the service's own write did not forget the cache: %q", f.Content)
		}
	}
}

func findDirective(tree []Directive, name string) *Directive {
	for i := range tree {
		if tree[i].Name == name {
			return &tree[i]
		}
		if found := findDirective(tree[i].Block, name); found != nil {
			return found
		}
	}
	return nil
}
