package proxysvc

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The fingerprint and serial are read against what openssl prints for the
// same file, since that is the tool an operator compares them with.
func TestCertificateFingerprintMatchesOpenSSL(t *testing.T) {
	if _, err := exec.LookPath("openssl"); err != nil {
		t.Skip("openssl is not installed")
	}
	dir := t.TempDir()
	certPath := filepath.Join(dir, "fullchain.pem")
	generate := exec.Command("openssl", "req", "-x509", "-newkey", "ec",
		"-pkeyopt", "ec_paramgen_curve:P-256", "-nodes", "-days", "30",
		"-subj", "/CN=app.example.test", "-keyout", filepath.Join(dir, "privkey.pem"), "-out", certPath)
	if out, err := generate.CombinedOutput(); err != nil {
		t.Fatalf("openssl req: %v: %s", err, out)
	}
	cert, err := readCertificate(certPath)
	if err != nil {
		t.Fatal(err)
	}

	openssl := func(flag string) string {
		out, err := exec.Command("openssl", "x509", "-noout", flag, "-sha256", "-in", certPath).Output()
		if err != nil {
			t.Fatalf("openssl x509 %s: %v", flag, err)
		}
		_, value, _ := strings.Cut(strings.TrimSpace(string(out)), "=")
		return value
	}
	if want := openssl("-fingerprint"); cert.Fingerprint != want {
		t.Errorf("fingerprint %q, openssl says %q", cert.Fingerprint, want)
	}
	if want := openssl("-serial"); strings.ReplaceAll(cert.Serial, ":", "") != want {
		t.Errorf("serial %q, openssl says %q", cert.Serial, want)
	}
	if cert.NotBefore.IsZero() || !cert.NotBefore.Before(cert.NotAfter) {
		t.Errorf("not before %v, not after %v", cert.NotBefore, cert.NotAfter)
	}
}

// Caddy's release copies sit beside the imports as caddy-<24 hex digits>.
// Caddy renews what it serves and never these, so listed as imports they
// raised an expiry alarm apiece; an import merely named caddy-something is
// still an import.
func TestListCertificatesLeavesCaddyEvidenceOut(t *testing.T) {
	imported := t.TempDir()
	for _, name := range []string{"caddy-0da2f3126af1d760c968313b", "caddy-shop", "bought"} {
		certPEM, _, _ := selfSigned(t, name+".example.com", time.Now().Add(10*24*time.Hour))
		if err := os.MkdirAll(filepath.Join(imported, name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(imported, name, "fullchain.pem"), []byte(certPEM), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var names []string
	for _, c := range listCertificates(filepath.Join(t.TempDir(), "live"), imported, nil) {
		names = append(names, c.Name)
		if c.Source != "imported" {
			t.Errorf("%s has source %q", c.Name, c.Source)
		}
	}
	if strings.Join(names, " ") != "bought caddy-shop" {
		t.Fatalf("listed %v", names)
	}
}
