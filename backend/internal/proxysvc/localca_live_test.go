package proxysvc

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"
	"syscall"
	"testing"
	"time"
)

// The host's real nginx, on a private prefix and a loopback port, serving a
// certificate the local CA issued: its configuration passes `nginx -t`, a
// client that trusts only the local CA's root accepts it by name and by
// address, and when the daily check finds it due the renewal is what nginx
// serves once the check has reloaded it — with no hand on the files.
func TestLiveNginxServesALocalCACertificateAndItsRenewal(t *testing.T) {
	root := liveNginx(t)
	t.Cleanup(UseCertificateDirsForTest(filepath.Join(root, "letsencrypt"), filepath.Join(root, "imported")))
	t.Cleanup(UsePrivateDirForTest(filepath.Join(root, "private")))
	if _, err := CreateLocalCA(); err != nil {
		t.Fatal(err)
	}
	issued, err := IssueFromLocalCA(PrivateCertificateRequest{Name: "app", Names: []string{"app.lan.test", "127.0.0.1"}})
	if err != nil {
		t.Fatal(err)
	}
	port := closedPort(t)
	site := fmt.Sprintf(`server {
    listen 127.0.0.1:%d ssl;
    server_name app.lan.test;
    ssl_certificate %s;
    ssl_certificate_key %s;
    return 200 "served";
}
`, port, issued.CertPath, issued.KeyPath)
	available := filepath.Join(root, "sites-available", "app")
	if err := os.WriteFile(available, []byte(site), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(available, filepath.Join(root, "sites-enabled", "app")); err != nil {
		t.Fatal(err)
	}
	if result := runValidator(context.Background(), "nginx", "-t"); !result.Valid {
		t.Fatalf("nginx -t refused the local CA's certificate: %s", result.Output)
	}

	var output bytes.Buffer
	var outputMu sync.Mutex
	command := exec.Command("nginx", "-g", "daemon off;")
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
	rootPEM, _, err := LocalCARoot()
	if err != nil {
		t.Fatal(err)
	}
	trusted := x509.NewCertPool()
	trusted.AppendCertsFromPEM(rootPEM)
	serve := func(serverName string) (*x509.Certificate, error) {
		deadline := time.Now().Add(10 * time.Second)
		for {
			conn, err := tls.DialWithDialer(&net.Dialer{Timeout: time.Second}, "tcp4", fmt.Sprintf("127.0.0.1:%d", port),
				&tls.Config{ServerName: serverName, RootCAs: trusted})
			if err == nil {
				defer conn.Close()
				return conn.ConnectionState().PeerCertificates[0], nil
			}
			if _, refused := err.(*net.OpError); !refused || time.Now().After(deadline) {
				return nil, err
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	for _, name := range []string{"app.lan.test", "127.0.0.1"} {
		if _, err := serve(name); err != nil {
			t.Fatalf("a client trusting the root refused %s: %v", name, err)
		}
	}
	if _, err := tls.Dial("tcp4", fmt.Sprintf("127.0.0.1:%d", port), &tls.Config{ServerName: "app.lan.test"}); err == nil {
		t.Fatal("a client trusting only the system's roots accepted the local CA's certificate")
	}

	// A certificate issued 357 days ago, as the files would hold one by now,
	// on the key the site names.
	ca := localRoot(t)
	caKey, err := readLocalCAKey(ca)
	if err != nil {
		t.Fatal(err)
	}
	leaves := localCALeaves(ca)
	if len(leaves) != 1 {
		t.Fatalf("found %d certificates of the local CA's", len(leaves))
	}
	names, _ := parseCertNames([]string{"app.lan.test", "127.0.0.1"})
	tmpl, err := leafTemplate(names, leaves[0].key.Public(), time.Now().Add(-357*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, leaves[0].key.Public(), caKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(issued.CertPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		t.Fatal(err)
	}
	service := New(root, "")
	if out, err := exec.Command("nginx", "-s", "reload").CombinedOutput(); err != nil {
		t.Fatalf("reload: %v: %s", err, out)
	}
	settle := func(want *big.Int) *x509.Certificate {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			served, err := serve("app.lan.test")
			if err == nil && served.SerialNumber.Cmp(want) == 0 {
				return served
			}
			if time.Now().After(deadline) {
				t.Fatalf("nginx did not come to serve serial %v: %v, %v", want, served, err)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	old := settle(tmpl.SerialNumber)
	if days := time.Until(old.NotAfter).Hours() / 24; days > 41 {
		t.Fatalf("the aged certificate has %.0f days left", days)
	}

	check := service.RenewLocalCALeaves(context.Background(), time.Now())
	if !slices.Equal(check.Renewed, []string{"app"}) || !slices.Equal(check.Reloaded, []string{"app"}) || check.Error != "" {
		t.Fatalf("the daily check: %+v", check)
	}
	renewed := readPEMCertificates(t, issued.CertPath)[0]
	served := settle(renewed.SerialNumber)
	if days := time.Until(served.NotAfter).Hours() / 24; days < 395 {
		t.Fatalf("nginx serves a renewal with %.0f days left", days)
	}
	if next := service.RenewLocalCALeaves(context.Background(), time.Now()); len(next.Renewed) != 0 {
		t.Fatalf("renewed again the same day: %+v", next)
	}
}
