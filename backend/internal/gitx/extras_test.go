package gitx

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPatchExportCheckImportAndContainment(t *testing.T) {
	dir, git := tempRepo(t)
	ctx := context.Background()
	s := New([]string{dir})
	write(t, dir, "a.txt", "imported\n")
	git("add", "a.txt")
	patch, err := s.ExportPatch(ctx, dir, "staged", "")
	if err != nil {
		t.Fatal(err)
	}
	git("reset", "--hard", "HEAD")
	req := PatchRequest{Body: patch, Staged: true}
	check, err := s.CheckPatch(ctx, dir, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(check.Files) != 1 || check.Files[0] != "a.txt" {
		t.Fatal(check)
	}
	req.Version = "wrong"
	if _, err := s.ImportPatch(ctx, dir, req); err == nil {
		t.Fatal("accepted stale patch")
	}
	req.Version = check.Version
	if _, err := s.ImportPatch(ctx, dir, req); err != nil {
		t.Fatal(err)
	}
	if got := git("show", ":a.txt"); got != "imported\n" {
		t.Fatal(got)
	}
	if _, err := s.CheckPatch(ctx, dir, req); err == nil {
		t.Fatal("overwrote existing changes")
	}
	git("reset", "--hard", "HEAD")
	for _, path := range []string{"../outside", ".git/config", "link/file"} {
		if strings.HasPrefix(path, "link/") {
			if err := os.Symlink(t.TempDir(), filepath.Join(dir, "link")); err != nil {
				t.Fatal(err)
			}
		}
		bad := strings.ReplaceAll(patch, "a.txt", path)
		if _, err := s.CheckPatch(ctx, dir, PatchRequest{Body: bad}); err == nil {
			t.Fatalf("accepted %s", path)
		}
	}
}

func TestSubmoduleInventoryUpdateAndDirtyRemoval(t *testing.T) {
	dir, git := tempRepo(t)
	child, _ := tempRepo(t)
	ctx := context.Background()
	s := New([]string{dir})
	git("-c", "protocol.file.allow=always", "submodule", "add", child, "vendor/lib")
	git("commit", "-am", "Add library")
	rows, err := s.Submodules(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || !rows[0].Initialized || rows[0].Head != rows[0].Expected || rows[0].Dirty {
		t.Fatal(rows)
	}
	if _, err := s.ManageSubmodule(ctx, dir, SubmoduleRequest{Action: "update", Path: "vendor/lib"}); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "vendor/lib/a.txt", "changed")
	if _, err := s.ManageSubmodule(ctx, dir, SubmoduleRequest{Action: "remove", Path: "vendor/lib"}); err == nil {
		t.Fatal("removed dirty checkout")
	}
	write(t, dir, "vendor/lib/a.txt", "one\n")
	if _, err := s.ManageSubmodule(ctx, dir, SubmoduleRequest{Action: "deinit", Path: "vendor/lib"}); err != nil {
		t.Fatal(err)
	}
	rows, err = s.Submodules(ctx, dir)
	if err != nil || rows[0].Initialized {
		t.Fatal(rows, err)
	}
	if _, err := s.ManageSubmodule(ctx, dir, SubmoduleRequest{Action: "add", Path: "../outside", URL: "https://example.com/repo"}); err == nil {
		t.Fatal("accepted outside submodule")
	}
	if _, err := s.ManageSubmodule(ctx, dir, SubmoduleRequest{Action: "add", Path: "evil", URL: "ext::oops"}); err == nil {
		t.Fatal("accepted command transport")
	}
}

func TestExportRootAndMergeCommitPatches(t *testing.T) {
	dir, git := tempRepo(t)
	s := New([]string{dir})
	ctx := context.Background()
	root, err := s.ExportPatch(ctx, dir, "commit", "HEAD")
	if err != nil || !strings.Contains(root, "+one") {
		t.Fatal(root, err)
	}
	git("checkout", "-b", "feature")
	write(t, dir, "feature.txt", "feature\n")
	git("add", "feature.txt")
	git("commit", "-m", "Feature")
	git("checkout", "main")
	write(t, dir, "main.txt", "main\n")
	git("add", "main.txt")
	git("commit", "-m", "Main")
	git("merge", "--no-ff", "--no-edit", "feature")
	patch, err := s.ExportPatch(ctx, dir, "commit", "HEAD")
	if err != nil || !strings.Contains(patch, "+feature") {
		t.Fatal(patch, err)
	}
	git("reset", "--hard", "HEAD^1")
	checked, err := s.CheckPatch(ctx, dir, PatchRequest{Body: patch, Staged: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ImportPatch(ctx, dir, PatchRequest{Body: patch, Staged: true, Version: checked.Version}); err != nil {
		t.Fatal(err)
	}
	if got := git("show", ":feature.txt"); got != "feature\n" {
		t.Fatal(got)
	}
}

func TestLFSLifecycleUsesRepositoryConfig(t *testing.T) {
	if _, err := exec.LookPath("git-lfs"); err != nil {
		t.Skip("git-lfs is an optional host tool; run with the runtime binary on PATH")
	}
	dir, git := tempRepo(t)
	s := New([]string{dir})
	ctx := context.Background()
	if _, err := s.ManageLFS(ctx, dir, "install", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ManageLFS(ctx, dir, "track", "*.bin"); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "asset.bin", "large binary content\x00")
	git("add", ".gitattributes", "asset.bin")
	if got := git("show", ":asset.bin"); !strings.Contains(got, "version https://git-lfs.github.com/spec/v1") {
		t.Fatal(got)
	}
	status, err := s.LFS(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Available || !strings.Contains(status.Files, "asset.bin") || !strings.Contains(status.Patterns, "*.bin") {
		t.Fatal(status)
	}
	if _, err := s.ManageLFS(ctx, dir, "untrack", "*.bin"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ManageLFS(ctx, dir, "track", "--evil"); err == nil {
		t.Fatal("accepted option pattern")
	}
}

func TestSubmoduleConflictCanRecordEitherCommit(t *testing.T) {
	dir, git := tempRepo(t)
	child, childGit := tempRepo(t)
	ctx := context.Background()
	s := New([]string{dir})
	before := strings.TrimSpace(childGit("rev-parse", "HEAD"))
	write(t, child, "a.txt", "second\n")
	childGit("commit", "-am", "Second")
	after := strings.TrimSpace(childGit("rev-parse", "HEAD"))
	git("-c", "protocol.file.allow=always", "submodule", "add", child, "vendor/lib")
	git("commit", "-am", "Submodule")
	cmd := exec.Command("git", "update-index", "--index-info")
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader("0 " + strings.Repeat("0", 40) + "\tvendor/lib\n160000 " + before + " 1\tvendor/lib\n160000 " + before + " 2\tvendor/lib\n160000 " + after + " 3\tvendor/lib\n")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatal(string(out), err)
	}
	conflict, err := s.Conflict(ctx, dir, "vendor/lib")
	if err != nil {
		t.Fatal(err)
	}
	if conflict.Editable || conflict.Ours.Object != before || conflict.Theirs.Object != after {
		t.Fatal(conflict)
	}
	if _, err := s.ResolveConflict(ctx, dir, ResolveConflictRequest{File: "vendor/lib", Version: conflict.Version, Choice: "theirs"}); err != nil {
		t.Fatal(err)
	}
	if got := git("ls-files", "--stage", "vendor/lib"); !strings.Contains(got, "160000 "+after+" 0") {
		t.Fatal(got)
	}
}
