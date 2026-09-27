package proxysvc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
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
	// A comment reading like a marker, which real `nginx -T` prints as part
	// of the file it is in: nginx names a file by its full path, a comment
	// does not.
	output += "# configuration file /etc/nginx/conf.d/commented.conf:\n" +
		"# configuration file for the app:\nmap $host $app { default 1; }\n" +
		"\n"
	want := []ConfigFile{
		{Path: "/etc/nginx/nginx.conf", Content: "events {}\nhttp {\n    include /etc/nginx/conf.d/*.conf;\n}\n"},
		{Path: "/etc/nginx/conf.d/no-newline.conf", Content: "server {}"},
		{Path: "/etc/nginx/conf.d/blank-lines.conf", Content: "\n\nserver {}\n\n"},
		{Path: "/etc/nginx/conf.d/commented.conf", Content: "# configuration file for the app:\nmap $host $app { default 1; }\n"},
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

// EffectiveConfig is read by every signed-in account, so it shows what
// ReadConfig shows and nothing more: a file nginx reads from outside the
// proxy's directories, by an include or through a link, is left out.
func TestEffectiveConfigShowsOnlyWhatReadConfigShows(t *testing.T) {
	root := liveNginx(t)
	service := New(root, filepath.Join(root, "Caddyfile"))
	outside := t.TempDir()
	secrets := filepath.Join(outside, "secrets.conf")
	if err := os.WriteFile(secrets, []byte("proxy_set_header X-Api-Key \"s3cr3t\";\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "linked.conf"), []byte("map $host $jd_linked { default s3cr3t; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	including := filepath.Join(root, "conf.d", "including.conf")
	if err := os.WriteFile(including, []byte("include "+secrets+";\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(root, "conf.d", "linked.conf")
	if err := os.Symlink(filepath.Join(outside, "linked.conf"), linked); err != nil {
		t.Fatal(err)
	}

	files, err := service.EffectiveConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, f := range files {
		paths = append(paths, f.Path)
		if strings.Contains(f.Content, "s3cr3t") {
			t.Fatalf("%s came back with content from outside the proxy directory", f.Path)
		}
	}
	want := []string{filepath.Join(root, "nginx.conf"), including}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("returned %v, want %v", paths, want)
	}
	for _, path := range []string{secrets, linked} {
		if _, err := service.ReadConfig(path); !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("ReadConfig(%s) = %v, want it refused like the dump", path, err)
		}
	}
}

// fakeNginx puts an nginx first on PATH whose `-T` prints dump and counts its
// runs in the file it returns.
func fakeNginx(t *testing.T, dump string) string {
	t.Helper()
	dir := t.TempDir()
	runs := filepath.Join(dir, "runs")
	script := fmt.Sprintf("#!/bin/sh\necho run >> '%s'\ncat <<'EOF'\n%sEOF\n", runs, dump)
	if err := os.WriteFile(filepath.Join(dir, "nginx"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return runs
}

// A tree rooted at whichever file came after a withheld main configuration
// would be wrong without saying so.
func TestEffectiveConfigRefusesAMainFileOutsideTheProxyDirectory(t *testing.T) {
	root := t.TempDir()
	fakeNginx(t, "# configuration file /usr/local/openresty/nginx/conf/nginx.conf:\n"+
		"http { include "+root+"/conf.d/*.conf; }\n\n"+
		"# configuration file "+root+"/conf.d/a.conf:\nserver {}\n\n")
	service := New(root, filepath.Join(root, "Caddyfile"))
	if _, err := service.EffectiveConfig(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "JD_NGINX_DIR") {
		t.Fatalf("got %v, want the main file's location explained", err)
	}
}

// s.mu can be held for minutes by a certificate order. A reader waits no
// longer than its own context, and the readers that queue meanwhile share one
// `nginx -T` rather than running one each once the lock is free.
func TestEffectiveConfigWaitsForTheLockOnlyAsLongAsItsCaller(t *testing.T) {
	root := t.TempDir()
	runs := fakeNginx(t, "# configuration file "+root+"/nginx.conf:\nevents {}\n\n")
	service := New(root, filepath.Join(root, "Caddyfile"))

	service.mu.Lock()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	returned := make(chan error, 1)
	go func() {
		_, err := service.EffectiveConfig(ctx)
		returned <- err
	}()
	select {
	case err := <-returned:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("a reader behind the lock got %v, want its own deadline", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a reader behind the lock waited past its own deadline")
	}
	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = service.EffectiveConfig(context.Background())
		}()
	}
	service.mu.Unlock()
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	b, err := os.ReadFile(runs)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(b), "run"); n != 1 {
		t.Fatalf("nginx -T ran %d times for readers that queued together, want once", n)
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
