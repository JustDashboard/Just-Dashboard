package gitx

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func selectedText(t *testing.T, d *PartialDiff, texts ...string) []int {
	t.Helper()
	selected := []int{}
	for _, h := range parseHunks(d.Body) {
		for _, line := range h.lines {
			for _, text := range texts {
				if line.text == text && line.kind != ' ' {
					selected = append(selected, line.id)
				}
			}
		}
	}
	if len(selected) != len(texts) {
		t.Fatalf("selected %d of %d from %s", len(selected), len(texts), d.Body)
	}
	return selected
}

func TestPartialStageAndUnstagePreserveOtherChanges(t *testing.T) {
	dir, git := tempRepo(t)
	write(t, dir, "a.txt", "one\nfirst\nsecond\n")
	s, ctx := New([]string{dir}), context.Background()
	d, err := s.PartialDiff(ctx, dir, "a.txt", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.StagePartial(ctx, dir, PartialStageRequest{File: d.File, Version: d.Version, Lines: selectedText(t, d, "first\n")}); err != nil {
		t.Fatal(err)
	}
	if got := git("show", ":a.txt"); got != "one\nfirst\n" {
		t.Fatalf("staged unexpected lines: %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "a.txt")); string(got) != "one\nfirst\nsecond\n" {
		t.Fatal("worktree changed")
	}
	d, err = s.PartialDiff(ctx, dir, "a.txt", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.StagePartial(ctx, dir, PartialStageRequest{File: d.File, Staged: true, Version: d.Version, Lines: d.Lines}); err != nil {
		t.Fatal(err)
	}
	if got := git("show", ":a.txt"); got != "one\n" {
		t.Fatalf("unstage: %q", got)
	}
}

func TestPartialStageReplacementAndNoNewline(t *testing.T) {
	dir, git := tempRepo(t)
	write(t, dir, "a.txt", "replacement")
	s, ctx := New([]string{dir}), context.Background()
	d, err := s.PartialDiff(ctx, dir, "a.txt", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.StagePartial(ctx, dir, PartialStageRequest{File: d.File, Version: d.Version, Lines: d.Lines}); err != nil {
		t.Fatal(err)
	}
	if got := git("show", ":a.txt"); got != "replacement" {
		t.Fatalf("lost no-newline: %q", got)
	}
	d, err = s.PartialDiff(ctx, dir, "a.txt", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.StagePartial(ctx, dir, PartialStageRequest{File: d.File, Staged: true, Version: d.Version, Lines: d.Lines}); err != nil {
		t.Fatal(err)
	}
	if got := git("show", ":a.txt"); got != "one\n" {
		t.Fatalf("restore: %q", got)
	}
}

func TestPartialStageUsesLiteralUnicodePathsAndRejectsStaleDiffs(t *testing.T) {
	dir, git := tempRepo(t)
	name := "café [1] file.txt"
	write(t, dir, name, "first\nsecond\n")
	s, ctx := New([]string{dir}), context.Background()
	d, err := s.PartialDiff(ctx, dir, name, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.StagePartial(ctx, dir, PartialStageRequest{File: name, Version: d.Version, Lines: selectedText(t, d, "first\n")}); err != nil {
		t.Fatal(err)
	}
	if got := git("show", ":"+name); got != "first\n" {
		t.Fatalf("stage unicode: %q", got)
	}
	if _, err := s.StagePartial(ctx, dir, PartialStageRequest{File: name, Version: d.Version, Lines: d.Lines}); err == nil {
		t.Fatal("accepted stale index")
	}
	d, err = s.PartialDiff(ctx, dir, name, false)
	if err != nil {
		t.Fatal(err)
	}
	write(t, dir, name, "different\n")
	if _, err := s.StagePartial(ctx, dir, PartialStageRequest{File: name, Version: d.Version, Lines: d.Lines}); err == nil {
		t.Fatal("accepted stale working tree")
	}
	if got := git("show", ":"+name); got != "first\n" {
		t.Fatalf("stale request changed index: %q", got)
	}
}

func TestPartialUnstageSelectedAddedLineKeepsTheOtherStagedLines(t *testing.T) {
	dir, git := tempRepo(t)
	write(t, dir, "new.txt", "first\nsecond\nthird\n")
	git("add", "new.txt")
	s, ctx := New([]string{dir}), context.Background()
	d, err := s.PartialDiff(ctx, dir, "new.txt", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.StagePartial(ctx, dir, PartialStageRequest{File: d.File, Staged: true, Version: d.Version, Lines: selectedText(t, d, "second\n")}); err != nil {
		t.Fatal(err)
	}
	if got := git("show", ":new.txt"); got != "first\nthird\n" {
		t.Fatalf("unstaged other changes: %q", got)
	}
	d, err = s.PartialDiff(ctx, dir, "new.txt", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.StagePartial(ctx, dir, PartialStageRequest{File: d.File, Staged: true, Version: d.Version, Lines: d.Lines}); err != nil {
		t.Fatal(err)
	}
	if got := git("ls-files", "--", "new.txt"); got != "" {
		t.Fatalf("new file still staged: %q", got)
	}
}

func TestPartialStageDeletionAndSeparatedHunks(t *testing.T) {
	dir, git := tempRepo(t)
	write(t, dir, "a.txt", "begin\n"+strings.Repeat("unchanged\n", 20)+"end\n")
	git("commit", "-qam", "long file")
	write(t, dir, "a.txt", "BEGIN\n"+strings.Repeat("unchanged\n", 20)+"END\n")
	s, ctx := New([]string{dir}), context.Background()
	d, err := s.PartialDiff(ctx, dir, "a.txt", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.StagePartial(ctx, dir, PartialStageRequest{File: d.File, Version: d.Version, Lines: selectedText(t, d, "end\n", "END\n")}); err != nil {
		t.Fatal(err)
	}
	if got := git("show", ":a.txt"); !strings.HasPrefix(got, "begin\n") || !strings.HasSuffix(got, "END\n") {
		t.Fatalf("mixed hunks: %q", got)
	}
	git("reset", "-q", "HEAD")
	if err := os.Remove(filepath.Join(dir, "a.txt")); err != nil {
		t.Fatal(err)
	}
	d, err = s.PartialDiff(ctx, dir, "a.txt", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.StagePartial(ctx, dir, PartialStageRequest{File: d.File, Version: d.Version, Lines: d.Lines}); err != nil {
		t.Fatal(err)
	}
	if got := git("ls-files", "--", "a.txt"); got != "" {
		t.Fatalf("deleted file still in index: %s", got)
	}
}
