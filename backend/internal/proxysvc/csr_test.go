package proxysvc

import (
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// signRequest is what the authority does with a request: sign its public key
// for its names.
func (a *testAuthority) signRequest(t *testing.T, csrPEM string, names []string) *x509.Certificate {
	t.Helper()
	block, _ := pem.Decode([]byte(csrPEM))
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if err := csr.CheckSignature(); err != nil {
		t.Fatal(err)
	}
	serial, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	tmpl := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: names[0]}, DNSNames: names,
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(365 * 24 * time.Hour),
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, a.cert, csr.PublicKey, a.key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	return cert
}

// The whole of buying a certificate: a key made here that never moves, a
// request an authority can verify, and the authority's certificate — sent
// intermediate first, as some are — completing it with nothing else pasted.
func TestSigningRequestRoundTrip(t *testing.T) {
	imported := usePrivateCertificates(t)
	request, err := CreateSigningRequest(SigningRequestInput{
		Name: "Shop", Names: []string{"shop.example.com", "www.shop.example.com"}, KeyType: KeyRSA2048,
		Subject: CSRSubject{Organization: "Example Ltd", Locality: "Chișinău", Province: "", Country: "md"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if request.Name != "shop" || request.KeyType != KeyRSA2048 || request.Replaces != nil || request.Error != "" ||
		!slices.Equal(request.Domains, []string{"shop.example.com", "www.shop.example.com"}) ||
		request.Subject != (CSRSubject{Organization: "Example Ltd", Locality: "Chișinău", Country: "MD"}) {
		t.Fatalf("request %+v", request)
	}
	block, _ := pem.Decode([]byte(request.CSR))
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if err := csr.CheckSignature(); err != nil {
		t.Fatalf("the request's signature: %v", err)
	}
	if csr.Subject.CommonName != "shop.example.com" || csr.Subject.Organization[0] != "Example Ltd" || csr.Subject.Country[0] != "MD" {
		t.Fatalf("subject %+v", csr.Subject)
	}
	if strings.Contains(request.CSR, "PRIVATE") {
		t.Fatal("the request carries a key")
	}
	dir := filepath.Join(privateDir, "requests", "shop")
	for path, want := range map[string]os.FileMode{privateDir: 0o700, dir: 0o700, request.KeyPath: 0o600, filepath.Join(dir, "request.csr"): 0o644} {
		if got := mode(t, path); got != want {
			t.Errorf("%s is %o, want %o", path, got, want)
		}
	}
	if path, err := exec.LookPath("openssl"); err == nil {
		out, err := exec.Command(path, "req", "-in", filepath.Join(dir, "request.csr"), "-noout", "-verify").CombinedOutput()
		if err != nil || !strings.Contains(strings.ToLower(string(out)), "verify ok") {
			t.Fatalf("openssl req -verify: %v: %s", err, out)
		}
	}
	listed, err := ListSigningRequests()
	if err != nil || len(listed) != 1 || listed[0].CSR != request.CSR {
		t.Fatalf("listed %+v, %v", listed, err)
	}
	waiting, _ := os.ReadFile(request.KeyPath)

	root := newAuthority(t, "Example Root", nil)
	intermediate := newAuthority(t, "Example Issuing CA", root)
	leaf := intermediate.signRequest(t, request.CSR, []string{"shop.example.com", "www.shop.example.com"})
	res, err := CompleteSigningRequest("shop", certPEM(intermediate.cert)+certPEM(leaf), false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Replaced || res.CertPath != filepath.Join(imported, "shop", "fullchain.pem") || !slices.Contains(res.Warnings, "The certificates were not in order; they were saved leaf first, as nginx sends them.") {
		t.Fatalf("result %+v", res)
	}
	saved := readPEMCertificates(t, res.CertPath)
	if len(saved) != 2 || saved[0].SerialNumber.Cmp(leaf.SerialNumber) != 0 {
		t.Fatalf("saved %d certificates, leaf first %v", len(saved), saved[0].Subject)
	}
	key, _ := os.ReadFile(res.KeyPath)
	if string(key) != string(waiting) || mode(t, res.KeyPath) != 0o600 {
		t.Fatal("the imported key is not the one that was waiting")
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the completed request is still waiting: %v", err)
	}
	if listed, _ := ListSigningRequests(); len(listed) != 0 {
		t.Fatalf("still listed: %+v", listed)
	}
}

func TestSigningRequestsRefuseWhatCannotBeRight(t *testing.T) {
	usePrivateCertificates(t)
	in := SigningRequestInput{Name: "app", Names: []string{"app.example.com"}}
	request, err := CreateSigningRequest(in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CreateSigningRequest(in); !errors.Is(err, ErrRequestExists) {
		t.Fatalf("a second request under one name: %v", err)
	}
	var input *CertificateInputError
	for _, subject := range []CSRSubject{{Country: "Moldova"}, {Country: "M1"}, {Organization: "Tab\there"}, {Locality: strings.Repeat("x", 65)}} {
		if _, err := CreateSigningRequest(SigningRequestInput{Name: "bad", Names: []string{"x.example.com"}, Subject: subject}); !errors.As(err, &input) {
			t.Errorf("subject %+v: %v", subject, err)
		}
	}
	if _, err := os.Stat(filepath.Join(privateDir, "requests", "bad")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a refused request left its directory")
	}

	// A certificate for somebody else's key is not this request's.
	_, otherPEM, _ := newAuthority(t, "Example CA", nil).issue(t, []string{"app.example.com"}, time.Now().Add(90*24*time.Hour))
	if _, err := CompleteSigningRequest("app", otherPEM, false); !errors.As(err, &input) || !strings.Contains(err.Error(), "not the one made for it") {
		t.Fatalf("another key's certificate: %v", err)
	}
	if _, err := CompleteSigningRequest("app", "no certificate here", false); !errors.As(err, &input) {
		t.Fatalf("no certificate: %v", err)
	}
	if listed, _ := ListSigningRequests(); len(listed) != 1 {
		t.Fatal("a refused completion forgot the request")
	}
	if _, err := CompleteSigningRequest("nothing", otherPEM, false); !errors.Is(err, ErrRequestNotFound) {
		t.Fatalf("completing no request: %v", err)
	}

	// The authority left a name out: imported, and said.
	leaf := newAuthority(t, "Example CA", nil).signRequest(t, request.CSR, []string{"www.example.com"})
	res, err := CompleteSigningRequest("app", certPEM(leaf), false)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(res.Warnings, "The request asked for app.example.com as well, which the certificate does not cover.") {
		t.Fatalf("warnings %v", res.Warnings)
	}
}

// Renewing a bought certificate: a new request under the name a site already
// serves, completed over it only when asked to replace it.
func TestCompletingARequestOverAnImportReplacesItOnlyWhenAsked(t *testing.T) {
	imported := usePrivateCertificates(t)
	current, currentKey, _ := selfSigned(t, "shop.example.com", time.Now().Add(20*24*time.Hour))
	if _, err := ImportCertificate("shop", current, currentKey, false); err != nil {
		t.Fatal(err)
	}
	request, err := CreateSigningRequest(SigningRequestInput{Name: "shop", Names: []string{"shop.example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	if request.Replaces == nil || request.Replaces.DaysLeft > 20 {
		t.Fatalf("the request does not say what it replaces: %+v", request.Replaces)
	}
	signed := newAuthority(t, "Example CA", nil).signRequest(t, request.CSR, []string{"shop.example.com"})
	var exists *ExistingImportError
	if _, err := CompleteSigningRequest("shop", certPEM(signed), false); !errors.As(err, &exists) {
		t.Fatalf("replaced without being asked: %v", err)
	}
	res, err := CompleteSigningRequest("shop", certPEM(signed), true)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Replaced {
		t.Fatalf("result %+v", res)
	}
	if kept, _ := os.ReadFile(filepath.Join(imported, "shop", "privkey.pem.bak")); string(kept) != currentKey {
		t.Fatal("the replaced key was not kept as .bak")
	}
}

func TestDiscardSigningRequest(t *testing.T) {
	usePrivateCertificates(t)
	if _, err := CreateSigningRequest(SigningRequestInput{Name: "app", Names: []string{"10.0.0.9"}, KeyType: KeyECDSAP384}); err != nil {
		t.Fatal(err)
	}
	listed, _ := ListSigningRequests()
	if len(listed) != 1 || listed[0].KeyType != KeyECDSAP384 || !slices.Equal(listed[0].Domains, []string{"10.0.0.9"}) {
		t.Fatalf("listed %+v", listed)
	}
	if err := DiscardSigningRequest("app"); err != nil {
		t.Fatal(err)
	}
	if listed, _ := ListSigningRequests(); len(listed) != 0 {
		t.Fatalf("still listed %+v", listed)
	}
	if err := DiscardSigningRequest("app"); !errors.Is(err, ErrRequestNotFound) {
		t.Fatalf("discarding it again: %v", err)
	}
	var input *CertificateInputError
	if err := DiscardSigningRequest("../ca"); !errors.As(err, &input) {
		t.Fatalf("a path for a name: %v", err)
	}
}

// A request whose key is gone still lists, saying it cannot be completed.
func TestAListedRequestSaysWhenItsKeyIsGone(t *testing.T) {
	usePrivateCertificates(t)
	request, err := CreateSigningRequest(SigningRequestInput{Name: "app", Names: []string{"app.example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(request.KeyPath); err != nil {
		t.Fatal(err)
	}
	listed, err := ListSigningRequests()
	if err != nil || len(listed) != 1 || !strings.HasPrefix(listed[0].Error, "its key could not be read") {
		t.Fatalf("listed %+v, %v", listed, err)
	}
}
