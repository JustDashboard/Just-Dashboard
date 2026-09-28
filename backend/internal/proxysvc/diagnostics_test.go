package proxysvc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// These lines are nginx 1.26.3's own, captured from `nginx -t -p <prefix> -c
// <prefix>/nginx.conf` runs against a private prefix (shortened here to
// /tmp/jd-nginx-test). The first shape is what stderr carries when nginx can
// open its startup error log, as it can running as root on the host; the
// second is what it prints when it cannot and falls back to stderr.
func TestParseNginxDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name   string
		output string
		want   []Diagnostic
	}{
		{
			name: "a conflicting server name is a warning with no location",
			output: `nginx: [warn] conflicting server name "dup.example.com" on 127.0.0.1:18080, ignored
nginx: the configuration file /tmp/jd-nginx-test/nginx.conf syntax is ok
nginx: configuration file /tmp/jd-nginx-test/nginx.conf test is successful`,
			want: []Diagnostic{{Level: "warn", Message: `conflicting server name "dup.example.com" on 127.0.0.1:18080, ignored`}},
		},
		{
			name: "an unknown directive names its file and line",
			output: `nginx: [emerg] unknown directive "frobnicate" in /tmp/jd-nginx-test/sites/b.conf:3
nginx: configuration file /tmp/jd-nginx-test/nginx.conf test failed`,
			want: []Diagnostic{{Level: "emerg", Message: `unknown directive "frobnicate"`, File: "/tmp/jd-nginx-test/sites/b.conf", Line: 3}},
		},
		{
			name:   "a bind failure has no location",
			output: `nginx: [emerg] bind() to 0.0.0.0:80 failed (13: Permission denied)`,
			want:   []Diagnostic{{Level: "emerg", Message: "bind() to 0.0.0.0:80 failed (13: Permission denied)"}},
		},
		{
			name: "an unclosed block is placed at the end of its file",
			output: `nginx: [emerg] unexpected end of file, expecting "}" in /tmp/jd-nginx-test/sites/b.conf:6
nginx: configuration file /tmp/jd-nginx-test/nginx.conf test failed`,
			want: []Diagnostic{{Level: "emerg", Message: `unexpected end of file, expecting "}"`, File: "/tmp/jd-nginx-test/sites/b.conf", Line: 6}},
		},
		{
			name: "the timestamped shape reads the same",
			output: `2026/09/27 16:27:19 [warn] 924720#924720: conflicting server name "dup.example.com" on 127.0.0.1:18080, ignored
nginx: the configuration file /tmp/jd-nginx-test/nginx.conf syntax is ok
nginx: configuration file /tmp/jd-nginx-test/nginx.conf test is successful`,
			want: []Diagnostic{{Level: "warn", Message: `conflicting server name "dup.example.com" on 127.0.0.1:18080, ignored`}},
		},
		{
			name: "a timestamped emergency keeps its location",
			output: `2026/09/27 16:27:19 [emerg] 924722#924722: unknown directive "frobnicate" in /tmp/jd-nginx-test/sites/b.conf:3
nginx: configuration file /tmp/jd-nginx-test/nginx.conf test failed`,
			want: []Diagnostic{{Level: "emerg", Message: `unknown directive "frobnicate"`, File: "/tmp/jd-nginx-test/sites/b.conf", Line: 3}},
		},
		{
			// conf.d/*.conf includes it, and a position cut at the space
			// left the path in the message and no line to open.
			name: "a file whose name has a space is placed whole",
			output: `nginx: [emerg] unknown directive "frobnicate" in /tmp/jd-nginx-test/conf.d/zz spaced name.conf:3
nginx: configuration file /tmp/jd-nginx-test/nginx.conf test failed`,
			want: []Diagnostic{{Level: "emerg", Message: `unknown directive "frobnicate"`, File: "/tmp/jd-nginx-test/conf.d/zz spaced name.conf", Line: 3}},
		},
		{
			name:   "a timestamped line places a spaced file whole too",
			output: `2026/09/28 00:49:55 [emerg] 3411286#3411286: unknown directive "frobnicate" in /tmp/jd-nginx-test/conf.d/zz spaced name.conf:3`,
			want:   []Diagnostic{{Level: "emerg", Message: `unknown directive "frobnicate"`, File: "/tmp/jd-nginx-test/conf.d/zz spaced name.conf", Line: 3}},
		},
		{
			name:   "a directory named with ' in' keeps its whole path",
			output: `nginx: [emerg] unknown directive "frobnicate" in /tmp/jd-nginx-test/sites in/b.conf:3`,
			want:   []Diagnostic{{Level: "emerg", Message: `unknown directive "frobnicate"`, File: "/tmp/jd-nginx-test/sites in/b.conf", Line: 3}},
		},
		{
			name:   "an ' in /' the message quotes is not the position",
			output: `nginx: [emerg] open() "/nowhere/a in /b.conf" failed (2: No such file or directory) in /tmp/jd-nginx-test/conf.d/q.conf:3`,
			want:   []Diagnostic{{Level: "emerg", Message: `open() "/nowhere/a in /b.conf" failed (2: No such file or directory)`, File: "/tmp/jd-nginx-test/conf.d/q.conf", Line: 3}},
		},
		{
			name:   "a directive quoting ' in /' is not the position either",
			output: `nginx: [emerg] unknown directive "fr in /ob" in /tmp/jd-nginx-test/conf.d/odd.conf:3`,
			want:   []Diagnostic{{Level: "emerg", Message: `unknown directive "fr in /ob"`, File: "/tmp/jd-nginx-test/conf.d/odd.conf", Line: 3}},
		},
		{
			name:   "quotes that do not pair still leave the position at the end",
			output: `nginx: [emerg] unknown directive "frob"x" in /tmp/jd-nginx-test/conf.d/odd.conf:3`,
			want:   []Diagnostic{{Level: "emerg", Message: `unknown directive "frob"x"`, File: "/tmp/jd-nginx-test/conf.d/odd.conf", Line: 3}},
		},
		{
			// The first place is part of the message; the file to open is
			// the second, where nginx found the path used again.
			name: "a path used twice is placed at its second use",
			output: `nginx: [emerg] the same path name "/tmp/jd-nginx-test/cache" used in /tmp/jd-nginx-test/conf.d/dup.conf:1 and in /tmp/jd-nginx-test/conf.d/dup.conf:2
nginx: configuration file /tmp/jd-nginx-test/nginx.conf test failed`,
			want: []Diagnostic{{Level: "emerg", Message: `the same path name "/tmp/jd-nginx-test/cache" used in /tmp/jd-nginx-test/conf.d/dup.conf:1 and`, File: "/tmp/jd-nginx-test/conf.d/dup.conf", Line: 2}},
		},
		{
			name:   "a timestamped path used twice in a spaced file is placed whole at its second use",
			output: `2026/09/28 01:33:59 [emerg] 3582985#3582985: the same path name "/tmp/jd-nginx-test/cache" used in /tmp/jd-nginx-test/conf.d/zz spaced dup.conf:1 and in /tmp/jd-nginx-test/conf.d/zz spaced dup.conf:2`,
			want:   []Diagnostic{{Level: "emerg", Message: `the same path name "/tmp/jd-nginx-test/cache" used in /tmp/jd-nginx-test/conf.d/zz spaced dup.conf:1 and`, File: "/tmp/jd-nginx-test/conf.d/zz spaced dup.conf", Line: 2}},
		},
		{
			name: "a path with different levels is placed where they differ",
			output: `nginx: [emerg] the same path name "/tmp/jd-nginx-test/lvl" in /tmp/jd-nginx-test/conf.d/lvl.conf:3 has the different levels than in /tmp/jd-nginx-test/conf.d/lvl.conf:4
nginx: configuration file /tmp/jd-nginx-test/nginx.conf test failed`,
			want: []Diagnostic{{Level: "emerg", Message: `the same path name "/tmp/jd-nginx-test/lvl" in /tmp/jd-nginx-test/conf.d/lvl.conf:3 has the different levels than`, File: "/tmp/jd-nginx-test/conf.d/lvl.conf", Line: 4}},
		},
		{
			name:   "a clean test has nothing to say",
			output: "nginx: the configuration file /etc/nginx/nginx.conf syntax is ok\nnginx: configuration file /etc/nginx/nginx.conf test is successful",
			want:   []Diagnostic{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ParseNginxDiagnostics(tc.output); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %#v\nwant %#v", got, tc.want)
			}
		})
	}
}

// Caddy 2.11's `caddy validate`, captured from the caddy:2-alpine image.
func TestParseCaddyDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name   string
		output string
		want   []Diagnostic
	}{
		{
			name: "an unrecognised directive",
			output: `{"level":"info","ts":1790526495.9457455,"msg":"using config from file","file":"/etc/caddy/Caddyfile"}
Error: adapting config using caddyfile: /etc/caddy/Caddyfile:3: unrecognized directive: frobnicate`,
			want: []Diagnostic{{Level: "error", Message: "adapting config using caddyfile: /etc/caddy/Caddyfile:3: unrecognized directive: frobnicate", File: "/etc/caddy/Caddyfile", Line: 3}},
		},
		{
			name: "an unclosed block, and an upstream address that is not a location",
			output: `{"level":"info","ts":1790526499.1150346,"msg":"using config from file","file":"/etc/caddy/Caddyfile"}
Error: adapting config using caddyfile: syntax error: unexpected token '127.0.0.1:3000', expecting '}', at /etc/caddy/Caddyfile:2 import chain: ['']`,
			want: []Diagnostic{{Level: "error", Message: "adapting config using caddyfile: syntax error: unexpected token '127.0.0.1:3000', expecting '}', at /etc/caddy/Caddyfile:2 import chain: ['']", File: "/etc/caddy/Caddyfile", Line: 2}},
		},
		{
			// "//127.0.0.1:3000" inside the URL used to be read as file
			// "//127.0.0.1", line 3000.
			name: "an unclosed block after an upstream URL",
			output: `{"level":"info","ts":1790531706.5374622,"msg":"using config from file","file":"/etc/caddy/c1"}
Error: adapting config using caddyfile: syntax error: unexpected token 'http://127.0.0.1:3000', expecting '}', at /etc/caddy/c1:2 import chain: ['']`,
			want: []Diagnostic{{Level: "error", Message: "adapting config using caddyfile: syntax error: unexpected token 'http://127.0.0.1:3000', expecting '}', at /etc/caddy/c1:2 import chain: ['']", File: "/etc/caddy/c1", Line: 2}},
		},
		{
			name: "an upstream URL with a path",
			output: `{"level":"info","ts":1790531708.0319908,"msg":"using config from file","file":"/etc/caddy/c2"}
Error: adapting config using caddyfile: parsing caddyfile tokens for 'reverse_proxy': parsing upstream 'http://127.0.0.1:3000/api': for now, URLs for proxy upstreams only support scheme, host, and port components, at /etc/caddy/c2:2`,
			want: []Diagnostic{{Level: "error", Message: "adapting config using caddyfile: parsing caddyfile tokens for 'reverse_proxy': parsing upstream 'http://127.0.0.1:3000/api': for now, URLs for proxy upstreams only support scheme, host, and port components, at /etc/caddy/c2:2", File: "/etc/caddy/c2", Line: 2}},
		},
		{
			name: "a valid file with a formatting warning",
			output: `{"level":"info","ts":1790526498.089536,"msg":"using config from file","file":"/etc/caddy/Caddyfile"}
Valid configuration
{"level":"info","ts":1790526498.093357,"msg":"adapted config to JSON","adapter":"caddyfile"}
{"level":"warn","ts":1790526498.0933943,"msg":"Caddyfile input is not formatted; run 'caddy fmt --overwrite' to fix inconsistencies","adapter":"caddyfile","file":"/etc/caddy/Caddyfile","line":2}
{"level":"info","ts":1790526498.0939267,"logger":"http.auto_https","msg":"server is listening only on the HTTPS port but has no TLS connection policies; adding one to enable TLS","server_name":"srv0","https_port":443}`,
			want: []Diagnostic{{Level: "warn", Message: "Caddyfile input is not formatted; run 'caddy fmt --overwrite' to fix inconsistencies", File: "/etc/caddy/Caddyfile", Line: 2}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ParseCaddyDiagnostics(tc.output); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %#v\nwant %#v", got, tc.want)
			}
		})
	}
}

// The real binary against a private prefix: a warning that leaves the test
// passing is counted, and an error is placed in the file that caused it.
func TestValidationCarriesTheTestsDiagnostics(t *testing.T) {
	root := liveNginx(t)
	service := New(root, filepath.Join(root, "Caddyfile"))
	site := "server {\n    listen 127.0.0.1:18080;\n    server_name dup.example.test;\n}\n"
	for _, name := range []string{"a", "b"} {
		if err := os.WriteFile(filepath.Join(root, "conf.d", name+".conf"), []byte(site), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	res, err := service.Test(context.Background(), KindNginx)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Valid || res.Warnings != 1 || len(res.Diagnostics) != 1 || res.Diagnostics[0].Level != "warn" {
		t.Fatalf("a duplicate server name should pass with one warning: %+v", res)
	}

	broken := filepath.Join(root, "conf.d", "b.conf")
	res, err = service.Validate(context.Background(), KindNginx, broken,
		"server {\n    listen 127.0.0.1:18081;\n    frobnicate on;\n}\n")
	if err != nil {
		t.Fatal(err)
	}
	want := []Diagnostic{{Level: "emerg", Message: `unknown directive "frobnicate"`, File: broken, Line: 3}}
	if res.Valid || res.Warnings != 0 || !reflect.DeepEqual(res.Diagnostics, want) {
		t.Fatalf("got %+v, want the emergency placed at %s:3", res, broken)
	}
}

// A Debian site is included through its sites-enabled link, and nginx names
// the link. The diagnostic has to name the file the editor opened, which is
// the sites-available one, or the page can never mark its line.
func TestValidationPlacesADiagnosticInTheLinkedSiteFile(t *testing.T) {
	root := liveNginx(t)
	service := New(root, filepath.Join(root, "Caddyfile"))
	site := filepath.Join(root, "sites-available", "app")
	if err := os.WriteFile(site, []byte("server {\n    listen 127.0.0.1:18095;\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(site, filepath.Join(root, "sites-enabled", "app")); err != nil {
		t.Fatal(err)
	}
	res, err := service.Validate(context.Background(), KindNginx, site,
		"server {\n    listen 127.0.0.1:18095;\n    frobnicate on;\n}\n")
	if err != nil {
		t.Fatal(err)
	}
	want := []Diagnostic{{Level: "emerg", Message: `unknown directive "frobnicate"`, File: site, Line: 3}}
	if res.Valid || !reflect.DeepEqual(res.Diagnostics, want) {
		t.Fatalf("got %+v, want the emergency placed at %s:3", res.Diagnostics, site)
	}
}

// nginx includes a conf.d file whose name has a space, and names it whole in
// its message; the diagnostic has to carry that name and line, or a refusal
// offers no way to the file.
func TestValidationPlacesADiagnosticInAFileWithASpaceInItsName(t *testing.T) {
	root := liveNginx(t)
	service := New(root, filepath.Join(root, "Caddyfile"))
	broken := filepath.Join(root, "conf.d", "zz spaced name.conf")
	res, err := service.Validate(context.Background(), KindNginx, broken,
		"server {\n    listen 127.0.0.1:18096;\n    frobnicate on;\n}\n")
	if err != nil {
		t.Fatal(err)
	}
	want := []Diagnostic{{Level: "emerg", Message: `unknown directive "frobnicate"`, File: broken, Line: 3}}
	if res.Valid || !reflect.DeepEqual(res.Diagnostics, want) {
		t.Fatalf("got %+v, want the emergency placed at %s:3", res.Diagnostics, broken)
	}
}

// nginx names two places when a cache path is declared twice; the message
// keeps the first, and the diagnostic opens the second, where nginx stopped.
func TestValidationPlacesAPathUsedTwiceAtItsSecondUse(t *testing.T) {
	root := liveNginx(t)
	service := New(root, filepath.Join(root, "Caddyfile"))
	broken := filepath.Join(root, "conf.d", "zz spaced dup.conf")
	cache := filepath.Join(root, "cache")
	res, err := service.Validate(context.Background(), KindNginx, broken,
		"proxy_cache_path "+cache+" keys_zone=a:1m;\nproxy_cache_path "+cache+" keys_zone=b:1m;\n")
	if err != nil {
		t.Fatal(err)
	}
	want := []Diagnostic{{
		Level:   "emerg",
		Message: `the same path name "` + cache + `" used in ` + broken + `:1 and`,
		File:    broken,
		Line:    2,
	}}
	if res.Valid || !reflect.DeepEqual(res.Diagnostics, want) {
		t.Fatalf("got %+v, want the emergency placed at %s:2", res.Diagnostics, broken)
	}
}

// fakeCaddy puts a caddy first on PATH that answers `caddy validate --config
// <file>` with Caddy 2's own lines, captured from caddy:2-alpine, naming the
// file it was given: an error for a candidate with an unknown subdirective,
// otherwise a pass with a formatting warning. Each file it is given is noted
// in its directory's "configs" with that file's mode and its directory's, and
// candidates are copied under a scratch root of the test's own.
func fakeCaddy(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	// t.TempDir honours the umask, which may leave it group-writable.
	scratch := filepath.Join(t.TempDir(), "scratch")
	if err := os.Mkdir(scratch, 0o700); err != nil {
		t.Fatal(err)
	}
	useCaddyScratch(t, scratch)
	script := `#!/bin/sh
config=$3
echo "$config $(stat -c %a "$config") $(stat -c %a "$(dirname "$config")")" >> "$(dirname "$0")/configs"
echo '{"level":"info","ts":1790531705.7703078,"msg":"using config from file","file":"'"$config"'"}'
if grep -q frob "$config"; then
	echo "Error: adapting config using caddyfile: parsing caddyfile tokens for 'reverse_proxy': unrecognized subdirective frob, at $config:3" >&2
	exit 1
fi
echo "Valid configuration"
echo '{"level":"warn","ts":1790526498.0933943,"msg":"Caddyfile input is not formatted; run '"'caddy fmt --overwrite'"' to fix inconsistencies","adapter":"caddyfile","file":"'"$config"'","line":2}'
`
	if err := os.WriteFile(filepath.Join(dir, "caddy"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return filepath.Join(dir, "configs")
}

// useCaddyScratch points candidate copies at root for one test.
func useCaddyScratch(t *testing.T, root string) {
	t.Helper()
	previous := caddyScratchRoot
	caddyScratchRoot = root
	t.Cleanup(func() { caddyScratchRoot = previous })
}

// The candidate used to be written to os.CreateTemp(""), the container's own
// /tmp, while caddy — not in the image — ran on the host through nsenter and
// looked for it in the host's /tmp: every check of a Caddyfile edit failed on
// a file that was not there. It goes under the directory mounted at the same
// path on both sides, in a directory of its own that nobody else can read,
// and nothing of it is left behind.
func TestCaddyCandidateIsCheckedWhereTheHostCanSeeIt(t *testing.T) {
	configs := fakeCaddy(t)
	dir := t.TempDir()
	caddyfile := filepath.Join(dir, "Caddyfile")
	service := New(filepath.Join(dir, "nginx"), caddyfile)

	res, err := service.Validate(context.Background(), KindCaddy, caddyfile, "example.test {\n\trespond ok\n}\n")
	if err != nil || !res.Valid {
		t.Fatalf("got %+v, %v", res, err)
	}
	seen, err := os.ReadFile(configs)
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(seen))
	if len(fields) != 3 || !strings.HasPrefix(fields[0], caddyScratchRoot+string(os.PathSeparator)) {
		t.Fatalf("caddy was given %q, want a file under %s", seen, caddyScratchRoot)
	}
	if fields[1] != "600" || fields[2] != "700" {
		t.Fatalf("the candidate was mode %s in a directory of mode %s, want 600 in 700", fields[1], fields[2])
	}
	if left, _ := os.ReadDir(caddyScratchRoot); len(left) != 0 {
		t.Fatalf("the check left %d entries behind in the scratch root", len(left))
	}
}

// The root is /tmp/just-dashboard on the host, in a directory every account
// can write to. One another account created, or can write into, could read a
// candidate or swap it before caddy reads it, so it is refused — before caddy
// is run on anything.
func TestCaddyCandidateIsNotWrittenWhereAnotherAccountCouldReachIt(t *testing.T) {
	configs := fakeCaddy(t)
	dir := t.TempDir()
	caddyfile := filepath.Join(dir, "Caddyfile")
	service := New(filepath.Join(dir, "nginx"), caddyfile)

	shared := filepath.Join(t.TempDir(), "shared")
	if err := os.Mkdir(shared, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(shared, 0o777); err != nil {
		t.Fatal(err)
	}
	elsewhere := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(elsewhere, link); err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{shared, link} {
		useCaddyScratch(t, root)
		if _, err := service.Validate(context.Background(), KindCaddy, caddyfile, "example.test {\n}\n"); err == nil ||
			!strings.Contains(err.Error(), "only the dashboard can write to") {
			t.Fatalf("scratch root %s was accepted: %v", root, err)
		}
	}
	if _, err := os.Stat(configs); !os.IsNotExist(err) {
		t.Fatalf("caddy was run although the candidate had nowhere safe to go: %v", err)
	}
	if left, _ := os.ReadDir(elsewhere); len(left) != 0 {
		t.Fatalf("a candidate was written through the symlinked root")
	}
}

// Caddy validates a temporary copy and names it in every message. The result
// has to name the Caddyfile instead: the copy is deleted before anyone reads
// it, and the page is looking for the file it opened.
func TestCaddyValidationNamesTheFileNotItsCopy(t *testing.T) {
	fakeCaddy(t)
	dir := t.TempDir()
	caddyfile := filepath.Join(dir, "Caddyfile")
	original := "example.test {\n\treverse_proxy 127.0.0.1:3000\n}\n"
	if err := os.WriteFile(caddyfile, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	service := New(filepath.Join(dir, "nginx"), caddyfile)
	broken := "example.test {\n\treverse_proxy 127.0.0.1:3000 {\n\t\tfrob on\n\t}\n}\n"
	message := "adapting config using caddyfile: parsing caddyfile tokens for 'reverse_proxy': unrecognized subdirective frob, at " + caddyfile + ":3"

	res, err := service.Validate(context.Background(), KindCaddy, caddyfile, broken)
	if err != nil {
		t.Fatal(err)
	}
	want := []Diagnostic{{Level: "error", Message: message, File: caddyfile, Line: 3}}
	if res.Valid || !reflect.DeepEqual(res.Diagnostics, want) || strings.Contains(res.Output, caddyScratchRoot) {
		t.Fatalf("got %+v\n%s", res.Diagnostics, res.Output)
	}

	res, err = service.WriteConfig(context.Background(), KindCaddy, caddyfile, broken)
	if !errors.Is(err, ErrInvalidConf) || !reflect.DeepEqual(res.Diagnostics, want) {
		t.Fatalf("a refused save got %v with %+v", err, res.Diagnostics)
	}
	if b, _ := os.ReadFile(caddyfile); string(b) != original {
		t.Fatalf("a refused save changed the file: %q", b)
	}

	res, err = service.Validate(context.Background(), KindCaddy, caddyfile, original)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Valid || res.Warnings != 1 || res.Diagnostics[0].File != caddyfile || res.Diagnostics[0].Line != 2 {
		t.Fatalf("the formatting warning is not placed in the Caddyfile: %+v", res.Diagnostics)
	}

	if _, err := service.Validate(context.Background(), KindCaddy, "/srv/app/Caddyfile", original); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("a path outside the proxy directories was accepted: %v", err)
	}
}
