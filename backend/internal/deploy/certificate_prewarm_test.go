package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
)

// gatedCertificateIssuer is a certificate service whose answer waits until
// the test releases it, counting every ask and remembering how it was asked.
type gatedCertificateIssuer struct {
	mu       sync.Mutex
	calls    int
	marked   []bool
	result   proxysvc.DeploymentCertificate
	err      error
	release  chan struct{}
	asked    chan struct{}
	stopped  chan struct{}
	answered int
}

func newGatedIssuer(result proxysvc.DeploymentCertificate, err error) *gatedCertificateIssuer {
	return &gatedCertificateIssuer{result: result, err: err, release: make(chan struct{}),
		asked: make(chan struct{}, 8), stopped: make(chan struct{}, 8)}
}

func (f *gatedCertificateIssuer) EnsureDeploymentCertificate(
	ctx context.Context,
	_ []string,
	log func(stream, text string),
) (proxysvc.DeploymentCertificate, error) {
	f.mu.Lock()
	f.calls++
	f.marked = append(f.marked, CertificateStartedDuringBuild(ctx))
	f.mu.Unlock()
	f.asked <- struct{}{}
	log("stdout", "fixture certbot output")
	select {
	case <-f.release:
	case <-ctx.Done():
		f.stopped <- struct{}{}
		return proxysvc.DeploymentCertificate{}, ctx.Err()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answered++
	return f.result, f.err
}

func (f *gatedCertificateIssuer) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

var issuedFixture = proxysvc.DeploymentCertificate{
	Outcome: proxysvc.CertificateIssued, Name: "app.example.com", Method: "nginx",
}

func waitFor(t *testing.T, signal <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func decodeCertificateEvidence(t *testing.T, raw json.RawMessage) certificateStepEvidence {
	t.Helper()
	var evidence certificateStepEvidence
	if err := json.Unmarshal(raw, &evidence); err != nil {
		t.Fatal(err)
	}
	return evidence
}

// The order placed while the build ran is the step's answer: the step waits
// for it and never orders again.
func TestCertificateStepUsesTheOrderStartedDuringTheBuild(t *testing.T) {
	t.Parallel()
	issuer := newGatedIssuer(issuedFixture, nil)
	executor := &NormalizedStepExecutor{certificates: issuer}
	names := []string{"app.example.com"}
	if !executor.prewarms.start(context.Background(), 7, names, issuer) {
		t.Fatal("no background attempt was started for a name that needs a certificate")
	}
	waitFor(t, issuer.asked, "the background order")
	if executor.prewarms.start(context.Background(), 7, names, issuer) {
		t.Fatal("a second background attempt was started for the same run")
	}

	logged := make(chan string, 16)
	result := make(chan StepResult, 1)
	go func() {
		attempt := executor.prewarms.take(7, names)
		result <- executor.certificateStepAfterPrewarm(context.Background(), attempt, names, func(_, text string) { logged <- text })
	}()
	if line := <-logged; !strings.Contains(line, "started while the build ran") {
		t.Fatalf("the step did not say it waits for the background order: %q", line)
	}
	close(issuer.release)
	step := <-result
	if step.State != StepPassed {
		t.Fatalf("step = %#v", step)
	}
	evidence := decodeCertificateEvidence(t, step.Evidence)
	if evidence.Outcome != "issued" || evidence.Prewarm == nil || !evidence.Prewarm.StartedDuringBuild ||
		evidence.Prewarm.Outcome != "issued" || evidence.Prewarm.StartedAt.IsZero() || evidence.Prewarm.FinishedAt.IsZero() {
		t.Fatalf("evidence = %s", step.Evidence)
	}
	if issuer.count() != 1 || !issuer.marked[0] {
		t.Fatalf("issuer asked %d times (marked %v), want once, by the background attempt", issuer.count(), issuer.marked)
	}
	close(logged)
	transcript := []string{}
	for line := range logged {
		transcript = append(transcript, line)
	}
	if joined := strings.Join(transcript, "\n"); !strings.Contains(joined, "fixture certbot output") {
		t.Fatalf("the background order's output did not reach the step's transcript: %q", joined)
	}
	if executor.prewarms.take(7, names) != nil {
		t.Fatal("the attempt was handed out twice")
	}
}

// A background attempt that found the names covered ordered nothing; the
// step asks again itself, which sees any change since and reuses.
func TestCertificateStepAsksAgainAfterAReusedPrewarm(t *testing.T) {
	t.Parallel()
	reused := proxysvc.DeploymentCertificate{Outcome: proxysvc.CertificateReused, Name: "example.com"}
	issuer := newGatedIssuer(reused, nil)
	close(issuer.release)
	executor := &NormalizedStepExecutor{certificates: issuer}
	names := []string{"app.example.com"}
	executor.prewarms.start(context.Background(), 8, names, issuer)
	step := executor.certificateStepAfterPrewarm(context.Background(), executor.prewarms.take(8, names), names, discardCertificateLog)
	evidence := decodeCertificateEvidence(t, step.Evidence)
	if step.State != StepPassed || evidence.Outcome != "reused" || evidence.Prewarm == nil || evidence.Prewarm.Outcome != "reused" {
		t.Fatalf("step = %#v, evidence %s", step, step.Evidence)
	}
	if issuer.count() != 2 || issuer.marked[1] {
		t.Fatalf("issuer asked %d times (marked %v), want the step's own second ask", issuer.count(), issuer.marked)
	}
}

// A failed background order is the step's failure — the run's one order —
// and the same names are not ordered in the background again for an hour,
// so a build that keeps failing does not keep placing orders.
func TestAFailedPrewarmFailsTheStepAndCoolsOff(t *testing.T) {
	t.Parallel()
	issuer := newGatedIssuer(proxysvc.DeploymentCertificate{}, errors.New("certbot could not issue a certificate for app.example.com: connection refused"))
	close(issuer.release)
	now := time.Date(2026, 11, 22, 9, 0, 0, 0, time.UTC)
	executor := &NormalizedStepExecutor{certificates: issuer}
	executor.prewarms.now = func() time.Time { return now }
	names := []string{"app.example.com"}
	executor.prewarms.start(context.Background(), 9, names, issuer)
	step := executor.certificateStepAfterPrewarm(context.Background(), executor.prewarms.take(9, names), names, discardCertificateLog)
	evidence := decodeCertificateEvidence(t, step.Evidence)
	if step.State != StepFailed || step.ErrorCode != "certificate_issue_failed" || evidence.Prewarm == nil ||
		evidence.Prewarm.Outcome != "failed" {
		t.Fatalf("step = %#v, evidence %s", step, step.Evidence)
	}
	if issuer.count() != 1 {
		t.Fatalf("issuer asked %d times, want once", issuer.count())
	}
	if executor.prewarms.start(context.Background(), 10, names, issuer) {
		t.Fatal("the same names were ordered in the background again inside the cool-off")
	}
	if !executor.prewarms.start(context.Background(), 11, []string{"other.example.com"}, issuer) {
		t.Fatal("other names were held back by an unrelated failure")
	}
	now = now.Add(prewarmFailureCooloff)
	if !executor.prewarms.start(context.Background(), 12, names, issuer) {
		t.Fatal("the cool-off never ended")
	}
}

// Cancelling the run cancels the background order, and an order cut short
// that way is not a failure that cools the names off.
func TestCancellingTheRunCancelsThePrewarm(t *testing.T) {
	t.Parallel()
	issuer := newGatedIssuer(issuedFixture, nil)
	executor := &NormalizedStepExecutor{certificates: issuer}
	names := []string{"app.example.com"}
	executor.prewarms.start(context.Background(), 13, names, issuer)
	waitFor(t, issuer.asked, "the background order")
	if _, err := executor.CleanupCancelledRun(context.Background(), EngineRun{ID: 13, Operation: OperationDeploy}, "token"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, issuer.stopped, "the background order to stop")
	if executor.prewarms.take(13, names) != nil {
		t.Fatal("a cancelled run's attempt was still held")
	}
	deadline := time.Now().Add(5 * time.Second)
	for !executor.prewarms.start(context.Background(), 14, names, issuer) {
		if time.Now().After(deadline) {
			t.Fatal("a cancelled order cooled the names off")
		}
		time.Sleep(10 * time.Millisecond)
	}
	waitFor(t, issuer.asked, "the next run's background order")
	executor.prewarms.finish(14, true)
	waitFor(t, issuer.stopped, "the next run's order to stop")

	// The run-finished observer cancels too.
	executor.prewarms.start(context.Background(), 15, []string{"cancel.example.com"}, issuer)
	waitFor(t, issuer.asked, "the second background order")
	executor.HousekeepingObserver().RunFinished(context.Background(), EngineRun{ID: 15, State: RunCancelled})
	waitFor(t, issuer.stopped, "the observer to stop the order")
}

// A run that fails while its order is in flight — a failed build — lets the
// order finish, so the next run finds the certificate, and the attempt is
// let go of once it has.
func TestAFailedBuildLetsThePrewarmFinish(t *testing.T) {
	t.Parallel()
	issuer := newGatedIssuer(issuedFixture, nil)
	executor := &NormalizedStepExecutor{certificates: issuer}
	executor.prewarms.start(context.Background(), 16, []string{"app.example.com"}, issuer)
	waitFor(t, issuer.asked, "the background order")
	executor.HousekeepingObserver().RunFinished(context.Background(), EngineRun{ID: 16, State: RunFailed})
	close(issuer.release)
	deadline := time.Now().Add(5 * time.Second)
	for {
		issuer.mu.Lock()
		answered := issuer.answered
		issuer.mu.Unlock()
		executor.prewarms.mu.Lock()
		_, held := executor.prewarms.runs[16]
		executor.prewarms.mu.Unlock()
		if answered == 1 && !held {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("answered %d, still held %v", answered, held)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Nothing starts without a certificate service or a name to order for.
func TestPrewarmNeedsAnIssuerAndANameThatNeedsACertificate(t *testing.T) {
	t.Parallel()
	var prewarms certificatePrewarms
	issuer := newGatedIssuer(issuedFixture, nil)
	if prewarms.start(context.Background(), 1, []string{"app.example.com"}, nil) ||
		prewarms.start(context.Background(), 1, nil, issuer) {
		t.Fatal("a background attempt started with nothing to do")
	}
	names := certificateNames([]PlannedDomain{
		{Hostname: "App.Example.com ", HTTPS: true},
		{Hostname: "plain.example.com"},
		{Hostname: "tunnel.example.com", HTTPS: true, Tunnel: true},
	})
	if len(names) != 1 || names[0] != "app.example.com" {
		t.Fatalf("certificate names = %v", names)
	}
}

// The step reads the frozen release; if it ever named other domains than the
// plan the build read, the step orders what the release serves.
func TestPrewarmForOtherNamesIsNotUsed(t *testing.T) {
	t.Parallel()
	issuer := newGatedIssuer(issuedFixture, nil)
	var prewarms certificatePrewarms
	prewarms.start(context.Background(), 2, []string{"old.example.com"}, issuer)
	waitFor(t, issuer.asked, "the background order")
	if prewarms.take(2, []string{"new.example.com"}) != nil {
		t.Fatal("an attempt for other names was handed to the step")
	}
	waitFor(t, issuer.stopped, "the mismatched order to stop")
}

// A step whose own context ends while it waits stops waiting and cancels the
// background order with it.
func TestCertificateStepStopsWaitingWhenItsRunStops(t *testing.T) {
	t.Parallel()
	issuer := newGatedIssuer(issuedFixture, nil)
	executor := &NormalizedStepExecutor{certificates: issuer}
	names := []string{"app.example.com"}
	executor.prewarms.start(context.Background(), 3, names, issuer)
	waitFor(t, issuer.asked, "the background order")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	step := executor.certificateStepAfterPrewarm(ctx, executor.prewarms.take(3, names), names, discardCertificateLog)
	if step.State != StepCancelled {
		t.Fatalf("step = %#v", step)
	}
	waitFor(t, issuer.stopped, "the background order to stop")
}

// Waiting out certbot's turn is nobody's answer: the step asks itself.
func TestCertificateStepAsksAgainWhenThePrewarmWaitedOutCertbot(t *testing.T) {
	t.Parallel()
	busy := &proxysvc.CertbotBusyError{Holder: "the Certificates page", Waited: 5 * time.Minute}
	issuer := newGatedIssuer(proxysvc.DeploymentCertificate{}, busy)
	close(issuer.release)
	executor := &NormalizedStepExecutor{certificates: issuer}
	names := []string{"app.example.com"}
	executor.prewarms.start(context.Background(), 4, names, issuer)
	attempt := executor.prewarms.take(4, names)
	<-attempt.done
	issuer.mu.Lock()
	issuer.result, issuer.err = issuedFixture, nil
	issuer.mu.Unlock()
	step := executor.certificateStepAfterPrewarm(context.Background(), attempt, names, discardCertificateLog)
	evidence := decodeCertificateEvidence(t, step.Evidence)
	if step.State != StepPassed || evidence.Prewarm == nil || evidence.Prewarm.Outcome != "interrupted" || issuer.count() != 2 {
		t.Fatalf("step = %#v, evidence %s, asked %d", step, step.Evidence, issuer.count())
	}
	if !executor.prewarms.start(context.Background(), 5, names, issuer) {
		t.Fatal("waiting for certbot cooled the names off")
	}
}

// faultingIssuer panics on its first ask once released, as a fault in
// certbot's handling would, and issues on every ask after.
type faultingIssuer struct {
	mu      sync.Mutex
	calls   int
	release chan struct{}
}

func (f *faultingIssuer) EnsureDeploymentCertificate(
	context.Context,
	[]string,
	func(stream, text string),
) (proxysvc.DeploymentCertificate, error) {
	f.mu.Lock()
	f.calls++
	first := f.calls == 1
	f.mu.Unlock()
	if first {
		<-f.release
		panic("fixture fault in certbot's handling")
	}
	return issuedFixture, nil
}

func (f *faultingIssuer) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// A panic in the background attempt ends it, not the dashboard, and reads as
// cut short: the step waiting for it stops at once and orders itself, and
// the names are not cooled off.
func TestAFaultedPrewarmFallsBackToTheStepsOwnOrder(t *testing.T) {
	t.Parallel()
	issuer := &faultingIssuer{release: make(chan struct{})}
	executor := &NormalizedStepExecutor{certificates: issuer}
	names := []string{"app.example.com"}
	executor.prewarms.start(context.Background(), 9, names, issuer)
	attempt := executor.prewarms.take(9, names)
	logged := make(chan string, 16)
	result := make(chan StepResult, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() {
		result <- executor.certificateStepAfterPrewarm(ctx, attempt, names, func(_, text string) { logged <- text })
	}()
	if line := <-logged; !strings.Contains(line, "started while the build ran") {
		t.Fatalf("the step did not wait for the background attempt: %q", line)
	}
	close(issuer.release)
	step := <-result
	evidence := decodeCertificateEvidence(t, step.Evidence)
	if step.State != StepPassed || evidence.Outcome != "issued" || evidence.Prewarm == nil ||
		evidence.Prewarm.Outcome != "interrupted" || issuer.count() != 2 {
		t.Fatalf("step = %#v, evidence %s, asked %d: want the step's own order after the fault", step, step.Evidence, issuer.count())
	}
	if !executor.prewarms.start(context.Background(), 10, names, issuer) {
		t.Fatal("a fault in the background attempt cooled the names off")
	}
}
