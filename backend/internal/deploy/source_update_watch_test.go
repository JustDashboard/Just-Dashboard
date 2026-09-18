package deploy

import (
	"context"
	"strings"
	"testing"
)

// TestGitWatcherReportsBranchChangedAfterASourceUpdateEvenWithTheSameResolvedRevision
// proves the source-change route needs no watcher-side code at all: the
// watcher's cursor is already keyed on the desired source's own config
// digest (gitWatchTargets' SourceKey), so writing a new source revision
// through SaveEnvironmentSource alone resets that cursor, and the very next
// poll reports branch_changed and dispatches once — even though the fake
// resolver here returns the same commit it always does, which is exactly
// the point: what changed is the branch being watched, not (yet) the remote.
func TestGitWatcherReportsBranchChangedAfterASourceUpdateEvenWithTheSameResolvedRevision(t *testing.T) {
	f, resolver, watcher := gitWatchFixture(t)
	ctx := context.Background()

	// enqueueGitFixture pre-seeds a run at the resolver's own revision, so the
	// very first poll reads as "already_attempted" rather than "watching" —
	// this only needs LatestRunID != 0 so the target is not skipped as
	// "awaiting first deployment".
	enqueueGitFixture(t, f, resolver.revision, TriggerManual)
	if err := watcher.poll(ctx); err != nil {
		t.Fatal(err)
	}
	assertGitRunCount(t, f, 1)
	status, err := f.runs.GitWatchStatus(ctx, f.projectID, f.envID)
	if err != nil || status.Branch != "main" {
		t.Fatalf("status after the first observation = %+v, %v", status, err)
	}

	// The identity recorded alongside the new source reflects what detection
	// found for "develop" when the operator saved it — the same commit the
	// resolver already knows about. The branch itself only really "changes"
	// once the watcher's own next poll resolves a different commit there,
	// exactly as force-pushing or fast-forwarding a real branch would.
	newSource := DraftSourceConfig{Kind: SourceGit, Mode: SourceModeGitURL, URL: "https://github.com/acme/app.git", Ref: "develop"}
	if _, err := f.variables.SaveEnvironmentSource(ctx, f.projectID, f.envID, 1, newSource, SourceIdentity{
		Kind: SourceGit, Remote: newSource.URL, Repository: "acme/app", Ref: "develop", Revision: resolver.revision,
	}); err != nil {
		t.Fatal(err)
	}
	resolver.revision = strings.Repeat("b", 40)

	if err := watcher.poll(ctx); err != nil {
		t.Fatal(err)
	}
	assertGitRunCount(t, f, 2)
	status, err = f.runs.GitWatchStatus(ctx, f.projectID, f.envID)
	if err != nil {
		t.Fatal(err)
	}
	if status.Branch != "develop" {
		t.Fatalf("status branch = %q, want develop", status.Branch)
	}
	if status.Reason != "branch_changed" {
		t.Fatalf("status reason = %q, want branch_changed", status.Reason)
	}

	// A second poll with nothing new observes no further change.
	if err := watcher.poll(ctx); err != nil {
		t.Fatal(err)
	}
	assertGitRunCount(t, f, 2)
}
