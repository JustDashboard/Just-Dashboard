package proxysvc

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// servedPair is a certificate as a TLS server holds it, from issue's PEM.
func servedPair(t *testing.T, certText, keyText string) tls.Certificate {
	t.Helper()
	pair, err := tls.X509KeyPair([]byte(certText), []byte(keyText))
	if err != nil {
		t.Fatal(err)
	}
	return pair
}

// tlsSite answers on a loopback port with whatever pick returns for each
// handshake, as nginx does between reloads, and returns the port. A site
// behind proxy_protocol refuses a connection that does not start with a
// PROXY header, as nginx does.
func tlsSite(t *testing.T, proxyProtocol bool, pick func() tls.Certificate) int {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	config := &tls.Config{GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
		pair := pick()
		return &pair, nil
	}}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(5 * time.Second))
				var raw net.Conn = conn
				if proxyProtocol {
					reader := bufio.NewReader(conn)
					header, err := reader.ReadString('\n')
					if err != nil || !strings.HasPrefix(header, "PROXY ") {
						return
					}
					raw = bufferedConn{conn, reader}
				}
				tls.Server(raw, config).Handshake()
			}()
		}
	}()
	return listener.Addr().(*net.TCPAddr).Port
}

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c bufferedConn) Read(p []byte) (int, error) { return c.reader.Read(p) }

// writeSite writes an nginx site naming certificate, enabled unless told
// otherwise, in the Debian layout nginxVHosts reads.
func writeSite(t *testing.T, nginxDir, name, listen, serverName, certificate string, enabled bool) {
	t.Helper()
	for _, dir := range []string{"sites-available", "sites-enabled"} {
		if err := os.MkdirAll(filepath.Join(nginxDir, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	content := fmt.Sprintf("server {\n    listen 80;\n    listen %s;\n    server_name %s;\n    ssl_certificate %s;\n    ssl_certificate_key %s;\n}\n",
		listen, serverName, certificate, strings.TrimSuffix(certificate, filepath.Base(certificate))+"privkey.pem")
	available := filepath.Join(nginxDir, "sites-available", name)
	if err := os.WriteFile(available, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if enabled {
		if err := os.Symlink(available, filepath.Join(nginxDir, "sites-enabled", name)); err != nil {
			t.Fatal(err)
		}
	}
}

// closedPort is a loopback port nothing listens on.
func closedPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	return port
}

// A certificate replaced on disk is served only once nginx reads it again,
// and the files cannot say whether it has. Each enabled site that names the
// certificate is asked over a handshake: one still on the test certificate,
// one a reload already reached, one behind proxy_protocol, one whose
// listener is down. A disabled site serves nothing and is not asked.
func TestServedCertificatesAsksEachSiteThatNamesIt(t *testing.T) {
	root := t.TempDir()
	letsencrypt, nginxDir := filepath.Join(root, "letsencrypt"), filepath.Join(root, "nginx")
	t.Cleanup(UseCertificateDirsForTest(letsencrypt, filepath.Join(root, "imported")))
	names := []string{"app.example.com", "www.app.example.com"}
	_, testCert, testKey := newAuthority(t, "(STAGING) Riddling Rhubarb R12", nil).issue(t, names, time.Now().Add(80*24*time.Hour))
	realLeaf, realCert, realKey := newAuthority(t, "R11", nil).issue(t, names, time.Now().Add(89*24*time.Hour))
	writeLineage(t, letsencrypt, "app.example.com", "https://acme-v02.api.letsencrypt.org/directory", realLeaf)
	certificate := filepath.Join(letsencrypt, "live", "app.example.com", "fullchain.pem")
	test, real := servedPair(t, testCert, testKey), servedPair(t, realCert, realKey)

	stale := tlsSite(t, false, func() tls.Certificate { return test })
	fresh := tlsSite(t, false, func() tls.Certificate { return real })
	proxied := tlsSite(t, true, func() tls.Certificate { return real })
	writeSite(t, nginxDir, "stale", fmt.Sprintf("127.0.0.1:%d ssl", stale), "app.example.com", certificate, true)
	writeSite(t, nginxDir, "fresh", fmt.Sprintf("127.0.0.1:%d ssl http2", fresh), "www.app.example.com app.example.com", certificate, true)
	writeSite(t, nginxDir, "proxied", fmt.Sprintf("127.0.0.1:%d ssl proxy_protocol", proxied), "app.example.com", certificate, true)
	writeSite(t, nginxDir, "down", fmt.Sprintf("127.0.0.1:%d ssl", closedPort(t)), "app.example.com", certificate, true)
	writeSite(t, nginxDir, "off", fmt.Sprintf("127.0.0.1:%d ssl", stale), "app.example.com", certificate, false)

	service := New(nginxDir, "")
	served, err := service.ServedCertificates(context.Background(), certificate, false)
	if err != nil {
		t.Fatal(err)
	}
	bySite := map[string]ServedCertificate{}
	for _, site := range served {
		bySite[site.Site] = site
	}
	if len(served) != 4 {
		t.Fatalf("asked %d sites, want the 4 enabled ones: %+v", len(served), served)
	}
	if got := bySite["stale"]; got.Current || !got.Staging || got.Error != "" || got.Issuer != "(STAGING) Riddling Rhubarb R12" || got.Name != "app.example.com" {
		t.Errorf("stale = %+v", got)
	}
	if got := bySite["fresh"]; !got.Current || got.Staging || got.Error != "" || got.Name != "www.app.example.com" || got.Serial == "" {
		t.Errorf("fresh = %+v", got)
	}
	if got := bySite["proxied"]; !got.Current || got.Error != "" {
		t.Errorf("proxied = %+v", got)
	}
	if got := bySite["down"]; got.Current || got.Error == "" || !strings.Contains(got.Error, "refused") {
		t.Errorf("down = %+v", got)
	}

	byLineage, err := service.ServedLineage(context.Background(), []string{"WWW.app.example.com", "app.example.com"}, false)
	if err != nil || len(byLineage) != 4 {
		t.Fatalf("lineage = %+v, %v", byLineage, err)
	}
	if _, err := service.ServedLineage(context.Background(), []string{"app.example.com"}, false); err == nil {
		t.Fatal("a lineage that is not exactly these names was checked")
	}
	if _, err := service.ServedCertificates(context.Background(), "/etc/passwd", false); !errors.Is(err, ErrCertificateNotListed) {
		t.Fatalf("an unlisted path = %v", err)
	}
}

// `nginx -s reload` returns once the signal is sent, and the old workers
// answer until the master has swapped them. Settling asks again while a site
// serves anything but the file, and gives up after a few tries.
func TestServedCertificatesWaitsForAReloadInFlight(t *testing.T) {
	previous := servedRecheckInterval
	servedRecheckInterval = 10 * time.Millisecond
	t.Cleanup(func() { servedRecheckInterval = previous })
	root := t.TempDir()
	letsencrypt, nginxDir := filepath.Join(root, "letsencrypt"), filepath.Join(root, "nginx")
	t.Cleanup(UseCertificateDirsForTest(letsencrypt, filepath.Join(root, "imported")))
	names := []string{"app.example.com"}
	_, testCert, testKey := newAuthority(t, "(STAGING) Riddling Rhubarb R12", nil).issue(t, names, time.Now().Add(80*24*time.Hour))
	realLeaf, realCert, realKey := newAuthority(t, "R11", nil).issue(t, names, time.Now().Add(89*24*time.Hour))
	writeLineage(t, letsencrypt, "app.example.com", "https://acme-v02.api.letsencrypt.org/directory", realLeaf)
	certificate := filepath.Join(letsencrypt, "live", "app.example.com", "fullchain.pem")
	test, real := servedPair(t, testCert, testKey), servedPair(t, realCert, realKey)

	var reloading, stuck atomic.Int32
	reloaded := tlsSite(t, false, func() tls.Certificate {
		if reloading.Add(1) <= 2 {
			return test
		}
		return real
	})
	never := tlsSite(t, false, func() tls.Certificate {
		stuck.Add(1)
		return test
	})
	writeSite(t, nginxDir, "reloaded", fmt.Sprintf("127.0.0.1:%d ssl", reloaded), "app.example.com", certificate, true)
	service := New(nginxDir, "")

	once, err := service.ServedCertificates(context.Background(), certificate, false)
	if err != nil || len(once) != 1 || once[0].Current {
		t.Fatalf("one look = %+v, %v", once, err)
	}
	settled, err := service.ServedCertificates(context.Background(), certificate, true)
	if err != nil || len(settled) != 1 || !settled[0].Current {
		t.Fatalf("settled = %+v, %v", settled, err)
	}

	writeSite(t, nginxDir, "never", fmt.Sprintf("127.0.0.1:%d ssl", never), "app.example.com", certificate, true)
	os.Remove(filepath.Join(nginxDir, "sites-enabled", "reloaded"))
	stale, err := service.ServedCertificates(context.Background(), certificate, true)
	if err != nil || len(stale) != 1 || stale[0].Current || !stale[0].Staging {
		t.Fatalf("never reloaded = %+v, %v", stale, err)
	}
	if got := stuck.Load(); got != servedRechecks+1 {
		t.Fatalf("asked %d times, want %d", got, servedRechecks+1)
	}
}

// A site is asked where nginx listens for it: a wildcard or bare port on
// loopback, a named address only when it is this host's, and only a
// listener with ssl.
func TestTLSListenReachesTheSiteFromThisHost(t *testing.T) {
	for _, c := range []struct {
		listen  []string
		network string
		address string
		proxy   bool
		err     string
	}{
		{[]string{"80", "[::]:80", "443 ssl"}, "tcp", "127.0.0.1:443", false, ""},
		{[]string{"[::]:443 ssl http2"}, "tcp", "[::1]:443", false, ""},
		{[]string{"0.0.0.0:8443 ssl default_server"}, "tcp", "127.0.0.1:8443", false, ""},
		{[]string{"*:443 ssl"}, "tcp", "127.0.0.1:443", false, ""},
		{[]string{"localhost:8443 ssl"}, "tcp", "127.0.0.1:8443", false, ""},
		{[]string{"127.0.0.2:443 ssl"}, "tcp", "127.0.0.2:443", false, ""},
		{[]string{"127.0.0.1 ssl"}, "tcp", "127.0.0.1:80", false, ""},
		{[]string{"443 ssl proxy_protocol"}, "tcp", "127.0.0.1:443", true, ""},
		{[]string{"unix:/run/nginx-tls.sock ssl"}, "unix", "/run/nginx-tls.sock", false, ""},
		{[]string{"443 quic reuseport", "443 ssl"}, "tcp", "127.0.0.1:443", false, ""},
		{[]string{"80", "443"}, "", "", false, "no listen directive with ssl"},
		{[]string{"203.0.113.9:443 ssl"}, "", "", false, "not an address of this host"},
		{[]string{"app.example.com:443 ssl"}, "", "", false, "a name rather than an address"},
	} {
		network, address, proxy, err := tlsListen(c.listen)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%q: err = %v, want %q", c.listen, err, c.err)
			}
			continue
		}
		if err != nil || network != c.network || address != c.address || proxy != c.proxy {
			t.Errorf("%q = %s %s %v %v, want %s %s %v", c.listen, network, address, proxy, err, c.network, c.address, c.proxy)
		}
	}
}

// The name asked for is one the site answers and the certificate covers, so
// the handshake reaches this site's server block and not another's.
func TestServerNameForAsksANameTheCertificateCovers(t *testing.T) {
	for _, c := range []struct {
		serverNames, domains []string
		want                 string
	}{
		{[]string{"www.app.example.com", "app.example.com"}, []string{"app.example.com", "www.app.example.com"}, "www.app.example.com"},
		{[]string{"_", "~^(?<sub>.+)\\.example\\.com$", "App.Example.com"}, []string{"app.example.com"}, "app.example.com"},
		{[]string{"other.example.com"}, []string{"app.example.com"}, "app.example.com"},
		{[]string{"api.example.com"}, []string{"*.example.com"}, "api.example.com"},
		{[]string{".example.com"}, []string{"example.com", "*.example.com"}, "example.com"},
		{[]string{"*.example.com"}, []string{"*.example.com"}, "jd-check.example.com"},
		{nil, nil, ""},
	} {
		if got := serverNameFor(c.serverNames, c.domains); got != c.want {
			t.Errorf("serverNameFor(%q, %q) = %q, want %q", c.serverNames, c.domains, got, c.want)
		}
	}
}

// The job's closing line says what each site answered, and only that.
func TestReplacementServedSaysWhatEachSiteServes(t *testing.T) {
	for _, c := range []struct {
		served []ServedCertificate
		want   string
	}{
		{nil, "The real certificate replaced the test one on disk. No enabled nginx site names it."},
		{[]ServedCertificate{{Site: "app", Current: true}, {Site: "www", Current: true}},
			"The real certificate replaced the test one on disk. nginx already serves it for app, www."},
		{[]ServedCertificate{{Site: "app", Staging: true}},
			"The real certificate replaced the test one on disk. app still serves the test one until nginx reloads."},
		{[]ServedCertificate{{Site: "app", Staging: true}, {Site: "www", Staging: true}, {Site: "api", Current: true}},
			"The real certificate replaced the test one on disk. nginx already serves it for api. app, www still serve the test one until nginx reloads."},
		{[]ServedCertificate{{Site: "app", Name: "app.example.com", Issuer: "Company CA"}, {Site: "www", Error: "dial tcp 127.0.0.1:443: connect: connection refused"}},
			"The real certificate replaced the test one on disk. app answers app.example.com with another certificate, from Company CA. What www serves could not be checked: dial tcp 127.0.0.1:443: connect: connection refused."},
	} {
		if got := ReplacementServed(c.served); got != c.want {
			t.Errorf("ReplacementServed(%+v) =\n%s\nwant\n%s", c.served, got, c.want)
		}
	}
}

// The host's real nginx, on a private prefix and a loopback port: it keeps
// serving the certificate it loaded after the file is replaced, and serves
// the new one once reloaded — which the check reads right after the signal.
func TestLiveNginxServesAReplacedCertificateOnlyOnceReloaded(t *testing.T) {
	binary, err := exec.LookPath("nginx")
	if err != nil {
		t.Skip("nginx is not installed")
	}
	root := t.TempDir()
	t.Cleanup(UseCertificateDirsForTest(filepath.Join(root, "letsencrypt"), filepath.Join(root, "imported")))
	names := []string{"app.example.test"}
	_, testCert, testKey := newAuthority(t, "(STAGING) Riddling Rhubarb R12", nil).issue(t, names, time.Now().Add(80*24*time.Hour))
	_, realCert, realKey := newAuthority(t, "R11", nil).issue(t, names, time.Now().Add(89*24*time.Hour))
	ssl := filepath.Join(root, "ssl")
	if err := os.MkdirAll(ssl, 0o755); err != nil {
		t.Fatal(err)
	}
	certificate := filepath.Join(ssl, "fullchain.pem")
	install := func(certText, keyText string) {
		t.Helper()
		if err := os.WriteFile(certificate, []byte(certText), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(ssl, "privkey.pem"), []byte(keyText), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	install(testCert, testKey)
	port := closedPort(t)
	writeSite(t, root, "app", fmt.Sprintf("127.0.0.1:%d ssl", port), "app.example.test", certificate, true)
	// The site's plain `listen 80` is the host's port; this nginx serves
	// only the TLS listener, from the same file the Service reads.
	site, _ := os.ReadFile(filepath.Join(root, "sites-available", "app"))
	served := strings.Replace(string(site), "    listen 80;\n", "", 1)
	config := filepath.Join(root, "nginx.conf")
	user := ""
	if os.Getuid() == 0 {
		user = "user root;\n"
	}
	if err := os.WriteFile(config, []byte(fmt.Sprintf(`%[1]spid %[2]s/nginx.pid;
error_log %[2]s/error.log;
events {}
http {
    access_log off;
    client_body_temp_path %[2]s/tmp-body;
    proxy_temp_path %[2]s/tmp-proxy;
    fastcgi_temp_path %[2]s/tmp-fastcgi;
    uwsgi_temp_path %[2]s/tmp-uwsgi;
    scgi_temp_path %[2]s/tmp-scgi;
    %[3]s
}
`, user, root, served)), 0o644); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	var outputMu sync.Mutex
	command := exec.Command(binary, "-e", filepath.Join(root, "startup.log"), "-c", config, "-g", "daemon off;")
	command.Stdout, command.Stderr = lockedWriter{&outputMu, &output}, lockedWriter{&outputMu, &output}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = command.Process.Signal(syscall.SIGTERM)
		_ = command.Wait()
		if t.Failed() {
			outputMu.Lock()
			t.Log(output.String())
			outputMu.Unlock()
		}
	})
	deadline := time.Now().Add(10 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp4", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond)
		if err == nil {
			conn.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("nginx did not listen: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}

	service := New(root, "")
	ask := func(settle bool) ServedCertificate {
		t.Helper()
		served, err := service.ServedCertificates(context.Background(), certificate, settle)
		if err != nil || len(served) != 1 {
			t.Fatalf("served = %+v, %v", served, err)
		}
		return served[0]
	}
	if got := ask(false); !got.Current || !got.Staging {
		t.Fatalf("before the replacement: %+v", got)
	}
	install(realCert, realKey)
	if got := ask(false); got.Current || !got.Staging {
		t.Fatalf("replaced on disk, not reloaded: %+v", got)
	}
	reload := exec.Command(binary, "-e", filepath.Join(root, "startup.log"), "-c", config, "-s", "reload")
	if out, err := reload.CombinedOutput(); err != nil {
		t.Fatalf("reload: %v: %s", err, out)
	}
	if got := ask(true); !got.Current || got.Staging {
		t.Fatalf("after the reload: %+v", got)
	}
}

type lockedWriter struct {
	mu     *sync.Mutex
	buffer *bytes.Buffer
}

func (w lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buffer.Write(p)
}
