package proxysvc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// freePort is a loopback port nothing listens on. `nginx -t` binds every
// address it is given, so an unprivileged test cannot test a site on 80.
func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

// installSite writes a rendered site into the private prefix on a loopback
// port instead of 80, and links it.
func installSite(t *testing.T, root string, spec *SiteSpec, port int) string {
	t.Helper()
	content, err := RenderNginx(spec)
	if err != nil {
		t.Fatal(err)
	}
	content = strings.ReplaceAll(content, "    listen 80;\n", fmt.Sprintf("    listen 127.0.0.1:%d;\n", port))
	content = strings.ReplaceAll(content, "    listen [::]:80;\n", "")
	available := filepath.Join(root, "sites-available", spec.Name)
	if err := os.WriteFile(available, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(available, filepath.Join(root, "sites-enabled", spec.Name)); err != nil {
		t.Fatal(err)
	}
	return content
}

// startNginx runs the private prefix's nginx in the foreground until the
// test ends.
func startNginx(t *testing.T, root string) {
	t.Helper()
	var output bytes.Buffer
	nginx := exec.Command(filepath.Join(root, "bin", "nginx"), "-g", "daemon off;")
	nginx.Stdout, nginx.Stderr = &output, &output
	if err := nginx.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = nginx.Process.Signal(syscall.SIGQUIT)
		_ = nginx.Wait()
		if t.Failed() {
			t.Log(output.String())
		}
	})
}

// siteGet asks the nginx on port for path as host, retrying while it starts.
func siteGet(t *testing.T, port int, host, path string, header ...string) (*http.Response, string) {
	t.Helper()
	client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{DisableCompression: true}}
	request, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:"+strconv.Itoa(port)+path, nil)
	request.Host = host
	for i := 0; i+1 < len(header); i += 2 {
		request.Header.Set(header[i], header[i+1])
	}
	var response *http.Response
	var err error
	for deadline := time.Now().Add(5 * time.Second); ; {
		response, err = client.Do(request)
		if err == nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("%s%s: %v", host, path, err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	return response, string(body)
}

// The host's real nginx reports a second claim on a name as a warning and
// exits 0; the conflict is read from that warning and names the site that
// holds the name.
func TestLiveServerNameConflictIsReadFromNginx(t *testing.T) {
	root := liveNginx(t)
	service := New(root, filepath.Join(root, "Caddyfile"))
	port := freePort(t)
	installSite(t, root, plainSpec("legacy", "app.example.com"), port)
	installSite(t, root, plainSpec("other", "other.example.com"), port)
	content := installSite(t, root, plainSpec("app", "App.example.com"), port)

	v := runValidator(context.Background(), "nginx", "-t")
	if !v.Valid {
		t.Fatalf("the rendered sites failed their test:\n%s", v.Output)
	}
	full := resolvedFile(filepath.Join(root, "sites-available", "app"))
	got := service.serverNameConflicts(v, []string{"App.example.com"}, content, full)
	want := []ServerNameConflict{{Domain: "app.example.com", Listen: fmt.Sprintf("127.0.0.1:%d", port), Site: "legacy"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("conflicts = %+v, want %+v\nnginx said:\n%s", got, want, v.Output)
	}
	// app sorts before legacy in sites-enabled, so nginx reads it first and
	// it is legacy's claim that is ignored.
	service.mu.Lock()
	service.orderConflicts(context.Background(), got, full, nil)
	service.mu.Unlock()
	if got[0].Effect != ConflictTakes || got[0].Site != "legacy" {
		t.Fatalf("ordered as %+v, want app taking the name from legacy", got[0])
	}

	// The same name on another address of the same port is no conflict:
	// nginx answers 127.0.0.2 from the blocks bound there.
	if err := os.Remove(filepath.Join(root, "sites-enabled", "app")); err != nil {
		t.Fatal(err)
	}
	moved := strings.ReplaceAll(content, "127.0.0.1:", "127.0.0.2:")
	if err := os.WriteFile(full, []byte(moved), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(full, filepath.Join(root, "sites-enabled", "app")); err != nil {
		t.Fatal(err)
	}
	v = runValidator(context.Background(), "nginx", "-t")
	if got := service.serverNameConflicts(v, []string{"app.example.com"}, moved, full); len(got) != 0 || !v.Valid {
		t.Fatalf("a separate address conflicted: %+v\n%s", got, v.Output)
	}
}

// What a refused save says about a contested name is what a running nginx
// then does. The refusal said "already served by" the other site whichever
// way round it was, so an edit to the site nginx answers was refused in the
// name of the one it ignores, and saving anyway took a working site's domain
// with no word that it would.
func TestLiveNameConflictSaysWhichSiteNginxAnswers(t *testing.T) {
	root := liveNginx(t)
	service := New(root, filepath.Join(root, "Caddyfile"))
	ctx := context.Background()
	port := freePort(t)
	listen := fmt.Sprintf("127.0.0.1:%d", port)
	upstreams := map[string]string{}
	for _, name := range []string{"legacy", "app", "zz"} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, name)
		}))
		t.Cleanup(server.Close)
		upstreams[name] = server.URL
	}
	spec := func(name string) *SiteSpec {
		s := plainSpec(name, "app.test")
		s.Upstream = upstreams[name]
		return s
	}
	// SaveSite on the test's own port: nginx -t binds what it is given.
	save := func(spec *SiteSpec, opts SiteSave) (*SiteResult, error) {
		t.Helper()
		content, err := RenderNginx(spec)
		if err != nil {
			t.Fatal(err)
		}
		content = strings.ReplaceAll(content, "    listen 80;\n", "    listen "+listen+";\n")
		content = strings.ReplaceAll(content, "    listen [::]:80;\n", "")
		service.mu.Lock()
		defer service.mu.Unlock()
		res, err := service.saveSiteLocked(ctx, spec, content, opts)
		if errors.Is(err, errSiteReloadFailed) {
			t.Fatalf("nginx did not reload: %v", err)
		}
		return res, err
	}
	answers := func(want string) {
		t.Helper()
		body := ""
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
			if _, body = siteGet(t, port, "app.test", "/"); body == want {
				return
			}
		}
		t.Fatalf("app.test answered %q, want %q", body, want)
	}

	installSite(t, root, spec("legacy"), port)
	startNginx(t, root)
	answers("legacy")

	// app sorts before legacy, so nginx reads it first.
	res, err := save(spec("app"), SiteSave{Enable: true, Reload: true})
	if !errors.Is(err, ErrServerNameConflict) {
		t.Fatalf("got %v, want a name conflict", err)
	}
	if want := "Saving anyway takes app.test on " + listen + " from legacy, since nginx reads this site first."; ConflictSummary(res.Conflicts) != want {
		t.Fatalf("summary = %q, want %q", ConflictSummary(res.Conflicts), want)
	}
	answers("legacy")
	if res, err = save(spec("app"), SiteSave{Enable: true, Reload: true, AllowConflict: true}); err != nil || !res.Reloaded {
		t.Fatalf("saving anyway: %v %+v", err, res)
	}
	answers("app")

	// zz sorts after app, and it is zz's claim nginx ignores.
	res, err = save(spec("zz"), SiteSave{Enable: true, Reload: true})
	if !errors.Is(err, ErrServerNameConflict) {
		t.Fatalf("got %v, want a name conflict", err)
	}
	if want := "nginx answers app.test on " + listen + " from app, which it reads first, and ignores this site's claim."; ConflictSummary(res.Conflicts) != want {
		t.Fatalf("summary = %q, want %q", ConflictSummary(res.Conflicts), want)
	}

	// The site answering the name is edited without a refusal, and goes on
	// answering it.
	edited := spec("app")
	edited.ClientMaxBody = "10m"
	res, err = save(edited, SiteSave{Overwrite: true, Reload: true})
	if err != nil {
		t.Fatalf("an edit to the site nginx answers from was refused: %v", err)
	}
	want := []ServerNameConflict{{Domain: "app.test", Listen: listen, Site: "legacy", Effect: ConflictKeeps}}
	if !reflect.DeepEqual(res.Conflicts, want) {
		t.Fatalf("conflicts = %+v, want %+v", res.Conflicts, want)
	}
	answers("app")
}

// What the rendered routing does, asked of a running nginx rather than read
// off the file: an upstream path replaces the location's prefix, a folder is
// served at its path and nothing beside it, a root location keeps nginx's
// own reading, a socket is reachable, and compression off means off under an
// http block that turns it on.
func TestLiveSiteRoutingServesWhatTheFormSays(t *testing.T) {
	root := liveNginx(t)
	conf := filepath.Join(root, "nginx.conf")
	raw, err := os.ReadFile(conf)
	if err != nil {
		t.Fatal(err)
	}
	// Debian's nginx.conf compresses for every site; so does this one.
	withGzip := strings.Replace(string(raw), "access_log off;",
		"access_log off;\n    gzip on;\n    gzip_types text/plain;\n    gzip_min_length 1;", 1)
	if err := os.WriteFile(conf, []byte(withGzip), 0o644); err != nil {
		t.Fatal(err)
	}

	// The request line as it arrived, encoding and all.
	echo := func(prefix string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprintf(w, "%spath=%s %s", prefix, r.RequestURI, strings.Repeat("x", 200))
		})
	}
	upstream := httptest.NewServer(echo(""))
	defer upstream.Close()
	// A socket path has to fit in 108 bytes, which a test's own temporary
	// directory does not always leave room for.
	sockets, err := os.MkdirTemp("", "jds")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(sockets) })
	socket := filepath.Join(sockets, "app.sock")
	unixListener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	unixServer := &http.Server{Handler: echo("unix ")}
	go unixServer.Serve(unixListener)
	defer unixServer.Close()

	www := filepath.Join(root, "www")
	for path, content := range map[string]string{
		"assets/app.css":         "css",
		"assets-private/key.pem": "secret",
		"static/x.txt":           "static",
	} {
		full := filepath.Join(www, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	port := freePort(t)

	routes := plainSpec("routes", "routes.test")
	routes.Gzip = false
	routes.Upstream = upstream.URL + "/app/"
	routes.Locations = []SiteLocation{
		{Path: "/api/", Upstream: upstream.URL + "/"},
		{Path: "/assets", Root: filepath.Join(www, "assets")},
		{Path: "/static", Root: www, RootMode: "root"},
		{Path: "/sock/", Upstream: "unix:" + socket + ":/"},
	}
	zipped := plainSpec("zipped", "zipped.test")
	zipped.Upstream = upstream.URL
	// The address as it is usually pasted, with a slash, on / and on a path
	// of the same name.
	slash := plainSpec("slash", "slash.test")
	slash.Upstream = upstream.URL + "/"
	slash.Locations = []SiteLocation{{Path: "/pkg/", Upstream: upstream.URL + "/pkg/"}}
	installSite(t, root, routes, port)
	installSite(t, root, zipped, port)
	installSite(t, root, slash, port)
	startNginx(t, root)
	get := func(host, path string, header ...string) (*http.Response, string) {
		t.Helper()
		return siteGet(t, port, host, path, header...)
	}

	for _, tc := range []struct{ host, path, want string }{
		{"routes.test", "/page", "path=/app/page "},
		{"routes.test", "/api/users?x=1", "path=/users?x=1 "},
		{"routes.test", "/assets/app.css", "css"},
		// A neighbour sharing the folder's first letters is not served from
		// disk; it falls through to the site's application.
		{"routes.test", "/assets-private/key.pem", "path=/app/assets-private/key.pem "},
		{"routes.test", "/static/x.txt", "static"},
		{"routes.test", "/sock/hello", "unix path=/hello "},
		// A path on the upstream gets the request's path decoded, which is
		// what SpecWarnings says; one that is the location's own is left
		// off, and the request goes through exactly as it was sent.
		{"routes.test", "/pkg/%40scope%2Fname", "path=/app/pkg/@scope/name "},
		{"slash.test", "/v1/%40scope%2Fname?x=%2F", "path=/v1/%40scope%2Fname?x=%2F "},
		{"slash.test", "/pkg/%40scope%2Fname", "path=/pkg/%40scope%2Fname "},
	} {
		if _, body := get(tc.host, tc.path); !strings.HasPrefix(body, tc.want) {
			t.Errorf("%s%s answered %.60q, want %q", tc.host, tc.path, body, tc.want)
		}
	}
	if !containsSubstring(SpecWarnings(routes), "/a%2Fb reaches the application as /app/a/b") {
		t.Errorf("the decoding is not among the warnings: %v", SpecWarnings(routes))
	}
	for _, warning := range SpecWarnings(slash) {
		if strings.Contains(warning, "%2F") {
			t.Errorf("a path that is left off is warned about: %s", warning)
		}
	}

	if response, _ := get("routes.test", "/page", "Accept-Encoding", "gzip"); response.Header.Get("Content-Encoding") != "" {
		t.Errorf("compression off still compressed: %v", response.Header)
	}
	if response, _ := get("zipped.test", "/page", "Accept-Encoding", "gzip"); response.Header.Get("Content-Encoding") != "gzip" {
		t.Errorf("compression on did not compress: %v", response.Header)
	}
}

// A site saved disabled, against the host's real nginx running a private
// prefix: tested with its link in place, reported without being refused,
// and not served afterwards even through a reload — the link is gone again
// before nginx is told to reload. Its warnings are placed at their line, a
// name it would contest is reported with which site nginx would answer it
// from, and "enable" then serves it.
func TestLiveDisabledSiteIsTestedAsEnabled(t *testing.T) {
	root := liveNginx(t)
	service := New(root, filepath.Join(root, "Caddyfile"))
	ctx := context.Background()
	port := freePort(t)
	listen := fmt.Sprintf("127.0.0.1:%d", port)
	upstreams := map[string]string{}
	for _, name := range []string{"legacy", "app", "zz"} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, name)
		}))
		t.Cleanup(server.Close)
		upstreams[name] = server.URL
	}
	spec := func(name, domain, custom string) *SiteSpec {
		s := plainSpec(name, domain)
		s.Upstream, s.Custom = upstreams[name], custom
		return s
	}
	save := func(spec *SiteSpec, opts SiteSave) (*SiteResult, string, error) {
		t.Helper()
		content, err := RenderNginx(spec)
		if err != nil {
			t.Fatal(err)
		}
		content = strings.ReplaceAll(content, "    listen 80;\n", "    listen "+listen+";\n")
		content = strings.ReplaceAll(content, "    listen [::]:80;\n", "")
		service.mu.Lock()
		defer service.mu.Unlock()
		res, err := service.saveSiteLocked(ctx, spec, content, opts)
		if errors.Is(err, errSiteReloadFailed) {
			t.Fatalf("nginx did not reload: %v", err)
		}
		return res, content, err
	}
	lineOf := func(content, text string) int {
		for i, line := range strings.Split(content, "\n") {
			if strings.TrimSpace(line) == text {
				return i + 1
			}
		}
		t.Fatalf("%q is not in the rendered file", text)
		return 0
	}
	answers := func(host, want string) {
		t.Helper()
		body := ""
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
			if _, body = siteGet(t, port, host, "/"); body == want {
				return
			}
		}
		t.Fatalf("%s answered %q, want %q", host, body, want)
	}

	// legacy is the first block on the port, so it answers any name no
	// other block claims.
	installSite(t, root, spec("legacy", "legacy.test", ""), port)
	startNginx(t, root)
	answers("app.test", "legacy")

	res, content, err := save(spec("app", "app.test", "frobnicate on;"), SiteSave{Reload: true})
	if err != nil {
		t.Fatalf("a disabled site failing its test enabled was refused: %v", err)
	}
	full := resolvedFile(filepath.Join(root, "sites-available", "app"))
	want := Diagnostic{Level: "emerg", Message: `unknown directive "frobnicate"`, File: full, Line: lineOf(content, "frobnicate on;")}
	if !res.TestedAsEnabled || res.Enabled || res.Validation.Valid || len(res.Validation.Diagnostics) == 0 || res.Validation.Diagnostics[0] != want {
		t.Fatalf("result = %+v, diagnostics %+v, want %+v\n%s", res, res.Validation.Diagnostics, want, res.Validation.Output)
	}
	if isLinked(t, root, "app") || res.Reloaded {
		t.Fatalf("the failing disabled site was left linked or reloaded: %+v", res)
	}
	if v := runValidator(ctx, "nginx", "-t"); !v.Valid {
		t.Fatalf("nginx no longer passes its test after the disabled save:\n%s", v.Output)
	}

	res, content, err = save(spec("app", "app.test", "ssi_types text/html;"), SiteSave{Overwrite: true, Reload: true})
	if err != nil || !res.Validation.Valid || !res.Reloaded || res.Enabled {
		t.Fatalf("a passing disabled save: %+v %v\n%s", res, err, res.Validation.Output)
	}
	wantWarning := []Diagnostic{{Level: "warn", Message: `duplicate MIME type "text/html"`, File: full, Line: lineOf(content, "ssi_types text/html;")}}
	if !reflect.DeepEqual(res.TestWarnings, wantWarning) {
		t.Fatalf("testWarnings = %+v, want %+v", res.TestWarnings, wantWarning)
	}
	// Reloaded, and still not served: the link came out before the reload.
	answers("app.test", "legacy")

	res, _, err = save(spec("app", "app.test", ""), SiteSave{Enable: true, Overwrite: true, Reload: true})
	if err != nil || !res.Enabled || !res.Reloaded {
		t.Fatalf("enabling: %+v %v", res, err)
	}
	answers("app.test", "app")

	// zz sorts after app, so enabled, its claim to app.test would be ignored.
	res, _, err = save(spec("zz", "app.test", ""), SiteSave{Reload: true})
	if err != nil {
		t.Fatalf("a disabled site contesting a name was refused: %v", err)
	}
	wantConflict := []ServerNameConflict{{Domain: "app.test", Listen: listen, Site: "app", Effect: ConflictIgnored}}
	if !reflect.DeepEqual(res.Conflicts, wantConflict) || isLinked(t, root, "zz") {
		t.Fatalf("conflicts = %+v, want %+v", res.Conflicts, wantConflict)
	}
	answers("app.test", "app")
}
