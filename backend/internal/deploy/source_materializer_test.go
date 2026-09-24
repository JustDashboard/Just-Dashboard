package deploy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLocalGitMaterializationUsesRecordedRevisionWithoutChangingWorkbench(t *testing.T) {
	t.Parallel()
	repository := t.TempDir()
	runPlanningGitFixture(t, repository, "init")
	runPlanningGitFixture(t, repository, "config", "user.email", "fixture@example.test")
	runPlanningGitFixture(t, repository, "config", "user.name", "Fixture")
	writeBuildFixture(t, repository, "message.txt", "release-one\n")
	runPlanningGitFixture(t, repository, "add", "message.txt")
	runPlanningGitFixture(t, repository, "commit", "-m", "release one")
	revisionOne := strings.TrimSpace(runPlanningGitOutput(t, repository, "rev-parse", "HEAD"))
	writeBuildFixture(t, repository, "message.txt", "release-two\n")
	runPlanningGitFixture(t, repository, "add", "message.txt")
	runPlanningGitFixture(t, repository, "commit", "-m", "release two")
	revisionTwo := strings.TrimSpace(runPlanningGitOutput(t, repository, "rev-parse", "HEAD"))

	cache, workspaces := t.TempDir(), t.TempDir()
	analyzer := NewHostSourceAnalyzer([]string{repository}, nil, cache, nil, nil)
	source := DraftSourceConfig{Kind: SourceLocal, Mode: SourceModeLocalCheckout, LocalPath: repository}
	materialized, err := analyzer.Materialize(context.Background(), source,
		SourceIdentity{Kind: SourceLocal, LocalPath: repository, Revision: revisionOne},
		41, workspaces)
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(materialized.Root, "message.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "release-one\n" {
		t.Fatalf("materialized content = %q", content)
	}
	current := strings.TrimSpace(runPlanningGitOutput(t, repository, "rev-parse", "HEAD"))
	if current != revisionTwo {
		t.Fatalf("operator checkout moved from %s to %s", revisionTwo, current)
	}
	sentinel := filepath.Join(materialized.Workspace, "sentinel")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	again, err := analyzer.Materialize(context.Background(), source,
		SourceIdentity{Kind: SourceLocal, LocalPath: repository, Revision: revisionOne},
		41, workspaces)
	if err != nil {
		t.Fatal(err)
	}
	if again.Root != materialized.Root {
		t.Fatalf("idempotent root = %q, want %q", again.Root, materialized.Root)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("idempotent materialization rebuilt a proven workspace: %v", err)
	}
	if cleaned, err := materialized.Cleanup(); err != nil || !cleaned {
		t.Fatalf("cleanup = %t, %v", cleaned, err)
	}
	if _, err := os.Stat(materialized.Workspace); !os.IsNotExist(err) {
		t.Fatalf("workspace survived cleanup: %v", err)
	}
}

func TestComposeMaterializationCopiesContainedTreeAndRejectsEscapingSymlink(t *testing.T) {
	t.Parallel()
	sourceRoot := t.TempDir()
	writeBuildFixture(t, sourceRoot, "compose.yml", "services:\n  app:\n    image: example/app:1\n")
	writeBuildFixture(t, sourceRoot, "shared/value.txt", "inside\n")
	if err := os.Symlink("shared/value.txt", filepath.Join(sourceRoot, "inside-link")); err != nil {
		t.Fatal(err)
	}
	analyzer := NewHostSourceAnalyzer([]string{sourceRoot}, []string{sourceRoot}, t.TempDir(), nil, nil)
	source := DraftSourceConfig{
		Kind: SourceCompose, Mode: SourceModeComposeLocal, LocalPath: sourceRoot,
		ComposeFiles: []ComposeDocument{{Path: "compose.yml", Order: 0}},
	}
	materialized, err := analyzer.Materialize(context.Background(), source,
		SourceIdentity{Kind: SourceCompose, LocalPath: sourceRoot, Digest: fakeContentDigest("compose")},
		42, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	link, err := os.Readlink(filepath.Join(materialized.Root, "inside-link"))
	if err != nil || link != "shared/value.txt" {
		t.Fatalf("contained symlink = %q, %v", link, err)
	}

	unsafe := t.TempDir()
	writeBuildFixture(t, unsafe, "compose.yml", "services: {}\n")
	if err := os.Symlink("../outside", filepath.Join(unsafe, "escape")); err != nil {
		t.Fatal(err)
	}
	unsafeAnalyzer := NewHostSourceAnalyzer([]string{unsafe}, []string{unsafe}, t.TempDir(), nil, nil)
	_, err = unsafeAnalyzer.Materialize(context.Background(), DraftSourceConfig{
		Kind: SourceCompose, Mode: SourceModeComposeLocal, LocalPath: unsafe,
		ComposeFiles: []ComposeDocument{{Path: "compose.yml", Order: 0}},
	}, SourceIdentity{Kind: SourceCompose, LocalPath: unsafe, Digest: fakeContentDigest("unsafe")}, 43, t.TempDir())
	if !errors.Is(err, ErrInvalidSource) {
		t.Fatalf("escaping symlink error = %v", err)
	}
}

func TestInlineComposeMaterializationPreservesFileOrderAndPrivacy(t *testing.T) {
	t.Parallel()
	workspaces := t.TempDir()
	analyzer := NewHostSourceAnalyzer(nil, nil, t.TempDir(), nil, nil)
	source := DraftSourceConfig{
		Kind: SourceCompose, Mode: SourceModeComposePaste,
		ComposeFiles: []ComposeDocument{
			{Path: "compose.yml", Content: "services: {}\n", Order: 0},
			{Path: "overrides/prod.yml", Content: "services: {}\n", Order: 1},
		},
	}
	materialized, err := analyzer.Materialize(context.Background(), source,
		SourceIdentity{Kind: SourceCompose, Digest: fakeContentDigest("inline")},
		44, workspaces)
	if err != nil {
		t.Fatal(err)
	}
	for _, relative := range []string{"compose.yml", "overrides/prod.yml"} {
		if _, err := os.Stat(filepath.Join(materialized.Root, relative)); err != nil {
			t.Fatalf("missing materialized %s: %v", relative, err)
		}
	}
	info, err := os.Stat(materialized.Workspace)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("workspace mode = %v, %v", info, err)
	}
}

// A branch that moved on since the run was planned still holds the recorded
// commit in its history; only one rewritten out of that history is gone.
func TestReleaseMirrorFindsARecordedCommitTheBranchMovedPast(t *testing.T) {
	t.Parallel()
	upstream := t.TempDir()
	runPlanningGitFixture(t, upstream, "init", "-q", "-b", "main")
	runPlanningGitFixture(t, upstream, "config", "user.email", "fixture@example.test")
	runPlanningGitFixture(t, upstream, "config", "user.name", "Fixture")
	commit := func(content string) string {
		writeBuildFixture(t, upstream, "message.txt", content)
		runPlanningGitFixture(t, upstream, "add", "message.txt")
		runPlanningGitFixture(t, upstream, "commit", "-q", "-m", content)
		return strings.TrimSpace(runPlanningGitOutput(t, upstream, "rev-parse", "HEAD"))
	}
	recorded := commit("planned\n")
	commit("pushed after planning\n")
	runPlanningGitFixture(t, upstream, "checkout", "-q", "-b", "side", recorded+"~0")
	sideOnly := commit("never on main\n")
	runPlanningGitFixture(t, upstream, "checkout", "-q", "main")

	environment := append(cleanPlanningGitEnvironment(os.Environ()),
		"GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_SYSTEM=/dev/null", "GIT_CONFIG_GLOBAL=/dev/null")
	mirror := filepath.Join(t.TempDir(), "release.git")
	if err := ensurePlanningMirror(context.Background(), mirror, upstream, environment); err != nil {
		t.Fatal(err)
	}
	if err := fetchReleaseRevision(context.Background(), mirror, environment, "main", recorded); err != nil {
		t.Fatalf("a commit the branch moved past = %v", err)
	}
	target := filepath.Join(t.TempDir(), "source")
	if err := fetchExactGit(context.Background(), mirror, upstream, target, recorded, environment); err != nil {
		t.Fatalf("materializing the recorded commit = %v", err)
	}
	if content, err := os.ReadFile(filepath.Join(target, "message.txt")); err != nil || string(content) != "planned\n" {
		t.Fatalf("materialized %q, %v", content, err)
	}

	err := fetchReleaseRevision(context.Background(), mirror, environment, "main", sideOnly)
	var failure *SourceFailure
	if !errors.As(err, &failure) || failure.Code != "source_revision_unavailable" || !errors.Is(err, ErrSourceUnavailable) {
		t.Fatalf("a commit outside the branch's history = %v", err)
	}
}

func runPlanningGitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	output, err := runPlanningGit(context.Background(), dir, nil, args...)
	if err != nil {
		t.Fatal(err)
	}
	return output
}

func TestExactGitMaterializationSurvivesAShallowBlobFilteredMirror(t *testing.T) {
	t.Parallel()
	repository := t.TempDir()
	runPlanningGitFixture(t, repository, "init", "--initial-branch", "main")
	runPlanningGitFixture(t, repository, "config", "user.email", "fixture@example.test")
	runPlanningGitFixture(t, repository, "config", "user.name", "Fixture")
	writeBuildFixture(t, repository, "message.txt", "release-one\n")
	runPlanningGitFixture(t, repository, "add", "message.txt")
	runPlanningGitFixture(t, repository, "commit", "-m", "release one")
	large := strings.Repeat("large-release-asset\n", 120_000)
	writeBuildFixture(t, repository, "public/asset.bin", large)
	writeBuildFixture(t, repository, "message.txt", "release-two\n")
	runPlanningGitFixture(t, repository, "add", "-A")
	runPlanningGitFixture(t, repository, "commit", "-m", "release two")
	revision := strings.TrimSpace(runPlanningGitOutput(t, repository, "rev-parse", "HEAD"))

	// The planning mirror is shallow, blob filtered, and publishes no branch
	// ref, which is exactly what made a local clone copy nothing at all.
	mirror := filepath.Join(t.TempDir(), "mirror.git")
	runPlanningGitFixture(t, t.TempDir(), "init", "--bare", "--", mirror)
	runPlanningGitFixture(t, mirror, "config", "remote.origin.url", repository)
	runPlanningGitFixture(t, mirror, "fetch", "--force", "--depth=1", "--no-tags",
		"origin", "+refs/heads/main:refs/just-dashboard/planning/one")

	target := filepath.Join(t.TempDir(), "source")
	if err := fetchExactGit(context.Background(), mirror, repository, target, revision, nil); err != nil {
		t.Fatal(err)
	}
	if head := strings.TrimSpace(runPlanningGitOutput(t, target, "rev-parse", "HEAD")); head != revision {
		t.Fatalf("materialized head = %q, want %q", head, revision)
	}
	content, err := os.ReadFile(filepath.Join(target, "public/asset.bin"))
	if err != nil || string(content) != large {
		t.Fatalf("large release asset was not materialized: %v", err)
	}
	origin := strings.TrimSpace(runPlanningGitOutput(t, target, "remote", "get-url", "origin"))
	if origin != repository {
		t.Fatalf("workspace origin = %q, want %q", origin, repository)
	}
}

func TestLocalGitMaterializationRecordsCommitMetadataTruncatingLongSubjects(t *testing.T) {
	t.Parallel()
	repository := t.TempDir()
	runPlanningGitFixture(t, repository, "init")
	runPlanningGitFixture(t, repository, "config", "user.email", "fixture@example.test")
	runPlanningGitFixture(t, repository, "config", "user.name", "Fixture Author")
	writeBuildFixture(t, repository, "message.txt", "release-one\n")
	runPlanningGitFixture(t, repository, "add", "message.txt")
	longSubject := strings.Repeat("x", 250)
	runPlanningGitFixture(t, repository, "commit", "-m", longSubject+"\n\nA longer body line that must not appear in the subject.")
	revision := strings.TrimSpace(runPlanningGitOutput(t, repository, "rev-parse", "HEAD"))

	workspaces := t.TempDir()
	analyzer := NewHostSourceAnalyzer([]string{repository}, nil, t.TempDir(), nil, nil)
	source := DraftSourceConfig{Kind: SourceLocal, Mode: SourceModeLocalCheckout, LocalPath: repository}
	identity := SourceIdentity{Kind: SourceLocal, LocalPath: repository, Revision: revision}
	materialized, err := analyzer.Materialize(context.Background(), source, identity, 51, workspaces)
	if err != nil {
		t.Fatal(err)
	}
	if materialized.Commit == nil {
		t.Fatal("commit metadata is nil for a Git checkout")
	}
	if materialized.Commit.SHA != revision {
		t.Fatalf("commit sha = %q, want %q", materialized.Commit.SHA, revision)
	}
	if materialized.Commit.Author != "Fixture Author" {
		t.Fatalf("commit author = %q", materialized.Commit.Author)
	}
	if subjectLen := len([]rune(materialized.Commit.Subject)); subjectLen != 200 {
		t.Fatalf("commit subject length = %d, want 200 (subject %q)", subjectLen, materialized.Commit.Subject)
	}
	if strings.ContainsAny(materialized.Commit.Subject, "\n\r") || strings.Contains(materialized.Commit.Subject, "body line") {
		t.Fatalf("commit subject leaked the message body: %q", materialized.Commit.Subject)
	}
	if _, err := time.Parse(time.RFC3339, materialized.Commit.AuthoredAt); err != nil {
		t.Fatalf("authoredAt = %q is not RFC3339: %v", materialized.Commit.AuthoredAt, err)
	}

	// The idempotent reuse path (a retried acquire_source step reopening its
	// own already-materialized workspace) reports the same commit rather than
	// leaving it nil.
	again, err := analyzer.Materialize(context.Background(), source, identity, 51, workspaces)
	if err != nil {
		t.Fatal(err)
	}
	if again.Commit == nil || again.Commit.SHA != revision {
		t.Fatalf("reused workspace commit = %#v", again.Commit)
	}
}

func TestMaterializationOmitsCommitMetadataForNonGitSources(t *testing.T) {
	t.Parallel()
	analyzer := NewHostSourceAnalyzer(nil, nil, t.TempDir(), nil, nil)
	source := DraftSourceConfig{
		Kind: SourceCompose, Mode: SourceModeComposePaste,
		ComposeFiles: []ComposeDocument{{Path: "compose.yml", Content: "services: {}\n", Order: 0}},
	}
	materialized, err := analyzer.Materialize(context.Background(), source,
		SourceIdentity{Kind: SourceCompose, Digest: fakeContentDigest("inline")}, 52, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if materialized.Commit != nil {
		t.Fatalf("non-Git source recorded commit metadata: %#v", materialized.Commit)
	}
}

func TestReadCommitMetadataFailsClosedOnAnUnreadableRevision(t *testing.T) {
	t.Parallel()
	repository := t.TempDir()
	runPlanningGitFixture(t, repository, "init")
	runPlanningGitFixture(t, repository, "config", "user.email", "fixture@example.test")
	runPlanningGitFixture(t, repository, "config", "user.name", "Fixture")
	writeBuildFixture(t, repository, "message.txt", "content\n")
	runPlanningGitFixture(t, repository, "add", "message.txt")
	runPlanningGitFixture(t, repository, "commit", "-m", "only commit")

	missing := strings.Repeat("a", 40)
	if _, err := readCommitMetadata(context.Background(), repository, missing); err == nil {
		t.Fatal("expected an error reading a revision the repository does not have")
	}
	if commit := commitMetadataForSource(context.Background(), SourceModeLocalCheckout, repository, missing); commit != nil {
		t.Fatalf("commitMetadataForSource swallowed nothing: %#v", commit)
	}
}
