package proxysvc

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The check that matters is that the key belongs to the certificate. A
// mismatched pair is accepted by every text editor and rejected by nginx at
// reload, which on a live server means finding out during an outage.

func selfSigned(t *testing.T, name string, notAfter time.Time) (string, string, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: name},
		DNSNames:     []string{name},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     notAfter,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
	return certPEM, keyPEM, key
}

func TestParseCertChain(t *testing.T) {
	certPEM, _, _ := selfSigned(t, "example.com", time.Now().Add(90*24*time.Hour))
	leaf, count, err := parseCertChain(certPEM)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 || leaf.Subject.CommonName != "example.com" {
		t.Fatalf("got %d certs, leaf %q", count, leaf.Subject.CommonName)
	}

	// A chain is more than one block, and the first is the leaf.
	other, _, _ := selfSigned(t, "intermediate.example", time.Now().Add(365*24*time.Hour))
	leaf, count, err = parseCertChain(certPEM + other)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 || leaf.Subject.CommonName != "example.com" {
		t.Fatalf("got %d certs, leaf %q", count, leaf.Subject.CommonName)
	}
}

func TestParseCertChainRejectsRubbish(t *testing.T) {
	for _, bad := range []string{"", "hello", "-----BEGIN CERTIFICATE-----\nnope\n-----END CERTIFICATE-----"} {
		if _, _, err := parseCertChain(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestKeyMatchesCertificate(t *testing.T) {
	certPEM, keyPEM, _ := selfSigned(t, "example.com", time.Now().Add(90*24*time.Hour))
	leaf, _, err := parseCertChain(certPEM)
	if err != nil {
		t.Fatal(err)
	}
	if err := keyMatchesCertificate(keyPEM, leaf); err != nil {
		t.Fatalf("a matching pair was rejected: %v", err)
	}

	// The whole reason this check exists.
	_, otherKey, _ := selfSigned(t, "other.example", time.Now().Add(90*24*time.Hour))
	if err := keyMatchesCertificate(otherKey, leaf); err == nil {
		t.Fatal("a mismatched key was accepted, which nginx would refuse at reload")
	}
	if err := keyMatchesCertificate("not a key", leaf); err == nil {
		t.Fatal("accepted something that is not a key")
	}
}

// Authorities hand back PKCS#1 as often as PKCS#8, and rejecting one of them
// would look like a broken key.
func TestKeyMatchesCertificateAcceptsPKCS1(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "rsa.example"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := x509.ParseCertificate(der)
	pkcs1 := string(pem.EncodeToMemory(&pem.Block{
		Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key),
	}))
	if err := keyMatchesCertificate(pkcs1, leaf); err != nil {
		t.Fatalf("PKCS#1 rejected: %v", err)
	}
}

func TestImportCertificateRejectsABadName(t *testing.T) {
	certPEM, keyPEM, _ := selfSigned(t, "example.com", time.Now().Add(24*time.Hour))
	for _, bad := range []string{"", "../../etc/passwd", "Name With Spaces", "a/b"} {
		if _, err := ImportCertificate(bad, certPEM, keyPEM, false); err == nil {
			t.Errorf("accepted name %q", bad)
		}
	}
}

// A DNS challenge is the only way to get a wildcard, and saying so before the
// attempt beats relaying certbot's version of it afterwards.
func TestIssueRefusesAWildcardOverHTTP(t *testing.T) {
	s := New("/etc/nginx", "/etc/caddy/Caddyfile")
	_, err := s.IssueArgs(context.Background(), IssueRequest{
		Domains: []string{"*.example.com"}, Email: "a@example.com", Method: "nginx",
	})
	if err == nil {
		t.Fatal("accepted a wildcard over an HTTP challenge")
	}
	if !strings.Contains(err.Error(), "DNS challenge") {
		t.Errorf("the error should say what to do instead: %v", err)
	}
}

func TestIssueDNSRequiresAKnownProvider(t *testing.T) {
	s := New("/etc/nginx", "/etc/caddy/Caddyfile")
	_, err := s.IssueArgs(context.Background(), IssueRequest{
		Domains: []string{"example.com"}, Email: "a@example.com", Method: "dns",
	})
	if err == nil {
		t.Fatal("accepted a DNS challenge with no provider")
	}
	_, err = s.IssueArgs(context.Background(), IssueRequest{
		Domains: []string{"example.com"}, Email: "a@example.com",
		Method: "dns", DNSProvider: "some-registrar",
	})
	if err == nil {
		t.Fatal("accepted an unknown provider")
	}
}

// Every plugin names its arguments after itself, and route53 has none —
// getting that wrong is an error about an unrecognised flag.
func TestDNSIssueArgs(t *testing.T) {
	cf, _ := DNSProviderFor("cloudflare")
	args := strings.Join(dnsIssueArgs(cf, 45), " ")
	for _, want := range []string{
		"--dns-cloudflare", "--dns-cloudflare-credentials", "--dns-cloudflare-propagation-seconds 45",
	} {
		if !strings.Contains(args, want) {
			t.Errorf("missing %q from %q", want, args)
		}
	}

	r53, _ := DNSProviderFor("route53")
	args = strings.Join(dnsIssueArgs(r53, 45), " ")
	if strings.Contains(args, "credentials") || strings.Contains(args, "propagation") {
		t.Errorf("route53 takes neither: %q", args)
	}
}

func TestWriteDNSCredentialsRejectsUnknownProviders(t *testing.T) {
	if _, err := WriteDNSCredentials("nope", "token = x"); err == nil {
		t.Error("accepted an unknown provider")
	}
	if _, err := WriteDNSCredentials("cloudflare", "   "); err == nil {
		t.Error("accepted empty credentials")
	}
}

func useImportedDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	previous := importedDir
	importedDir = dir
	t.Cleanup(func() { importedDir = previous })
	return dir
}

func readPEMCertificates(t *testing.T, path string) []*x509.Certificate {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	certs, err := parseCertificates(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	return certs
}

// `openssl ecparam -genkey` writes an EC PARAMETERS block before the key, and
// reading only the first block refused every key made that way.
func TestImportSkipsECParameters(t *testing.T) {
	dir := useImportedDir(t)
	certPEM, _, key := selfSigned(t, "ec.example.com", time.Now().Add(90*24*time.Hour))
	sec1, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	// The DER of the P-256 curve OID, which is the whole of that block.
	params := pem.EncodeToMemory(&pem.Block{Type: "EC PARAMETERS", Bytes: []byte{0x06, 0x08, 0x2a, 0x86, 0x48, 0xce, 0x3d, 0x03, 0x01, 0x07}})
	keyPEM := string(params) + string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: sec1}))
	res, err := ImportCertificate("ec", certPEM, keyPEM, false)
	if err != nil {
		t.Fatalf("an EC PARAMETERS block in front of the key was refused: %v", err)
	}
	// Only the key reaches the file nginx reads.
	saved, _ := os.ReadFile(filepath.Join(dir, "ec", "privkey.pem"))
	if strings.Contains(string(saved), "EC PARAMETERS") || !strings.Contains(string(saved), "EC PRIVATE KEY") || res.KeyPath != filepath.Join(dir, "ec", "privkey.pem") {
		t.Fatalf("saved key:\n%s", saved)
	}
}

// The same, from openssl itself, which is where these keys come from.
func TestImportTakesAnOpenSSLECParamKey(t *testing.T) {
	if _, err := exec.LookPath("openssl"); err != nil {
		t.Skip("openssl is not installed")
	}
	useImportedDir(t)
	dir := t.TempDir()
	key := filepath.Join(dir, "key.pem")
	cert := filepath.Join(dir, "cert.pem")
	if out, err := exec.Command("openssl", "ecparam", "-genkey", "-name", "prime256v1", "-out", key).CombinedOutput(); err != nil {
		t.Fatalf("openssl ecparam: %v: %s", err, out)
	}
	if out, err := exec.Command("openssl", "req", "-x509", "-key", key, "-days", "30", "-subj", "/CN=openssl.example.com", "-out", cert).CombinedOutput(); err != nil {
		t.Fatalf("openssl req: %v: %s", err, out)
	}
	keyPEM, _ := os.ReadFile(key)
	certPEM, _ := os.ReadFile(cert)
	if !strings.HasPrefix(string(keyPEM), "-----BEGIN EC PARAMETERS-----") {
		t.Skipf("this openssl does not write the parameters block:\n%s", keyPEM)
	}
	if _, err := ImportCertificate("openssl", string(certPEM), string(keyPEM), false); err != nil {
		t.Fatal(err)
	}
}

// Bundles arrive in any order. The leaf is the certificate the key belongs
// to, the chain follows signatures, and the root is left out of what nginx
// sends.
func TestImportOrdersAReversedBundle(t *testing.T) {
	dir := useImportedDir(t)
	root := newAuthority(t, "Bundle Root", nil)
	intermediate := newAuthority(t, "Bundle Intermediate", root)
	leaf, leafPEM, keyPEM := intermediate.issue(t, []string{"shop.example.com"}, time.Now().Add(200*24*time.Hour))

	res, err := ImportCertificate("shop", certPEM(root.cert)+certPEM(intermediate.cert)+leafPEM, keyPEM, false)
	if err != nil {
		t.Fatalf("a bundle with the root first was refused: %v", err)
	}
	if !res.ChainComplete || res.Cert.Domains[0] != "shop.example.com" {
		t.Fatalf("result = %+v", res)
	}
	saved := readPEMCertificates(t, filepath.Join(dir, "shop", "fullchain.pem"))
	if len(saved) != 2 || !saved[0].Equal(leaf) || !saved[1].Equal(intermediate.cert) {
		t.Fatalf("saved %d certificates, first %q", len(saved), saved[0].Subject.CommonName)
	}
	joined := strings.Join(res.Warnings, " ")
	if !strings.Contains(joined, "not in order") || !strings.Contains(joined, "root certificate was left out") {
		t.Fatalf("warnings = %q", res.Warnings)
	}
	if strings.Contains(joined, "Only the leaf") {
		t.Fatalf("a complete chain was called incomplete: %q", res.Warnings)
	}
}

// ChainComplete used to be "more than one block". An intermediate that did
// not sign the leaf is not part of its chain, whatever it is called.
func TestImportLeavesOutAWrongIntermediate(t *testing.T) {
	dir := useImportedDir(t)
	root := newAuthority(t, "Right Root", nil)
	right := newAuthority(t, "Shared Name", root)
	wrong := newAuthority(t, "Shared Name", newAuthority(t, "Other Root", nil))
	_, leafPEM, keyPEM := right.issue(t, []string{"api.example.com"}, time.Now().Add(200*24*time.Hour))

	res, err := ImportCertificate("api", leafPEM+certPEM(wrong.cert), keyPEM, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.ChainComplete {
		t.Fatal("a leaf with somebody else's intermediate was called complete")
	}
	if saved := readPEMCertificates(t, filepath.Join(dir, "api", "fullchain.pem")); len(saved) != 1 {
		t.Fatalf("the wrong intermediate was saved: %d certificates", len(saved))
	}
	joined := strings.Join(res.Warnings, " ")
	if !strings.Contains(joined, "Only the leaf") || !strings.Contains(joined, "1 of the 2 certificates supplied did not sign") {
		t.Fatalf("warnings = %q", res.Warnings)
	}
}

// Without its root in the bundle, a chain is complete when it reaches a root
// this server trusts — the configured private authority's included.
func TestImportChainCompletenessIsTrustNotLength(t *testing.T) {
	useImportedDir(t)
	root := newAuthority(t, "Private Root", nil)
	intermediate := newAuthority(t, "Private Intermediate", root)
	_, leafPEM, keyPEM := intermediate.issue(t, []string{"intranet.example.com"}, time.Now().Add(200*24*time.Hour))

	t.Setenv("JD_ACME_CA_ROOT", "")
	res, err := ImportCertificate("intranet", leafPEM+certPEM(intermediate.cert), keyPEM, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.ChainComplete || !strings.Contains(strings.Join(res.Warnings, " "), "does not reach a root this server trusts") {
		t.Fatalf("an untrusted private chain = %v %q", res.ChainComplete, res.Warnings)
	}

	rootFile := filepath.Join(t.TempDir(), "root.pem")
	if err := os.WriteFile(rootFile, []byte(certPEM(root.cert)), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("JD_ACME_CA_ROOT", rootFile)
	res, err = ImportCertificate("intranet", leafPEM+certPEM(intermediate.cert), keyPEM, true)
	if err != nil {
		t.Fatal(err)
	}
	if !res.ChainComplete || len(res.Warnings) != 0 {
		t.Fatalf("a chain to the configured root = %v %q", res.ChainComplete, res.Warnings)
	}
}

// An import of the same name used to be overwritten without a word. Now it
// is refused, and replacing it on purpose keeps the previous pair as .bak.
func TestImportRefusesToOverwriteWithoutBeingAsked(t *testing.T) {
	dir := useImportedDir(t)
	firstPEM, firstKey, _ := selfSigned(t, "same.example.com", time.Now().Add(90*24*time.Hour))
	if _, err := ImportCertificate("same", firstPEM, firstKey, false); err != nil {
		t.Fatal(err)
	}
	secondPEM, secondKey, _ := selfSigned(t, "same.example.com", time.Now().Add(365*24*time.Hour))
	_, err := ImportCertificate("same", secondPEM, secondKey, false)
	var exists *ExistingImportError
	if !errors.As(err, &exists) || exists.Existing == nil || !strings.Contains(err.Error(), "same.example.com") {
		t.Fatalf("overwrite without replace = %v", err)
	}
	if saved, _ := os.ReadFile(filepath.Join(dir, "same", "fullchain.pem")); string(saved) != firstPEM {
		t.Fatal("the refused import changed the file")
	}

	res, err := ImportCertificate("same", secondPEM, secondKey, true)
	if err != nil || !res.Replaced {
		t.Fatalf("replace = %+v, %v", res, err)
	}
	for file, want := range map[string]string{"fullchain.pem": secondPEM, "fullchain.pem.bak": firstPEM} {
		if saved, _ := os.ReadFile(filepath.Join(dir, "same", file)); string(saved) != want {
			t.Fatalf("%s does not hold what it should:\n%s", file, saved)
		}
	}
	for _, file := range []string{"privkey.pem", "privkey.pem.bak"} {
		st, err := os.Stat(filepath.Join(dir, "same", file))
		if err != nil || st.Mode().Perm() != 0o600 {
			t.Fatalf("%s: %v %v", file, st, err)
		}
	}
	if matches, _ := filepath.Glob(filepath.Join(dir, "same", ".import-*")); len(matches) != 0 {
		t.Fatalf("staging files left behind: %v", matches)
	}
}

func TestImportRefusesAnEncryptedKey(t *testing.T) {
	useImportedDir(t)
	certPEM, _, _ := selfSigned(t, "locked.example.com", time.Now().Add(90*24*time.Hour))
	encrypted := string(pem.EncodeToMemory(&pem.Block{Type: "ENCRYPTED PRIVATE KEY", Bytes: []byte{1, 2, 3}}))
	if _, err := ImportCertificate("locked", certPEM, encrypted, false); !errors.Is(err, errEncryptedKey) {
		t.Fatalf("an encrypted key = %v", err)
	}
}

// Caddy's release copies are hidden from the list, so an import named like
// one would disappear; the name is refused instead.
func TestImportRefusesACaddyEvidenceName(t *testing.T) {
	useImportedDir(t)
	certPEM, keyPEM, _ := selfSigned(t, "shop.example.com", time.Now().Add(90*24*time.Hour))
	if _, err := ImportCertificate("caddy-0123456789abcdef01234567", certPEM, keyPEM, false); err == nil {
		t.Fatal("an import took a Caddy evidence name")
	}
	if _, err := ImportCertificate("caddy-shop", certPEM, keyPEM, false); err != nil {
		t.Fatalf("an ordinary name starting with caddy was refused: %v", err)
	}
	// The evidence path itself refreshes its copy.
	if _, err := keepCaddyEvidence("caddy-0123456789abcdef01234567", certPEM, keyPEM); err != nil {
		t.Fatal(err)
	}
	if _, err := keepCaddyEvidence("caddy-0123456789abcdef01234567", certPEM, keyPEM); err != nil {
		t.Fatalf("refreshing evidence was refused: %v", err)
	}
}

// With the key belonging to none of the certificates, the answer says so
// rather than blaming the first one.
func TestImportFindsNoLeafForAForeignKey(t *testing.T) {
	useImportedDir(t)
	a, _, _ := selfSigned(t, "a.example.com", time.Now().Add(time.Hour))
	b, _, _ := selfSigned(t, "b.example.com", time.Now().Add(time.Hour))
	_, other, _ := selfSigned(t, "c.example.com", time.Now().Add(time.Hour))
	if _, err := ImportCertificate("foreign", a+b, other, false); err == nil || !strings.Contains(err.Error(), "none of the 2 certificates") {
		t.Fatalf("foreign key = %v", err)
	}
}
