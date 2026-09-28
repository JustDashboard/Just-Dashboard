package deploy

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

type pullRequestStateFake struct {
	mu     sync.Mutex
	states map[string]PullRequestState
	errs   map[string]error
	reads  []string
}

func pullKey(repository string, number int) string {
	return fmt.Sprintf("%s#%d", strings.ToLower(repository), number)
}

func (f *pullRequestStateFake) PullRequestState(_ context.Context, repository string, number int) (PullRequestState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := pullKey(repository, number)
	f.reads = append(f.reads, key)
	if err := f.errs[key]; err != nil {
		return PullRequestState{}, err
	}
	state, ok := f.states[key]
	if !ok {
		return PullRequestState{}, fmt.Errorf("%w: no fixture for %s", ErrPullRequestUnreadable, key)
	}
	return state, nil
}

func (f *pullRequestStateFake) set(key string, state PullRequestState) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.states == nil {
		f.states = map[string]PullRequestState{}
	}
	f.states[key] = state
}

func (f *pullRequestStateFake) fail(key string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.errs == nil {
		f.errs = map[string]error{}
	}
	f.errs[key] = err
}

func (f *pullRequestStateFake) read() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.reads...)
}

type previewClose struct {
	Number int
	Merged bool
}

type previewCloseFake struct {
	mu    sync.Mutex
	calls []previewClose
}

func (f *previewCloseFake) close(_ context.Context, target PreviewTarget, merged bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, previewClose{target.Number, merged})
	return nil
}

func (f *previewCloseFake) closed() []previewClose {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]previewClose(nil), f.calls...)
}

// openPreviewFixture opens an approved preview of another pull request, on
// a trigger of its own so the repository can differ from previewFixture's.
func openPreviewFixture(t *testing.T, f *automationFixture, repository string, number int) *PreviewRef {
	t.Helper()
	created, err := f.automation.CreateTrigger(t.Context(), f.projectID, f.environmentID, TriggerWrite{
		Name: "Previews " + repository, Kind: TriggerGitHub, Provider: "github", Enabled: true,
		Config: TriggerConfig{Repository: repository, Ref: "main", Preview: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	event := ProviderEvent{PreviewNumber: number, PreviewRef: fmt.Sprintf("refs/pull/%d/head", number), Revision: strings.Repeat("c", 40), Author: "dev", HeadRepository: repository}
	approvePreviewFixture(t, f, &created.Trigger, event)
	preview, _, err := f.automation.EnsurePreview(t.Context(), &created.Trigger, event)
	if err != nil {
		t.Fatal(err)
	}
	setSourceRemote(t, f, preview.EnvironmentID, "https://github.com/"+repository+".git", repository)
	return preview
}

func reconcilerFixture(t *testing.T) (*automationFixture, *PreviewRef, *pullRequestStateFake, *previewCloseFake, *PreviewReconciler) {
	t.Helper()
	f, preview := previewFixture(t)
	reader := &pullRequestStateFake{}
	closer := &previewCloseFake{}
	return f, preview, reader, closer, NewPreviewReconciler(f.automation, reader, closer.close, nil)
}

func previewRow(t *testing.T, f *automationFixture, previewID int64) (headRevision, origin, title string) {
	t.Helper()
	if err := f.store.DB.QueryRow(`SELECT head_revision,origin,title FROM deploy_preview_refs WHERE id=?`, previewID).Scan(&headRevision, &origin, &title); err != nil {
		t.Fatal(err)
	}
	return headRevision, origin, title
}

// Only a pull request the reconciler actually read as closed closes its
// preview. An open one, or one it could not read, leaves the preview alone.
func TestPreviewReconcilerClosesOnlyAPullRequestItReadAsClosed(t *testing.T) {
	t.Parallel()
	f, preview, reader, closer, reconciler := reconcilerFixture(t)
	ctx := context.Background()
	key := pullKey("acme/app", 7)
	reader.set(key, PullRequestState{Open: true, HeadSHA: strings.Repeat("a", 40), Title: "Add login"})
	if err := reconciler.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if len(closer.closed()) != 0 {
		t.Fatalf("an open pull request closed its preview: %v", closer.closed())
	}
	reader.fail(key, fmt.Errorf("%w: HTTP 404: Not Found", ErrPullRequestUnreadable))
	if err := reconciler.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if len(closer.closed()) != 0 {
		t.Fatalf("an unreadable pull request closed its preview: %v", closer.closed())
	}
	if err := reconciler.ReconcilePreview(ctx, PreviewTarget{PreviewID: preview.ID, Repository: "acme/app", Number: 7}); !errors.Is(err, ErrPullRequestUnreadable) {
		t.Fatalf("unreadable state error = %v", err)
	}
	if head, _, _ := previewRow(t, f, preview.ID); head != strings.Repeat("a", 40) {
		t.Fatalf("the open pass did not record the head: %q", head)
	}
	reader.fail(key, nil)
	reader.set(key, PullRequestState{Open: false, Merged: true, HeadSHA: strings.Repeat("d", 40)})
	if err := reconciler.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if got := closer.closed(); fmt.Sprint(got) != fmt.Sprint([]previewClose{{7, true}}) {
		t.Fatalf("closes = %v, want one merged close of #7", got)
	}
	if head, _, _ := previewRow(t, f, preview.ID); head != strings.Repeat("a", 40) {
		t.Fatalf("a closed pull request recorded a head: %q", head)
	}
	if reads := reader.read(); len(reads) != 4 {
		t.Fatalf("reads = %v, want one per pass and one for the explicit call", reads)
	}
}

// A new commit on the pull request is recorded as the preview's head each
// time it changes, including a move back to the approved revision, and the
// title follows the pull request without touching the origin.
func TestPreviewReconcilerRecordsEachHeadMoveAndTheTitle(t *testing.T) {
	t.Parallel()
	f, preview, reader, closer, reconciler := reconcilerFixture(t)
	ctx := context.Background()
	key := pullKey("acme/app", 7)
	if _, err := f.store.DB.Exec(`UPDATE deploy_preview_refs SET origin='dashboard' WHERE id=?`, preview.ID); err != nil {
		t.Fatal(err)
	}
	reader.set(key, PullRequestState{Open: true, HeadSHA: strings.Repeat("b", 40), Title: "  Fix login  "})
	if err := reconciler.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if head, origin, title := previewRow(t, f, preview.ID); head != strings.Repeat("b", 40) || origin != "dashboard" || title != "Fix login" {
		t.Fatalf("after a new head: head=%q origin=%q title=%q", head, origin, title)
	}
	targets, err := f.automation.OpenPreviewTargets(ctx)
	if err != nil || len(targets) != 1 || targets[0].HeadRevision != strings.Repeat("b", 40) || targets[0].Revision != strings.Repeat("a", 40) {
		t.Fatalf("targets after a new head = %#v, %v", targets, err)
	}
	reader.set(key, PullRequestState{Open: true, HeadSHA: strings.Repeat("a", 40), Title: "Fix login"})
	if err := reconciler.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if head, _, _ := previewRow(t, f, preview.ID); head != strings.Repeat("a", 40) {
		t.Fatalf("a move back to the approved revision was not recorded: %q", head)
	}
	reader.set(key, PullRequestState{Open: true, HeadSHA: "not a commit", Title: "Fix login"})
	if err := reconciler.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if head, _, _ := previewRow(t, f, preview.ID); head != strings.Repeat("a", 40) {
		t.Fatalf("a malformed head was recorded: %q", head)
	}
	if len(closer.closed()) != 0 {
		t.Fatalf("an open pull request closed its preview: %v", closer.closed())
	}
}

// A rate-limited answer pauses the periodic passes for ten minutes; an
// explicit repository pass after a dashboard merge still reads. Only the
// reader's verdict counts: an unreadable answer that merely mentions a
// number such as 429, or even the provider's words, pauses nothing, since
// the reader is the one that saw the status.
func TestPreviewReconcilerBacksOffAfterARateLimit(t *testing.T) {
	t.Parallel()
	_, _, reader, closer, reconciler := reconcilerFixture(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	reconciler.now = func() time.Time { return now }
	key := pullKey("acme/app", 7)
	unreadable := []string{
		"Could not resolve to a PullRequest with the number of 429.",
		"GitHub answered 404 to /repos/o/r/pulls/429: Not Found",
		"HTTP 403: API rate limit exceeded for installation ID 1",
	}
	for _, text := range unreadable {
		reader.fail(key, fmt.Errorf("%w: %s", ErrPullRequestUnreadable, text))
		if err := reconciler.Reconcile(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if reads := reader.read(); len(reads) != len(unreadable) {
		t.Fatalf("reads = %v, want one per pass: an unreadable answer is not a refusal", reads)
	}
	reader.fail(key, fmt.Errorf("%w: HTTP 403: API rate limit exceeded for installation ID 1", ErrPullRequestRateLimited))
	if err := reconciler.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if err := reconciler.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if reads := reader.read(); len(reads) != len(unreadable)+1 {
		t.Fatalf("reads during backoff = %v, want the one that hit the limit", reads)
	}
	if err := reconciler.ReconcileRepository(ctx, "ACME/App"); err != nil {
		t.Fatal(err)
	}
	if reads := reader.read(); len(reads) != len(unreadable)+2 {
		t.Fatalf("reads after an explicit repository pass = %v", reads)
	}
	now = now.Add(previewReconcileBackoff + time.Second)
	reader.fail(key, nil)
	reader.set(key, PullRequestState{Open: false, Merged: false})
	if err := reconciler.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if reads := reader.read(); len(reads) != len(unreadable)+3 {
		t.Fatalf("reads after the backoff = %v", reads)
	}
	if got := closer.closed(); fmt.Sprint(got) != fmt.Sprint([]previewClose{{7, false}}) {
		t.Fatalf("closes = %v", got)
	}
	for _, text := range append(unreadable, "HTTP 403: API rate limit exceeded", "HTTP 429: Too Many Requests", "HTTP 404: Not Found", "commit 4290abc not found") {
		if rateLimited(errors.New(text)) {
			t.Fatalf("rateLimited(%q) = true; only the reader's sentinel is a refusal", text)
		}
	}
	if !rateLimited(fmt.Errorf("%w: quota", ErrPullRequestRateLimited)) || !rateLimited(fmt.Errorf("%w: %w", ErrPullRequestUnreadable, ErrPullRequestRateLimited)) {
		t.Fatal("the rate-limit sentinel was not recognised")
	}
}

// The read budget is spent on a rotating window, so every preview is
// reached within a few passes, and a repository pass reads only that
// repository.
func TestPreviewReconcilerSpendsItsReadBudgetAcrossPassesAndRepositories(t *testing.T) {
	t.Parallel()
	f, _, reader, _, reconciler := reconcilerFixture(t)
	ctx := context.Background()
	openPreviewFixture(t, f, "acme/other", 9)
	reader.set(pullKey("acme/app", 7), PullRequestState{Open: true, HeadSHA: strings.Repeat("a", 40)})
	reader.set(pullKey("acme/other", 9), PullRequestState{Open: true, HeadSHA: strings.Repeat("c", 40)})
	reconciler.maxReads = 1
	for pass := 0; pass < 2; pass++ {
		if err := reconciler.Reconcile(ctx); err != nil {
			t.Fatal(err)
		}
	}
	reads := reader.read()
	sort.Strings(reads)
	if fmt.Sprint(reads) != "[acme/app#7 acme/other#9]" {
		t.Fatalf("two budgeted passes read %v, want one of each preview", reads)
	}
	reconciler.maxReads = previewReconcileReads
	if err := reconciler.ReconcileRepository(ctx, "acme/app"); err != nil {
		t.Fatal(err)
	}
	if reads := reader.read(); len(reads) != 3 || reads[2] != "acme/app#7" {
		t.Fatalf("repository pass read %v", reads)
	}
}

// After one failure in a repository the rest of its previews wait for the
// next pass, so a broken credential costs one read per pass rather than
// the whole budget.
func TestPreviewReconcilerStopsAtARepositorysFirstFailure(t *testing.T) {
	t.Parallel()
	f, _, reader, closer, reconciler := reconcilerFixture(t)
	ctx := context.Background()
	openPreviewFixture(t, f, "acme/app", 8)
	reader.fail(pullKey("acme/app", 7), fmt.Errorf("%w: gh: Could not resolve to a Repository", ErrPullRequestUnreadable))
	reader.set(pullKey("acme/app", 8), PullRequestState{Open: false, Merged: true})
	if err := reconciler.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if reads := reader.read(); fmt.Sprint(reads) != "[acme/app#7]" {
		t.Fatalf("reads = %v, want the pass to stop at the failure", reads)
	}
	if len(closer.closed()) != 0 {
		t.Fatalf("closes = %v", closer.closed())
	}
	reader.fail(pullKey("acme/app", 7), nil)
	reader.set(pullKey("acme/app", 7), PullRequestState{Open: true, HeadSHA: strings.Repeat("a", 40)})
	if err := reconciler.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if got := closer.closed(); fmt.Sprint(got) != fmt.Sprint([]previewClose{{8, true}}) {
		t.Fatalf("closes after the repository recovered = %v", got)
	}
}

func TestPreviewReconcilerStartRunsAPassAndStopWaitsForIt(t *testing.T) {
	t.Parallel()
	_, _, reader, _, reconciler := reconcilerFixture(t)
	reader.set(pullKey("acme/app", 7), PullRequestState{Open: true, HeadSHA: strings.Repeat("a", 40)})
	reconciler.interval = time.Hour
	reconciler.Start(context.Background())
	deadline := time.Now().Add(5 * time.Second)
	for len(reader.read()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	reconciler.Stop()
	if reads := reader.read(); len(reads) != 1 {
		t.Fatalf("reads after start and stop = %v", reads)
	}
	reconciler.Stop()
}
