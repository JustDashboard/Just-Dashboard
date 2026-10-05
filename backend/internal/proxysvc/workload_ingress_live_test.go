package proxysvc

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Owned loopback processes prove the handoff reaches real nginx workers and
// that compensation returns real traffic to the original upstream.
func TestLiveExistingIngressHandoffAndRollback(t *testing.T) {
	if os.Getenv("JD_DEPLOY_LIVE") != "1" {
		t.Skip("set JD_DEPLOY_LIVE=1 for the owned loopback proxy lifecycle")
	}
	root := liveNginx(t)
	blueListener, err := net.Listen("tcp4", "127.0.0.2:0")
	if err != nil {
		t.Fatal(err)
	}
	upstreamPort := blueListener.Addr().(*net.TCPAddr).Port
	greenListener, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.3", strconv.Itoa(upstreamPort)))
	if err != nil {
		blueListener.Close()
		t.Fatal(err)
	}
	backend := func(listener net.Listener, label string) *httptest.Server {
		server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, label+":"+r.URL.Path) }))
		server.Listener = listener
		server.Start()
		t.Cleanup(server.Close)
		return server
	}
	backend(blueListener, "blue")
	backend(greenListener, "green")
	jobsBlue, err := net.Listen("tcp4", "127.0.0.2:0")
	if err != nil {
		t.Fatal(err)
	}
	jobsPort := jobsBlue.Addr().(*net.TCPAddr).Port
	jobsGreen, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.3", strconv.Itoa(jobsPort)))
	if err != nil {
		jobsBlue.Close()
		t.Fatal(err)
	}
	backend(jobsBlue, "jobs-blue")
	backend(jobsGreen, "jobs-green")
	stable := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "stable:"+r.URL.Path) }))
	t.Cleanup(stable.Close)
	stablePort, _ := strconv.Atoi(strings.Split(stable.Listener.Addr().String(), ":")[1])
	port := freePort(t)
	tlsPort := freePort(t)
	certServer := httptest.NewTLSServer(nil)
	cert := certServer.TLS.Certificates[0]
	certServer.Close()
	key, err := x509.MarshalPKCS8PrivateKey(cert.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	certPath, keyPath := filepath.Join(root, "cert.pem"), filepath.Join(root, "key.pem")
	writeFile(t, certPath, string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]})))
	writeFile(t, keyPath, string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key})))
	authPath := filepath.Join(root, "operator-auth")
	writeFile(t, authPath, "operator:unavailable-fixture-hash\n")
	path := filepath.Join(root, "sites-available", "imported")
	before := fmt.Sprintf(`server {
 listen 127.0.0.1:%d;
 listen 127.0.0.1:%d ssl;
 ssl_certificate %s;
 ssl_certificate_key %s;
 server_name live-import.example.test;
 location /stable/ {
  proxy_pass %s/original/;
  proxy_set_header Upgrade $http_upgrade;
  proxy_set_header Connection "upgrade";
 }
 location /api/ {
  proxy_pass http://127.0.0.2:%d/preserved/; # exact URI stays owned
  proxy_set_header X-Original "unchanged";
  proxy_set_header Upgrade $http_upgrade;
  proxy_set_header Connection "upgrade";
  auth_basic off;
 }
 location /guarded/ {
  auth_basic "Operator owned";
  auth_basic_user_file %s;
  proxy_pass %s;
 }
 location /jobs/ {
  proxy_pass http://127.0.0.2:%d/queue/;
 }
}
`, port, tlsPort, certPath, keyPath, stable.URL, upstreamPort, authPath, stable.URL, jobsPort)
	writeFile(t, path, before)
	symlink(t, path, filepath.Join(root, "sites-enabled", "imported"))
	// Match only this fixture's nginx process, never the host's own proxy.
	daemon := exec.Command(filepath.Join(root, "bin", "nginx"), "-g", "daemon off;")
	if err := daemon.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { daemon.Process.Signal(syscall.SIGQUIT); daemon.Wait() })
	svc := New(root, filepath.Join(root, "Caddyfile")).WithIngressJournalDir(filepath.Join(root, "journals"))
	svc.pending.settle = 2 * time.Second
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	request := func(route string) string {
		body, status, err := cutoverGet("http://127.0.0.1:"+strconv.Itoa(port), "live-import.example.test", route, time.Second)
		if err != nil || status != 200 {
			return ""
		}
		return body
	}
	for deadline := time.Now().Add(10 * time.Second); ; {
		pending, err := svc.Pending(ctx, "")
		if err == nil && pending.Running && pending.Generation != "" && request("/api/item") == "blue:/preserved/item" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("fixture did not become active: %+v, %v", pending, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	targets := []ExistingIngressTarget{{Service: "stable", Host: "127.0.0.1", Port: stablePort, ContainerPort: stablePort}, {Service: "api", Address: "127.0.0.2", Network: "owned", ContainerPort: upstreamPort}, {Service: "jobs", Address: "127.0.0.2", Network: "owned", ContainerPort: jobsPort}}
	bindings, err := svc.CaptureExistingIngress(ctx, targets)
	if err != nil || len(bindings) != 4 {
		t.Fatalf("capture: %+v, %v", bindings, err)
	}
	for _, b := range bindings {
		if b.Status != "linked" || b.SourcePath != path {
			t.Fatalf("not exact active source: %+v", b)
		}
	}
	if string(mustIngressRead(t, path)) != before || request("/stable/item") != "stable:/original/item" {
		t.Fatal("capture mutated live workload")
	}
	targets[1].Address = "127.0.0.3"
	targets[2].Address = "127.0.0.3"
	if err := svc.ApplyExistingIngress(ctx, 31, 41, bindings, targets); err != nil {
		t.Fatal(err)
	}
	want := strings.ReplaceAll(before, "127.0.0.2:", "127.0.0.3:")
	if string(mustIngressRead(t, path)) != want || request("/api/item") != "green:/preserved/item" || request("/stable/item") != "stable:/original/item" {
		t.Fatal("handoff changed unrelated bytes or served wrong service")
	}
	if request("/jobs/item") != "jobs-green:/queue/item" {
		t.Fatal("second retarget service did not activate atomically")
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	response, err := client.Get("https://127.0.0.1:" + strconv.Itoa(tlsPort) + "/api/item")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 200 || string(body) != "green:/preserved/item" {
		t.Fatal("existing TLS route changed")
	}
	_, status, err := cutoverGet("http://127.0.0.1:"+strconv.Itoa(port), "live-import.example.test", "/guarded/", time.Second)
	if err != nil || status != http.StatusUnauthorized {
		t.Fatal("existing authentication changed")
	}
	if err := svc.RestoreExistingIngress(ctx, 31, 41, bindings); err != nil {
		t.Fatal(err)
	}
	if string(mustIngressRead(t, path)) != before || request("/api/item") != "blue:/preserved/item" {
		t.Fatal("rollback did not restore exact original traffic and bytes")
	}
	if request("/jobs/item") != "jobs-blue:/queue/item" {
		t.Fatal("second retarget service did not roll back")
	}
	reloads := 0
	svc.ingressReload = func(ctx context.Context, _ ExistingIngressBinding) error {
		reloads++
		if reloads == 1 {
			return errors.New("injected candidate proxy activation failure")
		}
		_, err := svc.Reload(ctx, KindNginx)
		return err
	}
	if err := svc.ApplyExistingIngress(ctx, 31, 42, bindings, targets); err == nil {
		t.Fatal("failed activation was reported as successful")
	}
	svc.ingressReload = nil
	if string(mustIngressRead(t, path)) != before || request("/api/item") != "blue:/preserved/item" {
		t.Fatal("failed activation did not compensate exact original route")
	}
	if request("/jobs/item") != "jobs-blue:/queue/item" {
		t.Fatal("failed activation did not compensate both retarget services")
	}
	writeFile(t, path, before+"# later operator edit\n")
	if !errors.Is(svc.VerifyExistingIngress(ctx, bindings), ErrExistingIngressChanged) {
		t.Fatal("operator conflict was not rejected before runtime stop")
	}
	if request("/api/item") != "blue:/preserved/item" {
		t.Fatal("conflict disturbed live requests")
	}
	t.Log("Verified real active nginx capture, per-path services, stable port, TLS/auth/URI preservation, exact IP-only handoff, rollback, failed activation compensation, and operator CAS conflict")
}

func mustIngressRead(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
