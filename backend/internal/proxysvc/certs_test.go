package proxysvc

import (
	"encoding/json"
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

// A staging authority's certificate is refused by every browser however many
// days it has left, and it read as a healthy Let's Encrypt one. The issuer's
// name is what flags it: a private authority's chain fails verification the
// same way and is no test certificate.
func TestSummariseFlagsAStagingIssuer(t *testing.T) {
	stagingRoot := newAuthority(t, "(STAGING) Pretend Pear X1", nil)
	for _, c := range []struct {
		issuer  *testAuthority
		staging bool
	}{
		{newAuthority(t, "(STAGING) Riddling Rhubarb R12", stagingRoot), true},
		// Let's Encrypt's staging hierarchy before 2020.
		{newAuthority(t, "Fake LE Intermediate X1", nil), true},
		{newAuthority(t, "R11", nil), false},
		{newAuthority(t, "Company Intermediate", nil), false},
	} {
		leaf, _, _ := c.issuer.issue(t, []string{"app.example.com"}, time.Now().Add(80*24*time.Hour))
		if got := summarise(leaf, "app.example.com", "").Staging; got != c.staging {
			t.Errorf("issuer %q: staging = %v, want %v", leaf.Issuer.CommonName, got, c.staging)
		}
	}
}

// The flag reaches the list wherever the certificate is found: here a file a
// site names, which is how a test certificate ends up served.
func TestListedStagingCertificateSaysSo(t *testing.T) {
	dir := t.TempDir()
	staging := newAuthority(t, "(STAGING) Riddling Rhubarb R12", nil)
	production := newAuthority(t, "R11", nil)
	write := func(name string, issuer *testAuthority) string {
		_, pemText, _ := issuer.issue(t, []string{name}, time.Now().Add(80*24*time.Hour))
		path := filepath.Join(dir, name, "fullchain.pem")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(pemText), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	testPath := write("test.example.com", staging)
	realPath := write("app.example.com", production)
	certs := listCertificates(filepath.Join(dir, "no-live"), filepath.Join(dir, "no-imports"), []VHost{
		{Name: "test.example.com", CertPath: testPath},
		{Name: "app.example.com", CertPath: realPath},
	}, nil)
	byName := map[string]Certificate{}
	for _, c := range certs {
		byName[c.Name] = c
	}
	test, real := byName["test.example.com"], byName["app.example.com"]
	if !test.Staging || real.Staging {
		t.Fatalf("test %v, real %v", test.Staging, real.Staging)
	}
	testJSON, _ := json.Marshal(test)
	realJSON, _ := json.Marshal(real)
	if !strings.Contains(string(testJSON), `"staging":true`) || strings.Contains(string(realJSON), `"staging"`) {
		t.Fatalf("json:\n%s\n%s", testJSON, realJSON)
	}
}
