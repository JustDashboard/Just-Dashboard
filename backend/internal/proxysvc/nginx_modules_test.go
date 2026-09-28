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

// hostNginxV is `nginx -V` on the Ubuntu 25.04 host this was written on:
// nginx 1.26.3 with the stream module built dynamic, its package
// (libnginx-mod-stream) not installed, and the stream submodules built static
// — which is what a substring search for "--with-stream" gets wrong.
const hostNginxV = `nginx version: nginx/1.26.3 (Ubuntu)
built with OpenSSL 3.4.1 11 Feb 2025
TLS SNI support enabled
configure arguments: --with-cc-opt='-g -O3 -Werror=implicit-function-declaration -fno-omit-frame-pointer -mno-omit-leaf-frame-pointer -ffile-prefix-map=/build/nginx-UFNhTx/nginx-1.26.3=. -flto=auto -ffat-lto-objects -fstack-protector-strong -fstack-clash-protection -Wformat -Werror=format-security -fcf-protection -fdebug-prefix-map=/build/nginx-UFNhTx/nginx-1.26.3=/usr/src/nginx-1.26.3-2ubuntu1.2 -fPIC -Wdate-time -D_FORTIFY_SOURCE=3' --with-ld-opt='-Wl,-Bsymbolic-functions -flto=auto -ffat-lto-objects -Wl,-z,relro -Wl,-z,now -fPIC' --prefix=/usr/share/nginx --conf-path=/etc/nginx/nginx.conf --http-log-path=/var/log/nginx/access.log --error-log-path=stderr --lock-path=/var/lock/nginx.lock --pid-path=/run/nginx.pid --modules-path=/usr/lib/nginx/modules --http-client-body-temp-path=/var/lib/nginx/body --http-fastcgi-temp-path=/var/lib/nginx/fastcgi --http-proxy-temp-path=/var/lib/nginx/proxy --http-scgi-temp-path=/var/lib/nginx/scgi --http-uwsgi-temp-path=/var/lib/nginx/uwsgi --with-compat --with-debug --with-pcre-jit --with-http_ssl_module --with-http_stub_status_module --with-http_realip_module --with-http_auth_request_module --with-http_v2_module --with-http_v3_module --with-http_dav_module --with-http_slice_module --with-threads --build=Ubuntu --with-http_addition_module --with-http_flv_module --with-http_gunzip_module --with-http_gzip_static_module --with-http_mp4_module --with-http_random_index_module --with-http_secure_link_module --with-http_sub_module --with-mail_ssl_module --with-stream_ssl_module --with-stream_ssl_preread_module --with-stream_realip_module --with-http_geoip_module=dynamic --with-http_image_filter_module=dynamic --with-http_perl_module=dynamic --with-http_xslt_module=dynamic --with-mail=dynamic --with-stream=dynamic --with-stream_geoip_module=dynamic
`

// alpineNginxV is nginx.org's own build, which the official Docker image
// ships: the stream module compiled in.
const alpineNginxV = `nginx version: nginx/1.27.5
built by gcc 14.2.0 (Alpine 14.2.0)
built with OpenSSL 3.3.3 11 Feb 2025
TLS SNI support enabled
configure arguments: --prefix=/etc/nginx --sbin-path=/usr/sbin/nginx --modules-path=/usr/lib/nginx/modules --with-http_ssl_module --with-stream --with-stream_realip_module --with-stream_ssl_module --with-stream_ssl_preread_module --with-cc-opt='-Os -fstack-clash-protection -Wformat -Werror=format-security -g'
`

func TestParseNginxBuild(t *testing.T) {
	host := parseNginxBuild(hostNginxV)
	if host.modules["stream"] != "dynamic" || host.modules["stream_ssl_module"] != "static" {
		t.Fatalf("modules = %v", host.modules)
	}
	if host.modulesPath != "/usr/lib/nginx/modules" || host.prefix != "/usr/share/nginx" {
		t.Fatalf("paths = %q %q", host.modulesPath, host.prefix)
	}
	if host.modules["cc-opt"] != "" {
		t.Error("a quoted --with-cc-opt split into words")
	}
	if alpine := parseNginxBuild(alpineNginxV); alpine.modules["stream"] != "static" {
		t.Fatalf("alpine modules = %v", alpine.modules)
	}
	if host.confPath != "/etc/nginx/nginx.conf" || host.errorLog != "stderr" || host.pidPath != "/run/nginx.pid" {
		t.Fatalf("host paths = %q %q %q", host.confPath, host.errorLog, host.pidPath)
	}
	bare := parseNginxBuild("nginx version: nginx/1.25.0\nconfigure arguments: --with-http_ssl_module\n")
	if _, ok := bare.modules["stream"]; ok || bare.modulesPath != "/usr/local/nginx/modules" {
		t.Fatalf("bare build: %+v", bare)
	}
	// configure's defaults sit under the prefix.
	if bare.confPath != "/usr/local/nginx/conf/nginx.conf" || bare.errorLog != "/usr/local/nginx/logs/error.log" ||
		bare.pidPath != "/usr/local/nginx/logs/nginx.pid" {
		t.Fatalf("bare paths = %q %q %q", bare.confPath, bare.errorLog, bare.pidPath)
	}
}

// moduleNginx is an nginx that prints version for -V and, for -T, dump on
// stdout — or failure on stderr with exit 1 when failure is set.
func moduleNginx(t *testing.T, version, dump, failure string) {
	t.Helper()
	dir := t.TempDir()
	write := func(name, content string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	versionFile, dumpFile, failureFile := write("version", version), write("dump", dump), write("failure", failure)
	script := fmt.Sprintf(`#!/bin/sh
case "$1" in
-V) cat '%s' >&2 ;;
-T) if [ -s '%s' ]; then cat '%s' >&2; exit 1; fi; cat '%s' ;;
esac
`, versionFile, failureFile, failureFile, dumpFile)
	if err := os.WriteFile(filepath.Join(dir, "nginx"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// withModulesPath points a dynamic build's modules directory at dir.
func withModulesPath(version, dir string) string {
	return strings.Replace(version, "--modules-path=/usr/lib/nginx/modules", "--modules-path="+dir, 1)
}

func TestStreamModule(t *testing.T) {
	loaded := "# configuration file /etc/nginx/nginx.conf:\ninclude /etc/nginx/modules-enabled/*.conf;\nevents {}\n\n" +
		"# configuration file /etc/nginx/modules-enabled/50-mod-stream.conf:\nload_module modules/ngx_stream_module.so;\n\n"
	notLoaded := "# configuration file /etc/nginx/nginx.conf:\ninclude /etc/nginx/modules-enabled/*.conf;\nevents {}\nhttp {}\n\n"
	unknownStream := "nginx: [emerg] unknown directive \"stream\" in /etc/nginx/nginx.conf:86\nnginx: configuration file /etc/nginx/nginx.conf test failed\n"
	otherFailure := "nginx: [warn] the \"user\" directive makes sense only if the master process runs with super-user privileges\n" +
		"nginx: [emerg] unexpected \"}\" in /etc/nginx/sites-enabled/app:12\n"

	installed := t.TempDir()
	if err := os.WriteFile(filepath.Join(installed, streamModuleFile), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	empty := t.TempDir()

	cases := []struct {
		name, version, dump, failure string
		state                        string
		usable                       bool
	}{
		{"built in", alpineNginxV, "", "", ModuleStatic, true},
		{"not built", "configure arguments: --with-http_ssl_module\n", "", "", ModuleAbsent, false},
		{"loaded by modules-enabled", withModulesPath(hostNginxV, installed), loaded, "", ModuleLoaded, true},
		// This host: the package is not installed and nothing loads it.
		{"not installed", withModulesPath(hostNginxV, empty), notLoaded, "", ModuleNotInstalled, false},
		{"installed, not loaded", withModulesPath(hostNginxV, installed), notLoaded, "", ModuleNotLoaded, false},
		// A stream block already in nginx.conf: nginx itself says the module
		// is missing, and the config test fails for every reload.
		{"stream block without the module", withModulesPath(hostNginxV, empty), "", unknownStream, ModuleNotInstalled, false},
		{"configuration broken elsewhere", withModulesPath(hostNginxV, installed), "", otherFailure, ModuleUnknown, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			moduleNginx(t, tc.version, tc.dump, tc.failure)
			got := New(t.TempDir(), "/nonexistent/Caddyfile").StreamModule(context.Background())
			if got.State != tc.state || got.Usable != tc.usable {
				t.Fatalf("got %+v, want %s usable=%v", got, tc.state, tc.usable)
			}
			if tc.state == ModuleUnknown && !strings.Contains(got.Detail, "unexpected") {
				t.Errorf("unknown without nginx's reason: %+v", got)
			}
			if (tc.state == ModuleNotInstalled || tc.state == ModuleNotLoaded) &&
				filepath.Base(got.Path) != streamModuleFile {
				t.Errorf("path = %q", got.Path)
			}
		})
	}
}

func TestStreamModuleWithoutNginx(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	got := New(t.TempDir(), "/nonexistent/Caddyfile").StreamModule(context.Background())
	if got.State != ModuleUnknown || got.Usable || !strings.Contains(got.Detail, "not found") {
		t.Fatalf("got %+v", got)
	}
}

func TestModulePackage(t *testing.T) {
	for _, tc := range []struct{ manager, module, want string }{
		{"apt", "stream", "libnginx-mod-stream"},
		{"apt", "stream_geoip_module", "libnginx-mod-stream-geoip"},
		{"apt", "http_image_filter_module", "libnginx-mod-http-image-filter"},
		{"apt", "http_xslt_module", "libnginx-mod-http-xslt-filter"},
		{"apt", "mail", "libnginx-mod-mail"},
		{"dnf", "stream", "nginx-mod-stream"},
		{"yum", "http_perl_module", "nginx-mod-http-perl"},
		{"apk", "stream", "nginx-mod-stream"},
		{"pacman", "stream", ""},
		{"", "stream", ""},
	} {
		if got := ModulePackage(tc.manager, tc.module); got != tc.want {
			t.Errorf("%s %s: %q, want %q", tc.manager, tc.module, got, tc.want)
		}
	}
}

// The module report lists the --with- modules nginx -V names — not threads,
// compat or the compiler flags — each placed by what the configuration loads
// and what the host has installed. This host: every dynamic module built,
// none installed, the static ones static.
func TestNginxModules(t *testing.T) {
	installed := t.TempDir()
	for _, file := range []string{"ngx_stream_module.so", "ngx_http_xslt_filter_module.so", "ngx_mail_module.so"} {
		if err := os.WriteFile(filepath.Join(installed, file), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dump := "# configuration file /etc/nginx/nginx.conf:\ninclude /etc/nginx/modules-enabled/*.conf;\nevents {}\n\n" +
		"# configuration file /etc/nginx/modules-enabled/50-mod-stream.conf:\nload_module modules/ngx_stream_module.so;\n\n"
	moduleNginx(t, withModulesPath(hostNginxV, installed), dump, "")
	svc := New(t.TempDir(), "/nonexistent/Caddyfile")
	report, err := svc.NginxModules(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if report.Version != "nginx/1.26.3 (Ubuntu)" || report.OpenSSL != "OpenSSL 3.4.1 11 Feb 2025" || report.ModulesPath != installed {
		t.Fatalf("report = %+v", report)
	}
	states := map[string]string{}
	var names []string
	for _, m := range report.Modules {
		states[m.Name] = m.State
		names = append(names, m.Name)
	}
	want := map[string]string{
		"http_ssl_module": ModuleStatic, "http_v2_module": ModuleStatic, "http_v3_module": ModuleStatic,
		"http_stub_status_module": ModuleStatic, "http_realip_module": ModuleStatic, "http_auth_request_module": ModuleStatic,
		"stream_ssl_preread_module": ModuleStatic,
		"stream":                    ModuleLoaded, "http_xslt_module": ModuleNotLoaded, "mail": ModuleNotLoaded,
		"http_geoip_module": ModuleNotInstalled, "stream_geoip_module": ModuleNotInstalled,
	}
	for name, state := range want {
		if states[name] != state {
			t.Errorf("%s = %q, want %q", name, states[name], state)
		}
	}
	for _, name := range []string{"threads", "compat", "debug", "pcre-jit", "cc-opt"} {
		if _, ok := states[name]; ok {
			t.Errorf("%s is listed as a module: %v", name, names)
		}
	}
	for _, m := range report.Modules {
		if m.Name == "http_xslt_module" && m.Path != filepath.Join(installed, "ngx_http_xslt_filter_module.so") {
			t.Errorf("xslt path = %q", m.Path)
		}
		if m.State == ModuleStatic && m.Path != "" {
			t.Errorf("a static module has a path: %+v", m)
		}
	}

	// Kept for a minute: a second ask does not run nginx again, unless
	// fresh, which the page uses after an install.
	moduleNginx(t, alpineNginxV, "", "")
	if again, _ := svc.NginxModules(context.Background(), false); again != report {
		t.Fatal("the report was not kept")
	}
	fresh, err := svc.NginxModules(context.Background(), true)
	if err != nil || fresh == report {
		t.Fatalf("fresh = %+v, %v", fresh, err)
	}
	for _, m := range fresh.Modules {
		if m.Name == "stream" && m.State != ModuleStatic {
			t.Fatalf("fresh stream = %+v", m)
		}
	}
}

// A configuration that fails for another reason says nothing about what is
// loaded: an installed module is unknown then, never "not loaded", while one
// whose file is absent is still not installed.
func TestNginxModulesWhenTheConfigurationFails(t *testing.T) {
	installed := t.TempDir()
	if err := os.WriteFile(filepath.Join(installed, "ngx_http_perl_module.so"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	moduleNginx(t, withModulesPath(hostNginxV, installed), "", "nginx: [emerg] unexpected \"}\" in /etc/nginx/sites-enabled/app:12\n")
	report, err := New(t.TempDir(), "/nonexistent/Caddyfile").NginxModules(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range report.Modules {
		switch m.Name {
		case "http_perl_module":
			if m.State != ModuleUnknown {
				t.Errorf("perl = %+v", m)
			}
		case "stream":
			if m.State != ModuleNotInstalled {
				t.Errorf("stream = %+v", m)
			}
		}
	}
	if !strings.Contains(report.Detail, "unexpected") {
		t.Errorf("detail = %q", report.Detail)
	}
}

func TestNginxModulesWithoutNginx(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if _, err := New(t.TempDir(), "/nonexistent/Caddyfile").NginxModules(context.Background(), false); !errors.Is(err, ErrNoNginx) {
		t.Fatalf("err = %v", err)
	}
}

// The listing carries the module's state, so the page can say "install the
// module" before it says "paste this block".
func TestStreamsReportTheModule(t *testing.T) {
	moduleNginx(t, withModulesPath(hostNginxV, t.TempDir()), "# configuration file /etc/nginx/nginx.conf:\nevents {}\n", "")
	status, err := New(t.TempDir(), "/nonexistent/Caddyfile").Streams(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Module.State != ModuleNotInstalled || status.Module.Usable {
		t.Fatalf("module = %+v", status.Module)
	}
}
