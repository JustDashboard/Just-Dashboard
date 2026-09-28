package api

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

// metricsNginx prints conf.d in its dump, as nginx does when nginx.conf
// includes it, fails its test while JD_TEST_NGINX_FAIL_TEST is set and its
// reload while JD_TEST_NGINX_FAIL_RELOAD is.
const metricsNginx = `#!/bin/sh
case "$1" in
-t)
	if [ -n "$JD_TEST_NGINX_FAIL_TEST" ]; then
		echo "nginx: [emerg] unknown directive \"stub_status\" in $JD_TEST_NGINX_ROOT/conf.d/jd-status.conf:7" >&2
		exit 1
	fi
	;;
-T)
	echo "# configuration file $JD_TEST_NGINX_ROOT/nginx.conf:"
	echo "events {}"
	for f in "$JD_TEST_NGINX_ROOT"/conf.d/*.conf; do
		[ -e "$f" ] || continue
		echo "# configuration file $f:"
		cat "$f"
	done
	;;
-s)
	if [ -n "$JD_TEST_NGINX_FAIL_RELOAD" ]; then
		echo 'nginx: [error] open() "/run/nginx.pid" failed (2: No such file or directory)' >&2
		exit 1
	fi
	;;
esac
exit 0
`

// metricsServer is an API server over a Debian-layout proxy directory with a
// scripted nginx, and a stub_status page on loopback for the status file to
// point at.
func metricsServer(t *testing.T) (*Server, string, int) {
	t.Helper()
	s := testServer(t)
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, sub := range []string{"sites-available", "sites-enabled", "conf.d", "bin"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "nginx.conf"), []byte("events {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bin", "nginx"), []byte(metricsNginx), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Join(dir, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("JD_TEST_NGINX_ROOT", dir)
	t.Setenv("JD_TEST_NGINX_FAIL_TEST", "")
	t.Setenv("JD_TEST_NGINX_FAIL_RELOAD", "")
	s.Cfg.NginxDir = dir
	s.initModules()

	var mu sync.Mutex
	var requests int64
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		requests++
		fmt.Fprintf(w, "Active connections: 1 \nserver accepts handled requests\n %d %d %d \nReading: 0 Writing: 1 Waiting: 0 \n", requests, requests, requests)
	}))
	t.Cleanup(stub.Close)
	return s, dir, stub.Listener.Addr().(*net.TCPAddr).Port
}

func decodeMetrics(t *testing.T, body []byte) proxysvc.StatusReport {
	t.Helper()
	var report proxysvc.StatusReport
	if err := json.Unmarshal(body, &report); err != nil {
		t.Fatalf("not a metrics report: %v: %s", err, body)
	}
	return report
}

func TestProxyMetricsRoutesAreGated(t *testing.T) {
	s := testServer(t)
	h := s.Routes()
	gates := routeGates(t, h)
	if got := gates["GET /api/v1/proxy/metrics"]; got != [2]int{0, 1} {
		t.Errorf("GET /proxy/metrics has %d capability checks and %d budgets, want a read", got[0], got[1])
	}
	if got := gates["PUT /api/v1/proxy/metrics"]; got != [2]int{1, 1} {
		t.Errorf("PUT /proxy/metrics has %d capability checks and %d budgets, want system.admin", got[0], got[1])
	}
	for _, role := range []auth.Role{auth.RoleReadOnly, auth.RoleLimited} {
		c := &client{t: t, h: h, cookie: signInAs(t, s, string(role)+"-account", role)}
		if w := c.do(http.MethodGet, "/api/v1/proxy/metrics", "", nil); w.Code != http.StatusOK {
			t.Errorf("a %s account reading the metrics got %d: %s", role, w.Code, w.Body.String())
		}
		if w := c.do(http.MethodPut, "/api/v1/proxy/metrics", `{"enabled":true}`, nil); w.Code != http.StatusForbidden {
			t.Errorf("a %s account switching the metrics got %d: %s", role, w.Code, w.Body.String())
		}
	}
}

func TestProxyMetricsSwitchThroughTheAPI(t *testing.T) {
	s, dir, port := metricsServer(t)
	c := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}
	file := filepath.Join(dir, "conf.d", "jd-status.conf")

	off := decodeMetrics(t, c.do(http.MethodGet, "/api/v1/proxy/metrics", "", nil).Body.Bytes())
	if !off.Supported || off.Enabled || off.Path != file || len(off.Samples) != 0 || off.Interval != 5 {
		t.Fatalf("before switching on: %+v", off)
	}

	// A status file in place and answering: switching on proves it with a
	// first reading and changes nothing.
	if err := os.WriteFile(file, []byte(proxysvc.RenderStatusServer(port)), 0o644); err != nil {
		t.Fatal(err)
	}
	w := c.do(http.MethodPut, "/api/v1/proxy/metrics", `{"enabled":true}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("switching on: %d %s", w.Code, w.Body.String())
	}
	on := decodeMetrics(t, w.Body.Bytes())
	if !on.Enabled || on.Endpoint != fmt.Sprintf("http://127.0.0.1:%d/jd-status", port) || on.Current == nil || len(on.Samples) != 1 {
		t.Fatalf("switched on: %+v", on)
	}
	held := decodeMetrics(t, c.do(http.MethodGet,
		fmt.Sprintf("/api/v1/proxy/metrics?epoch=%d&after=%d", on.Epoch, on.Samples[0].Seq), "", nil).Body.Bytes())
	if held.Epoch != on.Epoch || len(held.Samples) != 0 || held.Current == nil {
		t.Fatalf("a caller up to date was sent %+v", held)
	}

	w = c.do(http.MethodPut, "/api/v1/proxy/metrics", `{"enabled":false}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("switching off: %d %s", w.Code, w.Body.String())
	}
	if report := decodeMetrics(t, w.Body.Bytes()); report.Enabled || len(report.Samples) != 0 {
		t.Fatalf("switched off: %+v", report)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("the status file is still there: %v", err)
	}

	for action, detail := range map[string]string{
		"proxy.metrics.enable":  `"changed":false`,
		"proxy.metrics.disable": `"changed":true`,
	} {
		entries, _, err := s.Audit.List(t.Context(), audit.Filter{Action: action})
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 || !entries[0].Success || entries[0].Target != file || !strings.Contains(entries[0].Detail, detail) {
			t.Fatalf("%s audited as %+v", action, entries)
		}
	}
}

func TestProxyMetricsSwitchSaysWhyItRefused(t *testing.T) {
	s, dir, _ := metricsServer(t)
	c := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}
	file := filepath.Join(dir, "conf.d", "jd-status.conf")

	t.Setenv("JD_TEST_NGINX_FAIL_TEST", "1")
	w := c.do(http.MethodPut, "/api/v1/proxy/metrics", `{"enabled":true}`, nil)
	var refused struct {
		Error      struct{ Code, Message string }
		Validation *proxysvc.ValidationResult
	}
	if err := json.Unmarshal(w.Body.Bytes(), &refused); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusUnprocessableEntity || refused.Error.Code != "invalid_config" ||
		!strings.Contains(refused.Error.Message, `unknown directive "stub_status" in `+file+":7") ||
		refused.Validation == nil || refused.Validation.Valid {
		t.Fatalf("a failed test answered %d %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("the status file was left behind: %v", err)
	}
	entries, _, err := s.Audit.List(t.Context(), audit.Filter{Action: "proxy.metrics.enable"})
	if err != nil || len(entries) != 1 || entries[0].Success {
		t.Fatalf("the refusal was audited as %+v, %v", entries, err)
	}

	t.Setenv("JD_TEST_NGINX_FAIL_TEST", "")
	if err := os.WriteFile(file, []byte("server { listen 127.0.0.1:8080; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{"enabled":true}`, `{"enabled":false}`} {
		w := c.do(http.MethodPut, "/api/v1/proxy/metrics", body, nil)
		if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "not written by the dashboard") {
			t.Fatalf("%s over somebody's file answered %d %s", body, w.Code, w.Body.String())
		}
	}
	if report := decodeMetrics(t, c.do(http.MethodGet, "/api/v1/proxy/metrics", "", nil).Body.Bytes()); !report.Foreign || report.Enabled {
		t.Fatalf("somebody's file reads as %+v", report)
	}

	for _, bad := range []struct{ method, path, body string }{
		{http.MethodPut, "/api/v1/proxy/metrics", `{}`},
		{http.MethodPut, "/api/v1/proxy/metrics", `{"enabled":"yes"}`},
		{http.MethodPut, "/api/v1/proxy/metrics", `{"enabled":true,"port":80}`},
		{http.MethodGet, "/api/v1/proxy/metrics?epoch=soon", ""},
		{http.MethodGet, "/api/v1/proxy/metrics?after=-1", ""},
	} {
		if w := c.do(bad.method, bad.path, bad.body, nil); w.Code != http.StatusBadRequest {
			t.Errorf("%s %s %s answered %d %s", bad.method, bad.path, bad.body, w.Code, w.Body.String())
		}
	}
}

// A switch-off whose reload fails has still taken the file out: the refusal
// says what the status server does until nginx reloads, the audit that the
// file changed, and the metrics read as off.
func TestProxyMetricsSwitchOffWithoutAReload(t *testing.T) {
	s, dir, port := metricsServer(t)
	c := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}
	file := filepath.Join(dir, "conf.d", "jd-status.conf")
	if err := os.WriteFile(file, []byte(proxysvc.RenderStatusServer(port)), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("JD_TEST_NGINX_FAIL_RELOAD", "1")
	w := c.do(http.MethodPut, "/api/v1/proxy/metrics", `{"enabled":false}`, nil)
	var refused struct {
		Error struct{ Code, Message string }
	}
	if err := json.Unmarshal(w.Body.Bytes(), &refused); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusBadGateway || refused.Error.Code != "reload_failed" ||
		!strings.Contains(refused.Error.Message, fmt.Sprintf("still answers on 127.0.0.1:%d until nginx next reloads", port)) {
		t.Fatalf("a switch-off nginx did not reload for answered %d %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("the status file is still there: %v", err)
	}
	entries, _, err := s.Audit.List(t.Context(), audit.Filter{Action: "proxy.metrics.disable"})
	if err != nil || len(entries) != 1 || !strings.Contains(entries[0].Detail, `"changed":true`) || !strings.Contains(entries[0].Detail, `"reloaded":false`) {
		t.Fatalf("audited as %+v, %v", entries, err)
	}
	if report := decodeMetrics(t, c.do(http.MethodGet, "/api/v1/proxy/metrics", "", nil).Body.Bytes()); report.Enabled || len(report.Samples) != 0 {
		t.Fatalf("after the switch-off the metrics read %+v", report)
	}
}
