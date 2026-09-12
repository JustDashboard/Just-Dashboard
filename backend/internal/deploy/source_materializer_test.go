package deploy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
