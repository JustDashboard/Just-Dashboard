package deploy

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/files"
)

func TestProjectValidateUsesSymlinkAwareDeploymentResolver(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	p := validProject(filepath.Join(root, "escape"))

	if err := p.Validate(files.New([]string{root})); err == nil {
		t.Fatal("Validate accepted a repository symlink that resolves outside JD_DEPLOY_ROOTS")
	}
}

func TestProjectValidateCanonicalizesRepositoryAndComposePaths(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	p := validProject(filepath.Join(root, ".", "repo"))
	p.ComposeFile = "ops/../compose.yml"

	if err := p.Validate(files.New([]string{root})); err != nil {
		t.Fatal(err)
	}
	if p.RepoPath != repo {
		t.Fatalf("RepoPath = %q, want %q", p.RepoPath, repo)
	}
	if p.ComposeFile != "compose.yml" {
		t.Fatalf("ComposeFile = %q, want compose.yml", p.ComposeFile)
	}
}

func TestProjectValidateRejectsParentComposePath(t *testing.T) {
	p := validProject(t.TempDir())
	p.ComposeFile = "../compose.yml"
	if err := p.Validate(files.New([]string{p.RepoPath})); err == nil {
		t.Fatal("Validate accepted a Compose file outside the repository")
	}
}

func TestProjectValidateRejectsComposeSymlinkOutsideDeploymentRoots(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "compose.yml")
	if err := os.WriteFile(outside, []byte("services: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(repo, "compose.yml")); err != nil {
		t.Fatal(err)
	}
	p := validProject(repo)
	if err := p.Validate(files.New([]string{root})); err == nil {
		t.Fatal("Validate accepted a Compose symlink outside JD_DEPLOY_ROOTS")
	}
}

func validProject(repo string) *Project {
	return &Project{Name: "example", RepoPath: repo, Branch: "main", ComposeFile: "compose.yml"}
}

func TestTheMaskSaysNothingAboutTheValueButThatItIsSet(t *testing.T) {
	if got := mask(""); got != "" {
		t.Fatalf("mask(\"\") = %q, want empty", got)
	}
	want := mask("x")
	for _, value := range []string{"abc", "hunter2", "sk_live_0123456789abcdef", "postgres://u:p@db/app"} {
		if got := mask(value); got != want {
			t.Fatalf("mask(%q) = %q, want the same placeholder as every other value (%q)", value, got, want)
		}
	}
	for _, r := range want {
		if r != '•' {
			t.Fatalf("placeholder %q carries a character other than •", want)
		}
	}
}
