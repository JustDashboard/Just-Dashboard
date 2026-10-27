package api

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/audit"
	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

// The certificate a deployment run orders is a change on the host that no
// request asked for at that moment: an ACME order, and for a wildcard a
// record written into the operator's DNS zone through the credentials saved
// on the Certificates page. The run's transcript has it; the audit log is
// where every other change to the host is read back from, so it is there too,
// naming the provider whose credentials were used and never the credentials.

type auditedCertificateIssuer struct {
	issuer deploy.CertificateIssuer
	record func(context.Context, audit.Entry)
}

func (a auditedCertificateIssuer) EnsureDeploymentCertificate(
	ctx context.Context,
	domains []string,
	log func(stream, text string),
) (proxysvc.DeploymentCertificate, error) {
	result, err := a.issuer.EnsureDeploymentCertificate(ctx, domains, log)
	// A reused certificate changed nothing, and a refusal ordered nothing;
	// an order the authority turned down still reached it.
	if result.Outcome != proxysvc.CertificateIssued && (err == nil || result.Method == "") {
		return result, err
	}
	detail := map[string]any{"method": result.Method}
	if result.DNSProvider != "" {
		detail["dnsProvider"] = result.DNSProvider
	}
	if result.Name != "" {
		detail["certificate"] = result.Name
	}
	if err != nil {
		detail["error"] = err.Error()
	}
	encoded, _ := json.Marshal(detail)
	a.record(context.WithoutCancel(ctx), audit.Entry{
		Actor: "system", Action: "certificates.deployment.issue", Target: strings.Join(domains, ", "),
		Success: err == nil, Detail: string(encoded),
	})
	return result, err
}
