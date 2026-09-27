package proxysvc

import (
	"bytes"
	"context"
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

	echo := func(prefix string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprintf(w, "%spath=%s %s", prefix, r.URL.RequestURI(), strings.Repeat("x", 200))
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
	installSite(t, root, routes, port)
	installSite(t, root, zipped, port)

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

	client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{DisableCompression: true}}
	get := func(host, path string, header ...string) (*http.Response, string) {
		t.Helper()
		request, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:"+strconv.Itoa(port)+path, nil)
		request.Host = host
		for i := 0; i+1 < len(header); i += 2 {
			request.Header.Set(header[i], header[i+1])
		}
		var response *http.Response
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

	for _, tc := range []struct{ path, want string }{
		{"/page", "path=/app/page "},
		{"/api/users?x=1", "path=/users?x=1 "},
		{"/assets/app.css", "css"},
		// A neighbour sharing the folder's first letters is not served from
		// disk; it falls through to the site's application.
		{"/assets-private/key.pem", "path=/app/assets-private/key.pem "},
		{"/static/x.txt", "static"},
		{"/sock/hello", "unix path=/hello "},
	} {
		if _, body := get("routes.test", tc.path); !strings.HasPrefix(body, tc.want) {
			t.Errorf("%s answered %.60q, want %q", tc.path, body, tc.want)
		}
	}

	if response, _ := get("routes.test", "/page", "Accept-Encoding", "gzip"); response.Header.Get("Content-Encoding") != "" {
		t.Errorf("compression off still compressed: %v", response.Header)
	}
	if response, _ := get("zipped.test", "/page", "Accept-Encoding", "gzip"); response.Header.Get("Content-Encoding") != "gzip" {
		t.Errorf("compression on did not compress: %v", response.Header)
	}
}
