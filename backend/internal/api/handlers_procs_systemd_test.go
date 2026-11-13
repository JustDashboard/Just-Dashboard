package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/procs"
)

// The tests of this package stand a systemctl of their own on PATH, so they
// stand a manager beside it: a directory that is always there. Whether the
// machine running them was booted with systemd is not theirs to depend on.
func init() { procs.SystemdRunDir = os.TempDir() }

// GET /systemd/ answered 502 command_failed with systemctl's "System has not
// been booted with systemd" on a host that has the program and another init.
// The reads now say the module is not available, as they do without the
// program, and a control answers 503 with the reason and runs nothing.
func TestServicesSayThereIsNoSystemdInsteadOfFailing(t *testing.T) {
	s, bin := engineHost(t, map[string]string{"systemctl": "echo 'System has not been booted with systemd as init system (PID 1). Can not operate.' >&2\nexit 1\n"})
	before := procs.SystemdRunDir
	t.Cleanup(func() { procs.SystemdRunDir = before })
	procs.SystemdRunDir = filepath.Join(t.TempDir(), "absent")
	c := &client{t: t, h: s.Routes(), cookie: signIn(t, s)}

	for path, list := range map[string]string{"/api/v1/systemd/": "units", "/api/v1/systemd/timers": "timers"} {
		w := c.do(http.MethodGet, path, "", nil)
		var body map[string]json.RawMessage
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || w.Code != http.StatusOK {
			t.Fatalf("%s answered %d: %s", path, w.Code, w.Body)
		}
		if string(body["available"]) != "false" || string(body[list]) != "[]" {
			t.Fatalf("%s answered %s", path, w.Body)
		}
	}

	w := c.do(http.MethodPost, "/api/v1/systemd/daemon-reload", "", nil)
	var answer ingressAnswer
	if err := json.Unmarshal(w.Body.Bytes(), &answer); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusServiceUnavailable || answer.Error.Code != "systemd_not_running" ||
		answer.Error.Message != "systemd is not running on this host" || !strings.Contains(answer.Error.Reason, "/run/systemd") {
		t.Fatalf("got %d %s", w.Code, w.Body)
	}
	if ran := shimLog(t, bin, "systemctl"); ran != "" {
		t.Fatalf("systemctl was run with nothing to ask: %q", ran)
	}
}
