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
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()

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
    server {
        listen 127.0.0.1:%d ssl;
        server_name scan.test;
        ssl_certificate %s;
        ssl_certificate_key %s;
        %s
        return 200 "ok";
    }
}
`, user, root, root, strings.Join(temp, "\n"), port, certPath, keyPath, directives)
	if err := os.WriteFile(config, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	command := exec.Command(binary, "-p", root, "-c", config, "-g", "daemon off;")
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
	addr := "127.0.0.1:" + strconv.Itoa(port)
	deadline := time.Now().Add(10 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			conn.Close()
			return addr
		}
		if time.Now().After(deadline) {
			t.Fatalf("nginx did not start: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
