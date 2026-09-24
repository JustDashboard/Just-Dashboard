package deploy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A specific version is checked before it is deployed. When the branch has
// moved past it, the planning mirror fetches that commit by id the same
// bounded way it fetches the branch, and the worktree it checks out is that
// commit — never the branch head standing in for it.
func TestInspectRevisionFetchesAPinnedCommitTheBranchMovedPast(t *testing.T) {
	bin := t.TempDir()
	capture := filepath.Join(t.TempDir(), "argv")
	script := `#!/bin/sh
set -eu
printf '%s\n' "$*" >> "$INSPECT_GIT_ARGV"
case "$1" in
  init)
    destination=''
    for argument in "$@"; do destination="$argument"; done
    mkdir -p "$destination"
    ;;
  config)
    ;;
  fetch)
    case "$*" in
      *refs/just-dashboard/planning/pinned*)
        if test "${INSPECT_GIT_FAIL_PIN:-}" = 1; then exit 128; fi
        ;;
    esac
    ;;
  rev-parse)
    case "$2" in
      HEAD|refs/just-dashboard/planning/pinned) printf '%s\n' "$INSPECT_GIT_PINNED" ;;
      *) printf '%s\n' "$INSPECT_GIT_HEAD" ;;
    esac
    ;;
  worktree)
    if test "$2" = add; then
      after_separator=0
      destination=''
      for argument in "$@"; do
        if test "$after_separator" = 1; then destination="$argument"; break; fi
        if test "$argument" = --; then after_separator=1; fi
      done
      mkdir -p "$destination"
      printf '%s\n' '{"name":"pinned","scripts":{"start":"node index.js"}}' > "$destination/package.json"
    elif test "$2" = remove; then
      after_separator=0
      destination=''
      for argument in "$@"; do
        if test "$after_separator" = 1; then destination="$argument"; break; fi
        if test "$argument" = --; then after_separator=1; fi
      done
      rm -r "$destination"
    fi
    ;;
  *)
    exit 22
    ;;
esac
`
	gitPath := filepath.Join(bin, "git")
	writePlanningFixture(t, gitPath, script)
	if err := os.Chmod(gitPath, 0o755); err != nil {
		t.Fatal(err)
	}
	pinned, head := strings.Repeat("c", 40), strings.Repeat("d", 40)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("INSPECT_GIT_ARGV", capture)
	t.Setenv("INSPECT_GIT_PINNED", pinned)
	t.Setenv("INSPECT_GIT_HEAD", head)

	analyzer := NewHostSourceAnalyzer(nil, nil, t.TempDir(), nil, nil)
	source := DraftSourceConfig{Kind: SourceGit, Mode: SourceModeGitURL, URL: "https://git.example.test/team/repo.git", Ref: "main"}
	var inspected SourceIdentity
	var manifest []byte
	err := analyzer.InspectRevision(context.Background(), source, SourceIdentity{Kind: SourceGit, Revision: pinned},
		func(root string, identity SourceIdentity) error {
			inspected = identity
			var readErr error
			manifest, readErr = os.ReadFile(filepath.Join(root, "package.json"))
			return readErr
		})
	if err != nil {
		t.Fatal(err)
	}
	if inspected.Revision != pinned || !strings.Contains(string(manifest), "pinned") {
		t.Fatalf("inspected %#v with %s", inspected, manifest)
	}
	argv, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(argv), "ls-remote") {
		t.Fatalf("a pinned inspection asked the remote for its head: %s", argv)
	}
	if !strings.Contains(string(argv), "fetch --force --depth=1 --filter=blob:limit=1048576 --no-tags origin +"+pinned+":refs/just-dashboard/planning/pinned") ||
		!strings.Contains(string(argv), "worktree add --detach --force -- ") {
		t.Fatalf("pinned commit was not fetched and checked out: %s", argv)
	}

	t.Setenv("INSPECT_GIT_FAIL_PIN", "1")
	err = analyzer.InspectRevision(context.Background(), source, SourceIdentity{Kind: SourceGit, Revision: pinned},
		func(string, SourceIdentity) error { t.Fatal("an unfetched commit was inspected"); return nil })
	if !errors.Is(err, ErrSourceUnavailable) || !strings.Contains(err.Error(), "could not be fetched for inspection") {
		t.Fatalf("unfetchable pinned commit = %v", err)
	}

	if err := analyzer.InspectRevision(context.Background(), DraftSourceConfig{Kind: SourceImage, Mode: SourceModeImageReference, Image: "nginx:1"},
		SourceIdentity{}, func(string, SourceIdentity) error { return nil }); !errors.Is(err, ErrSourceHasNoTree) {
		t.Fatalf("an image source was inspected as a tree: %v", err)
	}
}

// A local checkout is read at its recorded commit, never its working tree.
func TestInspectRevisionReadsALocalCheckoutAtItsRecordedCommit(t *testing.T) {
	repository, revision := gitTree(t, map[string]string{"package.json": `{"name":"recorded"}`})
	writeBuildFixture(t, repository, "package.json", `{"name":"uncommitted edit"}`)
	analyzer := NewHostSourceAnalyzer([]string{repository}, nil, t.TempDir(), nil, nil)
	source := DraftSourceConfig{Kind: SourceLocal, Mode: SourceModeLocalCheckout, LocalPath: repository}
	var manifest []byte
	if err := analyzer.InspectRevision(context.Background(), source, SourceIdentity{Kind: SourceLocal, Revision: revision},
		func(root string, _ SourceIdentity) error {
			var err error
			manifest, err = os.ReadFile(filepath.Join(root, "package.json"))
			return err
		}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(manifest), "recorded") {
		t.Fatalf("the working tree was read instead of the recorded commit: %s", manifest)
	}
}
