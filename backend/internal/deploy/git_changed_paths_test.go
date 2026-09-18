package deploy

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestGitChangedPathsComparesCompleteTreesWithoutCheckout(t *testing.T) {
	repository, cache := t.TempDir(), t.TempDir()
	runPlanningGitFixture(t, repository, "init", "--initial-branch", "main")
	runPlanningGitFixture(t, repository, "config", "user.email", "fixture@example.test")
	runPlanningGitFixture(t, repository, "config", "user.name", "Fixture")
	writeBuildFixture(t, repository, "app/deleted.txt", "old\n")
	writeBuildFixture(t, repository, "app/renamed.txt", "rename\n")
	runPlanningGitFixture(t, repository, "add", "-A")
	runPlanningGitFixture(t, repository, "commit", "-m", "first")
	before := strings.TrimSpace(runPlanningGitOutput(t, repository, "rev-parse", "HEAD"))
	if err := os.Remove(filepath.Join(repository, "app/deleted.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(repository, "app/renamed.txt"), filepath.Join(repository, "app/new name.txt")); err != nil {
		t.Fatal(err)
	}
	writeBuildFixture(t, repository, "docs/with\nnewline.md", "new\n")
	runPlanningGitFixture(t, repository, "add", "-A")
	runPlanningGitFixture(t, repository, "commit", "-m", "second")
	after := strings.TrimSpace(runPlanningGitOutput(t, repository, "rev-parse", "HEAD"))
	source := DraftSourceConfig{Kind: SourceGit, Mode: SourceModeGitURL, URL: "https://github.com/acme/app.git", Ref: "main"}
	mirror := filepath.Join(cache, "git-watch-mirrors", strings.TrimPrefix(digestBytes([]byte(source.URL)), "sha256:")+".git")
	if err := os.MkdirAll(filepath.Dir(mirror), 0700); err != nil {
		t.Fatal(err)
	}
	runPlanningGitFixture(t, repository, "clone", "--mirror", "--", repository, mirror)
	analyzer := NewHostSourceAnalyzer(nil, nil, cache, nil, nil)
	paths, err := analyzer.ResolveGitChangedPaths(context.Background(), source, before, after)
	want := []string{"app/deleted.txt", "app/new name.txt", "app/renamed.txt", "docs/with\nnewline.md"}
	if err != nil || !slices.Equal(paths, want) {
		t.Fatalf("changed paths=%q, %v", paths, err)
	}
	if head := strings.TrimSpace(runPlanningGitOutput(t, repository, "rev-parse", "HEAD")); head != after {
		t.Fatal("operator checkout changed")
	}
	paths, err = analyzer.ResolveGitChangedPaths(context.Background(), source, after, before)
	if err != nil || !slices.Equal(paths, want) {
		t.Fatalf("force push comparison=%q, %v", paths, err)
	}
}
