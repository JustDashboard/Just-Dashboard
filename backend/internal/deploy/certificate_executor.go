package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

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
	// DNSProvider names whose saved credentials answered a wildcard's DNS
	// challenge; the credentials themselves never reach the run.
	DNSProvider string `json:"dnsProvider,omitempty"`
	// AccountExisted distinguishes an order against an ACME account this host
	// already had from one that registered a new account.
	AccountExisted bool `json:"accountExisted,omitempty"`
	// Prewarm is the background attempt the build step started, when the
	// step used one (certificate_prewarm.go).
	Prewarm *prewarmEvidence `json:"prewarm,omitempty"`
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
	release, err := e.store.ReleaseForRun(ctx, execution.Run.ID)
	if err != nil {
		return normalizedStepFailure(err)
	}
	// Read as it was frozen, so the names left out can be named: every
	// other step reads it without them.
	snapshot, err := decodeReleaseRuntimeSnapshot(release)
	if err != nil {
		return normalizedStepFailure(err)
	}
	log := func(stream, text string) {
		_ = stepLog(execution, stream, text)
	}
	domains, detached := e.detachedDomains(ctx, snapshot.Domains)
	if len(detached) > 0 {
		if len(domains) == 0 {
			return appDomainDetachedResult(detached)
		}
		log("status", detachedSentence(detached))
	}
	// Cloudflare terminates a tunnel's HTTPS; the route serves the name over
	// plain HTTP to the connector, so there is nothing to issue.
	names := certificateNames(domains)
	result := StepResult{}
	if attempt := e.prewarms.take(execution.Run.ID, names); attempt != nil {
		result = e.certificateStepAfterPrewarm(ctx, attempt, names, log)
	} else {
		result = e.certificateStep(ctx, names, log)
	}
	return withDetachedWarning(result, detached)
}

// withDetachedWarning turns a passed or skipped certificate step into a
// warning that names the app domain names left out of the route.
func withDetachedWarning(result StepResult, detached []string) StepResult {
	if len(detached) == 0 || (result.State != StepPassed && result.State != StepSkipped) {
		return result
	}
	var evidence map[string]any
	_ = json.Unmarshal(result.Evidence, &evidence)
	if evidence == nil {
		evidence = map[string]any{}
	}
	evidence["detached"] = detached
	evidence["warning"] = detachedSentence(detached)
	result.State, result.Evidence = StepWarning, mustJSON(evidence)
	return result
}

// certificateStepAfterPrewarm waits for the background attempt the build
// step started and answers from it: an issued certificate as it is, so one
// run never orders twice, and a failure as the step's own. When the attempt
// found the names covered, or was cut short before anyone answered, the step
// asks itself, which finds the certificate reused or orders it as before.
func (e *NormalizedStepExecutor) certificateStepAfterPrewarm(
	ctx context.Context,
	attempt *certificatePrewarm,
	names []string,
	log func(stream, text string),
) StepResult {
	waitStarted := e.prewarms.clock()
	select {
	case <-attempt.done:
	default:
		log("status", "Waiting for the certificate for "+strings.Join(names, ", ")+", which was started while the build ran")
		select {
		case <-attempt.done:
		case <-ctx.Done():
			// Cancelled with the run; the attempt stops on its own context.
			attempt.cancel()
			return normalizedStepFailure(ctx.Err())
		}
	}
	prewarm := &prewarmEvidence{
		StartedDuringBuild: true, StartedAt: attempt.startedAt, FinishedAt: attempt.finishedAt,
		WaitedMillis: e.prewarms.clock().Sub(waitStarted).Milliseconds(),
	}
	for _, line := range attempt.lines {
		log(line.stream, line.text)
	}
	switch {
	case attempt.err == nil && attempt.result.Outcome == proxysvc.CertificateIssued:
		prewarm.Outcome = "issued"
		log("status", "The certificate was ordered while the build ran")
		return e.certificateResult(names, attempt.result, nil, log, prewarm)
	case attempt.err == nil:
		prewarm.Outcome = "reused"
	case prewarmInterrupted(attempt.err):
		prewarm.Outcome = "interrupted"
		log("status", "The certificate attempt started during the build did not finish; asking again")
	default:
		prewarm.Outcome = "failed"
		log("status", "The certificate order started while the build ran failed")
		return e.certificateResult(names, proxysvc.DeploymentCertificate{}, attempt.err, log, prewarm)
	}
	return e.askCertificate(ctx, names, log, prewarm)
}

// certificateStep is the decision, separated from reading the release so it can
// be exercised against each of its four answers without a database.
func (e *NormalizedStepExecutor) certificateStep(
	ctx context.Context,
	names []string,
	log func(stream, text string),
) StepResult {
	return e.askCertificate(ctx, names, log, nil)
}

func (e *NormalizedStepExecutor) askCertificate(
	ctx context.Context,
	names []string,
	log func(stream, text string),
	prewarm *prewarmEvidence,
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
	return e.certificateResult(names, result, err, log, prewarm)
}

// certificateResult is the step's answer to what the issuer said, whether
// the step asked or the build step's background attempt did.
func (e *NormalizedStepExecutor) certificateResult(
	names []string,
	result proxysvc.DeploymentCertificate,
	err error,
	log func(stream, text string),
	prewarm *prewarmEvidence,
) StepResult {
	if err != nil {
		evidence := mustJSON(certificateStepEvidence{Domains: names, Outcome: "failed", Prewarm: prewarm})
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
		// A wildcard this host cannot prove — no saved DNS credentials answer
		// for its zone — is the same kind of answer: nothing the deployment
		// did, something to save on the host, which the message names. The
		// proxy serving the route is not a reason: the Docker Caddy ingress
		// serves a copy of what certbot proves, as nginx serves the file.
		if errors.Is(err, proxysvc.ErrCertificateWildcard) {
			return StepResult{
				State: StepUnavailable, ErrorCode: "certificate_wildcard_unavailable",
				ErrorMessage: err.Error(), Evidence: evidence,
			}
		}
		// certbot was somebody else's for longer than this step's bound:
		// nothing the authority said and nothing to fix on the host, so the
		// step says what it waited for and how long, in its own words
		// rather than certbot's "another instance is already running".
		var busy *proxysvc.CertbotBusyError
		if errors.As(err, &busy) {
			return StepResult{
				State: StepFailed, ErrorCode: "certificate_certbot_busy",
				ErrorMessage: fmt.Sprintf("the certificate step waited %s for certbot, which was busy with %s, and its time ran out; deploy again once that has finished",
					busy.Waited.Round(time.Second), busy.Holder),
				Evidence: evidence,
			}
		}
		return StepResult{
			State: StepFailed, ErrorCode: "certificate_issue_failed",
			ErrorMessage: err.Error(), Evidence: evidence,
		}
	}
	switch {
	case result.Outcome == proxysvc.CertificateIssued && result.DNSProvider != "":
		log("status", "Issued "+result.Name+" over a DNS challenge through the saved "+result.DNSProvider+" credentials")
	case result.Outcome == proxysvc.CertificateIssued:
		log("status", "Issued "+result.Name+" over the "+result.Method+" challenge")
	default:
		log("status", "Reused the existing "+result.Name+" certificate")
	}
	return StepResult{State: StepPassed, Evidence: mustJSON(certificateStepEvidence{
		Domains: names, Outcome: string(result.Outcome), Name: result.Name,
		Method: result.Method, DNSProvider: result.DNSProvider, AccountExisted: result.Registered,
		Prewarm: prewarm,
	})}
}
