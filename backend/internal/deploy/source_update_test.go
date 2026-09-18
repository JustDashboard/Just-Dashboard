package deploy

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestSaveEnvironmentSourceRefusesAChangedKindAndAStaleRevision(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newPlanningStoreFixture(t)
	projectID, environmentID := insertConfigurationFixture(t, fixture)

	imageSource := DraftSourceConfig{Kind: SourceImage, Mode: SourceModeImageReference, Image: "alpine:3"}
	if _, err := fixture.plans.SaveEnvironmentSource(ctx, projectID, environmentID, 1, imageSource, SourceIdentity{Kind: SourceImage}); !errors.Is(err, ErrInvalidSource) {
		t.Fatalf("kind change error = %v, want ErrInvalidSource", err)
	}

	gitSource := DraftSourceConfig{Kind: SourceGit, Mode: SourceModeGitURL, URL: "https://example.test/owner/repo.git", Ref: "develop"}
	if _, err := fixture.plans.SaveEnvironmentSource(ctx, projectID, environmentID, 0, gitSource, SourceIdentity{Kind: SourceGit}); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale revision error = %v, want ErrRevisionConflict", err)
	}

	if _, err := fixture.plans.SaveEnvironmentSource(ctx, 999999, environmentID, 1, gitSource, SourceIdentity{Kind: SourceGit}); !errors.Is(err, ErrEnvironmentNotFound) {
		t.Fatalf("unknown project error = %v, want ErrEnvironmentNotFound", err)
	}

	after, err := fixture.plans.SaveEnvironmentSource(ctx, projectID, environmentID, 1, gitSource, SourceIdentity{
		Kind: SourceGit, Remote: gitSource.URL, Ref: "develop", Revision: strings.Repeat("c", 40),
	})
	if err != nil {
		t.Fatal(err)
	}
	if after.Revision != 2 {
		t.Fatalf("after.Revision = %d, want 2", after.Revision)
	}
	var storedRef, storedIdentityRef string
	if err := fixture.store.DB.QueryRow(
		`SELECT json_extract(config_json,'$.ref'), json_extract(identity_json,'$.ref')
		   FROM deploy_sources WHERE environment_id = ? AND revision = 2`, environmentID).
		Scan(&storedRef, &storedIdentityRef); err != nil {
		t.Fatal(err)
	}
	if storedRef != "develop" || storedIdentityRef != "develop" {
		t.Fatalf("stored source ref = %q, identity ref = %q", storedRef, storedIdentityRef)
	}
	// Build and runtime clone forward untouched; only the source itself is new.
	var buildRevisions, runtimeRevisions int
	fixture.store.DB.QueryRow(`SELECT COUNT(*) FROM deploy_build_plans WHERE environment_id = ? AND revision = 2`, environmentID).Scan(&buildRevisions)
	fixture.store.DB.QueryRow(`SELECT COUNT(*) FROM deploy_runtime_plans WHERE environment_id = ? AND revision = 2`, environmentID).Scan(&runtimeRevisions)
	if buildRevisions != 1 || runtimeRevisions != 1 {
		t.Fatalf("build/runtime rows cloned to revision 2 = %d, %d, want 1, 1", buildRevisions, runtimeRevisions)
	}

	// The old desired revision is now stale.
	if _, err := fixture.plans.SaveEnvironmentSource(ctx, projectID, environmentID, 1, gitSource, SourceIdentity{Kind: SourceGit}); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("reusing the old revision error = %v, want ErrRevisionConflict", err)
	}
}

func TestSaveEnvironmentSourceRefusesAnUnknownCredential(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newPlanningStoreFixture(t)
	projectID, environmentID := insertConfigurationFixture(t, fixture)
	source := DraftSourceConfig{
		Kind: SourceGit, Mode: SourceModeGitURL, URL: "https://example.test/owner/repo.git",
		CredentialID: 777777,
	}
	if _, err := fixture.plans.SaveEnvironmentSource(ctx, projectID, environmentID, 1, source, SourceIdentity{Kind: SourceGit}); !errors.Is(err, ErrInvalidSource) {
		t.Fatalf("unknown credential error = %v, want ErrInvalidSource", err)
	}
}

// TestSaveEnvironmentSourceChangeIsPickedUpByPendingStateAsASourceChange
// proves the new route needs no changes to PendingState: it already compares
// the live release's frozen source digest against the desired one, so
// writing a new source revision alone is what makes it report a "source"
// pending change.
func TestSaveEnvironmentSourceChangeIsPickedUpByPendingStateAsASourceChange(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fixture := newReleaseStoreFixture(t)
	fixture.addPlan(t, 1, strings.Repeat("a", 40))
	run, lease := fixture.claimedRun(t, 1)
	release := fixture.candidate(t, *run, lease, fakeContentDigest("release-one"))
	fixture.finishCandidate(t, release.Release.ID, run.ID, lease.Token)

	// addPlan's own source is SourceLocal at /srv/release-fixture; the kind
	// must stay the same, so the "change" here is the path, mirroring an
	// operator editing a local checkout's directory.
	newSource := DraftSourceConfig{Kind: SourceLocal, Mode: SourceModeLocalCheckout, LocalPath: "/srv/release-fixture-two"}
	if _, err := fixture.variables.SaveEnvironmentSource(ctx, fixture.projectID, fixture.envID, 1, newSource, SourceIdentity{
		Kind: SourceLocal, LocalPath: newSource.LocalPath, Revision: strings.Repeat("b", 40),
	}); err != nil {
		t.Fatal(err)
	}
	state, err := fixture.variables.PendingState(ctx, fixture.projectID, fixture.envID)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Pending || len(state.Changes) != 1 || state.Changes[0].Kind != "source" || state.Changes[0].Change != "changed" {
		t.Fatalf("pending state after a source change = %#v", state)
	}
}
