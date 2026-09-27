package deploy

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

// On a Docker Caddy host the copy Caddy's issuance left as caddy-<hash> is
// the only certificate covering a release's domain. The Certificates page
// leaves it out of its inventory; the deployment's route summary must not, or
// every HTTPS domain Caddy serves reads as missing its certificate. It offers
// no link to a page that does not list it; an import still has its link.
func TestDomainRoutesReadCaddyEvidenceAsTheDomainsCertificate(t *testing.T) {
	imported := t.TempDir()
	t.Cleanup(proxysvc.UseCertificateDirsForTest(t.TempDir(), imported))
	const evidence = "caddy-0da2f3126af1d760c968313b"
	writeEvidence(t, filepath.Join(imported, evidence), "draw-io.jd.test", time.Now().Add(85*24*time.Hour))
	writeEvidence(t, filepath.Join(imported, "shop"), "shop.jd.test", time.Now().Add(85*24*time.Hour))

	proxy := proxysvc.New(t.TempDir(), "")
	summary := observeDomainRoutes(context.Background(), OperationsOwners{Proxy: proxy, Certificates: proxy},
		[]PlannedDomain{{Hostname: "draw-io.jd.test", HTTPS: true}, {Hostname: "shop.jd.test", HTTPS: true}}, 7)
	if len(summary.Domains) != 2 {
		t.Fatalf("domains = %+v", summary.Domains)
	}
	caddy, shop := summary.Domains[0], summary.Domains[1]
	if caddy.Certificate != "valid" || caddy.CertificateName != evidence || caddy.CertificateDaysLeft < 84 || caddy.CertificateLink != "" {
		t.Fatalf("draw-io.jd.test reads certificate=%q name=%q daysLeft=%d link=%q, want valid from %s and no link",
			caddy.Certificate, caddy.CertificateName, caddy.CertificateDaysLeft, caddy.CertificateLink, evidence)
	}
	if shop.Certificate != "valid" || shop.CertificateName != "shop" || shop.CertificateLink != "/proxy/certificates" {
		t.Fatalf("shop.jd.test reads %+v", shop)
	}
}

func writeEvidence(t *testing.T, dir, domain string, notAfter time.Time) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: domain}, DNSNames: []string{domain},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: notAfter,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for file, block := range map[string]*pem.Block{
		"fullchain.pem": {Type: "CERTIFICATE", Bytes: der},
		"privkey.pem":   {Type: "PRIVATE KEY", Bytes: keyDER},
	} {
		if err := os.WriteFile(filepath.Join(dir, file), pem.EncodeToMemory(block), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
