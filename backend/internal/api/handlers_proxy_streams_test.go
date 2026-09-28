package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
	"github.com/Wayy01/Just-Dashboard/backend/internal/updates"
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

// The listing shows any *.conf, and the page sends the name through
// encodeURIComponent: chi handed a%2Bb over as sent, so a+b.conf was "no such
// stream" and stayed. A link to nothing and a file that cannot be read are
// deleted too, and the answer says why no copy was kept.
func TestStreamDeleteTakesEveryFileTheListingShows(t *testing.T) {
	c, dir := streamServer(t, false)
	streams := filepath.Join(dir, "stream.d")
	type answer struct {
		Name, Backup, Link, Unread string
	}
	remove := func(name string) answer {
		t.Helper()
		// QueryEscape encodes these as encodeURIComponent does.
		w := c.do(http.MethodDelete, "/api/v1/proxy/streams/"+url.QueryEscape(name), "", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("delete %q: %d %s", name, w.Code, w.Body.String())
		}
		var res answer
		if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Lstat(filepath.Join(streams, name+".conf")); !os.IsNotExist(err) {
			t.Fatalf("%s.conf is still there", name)
		}
		return res
	}

	for _, name := range []string{"a+b", "a@b", "a:b=c&d$e,f;g"} {
		path := filepath.Join(streams, name+".conf")
		if err := os.WriteFile(path, []byte("server { listen 47913; proxy_pass 10.0.0.5:5432; }\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if res := remove(name); res.Name != name || res.Backup != path+".bak" {
			t.Fatalf("delete %q answered %+v", name, res)
		}
	}

	if err := os.Symlink("../streams-available/gone.conf", filepath.Join(streams, "dangling.conf")); err != nil {
		t.Fatal(err)
	}
	if res := remove("dangling"); res.Link != "../streams-available/gone.conf" || res.Backup != "" {
		t.Fatalf("dangling link answered %+v", res)
	}

	if os.Getuid() == 0 {
		t.Skip("root reads a file whatever its mode")
	}
	locked := filepath.Join(streams, "locked.conf")
	if err := os.WriteFile(locked, []byte("server { listen 47914; proxy_pass 10.0.0.5:5432; }\n"), 0o000); err != nil {
		t.Fatal(err)
	}
	if res := remove("locked"); !strings.Contains(res.Unread, "permission denied") || res.Backup != "" {
		t.Fatalf("unreadable file answered %+v", res)
	}
}

// A stream directory that cannot be read is an error the page can name and
// retry, never an empty list: "nothing forwarded" is a claim.
func TestStreamListNamesAnUnreadableDirectory(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads a directory whatever its mode")
	}
	c, dir := streamServer(t, true)
	streams := filepath.Join(dir, "stream.d")
	if err := os.Chmod(streams, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(streams, 0o755) })
	w := c.do(http.MethodGet, "/api/v1/proxy/streams/", "", nil)
	var res struct {
		Error struct {
			Code, Message, Operation, Resource string
			Retryable                          bool
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	e := res.Error
	if w.Code != http.StatusInternalServerError || e.Code != "stream_dir_unreadable" || !strings.Contains(e.Message, "permission denied") ||
		e.Operation != "read" || e.Resource != "the stream directory" || !e.Retryable {
		t.Fatalf("got %d %+v", w.Code, e)
	}
}

// includeServer is an API server over a temporary nginx directory shaped like
// Ubuntu's — modules-enabled included at the top level — whose nginx has the
// stream module built in, passes every test unless $dir/fail-test exists, and
// logs its runs to $dir/runs.
func includeServer(t *testing.T) (*client, *Server, string) {
	t.Helper()
	dir := t.TempDir()
	for _, sub := range []string{"stream.d", "bin", "modules-enabled"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	conf := "include " + dir + "/modules-enabled/*.conf;\nevents {}\nhttp {}\n"
	if err := os.WriteFile(filepath.Join(dir, "nginx.conf"), []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}
	shim := fmt.Sprintf(`#!/bin/sh
echo "$*" >> '%[1]s/runs'
case "$1" in
-V) echo "nginx version: nginx/1.27.5" >&2; echo "configure arguments: --with-http_ssl_module --with-stream --with-stream_ssl_preread_module" >&2 ;;
-t) if [ -e '%[1]s/fail-test' ]; then echo "nginx: [emerg] unknown directive \"strem\" in %[1]s/nginx.conf:9" >&2; exit 1; fi ;;
esac
exit 0
`, dir)
	if err := os.WriteFile(filepath.Join(dir, "bin", "nginx"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Join(dir, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	s := testServer(t)
	s.Cfg.NginxDir = dir
	s.initModules()
	return &client{t: t, h: s.Routes(), cookie: signIn(t, s)}, s, dir
}

// Planning and connecting need system.admin, disconnecting is destructive,
// and the module report is read by every account, like the status beside it.
func TestStreamIncludeRoutesAreGated(t *testing.T) {
	_, s, _ := includeServer(t)
	h := s.Routes()
	gates := routeGates(t, h)
	for _, rt := range []struct {
		method, path string
		want         [2]int
	}{
		{http.MethodGet, "/api/v1/proxy/modules", [2]int{0, 1}},
		{http.MethodGet, "/api/v1/proxy/streams/include/plan", [2]int{1, 1}},
		{http.MethodPost, "/api/v1/proxy/streams/include", [2]int{1, 1}},
		{http.MethodPost, "/api/v1/proxy/streams/include/remove", [2]int{2, 2}},
	} {
		if got := gates[rt.method+" "+rt.path]; got != rt.want {
			t.Errorf("%s %s: %v, want %v", rt.method, rt.path, got, rt.want)
		}
	}
	reader := &client{t: t, h: h, cookie: signInAs(t, s, "reader", auth.RoleReadOnly)}
	limited := &client{t: t, h: h, cookie: signInAs(t, s, "limited", auth.RoleLimited)}
	for _, c := range []*client{reader, limited} {
		for _, rt := range [][2]string{
			{http.MethodGet, "/api/v1/proxy/streams/include/plan"},
			{http.MethodPost, "/api/v1/proxy/streams/include"},
			{http.MethodPost, "/api/v1/proxy/streams/include/remove"},
		} {
			if w := c.do(rt[0], rt[1], `{}`, nil); w.Code != http.StatusForbidden {
				t.Errorf("%s %s = %d, want 403", rt[0], rt[1], w.Code)
			}
		}
	}
	if w := reader.do(http.MethodGet, "/api/v1/proxy/modules", "", nil); w.Code != http.StatusOK {
		t.Errorf("a read-only account got %d for the module report: %s", w.Code, w.Body.String())
	}
}

// The page's whole connect: the plan it shows, the connect it posts, the
// listing that then says whose include reads the directory, and the
// disconnect — each audited with the file it changed.
func TestStreamIncludeConnectsAndDisconnectsThroughTheAPI(t *testing.T) {
	c, s, dir := includeServer(t)
	w := c.do(http.MethodGet, "/api/v1/proxy/streams/include/plan", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("plan: %d %s", w.Code, w.Body.String())
	}
	var plan proxysvc.StreamIncludePlan
	if err := json.Unmarshal(w.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	dropIn := filepath.Join(dir, "modules-enabled", "zz-just-dashboard-stream.conf")
	if plan.Mode != "dropin" || plan.Path != dropIn || !strings.Contains(plan.After, "stream {") {
		t.Fatalf("plan = %+v", plan)
	}

	// A plan that no longer holds is refused, with its own code.
	stale, _ := json.Marshal(map[string]any{"mode": "nginx.conf", "path": filepath.Join(dir, "nginx.conf"), "reload": true})
	if w := c.do(http.MethodPost, "/api/v1/proxy/streams/include", string(stale), nil); w.Code != http.StatusConflict {
		t.Fatalf("stale plan: %d %s", w.Code, w.Body.String())
	} else if code, _ := errorCode(t, w.Body.Bytes()); code != "plan_changed" {
		t.Fatalf("stale plan code = %s", code)
	}

	// A connect whose test fails is a 422 with nginx's words, and nothing stays.
	if err := os.WriteFile(filepath.Join(dir, "fail-test"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"mode": plan.Mode, "path": plan.Path, "reload": true})
	if w := c.do(http.MethodPost, "/api/v1/proxy/streams/include", string(body), nil); w.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(w.Body.String(), "strem") {
		t.Fatalf("failed test: %d %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(dropIn); !os.IsNotExist(err) {
		t.Fatal("a drop-in that failed its test stayed")
	}
	os.Remove(filepath.Join(dir, "fail-test"))

	w = c.do(http.MethodPost, "/api/v1/proxy/streams/include", string(body), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("connect: %d %s", w.Code, w.Body.String())
	}
	var res proxysvc.StreamIncludeResult
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if !res.Reloaded || res.Mode != "dropin" {
		t.Fatalf("connect = %+v", res)
	}
	var status proxysvc.StreamStatus
	if err := json.Unmarshal(c.do(http.MethodGet, "/api/v1/proxy/streams/", "", nil).Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if !status.Included || status.Connection == nil || status.Connection.Path != dropIn {
		t.Fatalf("listing = %+v", status)
	}
	if w := c.do(http.MethodGet, "/api/v1/proxy/streams/include/plan", "", nil); w.Code != http.StatusConflict {
		t.Fatalf("second plan: %d", w.Code)
	} else if code, _ := errorCode(t, w.Body.Bytes()); code != "already_included" {
		t.Fatalf("second plan code = %s", code)
	}

	if w := c.do(http.MethodPost, "/api/v1/proxy/streams/include/remove", `{"reload":true}`, nil); w.Code != http.StatusOK {
		t.Fatalf("disconnect: %d %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(dropIn); !os.IsNotExist(err) {
		t.Fatal("the drop-in is still there")
	}
	if w := c.do(http.MethodPost, "/api/v1/proxy/streams/include/remove", `{"reload":true}`, nil); w.Code != http.StatusConflict {
		t.Fatalf("second disconnect: %d", w.Code)
	} else if code, _ := errorCode(t, w.Body.Bytes()); code != "not_connected" {
		t.Fatalf("second disconnect code = %s", code)
	}

	for _, action := range []string{"proxy.stream.include.add", "proxy.stream.include.remove"} {
		entries, _, err := s.Audit.List(context.Background(), audit.Filter{Action: action})
		if err != nil {
			t.Fatal(err)
		}
		var targets []string
		for _, e := range entries {
			targets = append(targets, e.Target+" "+e.Detail)
		}
		if len(entries) == 0 || !strings.Contains(strings.Join(targets, "\n"), dropIn) {
			t.Errorf("%s audited as %q", action, targets)
		}
	}
}

// fakeCatalogue is a package manager that has exactly the packages in has,
// answers err for every lookup when it is set, and logs what it was asked.
type fakeCatalogue struct {
	manager string
	has     map[string]bool
	err     error
	asked   []string
}

func (f *fakeCatalogue) Manager() string { return f.manager }

func (f *fakeCatalogue) Describe(_ context.Context, name string) (*updates.PackageDetail, error) {
	f.asked = append(f.asked, name)
	if f.err != nil {
		return nil, f.err
	}
	if !f.has[name] {
		return nil, updates.ErrUnknownPackage
	}
	return &updates.PackageDetail{Name: name}, nil
}

// A package is named only where the package manager has it: one it does not
// know is never offered, a manager that builds the module in has no package
// to name, and a lookup that fails for another reason names it anyway, for
// the install to say the rest. The answer is kept rather than asked again.
func TestModulePackageNamesOnlyWhatThePackageManagerHas(t *testing.T) {
	for _, tc := range []struct {
		name string
		fake *fakeCatalogue
		want string
	}{
		{"apt has it", &fakeCatalogue{manager: "apt", has: map[string]bool{"libnginx-mod-stream": true}}, "libnginx-mod-stream"},
		{"apt does not", &fakeCatalogue{manager: "apt"}, ""},
		{"dnf has it", &fakeCatalogue{manager: "dnf", has: map[string]bool{"nginx-mod-stream": true}}, "nginx-mod-stream"},
		{"the lookup fails", &fakeCatalogue{manager: "apt", err: errors.New("apt-cache: signal: killed")}, "libnginx-mod-stream"},
		{"built in", &fakeCatalogue{manager: "pacman", has: map[string]bool{"libnginx-mod-stream": true}}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &Server{}
			s.modules.proxyExtras.modulePackages.catalogue = tc.fake
			for range 2 {
				if got := s.modulePackage(context.Background(), "stream"); got != tc.want {
					t.Fatalf("package = %q, want %q", got, tc.want)
				}
			}
			if len(tc.fake.asked) > 1 {
				t.Fatalf("asked %q: the answer was not kept", tc.fake.asked)
			}
		})
	}
}

// Where nginx has no stream module, connecting is refused with a code the
// page answers with the install, and the listing and the module report name
// the package only when the package manager has it.
func TestStreamIncludeWaitsForTheModule(t *testing.T) {
	c, s, dir := includeServer(t)
	shim := fmt.Sprintf(`#!/bin/sh
case "$1" in
-V) echo "configure arguments: --modules-path=%[1]s/modules --with-stream=dynamic" >&2 ;;
-T) echo "# configuration file %[1]s/nginx.conf:"; echo "events {}" ;;
esac
exit 0
`, dir)
	if err := os.WriteFile(filepath.Join(dir, "bin", "nginx"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	w := c.do(http.MethodGet, "/api/v1/proxy/streams/include/plan", "", nil)
	if code, _ := errorCode(t, w.Body.Bytes()); w.Code != http.StatusConflict || code != "module_missing" {
		t.Fatalf("plan: %d %s", w.Code, w.Body.String())
	}
	var status proxysvc.StreamStatus
	if err := json.Unmarshal(c.do(http.MethodGet, "/api/v1/proxy/streams/", "", nil).Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.Module.State != proxysvc.ModuleNotInstalled {
		t.Fatalf("module = %+v", status.Module)
	}
	for _, has := range []bool{true, false} {
		packages := &s.modules.proxyExtras.modulePackages
		packages.catalogue = &fakeCatalogue{manager: "apt", has: map[string]bool{"libnginx-mod-stream": has}}
		packages.known = nil
		want := map[bool]string{true: "libnginx-mod-stream", false: ""}[has]
		var status proxysvc.StreamStatus
		if err := json.Unmarshal(c.do(http.MethodGet, "/api/v1/proxy/streams/", "", nil).Body.Bytes(), &status); err != nil {
			t.Fatal(err)
		}
		if status.Module.Package != want {
			t.Errorf("listing, package manager has it %v: package = %q", has, status.Module.Package)
		}
		var report proxysvc.NginxModules
		if err := json.Unmarshal(c.do(http.MethodGet, "/api/v1/proxy/modules?fresh=1", "", nil).Body.Bytes(), &report); err != nil {
			t.Fatal(err)
		}
		var stream *proxysvc.NginxModule
		for i := range report.Modules {
			if report.Modules[i].Name == "stream" {
				stream = &report.Modules[i]
			}
		}
		if stream == nil || stream.State != proxysvc.ModuleNotInstalled || stream.Package != want {
			t.Errorf("module report, package manager has it %v: %+v", has, stream)
		}
	}
}

// The module report: every --with- module and its state; 503 without nginx,
// which the engine line reads as "nothing to show".
func TestNginxModuleReport(t *testing.T) {
	c, _, dir := includeServer(t)
	w := c.do(http.MethodGet, "/api/v1/proxy/modules", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	var report proxysvc.NginxModules
	if err := json.Unmarshal(w.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Version != "nginx/1.27.5" || len(report.Modules) != 3 || report.Modules[1].Name != "stream" ||
		report.Modules[1].State != proxysvc.ModuleStatic {
		t.Fatalf("report = %+v", report)
	}
	if err := os.Remove(filepath.Join(dir, "bin", "nginx")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Join(dir, "bin"))
	if w := c.do(http.MethodGet, "/api/v1/proxy/modules?fresh=1", "", nil); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("without nginx: %d %s", w.Code, w.Body.String())
	}
}
