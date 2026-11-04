package sysinfo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// tree writes files of the given sizes under root, by their paths.
func tree(t *testing.T, root string, files map[string]int) {
	t.Helper()
	for path, size := range files {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(strings.Repeat("x", size)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func byName(entries []DirEntry) map[string]DirEntry {
	out := map[string]DirEntry{}
	for _, e := range entries {
		out[e.Name] = e
	}
	return out
}

var roomy = dirScanLimits{children: 100, visits: 100_000, workers: 4}

func TestDirBreakdownSumsEachChildLargestFirst(t *testing.T) {
	root := t.TempDir()
	files := map[string]int{"small/a": 10, "loose": 25}
	// A wide, deep tree, so the workers share it.
	for i := range 40 {
		files[fmt.Sprintf("big/d%d/e%d/f", i%7, i)] = 100
	}
	tree(t, root, files)
	if err := os.Symlink("/usr", filepath.Join(root, "small", "usr")); err != nil {
		t.Fatal(err)
	}

	entries, err := dirBreakdown(context.Background(), root, 10, roomy, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 || entries[0].Name != "big" || entries[1].Name != "loose" || entries[2].Name != "small" {
		t.Fatalf("entries = %+v, want big, loose, small", entries)
	}
	got := byName(entries)
	if got["big"].Size != 4000 || got["big"].Entries != 40 || !got["big"].IsDir || got["big"].Partial {
		t.Fatalf("big = %+v, want 4000 bytes in 40 files, whole", got["big"])
	}
	// The symlink into /usr counts as nothing.
	if got["small"].Size != 10 || got["small"].Entries != 1 || got["small"].Partial {
		t.Fatalf("small = %+v, want 10 bytes in 1 file, whole", got["small"])
	}
	if got["loose"].Size != 25 || got["loose"].IsDir {
		t.Fatalf("loose = %+v, want a 25-byte file", got["loose"])
	}

	if top, _ := dirBreakdown(context.Background(), root, 1, roomy, nil); len(top) != 1 || top[0].Name != "big" {
		t.Fatalf("limit 1 = %+v, want big alone", top)
	}
}

func TestDirBreakdownBoundsChildren(t *testing.T) {
	root := t.TempDir()
	tree(t, root, map[string]int{"one": 1, "two": 1, "tree/three": 1})
	if _, err := dirBreakdown(context.Background(), root, 10, dirScanLimits{children: 2, visits: 100, workers: 1}, nil); !errors.Is(err, ErrDirScanLimit) {
		t.Fatalf("child limit error = %v, want ErrDirScanLimit", err)
	}
}

// A tree larger than the scan's work is answered with what was counted, the
// entries it did not finish said to be floors — not refused, which is what a
// scan of / on any server with Docker on it used to be.
func TestDirBreakdownAnswersPartlyWhenItsWorkRunsOut(t *testing.T) {
	root := t.TempDir()
	files := map[string]int{"tiny": 3}
	for i := range 200 {
		files[fmt.Sprintf("big/d%d/f%d", i%10, i)] = 10
	}
	tree(t, root, files)

	entries, err := dirBreakdown(context.Background(), root, 10, dirScanLimits{children: 10, visits: 50, workers: 2}, nil)
	if err != nil {
		t.Fatalf("a scan out of work = %v, want what it counted", err)
	}
	got := byName(entries)
	big := got["big"]
	if !big.Partial || big.Size <= 0 || big.Size >= 2000 {
		t.Fatalf("big = %+v, want a partial floor below its 2000 bytes", big)
	}
	if got["tiny"].Partial || got["tiny"].Size != 3 {
		t.Fatalf("tiny = %+v, want its 3 bytes, whole", got["tiny"])
	}
}

func TestDirBreakdownAnswersPartlyWhenItsTimeRunsOut(t *testing.T) {
	root := t.TempDir()
	tree(t, root, map[string]int{"a/b/c": 5})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	entries, err := dirBreakdown(ctx, root, 10, roomy, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || !entries[0].Partial {
		t.Fatalf("entries = %+v, want a, partial", entries)
	}
}

func TestDirBreakdownSaysAnUnreadableSubtreeIsPartial(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read directories regardless of their mode")
	}
	root := t.TempDir()
	tree(t, root, map[string]int{"open/a": 5, "shut/inner/b": 7})
	locked := filepath.Join(root, "shut", "inner")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o700) })

	entries, err := dirBreakdown(context.Background(), root, 10, roomy, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := byName(entries)
	if !got["shut"].Partial {
		t.Fatalf("shut = %+v: an unreadable subtree answered as if whole", got["shut"])
	}
	if got["open"].Partial || got["open"].Size != 5 {
		t.Fatalf("open = %+v, want its 5 bytes, whole", got["open"])
	}
}

// Every read of the filesystem is made inside run, which is how the dashboard
// puts each worker in the host's mount namespace: a goroutine started outside it
// would read this container's files instead.
func TestDirBreakdownReadsOnlyInsideRun(t *testing.T) {
	root := t.TempDir()
	tree(t, root, map[string]int{"a/b/c": 1, "d/e": 1})
	var runs atomic.Int32
	run := func(fn func()) {
		runs.Add(1)
		fn()
	}
	if _, err := dirBreakdown(context.Background(), root, 10, dirScanLimits{children: 10, visits: 100, workers: 3}, run); err != nil {
		t.Fatal(err)
	}
	// The root's own read, and one per worker.
	if runs.Load() != 4 {
		t.Fatalf("run was called %d times, want 4: the root, then each of 3 workers", runs.Load())
	}
}

func TestDirBreakdownStaysOnItsFilesystem(t *testing.T) {
	if _, err := os.Stat("/proc/self"); err != nil {
		t.Skip("no /proc here")
	}
	// /proc is a mount of its own: a scan of / must not count /proc/kcore, which
	// reads as the whole address space — 128 TiB on x86-64.
	done := make(chan struct{})
	var entries []DirEntry
	var err error
	go func() {
		defer close(done)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		entries, err = dirBreakdown(ctx, "/", 0, dirScanLimits{children: 1000, visits: 20_000, workers: 4}, nil)
	}()
	<-done
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name == "proc" || e.Name == "sys" {
			t.Fatalf("/%s was scanned as part of /: %+v", e.Name, e)
		}
	}
}
