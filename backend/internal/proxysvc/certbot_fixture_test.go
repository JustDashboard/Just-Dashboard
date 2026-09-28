package proxysvc

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A certbot world a test owns: its configuration directory, a certbot on PATH
// that answers what the runtime asks, a systemctl that knows one timer, and
// certificates signed by authorities made on the spot.

func useLetsencryptDir(t *testing.T, dir string) {
	t.Helper()
	previous := letsencryptDir
	letsencryptDir = dir
	forgetCertbotRuntime()
	t.Cleanup(func() {
		letsencryptDir = previous
		forgetCertbotRuntime()
	})
}

// fakeCertbot puts a certbot on PATH that reports version 9.9.9 and lists the
// given authenticator plugins. Anything else fails the way certbot does while
// another instance holds its lock, so a test that needs no other command
// proves it did not run one. It returns the file each invocation's argv is
// appended to.
func fakeCertbot(t *testing.T, plugins ...string) string {
	t.Helper()
	bin := t.TempDir()
	log := filepath.Join(bin, "argv.log")
	var listing strings.Builder
	listing.WriteString("- - - - - - - - - - - - - - - - - - - - -\n")
	for _, p := range plugins {
		fmt.Fprintf(&listing, "* %s\nDescription: the %s plugin\nInterfaces: Authenticator, Plugin\nEntry point: EntryPoint(name='%s')\n\n", p, p, p)
	}
	script := fmt.Sprintf(`#!/bin/sh
echo "$*" >> %q
case "$1" in
--version) echo "certbot 9.9.9"; exit 0 ;;
plugins) cat <<'LIST'
%sLIST
exit 0 ;;
esac
echo "Another instance of Certbot is already running." >&2
exit 1
`, log, listing.String())
	writeExecutable(t, filepath.Join(bin, "certbot"), script)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	forgetCertbotRuntime()
	t.Cleanup(forgetCertbotRuntime)
	return log
}

// fakeSystemctl answers is-active for one timer and not-found for every unit
// renewalCandidate asks about.
func fakeSystemctl(t *testing.T, activeTimer string) {
	t.Helper()
	bin := t.TempDir()
	writeExecutable(t, filepath.Join(bin, "systemctl"), fmt.Sprintf(`#!/bin/sh
if [ "$1" = "is-active" ] && [ "$2" = %q ]; then echo active; exit 0; fi
if [ "$1" = "show" ]; then echo not-found; exit 0; fi
echo inactive
exit 3
`, activeTimer))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func writeExecutable(t *testing.T, path, script string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

type testAuthority struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

// newAuthority makes a CA: a root when parent is nil, an intermediate signed
// by parent otherwise.
func newAuthority(t *testing.T, name string, parent *testAuthority) *testAuthority {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	tmpl := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(5 * 365 * 24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	signer, signerKey := tmpl, key
	if parent != nil {
		signer, signerKey = parent.cert, parent.key
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, signer, &key.PublicKey, signerKey)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	return &testAuthority{cert: cert, key: key}
}

// issue signs a leaf for names, returning it and its key as PEM.
func (a *testAuthority) issue(t *testing.T, names []string, notAfter time.Time) (*x509.Certificate, string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	tmpl := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: names[0]}, DNSNames: names,
		NotBefore: time.Now().Add(-time.Hour), NotAfter: notAfter,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, a.cert, &key.PublicKey, a.key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	keyDER, _ := x509.MarshalPKCS8PrivateKey(key)
	return cert, certPEM(cert), string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
}

func certPEM(c *x509.Certificate) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Raw}))
}

// writeLineage lays a lineage out as certbot does: renewal/<name>.conf in
// the shape of this host's own, and live/<name>/ holding the files it names.
func writeLineage(t *testing.T, dir, name, server string, leaf *x509.Certificate, chain ...*x509.Certificate) {
	t.Helper()
	live := filepath.Join(dir, "live", name)
	if err := os.MkdirAll(live, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "renewal"), 0o755); err != nil {
		t.Fatal(err)
	}
	full := certPEM(leaf)
	for _, c := range chain {
		full += certPEM(c)
	}
	for file, content := range map[string]string{"cert.pem": certPEM(leaf), "fullchain.pem": full, "privkey.pem": "key"} {
		if err := os.WriteFile(filepath.Join(live, file), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	conf := fmt.Sprintf(`# renew_before_expiry = 30 days
version = 2.11.0
archive_dir = %[1]s/archive/%[2]s
cert = %[1]s/live/%[2]s/cert.pem
privkey = %[1]s/live/%[2]s/privkey.pem
chain = %[1]s/live/%[2]s/chain.pem
fullchain = %[1]s/live/%[2]s/fullchain.pem

# Options used in the renewal process
[renewalparams]
account = 0123456789abcdef0123456789abcdef
authenticator = nginx
installer = nginx
server = %[3]s
key_type = ecdsa
`, dir, name, server)
	if err := os.WriteFile(filepath.Join(dir, "renewal", name+".conf"), []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}
}

const (
	productionACME = "https://acme-v02.api.letsencrypt.org/directory"
	stagingACME    = "https://acme-staging-v02.api.letsencrypt.org/directory"
)
