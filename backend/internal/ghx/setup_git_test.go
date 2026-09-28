package ghx

import (
	"context"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Signing in on the Git page wrote gh's own helper line, which names this
// image's /usr/bin/gh. The Terminal page and ssh run git on the host, where
// that path is not gh, so their pushes asked for a username. What is left
// behind must name gh without a path, keep gh's blank reset entry in front of
// it, and read as unset until then so the page offers the repair.
func TestSetupGitLeavesGhToThePathOnEachSide(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, ".gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	gitConfig := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"config", "--global"}, args...)...).CombinedOutput()
		if err != nil && len(out) > 0 {
			t.Fatalf("git config %v: %s", args, out)
		}
		return string(out)
	}
	gitConfig("credential.helper", "store")

	s := New()
	s.command = func(_ context.Context, _, _ string, args ...string) (string, error) {
		if strings.Join(args, " ") != "auth setup-git --hostname github.com" {
			t.Fatalf("unexpected gh %v", args)
		}
		// Exactly what gh writes, for the host and its gist host.
		for _, h := range []string{"github.com", "gist.github.com"} {
			key := "credential.https://" + h + ".helper"
			gitConfig("--unset-all", key)
			gitConfig("--add", key, "")
			gitConfig("--add", key, "!/usr/bin/gh auth git-credential")
		}
		return "", nil
	}
	ctx := context.Background()
	dir := t.TempDir()

	if _, err := s.run(ctx, dir, "", "auth", "setup-git", "--hostname", "github.com"); err != nil {
		t.Fatal(err)
	}
	if s.credentialHelperSet(ctx, dir, "github.com") {
		t.Fatal("a helper pinned to /usr/bin/gh reads as set up")
	}

	if err := s.setupGit(ctx, dir, "github.com"); err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{"github.com", "gist.github.com"} {
		got := strings.Split(strings.TrimRight(gitConfig("--get-all", "credential.https://"+h+".helper"), "\n"), "\n")
		if want := []string{"", portableHelper}; !reflect.DeepEqual(got, want) {
			t.Fatalf("%s helpers = %q, want %q", h, got, want)
		}
	}
	if got := strings.TrimSpace(gitConfig("--get", "credential.helper")); got != "store" {
		t.Fatalf("generic helper = %q, want it untouched", got)
	}
	if !s.credentialHelperSet(ctx, dir, "github.com") {
		t.Fatal("the rewritten helper does not read as set up")
	}
}

func TestPathBoundHelpers(t *testing.T) {
	config := strings.Join([]string{
		"credential.helper store",
		"credential.https://github.com.helper ",
		"credential.https://github.com.helper !/usr/bin/gh auth git-credential",
		"credential.https://gist.github.com.helper !'/opt/my tools/gh' auth git-credential",
		"credential.https://ghe.example.com.helper !gh auth git-credential",
		"credential.https://gitlab.com.helper manager",
	}, "\n")
	got := pathBoundHelpers(config)
	want := []string{"credential.https://github.com.helper", "credential.https://gist.github.com.helper"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}
