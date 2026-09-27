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
// server could ever be seen refusing them. Runs a private nginx on a loopback
// port with every path inside the test's directory.
func TestLiveNginxRefusalsAreReportedAsRefused(t *testing.T) {
	binary, err := exec.LookPath("nginx")
	if err != nil {
		t.Skip("nginx is not installed")
	}
	root := t.TempDir()
	cert, _ := scanTestCert(t, 7, nil)
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
        ssl_protocols TLSv1.2 TLSv1.3;
        return 200 "ok";
    }
}
`, user, root, root, strings.Join(temp, "\n"), port, certPath, keyPath)
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
		conn, err := tls.Dial("tcp", addr, &tls.Config{InsecureSkipVerify: true})
		if err == nil {
			conn.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("nginx did not start: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}

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
