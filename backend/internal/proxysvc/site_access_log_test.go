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
		// A managed file edited by hand to drop a directive still logs where
		// the renderer would have put it.
		{"a managed file missing its error_log",
			strings.Replace(managed(func(*SiteSpec) {}), "    error_log  /var/log/nginx/app.error.log;\n", "", 1),
			"/var/log/nginx/app.access.log", "/var/log/nginx/app.error.log"},
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
}

// A site's record is read from the file it names, and only while that file —
// and every rotated generation beside it — is somewhere the logs page may
// read too: the path comes from a file an operator edits, and a generation
// can be a link to anywhere.
func TestSiteAccessLogReaderHoldsTheSiteToTheLogRoots(t *testing.T) {
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

	s := New(nginx, filepath.Join(nginx, "Caddyfile"))
	ctx := context.Background()
	reader, facts, err := s.SiteAccessLogReader(ctx, "shop", allow)
	if err != nil {
		t.Fatal(err)
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

	for name, want := range map[string]error{
		"elsewhere": ErrOutsideLogRoots,
		"quiet":     ErrNoSiteAccessLog,
		"missing":   ErrSiteNotFound,
		"../shop":   ErrSiteNotFound,
	} {
		if _, _, err := s.SiteAccessLogReader(ctx, name, allow); !errors.Is(err, want) {
			t.Errorf("%s: err = %v, want %v", name, err, want)
		}
	}
}
