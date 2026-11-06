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

// Asked nothing, a read lists the top folder, folders first, and reads the README
// whatever its case: what a person opens to learn what the project is. Asked for
// paths, it reads the commit the deployment would build, not the working tree.
func TestReadSourceReadsTheReadmeOfTheCommitItWouldBuild(t *testing.T) {
	repository, revision := gitTree(t, map[string]string{
		"Readme.md":    "# Shop\n\nA storefront for hand-made mugs.\n",
		"package.json": `{"name":"shop","scripts":{"start":"next start"}}`,
		"src/app.ts":   "export const app = 1\n",
	})
	// An edit nobody committed is not what would be built.
	if err := os.WriteFile(filepath.Join(repository, "Readme.md"), []byte("# Uncommitted\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fixture := newReleaseStoreFixture(t)
	fixture.setProfile(t, ProfileService)
	fixture.addSourcePlan(t, 1, repository, revision, BuildPlanConfig{Method: BuildRecipe, Recipe: "node"}, StoredBuildEvidence{})
	checker := &DeploymentChecker{runs: fixture.runs, planning: fixture.variables, now: time.Now,
		sources: NewHostSourceAnalyzer([]string{repository}, nil, t.TempDir(), nil, nil)}

	read, err := checker.ReadSource(context.Background(), fixture.projectID, fixture.envID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if read.Commit != revision || len(read.Folders) != 1 || len(read.Files) != 1 {
		t.Fatalf("read = %+v", read)
	}
	if entries := read.Folders[0].Entries; len(entries) != 3 || entries[0].Name != "src" || entries[0].Kind != "folder" {
		t.Fatalf("top folder = %+v", entries)
	}
	if file := read.Files[0]; file.Path != "Readme.md" || !strings.Contains(file.Content, "hand-made mugs") {
		t.Fatalf("README = %+v", file)
	}

	read, err = checker.ReadSource(context.Background(), fixture.projectID, fixture.envID, []string{"/package.json", "src", "nope.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if len(read.Files) != 1 || read.Files[0].Path != "package.json" || !strings.Contains(read.Files[0].Content, "next start") ||
		len(read.Folders) != 1 || read.Folders[0].Path != "src" || len(read.Unread) != 1 || !strings.Contains(read.Unread[0], "nope.txt") {
		t.Fatalf("read = %+v", read)
	}
}

// A deployment with no saved plan has no source to read, and says so in words.
func TestReadSourceSaysWhyThereIsNothingToRead(t *testing.T) {
	fixture := newReleaseStoreFixture(t)
	checker := &DeploymentChecker{runs: fixture.runs, planning: fixture.variables, now: time.Now}
	_, err := checker.ReadSource(context.Background(), fixture.projectID, fixture.envID, nil)
	if err == nil {
		t.Fatal("a deployment with no source was read")
	}
	if why := SourceReadFailure(ErrSourceHasNoTree); !strings.Contains(why, "not built from a repository") {
		t.Fatalf("why = %q", why)
	}
	if why := SourceReadFailure(errors.Join(ErrSourceUnavailable, ErrGitUnavailable)); !strings.Contains(why, "could not be fetched") {
		t.Fatalf("why = %q", why)
	}
}

// Whatever path the model writes, the read stays inside the checkout: a way up is
// read from the top, a link out is refused, the Git metadata is not read, a
// binary file is named without its bytes, and the read stops at its size.
func TestReadSourceStaysInsideTheCheckoutAndItsSize(t *testing.T) {
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	for name, content := range map[string]string{
		"etc/passwd":  "inside",
		"logo.png":    "\x89PNG\x00\x00",
		"big/a.txt":   strings.Repeat("a", sourceReadFileMax+10),
		"big/b.txt":   strings.Repeat("b", sourceReadFileMax),
		".git/config": "[remote]",
	} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(outside, "secret"), filepath.Join(root, "leak")); err != nil {
		t.Fatal(err)
	}
	read := &SourceRead{}
	if err := readSourceTree(root, []string{"../../etc/passwd", "leak", ".git/config", "logo.png", "big/a.txt", "big/b.txt"}, read); err != nil {
		t.Fatal(err)
	}
	byPath := map[string]SourceFile{}
	for _, file := range read.Files {
		byPath[file.Path] = file
	}
	if byPath["etc/passwd"].Content != "inside" {
		t.Fatalf("a way up was not read from the top: %+v", read)
	}
	if _, ok := byPath["leak"]; ok {
		t.Fatalf("a link out of the checkout was read: %+v", read)
	}
	if file := byPath["logo.png"]; !file.Binary || file.Content != "" {
		t.Fatalf("binary = %+v", file)
	}
	if file := byPath["big/a.txt"]; !file.Cut || len(file.Content) != sourceReadFileMax || file.Bytes != sourceReadFileMax+10 {
		t.Fatalf("a long file = cut %v, %d bytes", file.Cut, len(file.Content))
	}
	if file := byPath["big/b.txt"]; len(file.Content) != sourceReadTotalMax-sourceReadFileMax-len("inside") || !file.Cut {
		t.Fatalf("the second file was not held to what the read had left: %d bytes", len(file.Content))
	}
	unread := strings.Join(read.Unread, "\n")
	for _, want := range []string{"leak: a link that leads out of the repository", ".git/config: the repository's Git metadata is not read"} {
		if !strings.Contains(unread, want) {
			t.Errorf("unread lacks %q: %s", want, unread)
		}
	}

	listing := &SourceRead{}
	if err := readSourceTree(root, []string{"."}, listing); err != nil {
		t.Fatal(err)
	}
	for _, entry := range listing.Folders[0].Entries {
		if entry.Name == ".git" {
			t.Fatalf("the listing shows .git: %+v", listing.Folders[0].Entries)
		}
	}
}
