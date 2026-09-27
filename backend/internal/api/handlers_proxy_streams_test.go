package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

// streamServer is an API server over a temporary nginx directory whose nginx
// passes every test, fails its reload while $dir/fail-reload exists, and logs
// its runs to $dir/runs. included decides whether nginx.conf reads stream.d.
func streamServer(t *testing.T, included bool) (*client, string) {
	t.Helper()
	dir := t.TempDir()
	for _, sub := range []string{"stream.d", "bin"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	conf := "events {}\nhttp {}\n"
	if included {
		conf += "stream { include stream.d/*.conf; }\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "nginx.conf"), []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}
	shim := fmt.Sprintf(`#!/bin/sh
echo "$*" >> '%[1]s/runs'
if [ "$1" = "-s" ] && [ -e '%[1]s/fail-reload' ]; then echo "nginx: [alert] kill(1234, 1) failed (3: No such process)" >&2; exit 1; fi
exit 0
`, dir)
	if err := os.WriteFile(filepath.Join(dir, "bin", "nginx"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Join(dir, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	s := testServer(t)
	s.Cfg.NginxDir = dir
	s.initModules()
	return &client{t: t, h: s.Routes(), cookie: signIn(t, s)}, dir
}

func streamBody(spec proxysvc.StreamSpec, previous string, reload bool) string {
	b, _ := json.Marshal(map[string]any{"spec": spec, "previous": previous, "reload": reload})
	return string(b)
}

// A port these tests use nothing else on the host is likely to hold, since
// the save reads the host's real listeners.
func testStream(name string, port int) proxysvc.StreamSpec {
	return proxysvc.StreamSpec{Name: name, Listen: port, Protocol: "tcp", Upstream: "10.0.0.5:5432", AllowFrom: []string{"10.0.0.0/8"}}
}

func errorCode(t *testing.T, body []byte) (string, string) {
	t.Helper()
	var res struct {
		Error struct{ Code, Field string } `json:"error"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatal(err)
	}
	return res.Error.Code, res.Error.Field
}

func TestStreamSaveAnswersEachRefusalWithItsOwnCode(t *testing.T) {
	c, dir := streamServer(t, true)
	if w := c.do(http.MethodPost, "/api/v1/proxy/streams/", streamBody(testStream("replica", 47913), "", true), nil); w.Code != http.StatusOK {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	if w := c.do(http.MethodPost, "/api/v1/proxy/streams/", streamBody(testStream("bastion", 47914), "", true), nil); w.Code != http.StatusOK {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	if err := os.WriteFile(filepath.Join(dir, "stream.d", "manual.conf"),
		[]byte("server { listen 47920; deny 192.0.2.1; proxy_pass 10.0.0.5:22; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name, body, code, field string
		status                  int
	}{
		{"new over an existing name", streamBody(testStream("replica", 47915), "", true), "stream_exists", "spec.name", http.StatusConflict},
		{"rename onto another stream", streamBody(testStream("replica", 47914), "bastion", true), "stream_exists", "spec.name", http.StatusConflict},
		{"another stream's port", streamBody(testStream("third", 47913), "", true), "port_in_use", "spec.listen", http.StatusConflict},
		{"a hand-written file", streamBody(testStream("manual", 47920), "manual", true), "stream_handwritten", "", http.StatusConflict},
		{"a stream that is gone", streamBody(testStream("ghost", 47921), "ghost", true), "not_found", "", http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := c.do(http.MethodPost, "/api/v1/proxy/streams/", tc.body, nil)
			code, field := errorCode(t, w.Body.Bytes())
			if w.Code != tc.status || code != tc.code || field != tc.field {
				t.Fatalf("got %d %s %q: %s", w.Code, code, field, w.Body.String())
			}
		})
	}
}

// A reload that fails after the test passed is a saved stream, not "Not
// applied": 200, with why nginx did not reload.
func TestStreamSaveReportsAFailedReloadAsSaved(t *testing.T) {
	c, dir := streamServer(t, true)
	if err := os.WriteFile(filepath.Join(dir, "fail-reload"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	w := c.do(http.MethodPost, "/api/v1/proxy/streams/", streamBody(testStream("replica", 47913), "", true), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("got %d: %s", w.Code, w.Body.String())
	}
	var res proxysvc.StreamResult
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Reloaded || !strings.Contains(res.ReloadError, "No such process") {
		t.Fatalf("result = %+v", res)
	}
}

// Deleting a stream nginx never read does not reload: there is nothing to
// stop, and a reload would apply every other pending edit on the host. One it
// did read reloads, and a failed reload comes back in the body.
func TestStreamDeleteReloadsOnlyWhatNginxRead(t *testing.T) {
	for _, included := range []bool{false, true} {
		t.Run(fmt.Sprintf("included=%v", included), func(t *testing.T) {
			c, dir := streamServer(t, included)
			if err := os.WriteFile(filepath.Join(dir, "stream.d", "replica.conf"),
				[]byte("server { listen 47913; proxy_pass 10.0.0.5:5432; }\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "fail-reload"), nil, 0o644); err != nil {
				t.Fatal(err)
			}
			w := c.do(http.MethodDelete, "/api/v1/proxy/streams/replica", "", nil)
			if w.Code != http.StatusOK {
				t.Fatalf("got %d: %s", w.Code, w.Body.String())
			}
			var res struct {
				Reloaded    bool   `json:"reloaded"`
				ReloadError string `json:"reloadError"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
				t.Fatal(err)
			}
			runs, _ := os.ReadFile(filepath.Join(dir, "runs"))
			reloaded := strings.Contains(string(runs), "-s reload")
			if reloaded != included || res.Reloaded {
				t.Fatalf("reload ran: %v (runs %q), result %+v", reloaded, runs, res)
			}
			if included != (res.ReloadError != "") {
				t.Fatalf("reloadError = %q", res.ReloadError)
			}
			if _, err := os.Stat(filepath.Join(dir, "stream.d", "replica.conf.bak")); err != nil {
				t.Fatal("no backup kept")
			}
		})
	}
}

func TestStreamPreviewReturnsItsWarnings(t *testing.T) {
	c, _ := streamServer(t, true)
	spec := testStream("udp-thing", 47913)
	spec.Protocol, spec.ProxyProtocol, spec.AllowFrom = "udp", true, nil
	w := c.do(http.MethodPost, "/api/v1/proxy/streams/preview", streamBody(spec, "", false), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("got %d: %s", w.Code, w.Body.String())
	}
	var res struct {
		Content  string   `json:"content"`
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 2 || !strings.Contains(res.Warnings[1], "first datagram") {
		t.Fatalf("warnings = %q", res.Warnings)
	}
}

func TestStreamListCarriesTheIncludeAndEachStreamsAccess(t *testing.T) {
	c, dir := streamServer(t, true)
	if err := os.WriteFile(filepath.Join(dir, "stream.d", "open.conf"),
		[]byte("server { listen 47913; allow all; deny all; proxy_pass 10.0.0.5:5432; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := c.do(http.MethodGet, "/api/v1/proxy/streams/", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("got %d: %s", w.Code, w.Body.String())
	}
	var status proxysvc.StreamStatus
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if !status.Included || len(status.Streams) != 1 || !status.Streams[0].Open {
		t.Fatalf("status = %+v", status)
	}
}
