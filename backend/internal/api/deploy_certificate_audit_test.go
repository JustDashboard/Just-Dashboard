package api

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

type fixedCertificateIssuer struct {
	result proxysvc.DeploymentCertificate
	err    error
}

func (f fixedCertificateIssuer) EnsureDeploymentCertificate(context.Context, []string, func(string, string)) (proxysvc.DeploymentCertificate, error) {
	return f.result, f.err
}

// An order a run placed is in the audit log, through whose DNS credentials
// when it wrote to a zone, whether the authority signed it or not. A reused
// certificate and a refusal ordered nothing and are not.
func TestDeploymentCertificateOrdersAreAudited(t *testing.T) {
	for _, tc := range []struct {
		name    string
		issuer  fixedCertificateIssuer
		success bool
		detail  []string
	}{
		{name: "issued over DNS", issuer: fixedCertificateIssuer{result: proxysvc.DeploymentCertificate{
			Outcome: proxysvc.CertificateIssued, Name: "preview.example.com", Method: "dns-cloudflare", DNSProvider: "Cloudflare",
		}}, success: true, detail: []string{`"dnsProvider":"Cloudflare"`, `"method":"dns-cloudflare"`, `"certificate":"preview.example.com"`}},
		{name: "refused by the authority", issuer: fixedCertificateIssuer{
			result: proxysvc.DeploymentCertificate{Method: "dns-cloudflare", DNSProvider: "Cloudflare"},
			err:    errors.New("certbot could not issue a certificate: Incorrect TXT record"),
		}, detail: []string{`"dnsProvider":"Cloudflare"`, "Incorrect TXT record"}},
		{name: "reused", issuer: fixedCertificateIssuer{result: proxysvc.DeploymentCertificate{
			Outcome: proxysvc.CertificateReused, Name: "preview.example.com",
		}}},
		{name: "refused before ordering", issuer: fixedCertificateIssuer{err: proxysvc.ErrCertificateWildcard}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var entries []audit.Entry
			issuer := auditedCertificateIssuer{issuer: tc.issuer, record: func(_ context.Context, entry audit.Entry) {
				entries = append(entries, entry)
			}}
			_, err := issuer.EnsureDeploymentCertificate(context.Background(), []string{"*.preview.example.com"}, nil)
			if !errors.Is(err, tc.issuer.err) {
				t.Fatalf("error = %v, want the issuer's own", err)
			}
			if tc.detail == nil {
				if len(entries) != 0 {
					t.Fatalf("audited %#v for an order never placed", entries)
				}
				return
			}
			if len(entries) != 1 {
				t.Fatalf("entries = %#v", entries)
			}
			entry := entries[0]
			if entry.Actor != "system" || entry.Action != "certificates.deployment.issue" ||
				entry.Target != "*.preview.example.com" || entry.Success != tc.success {
				t.Fatalf("entry = %#v", entry)
			}
			for _, want := range tc.detail {
				if !strings.Contains(entry.Detail, want) {
					t.Fatalf("detail %s does not carry %s", entry.Detail, want)
				}
			}
		})
	}
}
