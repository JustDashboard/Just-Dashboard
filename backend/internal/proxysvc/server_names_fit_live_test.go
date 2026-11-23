package proxysvc

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// liveNamesHost is a Debian-layout nginx directory read by the host's real
// nginx, through a wrapper that points every test at its nginx.conf. extra
// goes at the top of its http block.
func liveNamesHost(t *testing.T, extra string) (*Service, *changeLog, string) {
	t.Helper()
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 to exercise real nginx")
	}
	binary, err := exec.LookPath("nginx")
	if err != nil {
		t.Fatal(err)
	}
	service, log, root := recordingService(t)
	if err := os.MkdirAll(filepath.Join(root, "conf.d"), 0o755); err != nil {
		t.Fatal(err)
	}
	user := ""
	if os.Getuid() == 0 {
		user = "user root;\n"
	}
	// nginx builds the server-name hash of an address only when more than
	// one server listens there, the way every site on port 80 does, so the
	// sites below share this one's address.
	conf := fmt.Sprintf("%spid %s/nginx.pid;\nerror_log %s/error.log;\nevents {}\nhttp {\n%s    access_log off;\n"+
		"    server { listen 127.0.0.1:8099 default_server; server_name existing.example.test; return 204; }\n"+
		"    include %s/conf.d/*.conf;\n    include %s/sites-enabled/*;\n}\n",
		user, root, root, extra, root, root)
	config := filepath.Join(root, "nginx.conf")
	if err := os.WriteFile(config, []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nexec \"$JD_TEST_NGINX_BINARY\" -c \"$JD_TEST_NGINX_CONFIG\" \"$@\"\n"
	if err := os.WriteFile(filepath.Join(root, "bin", "nginx"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("JD_TEST_NGINX_BINARY", binary)
	t.Setenv("JD_TEST_NGINX_CONFIG", config)
	return service, log, root
}

func liveSite(t *testing.T, root, site string, names ...string) {
	t.Helper()
	content := "server {\n    listen 127.0.0.1:8099;\n    server_name " + strings.Join(names, " ") + ";\n    return 204;\n}\n"
	if err := os.WriteFile(filepath.Join(root, "sites-enabled", site), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// nginx itself draws the line at 46 characters on a 64-byte bucket, and the
// fit moves it.
func TestLiveServerNamesFitTheBoundary(t *testing.T) {
	service, log, root := liveNamesHost(t, "")
	name46 := strings.Repeat("a", 31) + ".203-0-113-7.io" // 46 characters
	if len(name46) != 46 {
		t.Fatalf("name is %d characters", len(name46))
	}
	liveSite(t, root, "fits", name46)
	if res := service.testNginx(context.Background()); !res.Valid {
		t.Fatalf("46 characters refused: %s", res.Output)
	}
	if _, err := os.Stat(namesFile(root)); !os.IsNotExist(err) {
		t.Fatal("managed file written for a name that fits")
	}
	if got := runValidator(context.Background(), "nginx", "-t"); !got.Valid {
		t.Fatalf("plain test: %s", got.Output)
	}
	liveSite(t, root, "long", "b"+name46)
	if res := runValidator(context.Background(), "nginx", "-t"); res.Valid || !strings.Contains(res.Output, "server_names_hash_bucket_size: 64") {
		t.Skipf("this nginx takes 47 characters at its default bucket (%s); the boundary is the cache line's", res.Output)
	}
	res := service.testNginx(context.Background())
	if !res.Valid {
		t.Fatalf("47 characters still refused: %s", res.Output)
	}
	raw, _ := os.ReadFile(namesFile(root))
	if !strings.Contains(string(raw), "server_names_hash_bucket_size 128;") || len(log.changes) != 1 {
		t.Fatalf("managed file:\n%s\n%d changes", raw, len(log.changes))
	}
}

// A 63-character label under a long base, and a name long enough to need 256.
func TestLiveServerNamesFitLongLabels(t *testing.T) {
	service, _, root := liveNamesHost(t, "")
	label := strings.Repeat("l", 63)
	liveSite(t, root, "label", label+".203-0-113-7.sslip.io")
	if res := service.testNginx(context.Background()); !res.Valid {
		t.Fatalf("refused: %s", res.Output)
	}
	if raw, _ := os.ReadFile(namesFile(root)); !strings.Contains(string(raw), "server_names_hash_bucket_size 128;") {
		t.Fatalf("managed file:\n%s", raw)
	}
	liveSite(t, root, "deep", label+"."+label+".shop.example.com") // 144 characters
	if res := service.testNginx(context.Background()); !res.Valid {
		t.Fatalf("refused: %s", res.Output)
	}
	if raw, _ := os.ReadFile(namesFile(root)); !strings.Contains(string(raw), "server_names_hash_bucket_size 256;") {
		t.Fatalf("managed file:\n%s", raw)
	}
}

// Many long names on one address. nginx since 1.5 answers a hash it cannot
// build within server_names_hash_max_size with a warning, "could not build
// optimal server_names_hash", and passes; the emerg of that form is older
// nginx's. The bucket the longest name needs is still written.
func TestLiveServerNamesFitManyNames(t *testing.T) {
	service, _, root := liveNamesHost(t, "")
	var names []string
	for i := range 400 {
		names = append(names, fmt.Sprintf("app-%03d-%s.203-0-113-7.sslip.io", i, strings.Repeat("n", 30)))
	}
	liveSite(t, root, "many", names...)
	res := service.testNginx(context.Background())
	if !res.Valid {
		t.Fatalf("refused: %s", res.Output)
	}
	t.Logf("nginx said: %s", res.Output)
	if raw, _ := os.ReadFile(namesFile(root)); !strings.Contains(string(raw), "server_names_hash_bucket_size 128;") {
		t.Fatalf("managed file:\n%s", raw)
	}
}

func TestLiveServerNamesFitLeavesTheOperatorsValue(t *testing.T) {
	service, log, root := liveNamesHost(t, "    server_names_hash_bucket_size 64;\n")
	liveSite(t, root, "long", longName)
	res := service.testNginx(context.Background())
	if res.Valid {
		t.Fatal("passed over the operator's bucket")
	}
	want := "set it to 128 in " + filepath.Join(root, "nginx.conf") + ":5"
	if headline := FailureHeadline(res); !strings.HasSuffix(headline, want) {
		t.Fatalf("headline = %q, want it to end %q", headline, want)
	}
	if _, err := os.Stat(namesFile(root)); !os.IsNotExist(err) || len(log.changes) != 0 {
		t.Fatalf("wrote beside the operator's directive: %v, %d changes", err, len(log.changes))
	}
}

// A site saved disabled is tested as enabled; the raise that test needs is
// made when it is enabled, not now. Enabling it makes it. The file is written
// as it stands rather than from a form, because `nginx -t` binds the ports a
// site listens on and the form's site listens on 80.
func TestLiveServerNamesFitADisabledSiteOnlyWhenEnabled(t *testing.T) {
	service, _, root := liveNamesHost(t, "")
	content := managedMarker + "\nserver {\n    listen 127.0.0.1:8099;\n    server_name " + longName + ";\n    return 204;\n}\n"
	service.mu.Lock()
	res, err := service.saveSiteLocked(context.Background(), &SiteSpec{Name: "store"}, content, SiteSave{})
	service.mu.Unlock()
	if err != nil || !res.Validation.Valid || !res.TestedAsEnabled {
		t.Fatalf("save: %v %+v", err, res.Validation)
	}
	if !strings.Contains(res.Validation.Note, "when the change is made") {
		t.Fatalf("note = %q", res.Validation.Note)
	}
	if _, err := os.Stat(namesFile(root)); !os.IsNotExist(err) {
		t.Fatal("a disabled save made the raise")
	}
	if err := service.SetVHostEnabled(context.Background(), "store", true); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if raw, _ := os.ReadFile(namesFile(root)); !strings.Contains(string(raw), "server_names_hash_bucket_size 128;") {
		t.Fatalf("enable did not raise:\n%s", raw)
	}
}

// The first deploy of a long project name: the challenge site for its
// generated sslip.io name is served by a running nginx, and taking it away
// leaves only the raise.
func TestLiveDeploymentWebrootServesALongName(t *testing.T) {
	service, _, root := liveNamesHost(t, "")
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	config := filepath.Join(root, "nginx.conf")
	raw, _ := os.ReadFile(config)
	conf := strings.Replace(string(raw), "127.0.0.1:8099 default_server; server_name existing.example.test; return 204;",
		fmt.Sprintf("127.0.0.1:%d default_server; server_name existing.example.test; return 200 existing;", port), 1)
	if err := os.WriteFile(config, []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}
	binary := os.Getenv("JD_TEST_NGINX_BINARY")
	var output bytes.Buffer
	command := exec.Command(binary, "-c", config, "-g", "daemon off;")
	command.Stdout, command.Stderr = &output, &output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = command.Process.Signal(syscall.SIGTERM)
		_ = command.Wait()
		if t.Failed() {
			t.Log(output.String())
		}
	})
	webroot := filepath.Join(root, "webroot")
	listeners := []Listener{{Address: "127.0.0.1", Port: uint32(port), Process: "nginx", Protocol: "tcp"}}
	probe := func(ctx context.Context, domains []string) error {
		return probeDeploymentWebroot(ctx, domains, webroot, listeners)
	}
	client := &http.Client{Timeout: time.Second}
	up := func(ctx context.Context) error {
		request, _ := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("http://127.0.0.1:%d", port), nil)
		request.Host = "existing.example.test"
		response, err := client.Do(request)
		if err != nil {
			return err
		}
		response.Body.Close()
		return nil
	}
	if err := waitDeploymentWebroot(context.Background(), up); err != nil {
		t.Fatal(err)
	}
	names := []string{longName}
	cleanup, err := service.prepareDeploymentWebroot(context.Background(), names, webroot, listeners, probe)
	if err != nil {
		t.Fatal(err)
	}
	// An old worker can still take a connection for a moment after the
	// reload, as prepareDeploymentWebroot's own wait allows for.
	if err := waitDeploymentWebroot(context.Background(), func(ctx context.Context) error { return probe(ctx, names) }); err != nil {
		t.Fatal(err)
	}
	if err := cleanup(); err != nil {
		t.Fatal(err)
	}
	files, _ := os.ReadDir(filepath.Join(root, "conf.d"))
	if len(files) != 1 || files[0].Name() != serverNamesFile {
		t.Fatalf("conf.d after the challenge: %v", files)
	}
	for _, dir := range []string{"sites-available", "sites-enabled"} {
		if entries, _ := os.ReadDir(filepath.Join(root, dir)); len(entries) != 0 {
			t.Fatalf("challenge left %v in %s", entries, dir)
		}
	}
}
