package deploy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"
)

// The reconciler is what closes a preview when its pull request merges on
// GitHub and no webhook reaches this dashboard: a tailnet-only install
// receives none. It asks GitHub about every open preview once a minute,
// with a small read budget so a busy fleet does not spend the API quota,
// and stands back for ten minutes when GitHub says the quota is gone.
const (
	PreviewReconcileInterval = 60 * time.Second
	previewReconcileReads    = 50
	previewReconcileWorkers  = 4
	previewReconcileBackoff  = 10 * time.Minute
)

// PullRequestState is what the reconciler needs to know about a pull
// request: whether it is still open, whether it left by merging, and the
// commit and title it carries now.
type PullRequestState struct {
	Open    bool
	Merged  bool
	HeadSHA string
	Title   string
}

// ErrPullRequestUnreadable wraps every failure to read a pull request's
// state. The reconciler acts only on a state it read: a 404, a revoked
// credential, a rate limit or no network never closes a preview, because
// closing is destructive and the answer was not "closed".
var ErrPullRequestUnreadable = errors.New("pull request state could not be read")

// ErrPullRequestRateLimited marks a read GitHub refused for quota. The reader
// that talked to GitHub is the one that can tell a quota refusal from any
// other failure and wraps it; the reconciler backs off on nothing else, since
// an error that merely mentions a number such as 429 — a pull request's, a
// request id's — is not a refusal, and a false backoff pauses every
// repository for ten minutes.
var ErrPullRequestRateLimited = errors.New("pull request reads are rate limited")

type PullRequestStateReader interface {
	PullRequestState(ctx context.Context, repository string, number int) (PullRequestState, error)
}

// PreviewCloser closes one preview whose pull request left the open state;
// merged says whether it merged or was closed unmerged.
type PreviewCloser func(ctx context.Context, target PreviewTarget, merged bool) error

type PreviewReconciler struct {
	automation *AutomationStore
	reader     PullRequestStateReader
	close      PreviewCloser
	log        *slog.Logger
	interval   time.Duration
	maxReads   int
	now        func() time.Time

	mu           sync.Mutex
	backoffUntil time.Time
	passes       uint64
	// titles is the last title written per preview. The open-target
	// reading does not carry the stored title, so this is what makes a
	// title write happen once per change rather than once per pass.
	titles map[int64]string

	cancel context.CancelFunc
	done   chan struct{}
}

func NewPreviewReconciler(automation *AutomationStore, reader PullRequestStateReader, close PreviewCloser, log *slog.Logger) *PreviewReconciler {
	return &PreviewReconciler{
		automation: automation, reader: reader, close: close, log: log,
		interval: PreviewReconcileInterval, maxReads: previewReconcileReads,
		now: time.Now, titles: map[int64]string{},
	}
}

func (r *PreviewReconciler) Start(ctx context.Context) {
	if r == nil || r.cancel != nil {
		return
	}
	ctx, r.cancel = context.WithCancel(ctx)
	r.done = make(chan struct{})
	go func() {
		defer close(r.done)
		ticker := time.NewTicker(r.interval)
		defer ticker.Stop()
		for {
			_ = r.Reconcile(ctx)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func (r *PreviewReconciler) Stop() {
	if r == nil || r.cancel == nil {
		return
	}
	r.cancel()
	<-r.done
	r.cancel = nil
}

// Reconcile is one pass over every open preview: the poll loop's body,
// also safe to call by hand. While backed off from a rate limit it does
// nothing at all.
func (r *PreviewReconciler) Reconcile(ctx context.Context) error {
	if r.backedOff() {
		return nil
	}
	targets, err := r.automation.OpenPreviewTargets(ctx)
	if err != nil {
		return err
	}
	r.reconcile(ctx, targets, false)
	r.forgetClosed(targets)
	return ctx.Err()
}

// ReconcileRepository is a pass over one repository's open previews, for
// the moment right after a merge from the dashboard. It ignores the
// rate-limit backoff: the merge that just went through proves the account
// can still talk to GitHub, and the operator is waiting for the answer.
func (r *PreviewReconciler) ReconcileRepository(ctx context.Context, repository string) error {
	targets, err := r.automation.OpenPreviewTargets(ctx)
	if err != nil {
		return err
	}
	selected := make([]PreviewTarget, 0, len(targets))
	for _, target := range targets {
		if strings.EqualFold(target.Repository, repository) {
			selected = append(selected, target)
		}
	}
	r.reconcile(ctx, selected, true)
	return ctx.Err()
}

// ReconcilePreview reads one preview's pull request and acts on it. Only a
// state that was actually read and says "not open" closes the preview.
func (r *PreviewReconciler) ReconcilePreview(ctx context.Context, target PreviewTarget) error {
	readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	state, err := r.reader.PullRequestState(readCtx, target.Repository, target.Number)
	cancel()
	if err != nil {
		if rateLimited(err) {
			r.backOff()
		}
		if errors.Is(err, ErrPullRequestUnreadable) {
			return err
		}
		return fmt.Errorf("%w: %v", ErrPullRequestUnreadable, err)
	}
	if !state.Open {
		return r.close(ctx, target, state.Merged)
	}
	if validGitObjectID(state.HeadSHA) && state.HeadSHA != target.HeadRevision {
		if err := r.automation.RecordPreviewHead(ctx, target.PreviewID, state.HeadSHA); err != nil {
			return err
		}
	}
	title := strings.TrimSpace(state.Title)
	if title == "" || !r.titleChanged(target.PreviewID, title) {
		return nil
	}
	if err := r.automation.MarkPreviewOrigin(ctx, target.PreviewID, target.Origin, title); err != nil {
		r.forgetTitle(target.PreviewID)
		return err
	}
	return nil
}

// reconcile spends the read budget over the targets, one repository at a
// time within a worker so a repository whose identity is broken costs one
// read, and up to four repositories at once. An explicit pass reads
// through a rate-limit backoff; the periodic one does not.
func (r *PreviewReconciler) reconcile(ctx context.Context, targets []PreviewTarget, explicit bool) {
	ordered := append([]PreviewTarget(nil), targets...)
	sort.Slice(ordered, func(i, j int) bool {
		left, right := strings.ToLower(ordered[i].Repository), strings.ToLower(ordered[j].Repository)
		if left != right {
			return left < right
		}
		return ordered[i].Number < ordered[j].Number
	})
	pass := r.nextPass()
	if len(ordered) > r.maxReads {
		// Rotate where the budget starts, so a fleet with more open previews
		// than reads still reaches all of them over a few passes.
		start := int(pass % uint64(len(ordered)))
		rotated := append(append([]PreviewTarget(nil), ordered[start:]...), ordered[:start]...)
		r.warn("preview reconciler: read budget reached, some previews wait for the next pass",
			"reads", r.maxReads, "dropped", len(ordered)-r.maxReads)
		ordered = rotated[:r.maxReads]
	}
	groups := map[string][]PreviewTarget{}
	var repositories []string
	for _, target := range ordered {
		key := strings.ToLower(target.Repository)
		if _, seen := groups[key]; !seen {
			repositories = append(repositories, key)
		}
		groups[key] = append(groups[key], target)
	}
	var group sync.WaitGroup
	slots := make(chan struct{}, previewReconcileWorkers)
	for _, repository := range repositories {
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			group.Wait()
			return
		}
		group.Add(1)
		go func(targets []PreviewTarget) {
			defer group.Done()
			defer func() { <-slots }()
			r.reconcileRepository(ctx, targets, explicit)
		}(groups[repository])
	}
	group.Wait()
}

// reconcileRepository walks one repository's previews in order and stops
// at the first failure: the failure is logged once for the repository, and
// its remaining previews are read again on the next pass rather than
// spending more of the budget on the same broken credential.
func (r *PreviewReconciler) reconcileRepository(ctx context.Context, targets []PreviewTarget, explicit bool) {
	for _, target := range targets {
		if ctx.Err() != nil || (!explicit && r.backedOff()) {
			return
		}
		err := r.ReconcilePreview(ctx, target)
		if err == nil {
			continue
		}
		if errors.Is(err, context.Canceled) {
			return
		}
		r.warn("preview reconciler: repository skipped for this pass",
			"repository", target.Repository, "number", target.Number, "error", err)
		return
	}
}

func (r *PreviewReconciler) backOff() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.now().Before(r.backoffUntil) {
		r.warn("preview reconciler: GitHub rate limit reached, pausing pull request reads",
			"for", previewReconcileBackoff.String())
	}
	r.backoffUntil = r.now().Add(previewReconcileBackoff)
}

func (r *PreviewReconciler) backedOff() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.now().Before(r.backoffUntil)
}

func (r *PreviewReconciler) nextPass() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.passes++
	return r.passes - 1
}

func (r *PreviewReconciler) titleChanged(previewID int64, title string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if last, ok := r.titles[previewID]; ok && last == title {
		return false
	}
	r.titles[previewID] = title
	return true
}

func (r *PreviewReconciler) forgetTitle(previewID int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.titles, previewID)
}

// forgetClosed drops the title memory of previews that are no longer open,
// so a preview reopened later writes its title afresh.
func (r *PreviewReconciler) forgetClosed(open []PreviewTarget) {
	r.mu.Lock()
	defer r.mu.Unlock()
	keep := make(map[int64]bool, len(open))
	for _, target := range open {
		keep[target.PreviewID] = true
	}
	for previewID := range r.titles {
		if !keep[previewID] {
			delete(r.titles, previewID)
		}
	}
}

func (r *PreviewReconciler) warn(message string, args ...any) {
	if r.log != nil {
		r.log.Warn(message, args...)
	}
}

func rateLimited(err error) bool {
	return errors.Is(err, ErrPullRequestRateLimited)
}
