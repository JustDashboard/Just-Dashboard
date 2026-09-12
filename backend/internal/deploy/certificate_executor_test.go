package deploy

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

type certificateIssuerFake struct {
	domains []string
	result  proxysvc.DeploymentCertificate
	err     error
}

func (f *certificateIssuerFake) EnsureDeploymentCertificate(
	_ context.Context,
	domains []string,
	log func(stream, text string),
) (proxysvc.DeploymentCertificate, error) {
	f.domains = append([]string(nil), domains...)
	if log != nil {
		log("stdout", "fixture certbot output")
	}
	return f.result, f.err
}

func discardCertificateLog(string, string) {}

// The ordinary answers: a plan with no HTTPS domain asks the authority for
// nothing, and a name already covered is reused rather than re-ordered.
func TestCertificateStepSkipsAndReusesWithoutOrdering(t *testing.T) {
	executor := &NormalizedStepExecutor{certificates: &certificateIssuerFake{}}
	skipped := executor.certificateStep(context.Background(), nil, discardCertificateLog)
	if skipped.State != StepSkipped {
		t.Fatalf("no HTTPS domain = %#v, want skipped", skipped)
	}

	reuse := &certificateIssuerFake{result: proxysvc.DeploymentCertificate{
		Outcome: proxysvc.CertificateReused, Name: "example.com",
		CertPath: "/etc/letsencrypt/live/example.com/fullchain.pem",
		KeyPath:  "/etc/letsencrypt/live/example.com/privkey.pem",
	}}
	reused := (&NormalizedStepExecutor{certificates: reuse}).
		certificateStep(context.Background(), []string{"app.example.com"}, discardCertificateLog)
	if reused.State != StepPassed {
		t.Fatalf("reuse = %#v, want passed", reused)
	}
	if got := string(reused.Evidence); !strings.Contains(got, `"outcome":"reused"`) ||
		!strings.Contains(got, "example.com") {
		t.Fatalf("reuse evidence = %s", got)
	}
	// Paths are the Certificates feature's to hold. The deployment's evidence
	// names the lineage and nothing that could be read as key material.
	if strings.Contains(string(reused.Evidence), "privkey") {
		t.Fatalf("certificate evidence carried key material: %s", reused.Evidence)
	}
	if len(reuse.domains) != 1 || reuse.domains[0] != "app.example.com" {
		t.Fatalf("issuer saw %#v, want exactly the planned name", reuse.domains)
	}
}

// Issuance records how it was obtained, because "this run registered an ACME
// account and ordered a certificate" is a side effect on the host that the
// transcript has to be able to account for afterwards.
func TestCertificateStepRecordsAnIssuedCertificate(t *testing.T) {
	fake := &certificateIssuerFake{result: proxysvc.DeploymentCertificate{
		Outcome: proxysvc.CertificateIssued, Name: "app-a1b2c3.203-0-113-7.sslip.io",
		Method: "nginx", Registered: false,
	}}
	logged := []string{}
	result := (&NormalizedStepExecutor{certificates: fake}).certificateStep(
		context.Background(), []string{"app-a1b2c3.203-0-113-7.sslip.io"},
		func(_, text string) { logged = append(logged, text) },
	)
	if result.State != StepPassed {
		t.Fatalf("issue = %#v, want passed", result)
	}
	evidence := string(result.Evidence)
	if !strings.Contains(evidence, `"outcome":"issued"`) || !strings.Contains(evidence, `"method":"nginx"`) {
		t.Fatalf("issue evidence = %s", evidence)
	}
	if strings.Join(logged, "\n") == "" || !strings.Contains(strings.Join(logged, "\n"), "fixture certbot output") {
		t.Fatalf("certbot output did not reach the transcript: %#v", logged)
	}
}

// A host that cannot issue is unavailable, not failed: nothing the deployment
// did went wrong, and the distinction is what tells the operator whether to
// look at their repository or at their server.
func TestCertificateStepSeparatesUnavailableFromRefused(t *testing.T) {
	unavailable := (&NormalizedStepExecutor{certificates: &certificateIssuerFake{
		err: proxysvc.ErrCertbotUnavailable,
	}}).certificateStep(context.Background(), []string{"app.example.com"}, discardCertificateLog)
	if unavailable.State != StepUnavailable || unavailable.ErrorCode != "certificate_unavailable" {
		t.Fatalf("no certbot = %#v, want unavailable", unavailable)
	}

	refused := (&NormalizedStepExecutor{certificates: &certificateIssuerFake{
		err: errors.New("certbot could not issue a certificate for app.example.com: DNS problem"),
	}}).certificateStep(context.Background(), []string{"app.example.com"}, discardCertificateLog)
	if refused.State != StepFailed || refused.ErrorCode != "certificate_issue_failed" {
		t.Fatalf("refused order = %#v, want failed", refused)
	}

	missing := (&NormalizedStepExecutor{}).certificateStep(
		context.Background(), []string{"app.example.com"}, discardCertificateLog)
	if missing.State != StepUnavailable || missing.ErrorCode != "certificate_unavailable" {
		t.Fatalf("no issuer configured = %#v, want unavailable", missing)
	}
}

// The position in the release path is the safety property: a certificate that
// cannot be obtained stops the run before a candidate container exists, so
// there is nothing to stop, restore or explain afterwards.
func TestCertificateFailureStopsTheRunBeforeAnythingStarts(t *testing.T) {
	fixture := newOrchestrationFixture(t)
	environmentID := fixture.addEnvironment(t, "production", EnvironmentProduction)
	run := fixture.enqueue(t, environmentID)
	executor := &recordingExecutor{results: map[StepKey]StepResult{
		StepProvisionCertificate: {
			State: StepFailed, ErrorCode: "certificate_issue_failed",
			ErrorMessage: "the authority refused the order",
		},
	}}
	engine := NewEngine(fixture.runs, executor, fixedReconciler{}, EngineConfig{
		WorkerID: "certificate-fixture", PollEvery: 5 * time.Millisecond,
	}, nil)
	if err := engine.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := waitForRunState(t, fixture.runs, run.ID, RunFailed)
	if got.TerminalCode != "certificate_issue_failed" {
		t.Fatalf("terminal code = %q", got.TerminalCode)
	}
	executed := executor.executed()
	for _, key := range []StepKey{StepStartCandidate, StepActivate, StepRetirePrevious} {
		for _, ran := range executed {
			if ran == key {
				t.Fatalf("%s ran after the certificate step failed: %#v", key, executed)
			}
		}
	}
	if executed[len(executed)-1] != StepProvisionCertificate {
		t.Fatalf("executed = %#v, want the certificate step last", executed)
	}
}
