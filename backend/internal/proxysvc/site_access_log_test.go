package proxysvc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Where a site logs is read from its own file: a hand-written site logs
// wherever its author said, and a page that guessed the managed spelling for
// it read an empty file and reported a site nobody visits.
func TestParseSiteSpecReadsWhereTheSiteLogs(t *testing.T) {
	managed := func(mutate func(*SiteSpec)) string {
		spec := proxySpec()
		mutate(spec)
		content, err := RenderNginx(spec)
		if err != nil {
			t.Fatal(err)
		}
		return content
	}
	server := func(body string) string {
		return "server {\n    listen 80;\n    server_name shop.example.com;\n" + body + "\n    location / { proxy_pass http://127.0.0.1:3000; }\n}\n"
	}
	cases := []struct {
		name, content     string
		access, errorFile string
	}{
		{"the form's own files", managed(func(*SiteSpec) {}),
			"/var/log/nginx/app.access.log", "/var/log/nginx/app.error.log"},
		{"the form with logging off", managed(func(s *SiteSpec) { s.AccessLog = false }), "", ""},
		// A managed file edited by hand to drop a directive logs where nginx
		// then puts it — the http block's shared file — not where the
		// renderer would have: nothing writes that file any more.
		{"a managed file missing its error_log",
			strings.Replace(managed(func(*SiteSpec) {}), "    error_log  /var/log/nginx/app.error.log;\n", "", 1),
			"/var/log/nginx/app.access.log", ""},
		{"a named format and a level after the path",
			server("    access_log /srv/logs/shop.access.log main buffer=32k;\n    error_log /srv/logs/shop.error.log warn;"),
			"/srv/logs/shop.access.log", "/srv/logs/shop.error.log"},
		{"a quoted path", server(`    access_log "/var/log/nginx/shop.log";`), "/var/log/nginx/shop.log", ""},
		// None of these is a file the reader can follow.
		{"syslog and stderr", server("    access_log syslog:server=unix:/dev/log;\n    error_log stderr;"), "", ""},
		{"off", server("    access_log off;"), "", ""},
		{"a stream under /dev", server("    access_log /dev/stdout;\n    error_log /dev/stderr;"), "", ""},
		{"a file per host", server("    access_log /var/log/nginx/$host.access.log;"), "", ""},
		{"a path relative to nginx's prefix", server("    access_log logs/shop.access.log;"), "", ""},
		// No directive: nginx's shared log, which is nobody's in particular.
		{"no directive at all", server(""), "", ""},
		// A location silencing one path does not move the site's log.
		{"a location that turns logging off",
			"server {\n    server_name shop.example.com;\n    access_log /var/log/nginx/shop.access.log;\n    location = /favicon.ico { access_log off; }\n}\n",
			"/var/log/nginx/shop.access.log", ""},
		// A plain-HTTP server that only redirects often logs nothing, and the
		// one beside it is the site's.
		{"a redirecting server that logs nothing",
			"server {\n    listen 80;\n    access_log off;\n    return 301 https://$host$request_uri;\n}\n" +
				"server {\n    listen 443 ssl;\n    access_log /var/log/nginx/shop.access.log;\n    error_log /var/log/nginx/shop.error.log;\n}\n",
			"/var/log/nginx/shop.access.log", "/var/log/nginx/shop.error.log"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec, _ := ParseSiteSpec("app", tc.content)
			if spec.AccessLogPath != tc.access || spec.ErrorLogPath != tc.errorFile {
				t.Errorf("access %q error %q, want %q %q", spec.AccessLogPath, spec.ErrorLogPath, tc.access, tc.errorFile)
			}
		})
	}

	// A location silencing one path leaves the site logging, to nginx's
	// shared file: read as the site's switch, the form offered to turn on a
	// log that was on, and the site's page said it kept none.
	spec, _ := ParseSiteSpec("shop", server("    location = /favicon.ico { access_log off; }"))
	if !spec.AccessLog || spec.AccessLogPath != "" {
		t.Errorf("access log %v at %q; want on, to the shared file", spec.AccessLog, spec.AccessLogPath)
	}
}

// A site's record is read from the file it names, and only while that file —
// and every rotated generation beside it — is somewhere the logs page may
// read too: the path comes from a file an operator edits, and a generation
// can be a link to anywhere. The record is the file rather than the site, so
// an edit that moves the log is read from the next question on.
func TestSiteRequestRouteHoldsTheSiteToTheLogRoots(t *testing.T) {
	root, outside, nginx := t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(nginx, "sites-available"), 0o755); err != nil {
		t.Fatal(err)
	}
	site := func(name, directive string) {
		t.Helper()
		content := fmt.Sprintf("server {\n    server_name %s.example.com;\n    %s\n}\n", name, directive)
		if err := os.WriteFile(filepath.Join(nginx, "sites-available", name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, line string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	allow := func(path string) error {
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			resolved = path
		}
		if strings.HasPrefix(resolved, root+string(os.PathSeparator)) {
			return nil
		}
		return errors.New("outside the roots")
	}
	live := filepath.Join(root, "shop.access.log")
	site("shop", "access_log "+live+";")
	site("elsewhere", "access_log "+filepath.Join(outside, "elsewhere.access.log")+";")
	site("quiet", "access_log off;")
	write(live, `203.0.113.9 - - [27/Sep/2026:10:00:01 +0000] "GET /cart HTTP/1.1" 502 157 "-" "curl/8.5.0"`)
	write(live+".1", `203.0.113.9 - - [27/Sep/2026:09:00:01 +0000] "GET / HTTP/1.1" 200 612 "-" "curl/8.5.0"`)
	secret := filepath.Join(outside, "secret.log")
	write(secret, "not a request log")
	if err := os.Symlink(secret, live+".2"); err != nil {
		t.Fatal(err)
	}
	// Files that only begin with the same letters are other records.
	write(live+".json", `{"not":"this site's"}`)
	write(filepath.Join(root, "shop.access.log-v2"), "nor this")

	s := New(nginx, filepath.Join(nginx, "Caddyfile"))
	ctx := context.Background()
	route, err := s.SiteRequestRoute(ctx, "shop", allow)
	if err != nil {
		t.Fatal(err)
	}
	reader, facts, ok := SiteRecordReader(route, allow)
	if !ok {
		t.Fatalf("route %q is not a site's file", route)
	}
	if facts.Path != live || facts.Driver != "nginx" || facts.Latency {
		t.Errorf("facts = %+v; nginx's combined format carries no duration", facts)
	}
	rolled, err := reader.Rolled(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rolled) != 1 {
		t.Fatalf("rolled = %+v, want the one generation inside the roots", rolled)
	}
	var lines []string
	if _, _, _, err := reader.Read(ctx, "", 0, 1<<20, func(l string) { lines = append(lines, l) }); err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || !strings.Contains(lines[0], "/cart") {
		t.Fatalf("read %q", lines)
	}

	// Moved by an edit, the site is another record.
	moved := filepath.Join(root, "shop-moved.access.log")
	site("shop", "access_log "+moved+";")
	if again, err := s.SiteRequestRoute(ctx, "shop", allow); err != nil || again == route {
		t.Errorf("after the edit the route is %q (%v), still %q", again, err, route)
	}

	for name, want := range map[string]error{
		"elsewhere": ErrOutsideLogRoots,
		"quiet":     ErrNoSiteAccessLog,
		"missing":   ErrSiteNotFound,
		"../shop":   ErrSiteNotFound,
	} {
		if _, err := s.SiteRequestRoute(ctx, name, allow); !errors.Is(err, want) {
			t.Errorf("%s: err = %v, want %v", name, err, want)
		}
	}

	// A deployment's route is the record its Logs page holds already, where
	// its file logs where the renderer put it. On an nginx host a route is
	// that file, so a name shaped like one with no file is no site at all.
	deployment := "just-dashboard-env-7.conf"
	if _, err := s.SiteRequestRoute(ctx, deployment, allow); !errors.Is(err, ErrSiteNotFound) {
		t.Errorf("a deployment's name with no route: %v", err)
	}
	if _, _, ok := SiteRecordReader(deployment, allow); ok {
		t.Error("a deployment's route read as a site's file")
	}
	if _, err := s.SiteRequestRoute(ctx, "just-dashboard-Not A Route", allow); !errors.Is(err, ErrSiteNotFound) {
		t.Errorf("a name no renderer writes: %v", err)
	}
	rendered, err := RenderNginx(&SiteSpec{Name: deployment, Kind: "proxy", Domains: []string{"shop.example.com"}, Upstream: "http://127.0.0.1:3000", AccessLog: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nginx, "sites-available", deployment), []byte(rendered), 0o644); err != nil {
		t.Fatal(err)
	}
	if route, err := s.SiteRequestRoute(ctx, deployment, func(string) error { return nil }); err != nil || route != deployment {
		t.Errorf("a deployment's own file: %q %v", route, err)
	}
}

// On the shared Docker Caddy ingress a deployment's route has no file on the
// host, and its name is its record — but only a name the ingress holds a
// route for. Any reader can type the shape of one.
func TestSiteRequestRouteAsksTheIngressForADeploymentsRoute(t *testing.T) {
	root := t.TempDir()
	bin, routes := filepath.Join(root, "bin"), filepath.Join(root, "routes")
	for _, dir := range []string{bin, routes} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	ingress := filepath.Join(root, "ingress.json")
	if err := os.WriteFile(ingress, []byte(`[{"Id":"caddy-id","Name":"/edge","State":{"Running":true},`+
		`"Config":{"Cmd":["caddy","run","--config","/etc/caddy/Caddyfile"]},`+
		`"Mounts":[{"Type":"bind","Source":"/srv/edge/Caddyfile","Destination":"/etc/caddy/Caddyfile"},`+
		`{"Type":"volume","Destination":"/config","RW":true},{"Type":"volume","Destination":"/data","RW":true}],`+
		`"NetworkSettings":{"Ports":{"80/tcp":[{"HostIp":"0.0.0.0","HostPort":"80"}],"443/tcp":[{"HostIp":"0.0.0.0","HostPort":"443"}]}}}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	// `docker exec -i <id> sh -c <test> sh <path>` answers whether the route
	// file is in the ingress, which is whether it is in routes here.
	script := "#!/bin/sh\n" +
		"case \"$1\" in\n" +
		"ps) echo caddy-id ;;\n" +
		"inspect) cat \"$JD_TEST_INGRESS\" ;;\n" +
		"exec) if [ -e \"$JD_TEST_ROUTES/$(basename \"$8\")\" ]; then printf present; else printf absent; fi ;;\n" +
		"*) exit 1 ;;\n" +
		"esac\n"
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("JD_TEST_INGRESS", ingress)
	t.Setenv("JD_TEST_ROUTES", routes)
	if err := os.WriteFile(filepath.Join(routes, "just-dashboard-env-7.conf.caddy"), []byte("route"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := NewWithDockerIngress(t.TempDir(), filepath.Join(root, "Caddyfile"))
	allow := func(string) error { return nil }
	if route, err := s.SiteRequestRoute(context.Background(), "just-dashboard-env-7.conf", allow); err != nil || route != "just-dashboard-env-7.conf" {
		t.Errorf("a route the ingress holds: %q %v", route, err)
	}
	if _, err := s.SiteRequestRoute(context.Background(), "just-dashboard-anything", allow); !errors.Is(err, ErrSiteNotFound) {
		t.Errorf("a name the ingress has no route for: %v", err)
	}
}
