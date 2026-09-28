package proxysvc

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A real nginx with ssl_protocols TLSv1.2 TLSv1.3 — the configuration the
// site form writes and most guides recommend — refuses TLS 1.0 and 1.1 with
// OpenSSL's protocol_version alert. The report read that alert's words as this
// client's own error and showed both as "unknown", so no correctly configured
// server could ever be seen refusing them.
func TestLiveNginxRefusalsAreReportedAsRefused(t *testing.T) {
	cert, _ := scanTestCert(t, 7, nil)
	addr := startLiveNginx(t, cert, "ssl_protocols TLSv1.2 TLSv1.3;")

	got := map[string]ProtocolResult{}
	for _, p := range probeProtocols(context.Background(), addr, "scan.test") {
		got[p.Name] = p
	}
	for name, want := range map[string]string{
		"TLS 1.0": "refused", "TLS 1.1": "refused", "TLS 1.2": "offered", "TLS 1.3": "offered",
	} {
		if got[name].Status != want {
			t.Errorf("%s = %+v, want %s", name, got[name], want)
		}
	}
	if !strings.HasPrefix(got["TLS 1.0"].Detail, "The server answered: ") {
		t.Errorf("the refusal should quote nginx's alert: %q", got["TLS 1.0"].Detail)
	}
}

// An nginx still taking TLS 1.0 and 1.1, with only RSA key exchange for them,
// answered the probe's default offer with handshake_failure, and the report
// said both were refused — grade A for a server offering retired versions.
func TestLiveNginxLegacyVersionsWithOnlyRSAKeyExchangeAreOffered(t *testing.T) {
	// SECLEVEL=0 because OpenSSL 3 refuses TLS 1.0 and 1.1 at any higher
	// level whatever ssl_protocols says.
	addr := startLiveNginx(t, rsaTestCert(t),
		"ssl_protocols TLSv1 TLSv1.1 TLSv1.2;\n        ssl_ciphers ECDHE-RSA-AES128-GCM-SHA256:AES128-SHA:@SECLEVEL=0;")

	protocols := probeProtocols(context.Background(), addr, "scan.test")
	for name, want := range map[string]string{
		"TLS 1.0": "offered", "TLS 1.1": "offered", "TLS 1.2": "offered", "TLS 1.3": "refused",
	} {
		if got := protocolStatus(protocols, name); got != want {
			t.Errorf("%s = %s, want %s: %+v", name, got, want, protocols)
		}
	}
}

// An nginx taking TLS 1.2 only with RSA key exchange was reported as nothing
// answering. It is reachable, legacy-only, and its HTTPS answer is read.
func TestLiveNginxWithOnlyRSAKeyExchangeIsReachable(t *testing.T) {
	addr := startLiveNginx(t, rsaTestCert(t), "ssl_protocols TLSv1.2;\n        ssl_ciphers AES128-GCM-SHA256;")

	conn, legacy, err := handshake(context.Background(), addr, "scan.test")
	if err != nil {
		t.Fatal(err)
	}
	state := conn.ConnectionState()
	conn.Close()
	if !legacy || tls.CipherSuiteName(state.CipherSuite) != "TLS_RSA_WITH_AES_128_GCM_SHA256" {
		t.Fatalf("legacy=%v with %s", legacy, tls.CipherSuiteName(state.CipherSuite))
	}
	http := scanHTTP(context.Background(), "https://"+addr+"/", "http://127.0.0.1:1/", tlsOffer("", oldestVersion, newestVersion))
	if http.HTTPSError != "" || http.StatusCode != 200 {
		t.Fatalf("got %+v", http)
	}
}

// nginx's ssl_reject_handshake answers with an alert: a refusal, not silence.
func TestLiveNginxRejectedHandshakeIsARefusal(t *testing.T) {
	cert, _ := scanTestCert(t, 10, nil)
	addr := startLiveNginx(t, cert, "ssl_reject_handshake on;")
	host, port := splitAddr(t, addr)

	scan := ScanTLS(context.Background(), host, port)
	if scan.Reachable || !hasFinding(scan, "tls.refused") || !strings.Contains(scan.Findings[0].Detail, "unrecognized name") {
		t.Fatalf("got %q %+v", scan.Summary, scan.Findings)
	}
}

// startLiveNginx runs a private nginx serving cert on a loopback port, with
// directives in its server block and every path inside the test's directory,
// and returns its address. It skips when nginx is not installed.
func startLiveNginx(t *testing.T, cert tls.Certificate, directives string) string {
	t.Helper()
	return scanNginx(t, cert, 1, func(ports []int, certPath, keyPath string) string {
		return fmt.Sprintf(`    server {
        listen 127.0.0.1:%d ssl;
        server_name scan.test;
        ssl_certificate %s;
        ssl_certificate_key %s;
        %s
        return 200 "ok";
    }`, ports[0], certPath, keyPath, directives)
	})[0]
}

// scanNginx runs a private nginx whose http block holds the servers written
// for count free loopback ports and cert's files, and returns an address per
// port. It skips when nginx is not installed.
func scanNginx(t *testing.T, cert tls.Certificate, count int, servers func(ports []int, certPath, keyPath string) string) []string {
	t.Helper()
	binary, err := exec.LookPath("nginx")
	if err != nil {
		t.Skip("nginx is not installed")
	}
	root := t.TempDir()
	key, err := x509.MarshalPKCS8PrivateKey(cert.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	certPath, keyPath := filepath.Join(root, "cert.pem"), filepath.Join(root, "key.pem")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}), 0o600); err != nil {
		t.Fatal(err)
	}
	// Each port is held until nginx is about to take it, so no two of them
	// come out the same.
	ports := make([]int, count)
	held := make([]net.Listener, count)
	for i := range ports {
		listener, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		held[i], ports[i] = listener, listener.Addr().(*net.TCPAddr).Port
	}

	user := ""
	if os.Getuid() == 0 {
		user = "user root;\n"
	}
	temp := []string{}
	for _, name := range []string{"client_body", "proxy", "fastcgi", "uwsgi", "scgi"} {
		temp = append(temp, fmt.Sprintf("    %s_temp_path %s/%s;", name, root, name))
	}
	config := filepath.Join(root, "nginx.conf")
	content := fmt.Sprintf(`%spid %s/nginx.pid;
error_log %s/error.log;
events {}
http {
    access_log off;
%s
%s
}
`, user, root, root, strings.Join(temp, "\n"), servers(ports, certPath, keyPath))
	if err := os.WriteFile(config, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	command := exec.Command(binary, "-p", root, "-c", config, "-g", "daemon off;")
	command.Stdout, command.Stderr = &output, &output
	for _, listener := range held {
		listener.Close()
	}
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
	addrs := make([]string, count)
	for i, port := range ports {
		addrs[i] = "127.0.0.1:" + strconv.Itoa(port)
		deadline := time.Now().Add(10 * time.Second)
		for {
			conn, err := net.DialTimeout("tcp", addrs[i], time.Second)
			if err == nil {
				conn.Close()
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("nginx did not start: %v", err)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	return addrs
}

// nginx's `listen N;` without ssl — the commonest way to break HTTPS in a
// config — answers the ClientHello with an HTTP 400, and the report said
// nothing answered and to check DNS.
func TestLiveNginxListenWithoutSSLIsDiagnosed(t *testing.T) {
	cert, _ := scanTestCert(t, 14, nil)
	addr := scanNginx(t, cert, 1, func(ports []int, _, _ string) string {
		return fmt.Sprintf("    server {\n        listen 127.0.0.1:%d;\n        return 200 \"ok\";\n    }", ports[0])
	})[0]
	host, port := splitAddr(t, addr)
	scan := ScanTLS(context.Background(), host, port)
	f := scan.Failure
	if scan.Grade != "F" || f == nil || f.Stage != "handshake" || f.Reason != "plain-http" || f.Answer != "HTTP/" || f.Where != "here" {
		t.Fatalf("got %q %+v", scan.Summary, f)
	}
	if !hasFinding(scan, "tls.plain-http") || !strings.Contains(scan.Findings[0].Advice, fmt.Sprintf("listen %d ssl;", port)) {
		t.Errorf("findings %+v", scan.Findings)
	}
}

// A real nginx configured the way the preload list asks: port 80 sends
// visitors straight to HTTPS on the same host, and HTTPS answers with a
// year's max-age, includeSubDomains and preload. Its answers meet every rule
// the scan can see, and the six-month max-age the site form writes does not.
func TestLiveNginxPreloadRules(t *testing.T) {
	cert, _ := scanTestCert(t, 15, nil)
	addrs := scanNginx(t, cert, 3, func(ports []int, certPath, keyPath string) string {
		return fmt.Sprintf(`    server {
        listen 127.0.0.1:%d;
        return 301 https://$host$request_uri;
    }
    server {
        listen 127.0.0.1:%d ssl;
        ssl_certificate %s;
        ssl_certificate_key %s;
        add_header Strict-Transport-Security "max-age=31536000; includeSubDomains; preload" always;
        return 200 "ok";
    }
    server {
        listen 127.0.0.1:%d ssl;
        ssl_certificate %s;
        ssl_certificate_key %s;
        add_header Strict-Transport-Security "max-age=15552000; includeSubDomains" always;
        return 200 "ok";
    }`, ports[0], ports[1], certPath, keyPath, ports[2], certPath, keyPath)
	})
	plain, preloaded, sixMonths := addrs[0], addrs[1], addrs[2]
	www := &PreloadRule{ID: "www", Passed: true}

	ready := scanHTTP(context.Background(), "https://"+preloaded+"/", "http://"+plain+"/", tlsOffer("", 0, 0))
	if ready.Service != "http" || ready.RedirectVerdict != "same-host" || ready.HSTS == nil {
		t.Fatalf("got %+v", ready)
	}
	scan := goodScan()
	scan.HTTP = ready
	if check := preloadCheck("example.com", scan, www); !check.Eligible {
		t.Fatalf("every rule should pass: %+v", check)
	}

	short := scanHTTP(context.Background(), "https://"+sixMonths+"/", "http://"+plain+"/", tlsOffer("", 0, 0))
	scan = goodScan()
	scan.HTTP = short
	check := preloadCheck("example.com", scan, www)
	failed := []string{}
	for _, rule := range check.Rules {
		if !rule.Passed {
			failed = append(failed, rule.ID)
		}
	}
	if check.Eligible || strings.Join(failed, " ") != "max-age preload" {
		t.Fatalf("failed %v: %+v", failed, check.Rules)
	}
}
