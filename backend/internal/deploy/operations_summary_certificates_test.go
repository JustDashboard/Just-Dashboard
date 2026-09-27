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
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

// On a Docker Caddy host the copy Caddy's issuance left as caddy-<hash> is
// the only certificate covering a release's domain. The Certificates page
// leaves it out of its inventory; the deployment's route summary must not, or
// every HTTPS domain Caddy serves reads as missing its certificate. It offers
// no link to a page that does not list it, and no days left, since Caddy
// renews what it serves and never the copy; an import keeps both.
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
	if caddy.Certificate != "valid" || caddy.CertificateName != evidence || caddy.CertificateRenewedBy != "caddy" ||
		caddy.CertificateDaysLeft != 0 || caddy.CertificateLink != "" {
		t.Fatalf("draw-io.jd.test reads certificate=%q name=%q renewedBy=%q daysLeft=%d link=%q, want valid from %s, renewed by caddy, no days and no link",
			caddy.Certificate, caddy.CertificateName, caddy.CertificateRenewedBy, caddy.CertificateDaysLeft, caddy.CertificateLink, evidence)
	}
	if shop.Certificate != "valid" || shop.CertificateName != "shop" || shop.CertificateRenewedBy != "" ||
		shop.CertificateDaysLeft < 84 || shop.CertificateLink != "/proxy/certificates" {
		t.Fatalf("shop.jd.test reads %+v", shop)
	}
}

// A copy is refreshed only when a release runs, so a release left alone ages
// its copy into the renewal window and past it while Caddy serves the
// certificate it renewed. Graded by the copy, every such deployment warned
// "Renew the certificate in Proxy" and linked to a Certificates page that
// does not list it. An import nobody renews still raises both findings.
func TestDiagnosisRaisesNoExpiryFindingForAnAgeingCaddyCopy(t *testing.T) {
	imported := t.TempDir()
	t.Cleanup(proxysvc.UseCertificateDirsForTest(t.TempDir(), imported))
	writeEvidence(t, filepath.Join(imported, "caddy-0da2f3126af1d760c968313b"), "old.jd.test", time.Now().Add(10*24*time.Hour))
	writeEvidence(t, filepath.Join(imported, "caddy-111111111111111111111111"), "older.jd.test", time.Now().Add(-24*time.Hour))
	writeEvidence(t, filepath.Join(imported, "shop"), "shop.jd.test", time.Now().Add(10*24*time.Hour))
	writeEvidence(t, filepath.Join(imported, "gone"), "gone.jd.test", time.Now().Add(-24*time.Hour))

	proxy := proxysvc.New(t.TempDir(), "")
	summary := observeDomainRoutes(context.Background(), OperationsOwners{Proxy: proxy, Certificates: proxy}, []PlannedDomain{
		{Hostname: "old.jd.test", HTTPS: true}, {Hostname: "older.jd.test", HTTPS: true},
		{Hostname: "shop.jd.test", HTTPS: true}, {Hostname: "gone.jd.test", HTTPS: true},
	}, 7)
	rows := map[string]DomainRoute{}
	for _, row := range summary.Domains {
		rows[row.Hostname] = row
	}
	for _, hostname := range []string{"old.jd.test", "older.jd.test"} {
		row := rows[hostname]
		if row.Certificate != "valid" || row.CertificateRenewedBy != "caddy" || row.CertificateDaysLeft != 0 || row.CertificateLink != "" {
			t.Fatalf("%s reads certificate=%q renewedBy=%q daysLeft=%d link=%q, want valid and renewed by caddy with no days or link",
				hostname, row.Certificate, row.CertificateRenewedBy, row.CertificateDaysLeft, row.CertificateLink)
		}
	}
	if row := rows["shop.jd.test"]; row.Certificate != "expiring" || row.CertificateLink != "/proxy/certificates" {
		t.Fatalf("shop.jd.test reads %+v, want expiring with its link", row)
	}
	if row := rows["gone.jd.test"]; row.Certificate != "expired" || row.CertificateLink != "/proxy/certificates" {
		t.Fatalf("gone.jd.test reads %+v, want expired with its link", row)
	}

	input := healthyDiagnosisInput()
	input.Domains = summary
	diagnosis := Diagnose(input)
	got := []string{}
	for _, finding := range diagnosis.Findings {
		if !strings.HasPrefix(finding.Code, "certificate_") {
			continue
		}
		got = append(got, finding.Code+" "+strings.Fields(finding.Title)[3])
		if finding.DeepLink != "/proxy/certificates" {
			t.Fatalf("%q links to %q", finding.Code, finding.DeepLink)
		}
	}
	sort.Strings(got)
	if strings.Join(got, ", ") != "certificate_expired gone.jd.test, certificate_expiring shop.jd.test" {
		t.Fatalf("certificate findings = %v, want only the two imports'", got)
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
		NotBefore: notAfter.Add(-90 * 24 * time.Hour), NotAfter: notAfter,
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
