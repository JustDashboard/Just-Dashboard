package proxysvc

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
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
	res := service.Test(context.Background(), KindNginx)
	if !res.Valid || res.Warnings != 1 || len(res.Diagnostics) != 1 || res.Diagnostics[0].Level != "warn" {
		t.Fatalf("a duplicate server name should pass with one warning: %+v", res)
	}

	broken := filepath.Join(root, "conf.d", "b.conf")
	res, err := service.Validate(context.Background(), KindNginx, broken,
		"server {\n    listen 127.0.0.1:18081;\n    frobnicate on;\n}\n")
	if err != nil {
		t.Fatal(err)
	}
	want := []Diagnostic{{Level: "emerg", Message: `unknown directive "frobnicate"`, File: broken, Line: 3}}
	if res.Valid || res.Warnings != 0 || !reflect.DeepEqual(res.Diagnostics, want) {
		t.Fatalf("got %+v, want the emergency placed at %s:3", res, broken)
	}
}
