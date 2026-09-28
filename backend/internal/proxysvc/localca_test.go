package proxysvc

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// usePrivateCertificates points the imports, certbot's tree and the private
// directory at a test's own, and answers with the imports directory.
func usePrivateCertificates(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	imported := filepath.Join(root, "imported")
	t.Cleanup(UseCertificateDirsForTest(filepath.Join(root, "letsencrypt"), imported))
	t.Cleanup(UsePrivateDirForTest(filepath.Join(root, "private")))
	return imported
}

func mode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

func localRoot(t *testing.T) *x509.Certificate {
	t.Helper()
	root, err := readLocalCARoot()
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestParseCertNames(t *testing.T) {
	names, err := parseCertNames([]string{" App.Lan.Test. ", "192.168.1.10", "[::1]", "::ffff:10.0.0.1", "*.lan.test", "app.lan.test", "192.168.1.10", ""})
	if err != nil {
		t.Fatal(err)
	}
	if got := names.all(); !slices.Equal(got, []string{"app.lan.test", "*.lan.test", "192.168.1.10", "::1", "10.0.0.1"}) {
		t.Fatalf("names = %v", got)
	}
	if names.first != "app.lan.test" {
		t.Fatalf("first = %q", names.first)
	}
	if only, _ := parseCertNames([]string{"10.0.0.5", "nas"}); only.first != "10.0.0.5" || !slices.Equal(only.dns, []string{"nas"}) {
		t.Fatalf("an address given first is the common name: %+v", only)
	}
	many := make([]string, 101)
	for i := range many {
		many[i] = fmt.Sprintf("host%d.lan.test", i)
	}
	for _, bad := range [][]string{
		{},
		{"  "},
		{"two words.test"},
		{"10.0.0.256"},
		{"fe80::1%eth0"},
		{"under_score.lan.test"},
		{"-dash.lan.test"},
		{"a.*.lan.test"},
		{strings.Repeat("a", 64) + ".test"},
		many,
	} {
		var input *CertificateInputError
		if _, err := parseCertNames(bad); !errors.As(err, &input) {
			t.Errorf("%v: err = %v, want an input error", bad, err)
		}
	}
}

func TestKeyTypesAreWhatTheirNamesSay(t *testing.T) {
	for kind, check := range map[KeyType]func(any) bool{
		"":           func(k any) bool { e, ok := k.(*ecdsa.PublicKey); return ok && e.Curve == elliptic.P256() },
		KeyECDSAP256: func(k any) bool { e, ok := k.(*ecdsa.PublicKey); return ok && e.Curve == elliptic.P256() },
		KeyECDSAP384: func(k any) bool { e, ok := k.(*ecdsa.PublicKey); return ok && e.Curve == elliptic.P384() },
		KeyRSA2048:   func(k any) bool { r, ok := k.(*rsa.PublicKey); return ok && r.N.BitLen() == 2048 },
		KeyRSA3072:   func(k any) bool { r, ok := k.(*rsa.PublicKey); return ok && r.N.BitLen() == 3072 },
		KeyRSA4096:   func(k any) bool { r, ok := k.(*rsa.PublicKey); return ok && r.N.BitLen() == 4096 },
	} {
		key, err := generateKey(kind)
		if err != nil {
			t.Fatal(err)
		}
		if !check(key.Public()) {
			t.Errorf("%q made a %T", kind, key.Public())
		}
		if want := kind; want != "" && keyTypeOf(key.Public()) != want {
			t.Errorf("keyTypeOf(%q key) = %q", kind, keyTypeOf(key.Public()))
		}
	}
	var input *CertificateInputError
	if _, err := generateKey("dsa-1024"); !errors.As(err, &input) {
		t.Fatalf("an unknown key type: %v", err)
	}
}

// The point of the local CA: what it signs verifies against its root and
// nothing else, for names and addresses alike, inside the lifetime every
// client accepts, and with every key where only root reads it.
func TestLocalCAIssuesACertificateItsRootVerifies(t *testing.T) {
	imported := usePrivateCertificates(t)
	ca, err := CreateLocalCA()
	if err != nil {
		t.Fatal(err)
	}
	if !ca.Exists || !strings.HasPrefix(ca.Name, "Just Dashboard local CA") || ca.Fingerprint == "" {
		t.Fatalf("created %+v", ca)
	}
	res, err := IssueFromLocalCA(PrivateCertificateRequest{Name: "NAS", Names: []string{"nas.lan.test", "192.168.1.10", "::1"}, KeyType: KeyRSA2048})
	if err != nil {
		t.Fatal(err)
	}
	if res.Name != "nas" || res.CertPath != filepath.Join(imported, "nas", "fullchain.pem") || !res.Cert.LocalCA || res.Replaced {
		t.Fatalf("result %+v", res)
	}
	if !slices.Equal(res.Cert.Domains, []string{"nas.lan.test", "192.168.1.10", "::1"}) {
		t.Fatalf("the certificate lists %v; an address is one of its names", res.Cert.Domains)
	}
	root := localRoot(t)
	if !root.IsCA || !root.MaxPathLenZero || root.NotAfter.Sub(root.NotBefore) < 9*365*24*time.Hour {
		t.Fatalf("root: CA %v, path length zero %v, lifetime %v", root.IsCA, root.MaxPathLenZero, root.NotAfter.Sub(root.NotBefore))
	}
	leaf := readPEMCertificates(t, res.CertPath)
	if len(leaf) != 1 {
		t.Fatalf("fullchain holds %d certificates; the root stays out of it", len(leaf))
	}
	roots := x509.NewCertPool()
	roots.AddCert(root)
	for _, name := range []string{"nas.lan.test", "192.168.1.10", "::1"} {
		if _, err := leaf[0].Verify(x509.VerifyOptions{DNSName: name, Roots: roots}); err != nil {
			t.Errorf("%s does not verify against the root: %v", name, err)
		}
	}
	if _, err := leaf[0].Verify(x509.VerifyOptions{DNSName: "nas.lan.test"}); err == nil {
		t.Error("a local CA's certificate verified against the system's roots")
	}
	if lifetime := leaf[0].NotAfter.Sub(leaf[0].NotBefore); lifetime > 397*24*time.Hour {
		t.Errorf("valid for %v, more than 397 days", lifetime)
	}
	if leaf[0].IsCA || !slices.Contains(leaf[0].ExtKeyUsage, x509.ExtKeyUsageServerAuth) || leaf[0].KeyUsage&x509.KeyUsageKeyEncipherment == 0 {
		t.Errorf("an RSA server certificate: CA %v, usages %v, key usage %b", leaf[0].IsCA, leaf[0].ExtKeyUsage, leaf[0].KeyUsage)
	}
	certPath, keyPath := localCAPaths()
	for path, want := range map[string]os.FileMode{
		privateDir: 0o700, filepath.Dir(certPath): 0o700, keyPath: 0o600, certPath: 0o644,
		res.KeyPath: 0o600, res.CertPath: 0o644,
	} {
		if got := mode(t, path); got != want {
			t.Errorf("%s is %o, want %o", path, got, want)
		}
	}
	raw, err := os.ReadFile(res.KeyPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := keyMatchesCertificate(string(raw), leaf[0]); err != nil {
		t.Fatal(err)
	}
	if path, err := exec.LookPath("openssl"); err == nil {
		out, err := exec.Command(path, "verify", "-CAfile", certPath, res.CertPath).CombinedOutput()
		if err != nil || !strings.Contains(string(out), ": OK") {
			t.Fatalf("openssl verify: %v: %s", err, out)
		}
	}
}

func TestLocalCAIsMadeOnceAndIssuesOnlyOnceMade(t *testing.T) {
	usePrivateCertificates(t)
	if _, err := IssueFromLocalCA(PrivateCertificateRequest{Name: "app", Names: []string{"app.lan.test"}}); !errors.Is(err, ErrNoLocalCA) {
		t.Fatalf("issued without a local CA: %v", err)
	}
	if _, _, err := LocalCARoot(); !errors.Is(err, ErrNoLocalCA) {
		t.Fatalf("a root with no local CA: %v", err)
	}
	first, err := CreateLocalCA()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CreateLocalCA(); !errors.Is(err, ErrLocalCAExists) {
		t.Fatalf("a second root: %v", err)
	}
	if again := describeRoot(localRoot(t)); again.Fingerprint != first.Fingerprint {
		t.Fatal("the refused second root replaced the first")
	}
	pemText, filename, err := LocalCARoot()
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(pemText)
	if block == nil || block.Type != "CERTIFICATE" || !strings.HasPrefix(filename, "just-dashboard-local-ca") || !strings.HasSuffix(filename, ".crt") {
		t.Fatalf("root %q as %q", pemText, filename)
	}
	if strings.Contains(string(pemText), "PRIVATE") {
		t.Fatal("the root download carries a key")
	}
}

func TestPrivateCertificatesRefuseANameInUseUnlessReplacing(t *testing.T) {
	imported := usePrivateCertificates(t)
	if _, err := CreateLocalCA(); err != nil {
		t.Fatal(err)
	}
	req := PrivateCertificateRequest{Name: "app", Names: []string{"app.lan.test"}}
	first, err := IssueFromLocalCA(req)
	if err != nil {
		t.Fatal(err)
	}
	var exists *ExistingImportError
	if _, err := SelfSignCertificate(req); !errors.As(err, &exists) || exists.Existing == nil || exists.Existing.Name != "app" {
		t.Fatalf("overwrote a name in use: %v", err)
	}
	req.Replace = true
	second, err := SelfSignCertificate(req)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Replaced || !second.Cert.SelfSigned {
		t.Fatalf("replacement %+v", second)
	}
	backup := readPEMCertificates(t, filepath.Join(imported, "app", "fullchain.pem.bak"))
	if backup[0].SerialNumber.Cmp(readPEMCertificates(t, first.CertPath)[0].SerialNumber) == 0 {
		t.Fatal("the backup is the new certificate")
	}
	if _, err := os.Stat(filepath.Join(imported, "app", "privkey.pem.bak")); err != nil {
		t.Fatalf("the replaced key was not kept: %v", err)
	}
	var input *CertificateInputError
	for _, name := range []string{"", "../etc", "Caddy-0da2f3126af1d760c968313b", ".ca"} {
		if _, err := SelfSignCertificate(PrivateCertificateRequest{Name: name, Names: []string{"x.test"}}); !errors.As(err, &input) {
			t.Errorf("name %q: %v", name, err)
		}
	}
}

func TestSelfSignedCertificateIsItsOwnRoot(t *testing.T) {
	usePrivateCertificates(t)
	res, err := SelfSignCertificate(PrivateCertificateRequest{Name: "printer", Names: []string{"10.0.0.7"}, KeyType: KeyECDSAP384})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Cert.SelfSigned || res.Cert.LocalCA || !slices.Equal(res.Cert.Domains, []string{"10.0.0.7"}) || len(res.Warnings) != 1 {
		t.Fatalf("result %+v", res)
	}
	leaf := readPEMCertificates(t, res.CertPath)[0]
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	if _, err := leaf.Verify(x509.VerifyOptions{DNSName: "10.0.0.7", Roots: pool}); err != nil {
		t.Fatalf("does not verify against itself: %v", err)
	}
	if keyTypeOf(leaf.PublicKey) != KeyECDSAP384 || leaf.Subject.CommonName != "10.0.0.7" {
		t.Fatalf("key %q, name %q", keyTypeOf(leaf.PublicKey), leaf.Subject.CommonName)
	}
}

// recordingNginx puts an nginx on PATH that records what it was asked and fails
// its test while fail exists.
func recordingNginx(t *testing.T) (calls, fail string) {
	t.Helper()
	dir := t.TempDir()
	calls, fail = filepath.Join(dir, "calls"), filepath.Join(dir, "fail")
	script := fmt.Sprintf("#!/bin/sh\necho \"$*\" >> %q\nif [ \"$1\" = \"-t\" ] && [ -e %q ]; then echo 'nginx: [emerg] unknown directive \"brokn\"' >&2; exit 1; fi\nexit 0\n", calls, fail)
	if err := os.WriteFile(filepath.Join(dir, "nginx"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return calls, fail
}

// The daily check with its clock moved: nothing is due a year out, a
// certificate 44 days from expiry is renewed for another 397 days on the key
// it has, nginx is reloaded once for the enabled site naming it, and a
// certificate this CA did not sign is not its business.
func TestLocalCARenewalWithAFakeClock(t *testing.T) {
	imported := usePrivateCertificates(t)
	nginxDir := t.TempDir()
	calls, fail := recordingNginx(t)
	if _, err := CreateLocalCA(); err != nil {
		t.Fatal(err)
	}
	app, err := IssueFromLocalCA(PrivateCertificateRequest{Name: "app", Names: []string{"app.lan.test", "10.0.0.2"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := IssueFromLocalCA(PrivateCertificateRequest{Name: "spare", Names: []string{"spare.lan.test"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := SelfSignCertificate(PrivateCertificateRequest{Name: "own", Names: []string{"own.lan.test"}}); err != nil {
		t.Fatal(err)
	}
	writeSite(t, nginxDir, "app", "443 ssl", "app.lan.test", app.CertPath, true)
	writeSite(t, nginxDir, "old-app", "443 ssl", "app.lan.test", app.CertPath, false)
	service := New(nginxDir, "")
	before := readPEMCertificates(t, app.CertPath)[0]
	keyBefore, _ := os.ReadFile(app.KeyPath)

	early := service.RenewLocalCALeaves(context.Background(), before.NotAfter.Add(-46*24*time.Hour))
	if len(early.Renewed) != 0 || len(early.Reloaded) != 0 || early.Error != "" {
		t.Fatalf("46 days out: %+v", early)
	}
	if _, err := os.Stat(calls); err == nil {
		t.Fatal("nginx was asked something when nothing was renewed")
	}

	now := before.NotAfter.Add(-44 * 24 * time.Hour)
	check := service.RenewLocalCALeaves(context.Background(), now)
	if !slices.Equal(check.Renewed, []string{"app", "spare"}) || !slices.Equal(check.Reloaded, []string{"app"}) || check.Error != "" || len(check.Failed) != 0 {
		t.Fatalf("44 days out: %+v", check)
	}
	after := readPEMCertificates(t, app.CertPath)[0]
	if after.SerialNumber.Cmp(before.SerialNumber) == 0 {
		t.Fatal("the certificate was not replaced")
	}
	if !after.NotBefore.Equal(now.Add(-time.Hour).UTC().Truncate(time.Second)) || after.NotAfter.Sub(after.NotBefore) != 397*24*time.Hour {
		t.Fatalf("renewed from %v to %v at %v", after.NotBefore, after.NotAfter, now)
	}
	if !slices.Equal(after.DNSNames, before.DNSNames) || !after.IPAddresses[0].Equal(net.ParseIP("10.0.0.2")) {
		t.Fatalf("renewed for %v %v", after.DNSNames, after.IPAddresses)
	}
	if keyAfter, _ := os.ReadFile(app.KeyPath); string(keyAfter) != string(keyBefore) {
		t.Fatal("the renewal changed the key the site names")
	}
	if backup := readPEMCertificates(t, app.CertPath+".bak")[0]; backup.SerialNumber.Cmp(before.SerialNumber) != 0 {
		t.Fatal("the previous certificate was not kept as .bak")
	}
	if !signedBy(after, localRoot(t)) {
		t.Fatal("the renewal is not the local CA's")
	}
	recorded, _ := os.ReadFile(calls)
	if got := strings.Fields(strings.ReplaceAll(string(recorded), "\n", " | ")); strings.Join(got, " ") != "-t | -s reload |" {
		t.Fatalf("nginx was asked %q, want one test and one reload", recorded)
	}
	own := readPEMCertificates(t, filepath.Join(imported, "own", "fullchain.pem"))[0]
	if time.Until(own.NotAfter) < 390*24*time.Hour {
		t.Fatal("a self-signed certificate was renewed")
	}

	// A configuration that fails its test: renewed on disk, and the check
	// says the site keeps the previous certificate.
	if err := os.WriteFile(fail, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	later := service.RenewLocalCALeaves(context.Background(), after.NotAfter.Add(-10*24*time.Hour))
	if !slices.Contains(later.Renewed, "app") || len(later.Reloaded) != 0 ||
		!strings.Contains(later.Error, "nginx was not reloaded: its configuration test failed") ||
		!strings.Contains(later.Error, "app keeps serving the previous certificate") {
		t.Fatalf("with a failing test: %+v", later)
	}

	// Without its key a certificate cannot be renewed, and says so.
	if err := os.Remove(fail); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(imported, "spare", "privkey.pem")); err != nil {
		t.Fatal(err)
	}
	missing := service.RenewLocalCALeaves(context.Background(), time.Now().Add(5*365*24*time.Hour))
	if len(missing.Failed) != 1 || !strings.HasPrefix(missing.Failed[0], "spare: its key could not be read") {
		t.Fatalf("a certificate without its key: %+v", missing)
	}
}

func TestLocalCARenewalWithNoLocalCADoesNothing(t *testing.T) {
	usePrivateCertificates(t)
	calls, _ := recordingNginx(t)
	check := New(t.TempDir(), "").RenewLocalCALeaves(context.Background(), time.Now())
	if len(check.Renewed)+len(check.Failed)+len(check.Reloaded) != 0 || check.Error != "" {
		t.Fatalf("%+v", check)
	}
	if _, err := os.Stat(calls); err == nil {
		t.Fatal("nginx was asked something")
	}
}

func TestLocalCAStateAndTheInventoryMarkItsCertificates(t *testing.T) {
	usePrivateCertificates(t)
	nginxDir := t.TempDir()
	service := New(nginxDir, "")
	if state, err := service.LocalCAState(); err != nil || state.Exists || len(state.Leaves) != 0 || state.RenewBefore != 45 {
		t.Fatalf("before: %+v, %v", state, err)
	}
	if _, err := CreateLocalCA(); err != nil {
		t.Fatal(err)
	}
	app, err := IssueFromLocalCA(PrivateCertificateRequest{Name: "app", Names: []string{"app.lan.test"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SelfSignCertificate(PrivateCertificateRequest{Name: "own", Names: []string{"own.lan.test"}}); err != nil {
		t.Fatal(err)
	}
	writeSite(t, nginxDir, "app", "443 ssl", "app.lan.test", app.CertPath, false)
	state, err := service.LocalCAState()
	if err != nil {
		t.Fatal(err)
	}
	if !state.Exists || state.Error != "" || len(state.Leaves) != 1 {
		t.Fatalf("state %+v", state)
	}
	leaf := state.Leaves[0]
	if leaf.Name != "app" || !slices.Equal(leaf.UsedBy, []string{"app"}) || !leaf.RenewsAt.Equal(leaf.NotAfter.Add(-45*24*time.Hour)) || leaf.DaysLeft < 395 {
		t.Fatalf("leaf %+v", leaf)
	}

	certs, err := service.CertificateInventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	marked := map[string]bool{}
	for _, c := range certs {
		marked[c.Name] = c.LocalCA
	}
	if !marked["app"] || marked["own"] {
		t.Fatalf("marked %v", marked)
	}

	_, keyPath := localCAPaths()
	if err := os.Remove(keyPath); err != nil {
		t.Fatal(err)
	}
	if state, _ := service.LocalCAState(); !strings.Contains(state.Error, "the local CA's key could not be read") {
		t.Fatalf("without its key: %+v", state)
	}
	if _, err := IssueFromLocalCA(PrivateCertificateRequest{Name: "next", Names: []string{"next.lan.test"}}); err == nil {
		t.Fatal("issued without the local CA's key")
	}
}
