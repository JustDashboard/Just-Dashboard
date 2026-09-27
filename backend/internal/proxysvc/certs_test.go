package proxysvc

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
