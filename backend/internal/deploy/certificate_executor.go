package deploy

import (
	"context"
	"errors"
	"strings"

	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

// CertificateIssuer is the narrow contract the certificate step holds over the
// Certificates feature. Issuance stays that feature's code; the deployment
// only says which exact hostnames its own frozen plan asked to serve.
type CertificateIssuer interface {
	EnsureDeploymentCertificate(
		ctx context.Context,
		domains []string,
		log func(stream, text string),
	) (proxysvc.DeploymentCertificate, error)
}

// WithCertificateIssuer attaches automatic HTTPS provisioning.
//
// Optional, like every other owning-feature join: a host without it runs the
// step as unavailable when a plan actually needed a certificate, and skips it
// otherwise — it never silently continues to a cutover that will fail.
func (e *NormalizedStepExecutor) WithCertificateIssuer(issuer CertificateIssuer) *NormalizedStepExecutor {
	e.certificates = issuer
	return e
}

type certificateStepEvidence struct {
	Domains []string `json:"domains"`
	Outcome string   `json:"outcome"`
	Name    string   `json:"certificate,omitempty"`
	Method  string   `json:"method,omitempty"`
	// AccountExisted distinguishes an order against an ACME account this host
	// already had from one that registered a new account.
	AccountExisted bool `json:"accountExisted,omitempty"`
}

// provisionCertificate gets the certificate this run's own plan requires.
//
// It reads the frozen release snapshot rather than desired configuration, for
// the reason every other step does: the names it orders for have to be the
// names the route will carry, and a domain edited after enqueue belongs to the
// next run. A plan with no HTTPS domain does no work and says so.
func (e *NormalizedStepExecutor) provisionCertificate(
	ctx context.Context,
	execution StepExecution,
) StepResult {
	_, snapshot, err := e.releaseSnapshotForRun(ctx, execution.Run.ID)
	if err != nil {
		return normalizedStepFailure(err)
	}
	names := make([]string, 0, len(snapshot.Domains))
	for _, domain := range snapshot.Domains {
		if domain.HTTPS {
			names = append(names, strings.ToLower(strings.TrimSpace(domain.Hostname)))
		}
	}
	return e.certificateStep(ctx, names, func(stream, text string) {
		_ = stepLog(execution, stream, text)
	})
}

// certificateStep is the decision, separated from reading the release so it can
// be exercised against each of its four answers without a database.
func (e *NormalizedStepExecutor) certificateStep(
	ctx context.Context,
	names []string,
	log func(stream, text string),
) StepResult {
	if len(names) == 0 {
		return StepResult{State: StepSkipped, Evidence: mustJSON(map[string]any{
			"reason": "this release publishes no HTTPS domain",
		})}
	}
	if e.certificates == nil {
		return StepResult{
			State: StepUnavailable, ErrorCode: "certificate_unavailable",
			ErrorMessage: "this release needs a certificate and no certificate service is configured",
			Evidence:     mustJSON(certificateStepEvidence{Domains: names, Outcome: "unavailable"}),
		}
	}
	log("status", "Resolving a certificate for "+strings.Join(names, ", "))
	result, err := e.certificates.EnsureDeploymentCertificate(ctx, names, log)
	if err != nil {
		evidence := mustJSON(certificateStepEvidence{Domains: names, Outcome: "failed"})
		// "This host has no certbot" and "the authority refused the order" are
		// different problems for the operator: one is a host to fix, the other
		// is a name or a firewall. Unavailable is the honest state for the
		// first, and it is still terminal — a cutover would fail anyway.
		if errors.Is(err, proxysvc.ErrCertbotUnavailable) {
			return StepResult{
				State: StepUnavailable, ErrorCode: "certificate_unavailable",
				ErrorMessage: err.Error(), Evidence: evidence,
			}
		}
		return StepResult{
			State: StepFailed, ErrorCode: "certificate_issue_failed",
			ErrorMessage: err.Error(), Evidence: evidence,
		}
	}
	if result.Outcome == proxysvc.CertificateIssued {
		log("status", "Issued "+result.Name+" over the "+result.Method+" challenge")
	} else {
		log("status", "Reused the existing "+result.Name+" certificate")
	}
	return StepResult{State: StepPassed, Evidence: mustJSON(certificateStepEvidence{
		Domains: names, Outcome: string(result.Outcome), Name: result.Name,
		Method: result.Method, AccountExisted: result.Registered,
	})}
}
