package deploy

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/proxysvc"
	"github.com/Wayy01/Just-Dashboard/backend/internal/safego"
)

// Getting the certificate while the build runs.
//
// A first deploy onto a new name spent about thirteen seconds in
// provision_certificate after a build that had nothing to do with it. The
// names are in the stored plan before the build starts, so the build step
// starts the same work in the background (certificatePrewarms.start), and the
// certificate step, still where it was as the gate before start_candidate,
// waits for that attempt and reads its answer instead of ordering again.
//
// What it never does:
//   - order twice for one run: the step uses an issued answer as it is, and
//     only asks again itself when the background attempt found the name
//     already covered (a cheap lookup that sees any change since), waited
//     out certbot's turn, or was cut short;
//   - outlive a cancelled run: CleanupCancelledRun and the run-finished
//     observer cancel it;
//   - become a loop: a build that keeps failing would otherwise place an order
//     on every push. After a background attempt fails for a set of names, no
//     background attempt is made for that set for prewarmFailureCooloff; the
//     step itself still orders after a build that passes, as it always did.
//
// A build that fails after the order was placed leaves the certificate on the
// host, where the next run's step finds it and reuses it.

// prewarmFailureCooloff is how long a failed background order keeps the same
// names from being ordered in the background again.
const prewarmFailureCooloff = time.Hour

// prewarmBound is the longest a background attempt runs. The issuer bounds
// an HTTP-01 order at five minutes, its wait for certbot included.
const prewarmBound = 10 * time.Minute

// prewarmLogLines bounds what a background attempt keeps of its transcript
// for the certificate step to show.
const prewarmLogLines = 64

type certificatePrewarm struct {
	names     []string
	startedAt time.Time
	cancel    context.CancelFunc
	done      chan struct{}
	abandoned bool

	// Set before done is closed.
	finishedAt time.Time
	result     proxysvc.DeploymentCertificate
	err        error
	lines      []prewarmLine
}

type prewarmLine struct{ stream, text string }

type certificatePrewarms struct {
	mu     sync.Mutex
	runs   map[int64]*certificatePrewarm
	failed map[string]time.Time
	now    func() time.Time
}

func (p *certificatePrewarms) clock() time.Time {
	if p.now != nil {
		return p.now()
	}
	return time.Now()
}

// certificateNames is which of a plan's domains need a certificate from this
// host: an HTTPS name that is not a Cloudflare Tunnel's, whose HTTPS
// Cloudflare terminates.
func certificateNames(domains []PlannedDomain) []string {
	names := make([]string, 0, len(domains))
	for _, domain := range domains {
		if domain.HTTPS && !domain.Tunnel {
			names = append(names, strings.ToLower(strings.TrimSpace(domain.Hostname)))
		}
	}
	return names
}

func prewarmKey(names []string) string {
	sorted := slices.Clone(names)
	slices.Sort(sorted)
	return strings.Join(sorted, ",")
}

type prewarmContextKey struct{}

// CertificateStartedDuringBuild says the certificate work on ctx is the
// background attempt the build step started, which the audit entry for an
// order placed by it says.
func CertificateStartedDuringBuild(ctx context.Context) bool {
	started, _ := ctx.Value(prewarmContextKey{}).(bool)
	return started
}

// MarkCertificateStartedDuringBuild is ctx carrying that mark.
func MarkCertificateStartedDuringBuild(ctx context.Context) context.Context {
	return context.WithValue(ctx, prewarmContextKey{}, true)
}

// start begins the background attempt for a run's names unless the run has
// one already, there is nothing to order, or the same names failed in the
// background within the cool-off. It reports whether it started one.
func (p *certificatePrewarms) start(
	ctx context.Context,
	runID int64,
	names []string,
	issuer CertificateIssuer,
) bool {
	if issuer == nil || len(names) == 0 {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.runs == nil {
		p.runs, p.failed = map[int64]*certificatePrewarm{}, map[string]time.Time{}
	}
	if _, running := p.runs[runID]; running {
		return false
	}
	now := p.clock()
	for key, at := range p.failed {
		if now.Sub(at) >= prewarmFailureCooloff {
			delete(p.failed, key)
		}
	}
	key := prewarmKey(names)
	if _, cooling := p.failed[key]; cooling {
		return false
	}
	// Detached from the build step, which ends long before the order may;
	// bounded, and cancelled with the run.
	workCtx, cancel := context.WithTimeout(MarkCertificateStartedDuringBuild(context.WithoutCancel(ctx)), prewarmBound)
	attempt := &certificatePrewarm{names: slices.Clone(names), startedAt: now.UTC(), cancel: cancel, done: make(chan struct{})}
	p.runs[runID] = attempt
	go func() {
		defer cancel()
		lines := []prewarmLine{}
		var result proxysvc.DeploymentCertificate
		answered := false
		err := safego.Try(nil, "certificate prewarm", func() error {
			var err error
			result, err = issuer.EnsureDeploymentCertificate(workCtx, attempt.names, func(stream, text string) {
				if len(lines) < prewarmLogLines {
					lines = append(lines, prewarmLine{stream, text})
				}
			})
			answered = true
			return err
		})
		if !answered {
			// A panic in certbot's handling ends this prewarm, not the
			// dashboard, and nobody answered it: it still publishes, as cut
			// short, so the certificate step stops waiting and issues itself.
			err = fmt.Errorf("%w: %w", errPrewarmFault, err)
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		attempt.result, attempt.err, attempt.lines, attempt.finishedAt = result, err, lines, p.clock().UTC()
		if err != nil && !prewarmInterrupted(err) {
			p.failed[key] = attempt.finishedAt
		}
		close(attempt.done)
		if attempt.abandoned && p.runs[runID] == attempt {
			delete(p.runs, runID)
		}
	}()
	return true
}

// errPrewarmFault is a background attempt a panic in the dashboard ended.
var errPrewarmFault = errors.New("the certificate attempt started during the build stopped on a fault")

// prewarmInterrupted is a background attempt that did not get an answer from
// anyone: cut short, by a cancellation or a fault, or out of time waiting for
// certbot's turn. The step asks again itself, and the names are not cooled
// off.
func prewarmInterrupted(err error) bool {
	var busy *proxysvc.CertbotBusyError
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, errPrewarmFault) || errors.As(err, &busy)
}

// take hands the step the run's background attempt for exactly these names,
// removing it; nil when there is none.
func (p *certificatePrewarms) take(runID int64, names []string) *certificatePrewarm {
	p.mu.Lock()
	defer p.mu.Unlock()
	attempt := p.runs[runID]
	if attempt == nil {
		return nil
	}
	delete(p.runs, runID)
	if prewarmKey(attempt.names) != prewarmKey(names) {
		// The step reads the frozen release, the build the stored plan; they
		// name the same domains, and if they ever did not the step orders
		// what the release will serve.
		attempt.cancel()
		return nil
	}
	return attempt
}

// finish lets go of a finished run's background attempt: cancelled with the
// run when cancel is set, otherwise left to finish its order, whose
// certificate the next run reuses.
func (p *certificatePrewarms) finish(runID int64, cancel bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	attempt := p.runs[runID]
	if attempt == nil {
		return
	}
	if cancel {
		attempt.cancel()
	}
	select {
	case <-attempt.done:
		delete(p.runs, runID)
	default:
		attempt.abandoned = true
	}
}

// startCertificatePrewarm is the build step's half: it starts the background
// attempt for the plan's names and says so in the build's transcript.
func (e *NormalizedStepExecutor) startCertificatePrewarm(ctx context.Context, execution StepExecution, plan *StoredExecutionPlan) {
	if e.certificates == nil || plan == nil {
		return
	}
	_, _, domains, err := decodeStoredPlanInputs(plan)
	if err != nil {
		return
	}
	// What the certificate step will ask for: without the app domain names
	// this server no longer holds (app_domains.go).
	domains, _ = e.detachedDomains(ctx, domains)
	names := certificateNames(domains)
	if e.prewarms.start(ctx, execution.Run.ID, names, e.certificates) {
		_ = stepLog(execution, "status", "Resolving the certificate for "+strings.Join(names, ", ")+" while the build runs")
	}
}

// prewarmEvidence is what the certificate step's evidence says about a
// background attempt it used.
type prewarmEvidence struct {
	StartedDuringBuild bool      `json:"startedDuringBuild"`
	StartedAt          time.Time `json:"startedAt,omitzero"`
	FinishedAt         time.Time `json:"finishedAt,omitzero"`
	// Outcome is the attempt's own answer: issued, reused, failed or
	// interrupted (the step then asked itself).
	Outcome string `json:"outcome"`
	// WaitedMillis is how long the certificate step waited for it.
	WaitedMillis int64 `json:"waitedMillis"`
}
