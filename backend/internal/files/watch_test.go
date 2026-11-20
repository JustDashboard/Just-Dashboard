package files

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newTestHub(t *testing.T, root string, total, perOwner int) *WatchHub {
	t.Helper()
	hub := NewWatchHub(New([]string{root}), total, perOwner)
	hub.settle, hub.window = 20*time.Millisecond, 50*time.Millisecond
	return hub
}

// collect reads batches until want is satisfied by what has arrived, or fails.
func collect(t *testing.T, w *Watch, want func(seen map[string]*Entry, removed map[string]bool, batches int) bool) (map[string]*Entry, map[string]bool, int) {
	t.Helper()
	seen, removed, batches := map[string]*Entry{}, map[string]bool{}, 0
	deadline := time.After(5 * time.Second)
	for !want(seen, removed, batches) {
		select {
		case change, ok := <-w.C:
			if !ok {
				t.Fatalf("watch closed early; seen %v removed %v", seen, removed)
			}
			batches++
			if change.Gone || change.Resync {
				t.Fatalf("unexpected batch %+v", change)
			}
			for i := range change.Entries {
				e := change.Entries[i]
				seen[e.Name], removed[e.Name] = &e, false
			}
			for _, name := range change.Removed {
				removed[name] = true
				delete(seen, name)
			}
		case <-deadline:
			t.Fatalf("timed out; seen %v removed %v after %d batches", seen, removed, batches)
		}
	}
	return seen, removed, batches
}

func TestWatchReportsCreateWriteRenameAndRemove(t *testing.T) {
	root := t.TempDir()
	hub := newTestHub(t, root, 8, 8)
	w, err := hub.Watch(root, "a", false, "")
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if w.Path != root {
		t.Fatalf("watch path %q", w.Path)
	}

	file := filepath.Join(root, "made.txt")
	if err := os.WriteFile(file, []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	seen, _, _ := collect(t, w, func(seen map[string]*Entry, _ map[string]bool, _ int) bool {
		return seen["made.txt"] != nil && seen["made.txt"].Size == 3
	})
	if seen["made.txt"].Path != file || seen["made.txt"].IsDir {
		t.Fatalf("created entry %+v", seen["made.txt"])
	}

	// Grown by something else: a log being appended to.
	f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(" two three")
	f.Close()
	collect(t, w, func(seen map[string]*Entry, _ map[string]bool, _ int) bool {
		return seen["made.txt"] != nil && seen["made.txt"].Size == 13
	})

	if err := os.Rename(file, filepath.Join(root, "renamed.txt")); err != nil {
		t.Fatal(err)
	}
	collect(t, w, func(seen map[string]*Entry, removed map[string]bool, _ int) bool {
		return removed["made.txt"] && seen["renamed.txt"] != nil
	})

	if err := os.Remove(filepath.Join(root, "renamed.txt")); err != nil {
		t.Fatal(err)
	}
	collect(t, w, func(_ map[string]*Entry, removed map[string]bool, _ int) bool {
		return removed["renamed.txt"]
	})

	if err := os.Mkdir(filepath.Join(root, "folder"), 0o755); err != nil {
		t.Fatal(err)
	}
	seen, _, _ = collect(t, w, func(seen map[string]*Entry, _ map[string]bool, _ int) bool {
		return seen["folder"] != nil
	})
	if !seen["folder"].IsDir {
		t.Fatalf("folder entry %+v", seen["folder"])
	}
}

func TestWatchCoalescesABurst(t *testing.T) {
	root := t.TempDir()
	hub := newTestHub(t, root, 8, 8)
	hub.settle, hub.window = 100*time.Millisecond, 200*time.Millisecond
	w, err := hub.Watch(root, "a", false, "")
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	for i := range 150 {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("f%03d", i)), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_, _, batches := collect(t, w, func(seen map[string]*Entry, _ map[string]bool, _ int) bool {
		return len(seen) == 150
	})
	if batches > 3 {
		t.Fatalf("150 creations arrived in %d batches; a burst should be a few", batches)
	}

	// Past the batch bound the page is asked to read the listing once. A
	// hub of its own, set before its watch starts, with a settle long enough
	// that the whole burst is in before the first batch on a loaded machine.
	bounded := newTestHub(t, root, 8, 8)
	bounded.settle, bounded.window, bounded.maxBatch = time.Second, time.Second, 20
	b, err := bounded.Watch(root, "a", false, "")
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	for i := range 50 {
		os.Remove(filepath.Join(root, fmt.Sprintf("f%03d", i)))
	}
	select {
	case change := <-b.C:
		if !change.Resync {
			t.Fatalf("an oversized batch was sent row by row: %d entries, %d removed", len(change.Entries), len(change.Removed))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no batch after an oversized burst")
	}
}

func TestWatchLeavesOutHiddenAndStagingNames(t *testing.T) {
	root := t.TempDir()
	hub := newTestHub(t, root, 8, 8)
	w, err := hub.Watch(root, "a", false, "")
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	os.WriteFile(filepath.Join(root, ".hidden"), nil, 0o644)
	os.WriteFile(filepath.Join(root, ".jd-part-0123"), nil, 0o600)
	os.WriteFile(filepath.Join(root, "shown"), nil, 0o644)
	seen, _, _ := collect(t, w, func(seen map[string]*Entry, _ map[string]bool, _ int) bool {
		return seen["shown"] != nil
	})
	if seen[".hidden"] != nil || seen[".jd-part-0123"] != nil {
		t.Fatalf("hidden or staging names reported: %v", seen)
	}

	shown, err := hub.Watch(root, "a", true, "")
	if err != nil {
		t.Fatal(err)
	}
	defer shown.Close()
	os.WriteFile(filepath.Join(root, ".dotfile"), nil, 0o644)
	os.WriteFile(filepath.Join(root, ".jd-part-4567"), nil, 0o600)
	seen, _, _ = collect(t, shown, func(seen map[string]*Entry, _ map[string]bool, _ int) bool {
		return seen[".dotfile"] != nil
	})
	if seen[".jd-part-4567"] != nil {
		t.Fatal("a staging name was reported with hidden files on")
	}
	entries, err := New([]string{root}).List(root, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries.Entries {
		if IsStagingName(e.Name) {
			t.Fatalf("listing shows staging file %s", e.Name)
		}
	}
}

func TestWatchOneNameForAnEditor(t *testing.T) {
	root := t.TempDir()
	hub := newTestHub(t, root, 8, 8)
	w, err := hub.Watch(root, "a", true, "config.yml")
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	os.WriteFile(filepath.Join(root, "other.txt"), nil, 0o644)
	os.WriteFile(filepath.Join(root, "config.yml"), []byte("a: 1"), 0o644)
	seen, _, _ := collect(t, w, func(seen map[string]*Entry, _ map[string]bool, _ int) bool {
		return seen["config.yml"] != nil
	})
	if seen["other.txt"] != nil {
		t.Fatal("a watch for one name reported another")
	}
	if _, err := hub.Watch(root, "a", true, "../etc/passwd"); err == nil {
		t.Fatal("a name with a path in it was accepted")
	}
}

func TestWatchLimitsAndRelease(t *testing.T) {
	root := t.TempDir()
	hub := newTestHub(t, root, 3, 2)
	a1, err := hub.Watch(root, "a", false, "")
	if err != nil {
		t.Fatal(err)
	}
	a2, err := hub.Watch(root, "a", false, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hub.Watch(root, "a", false, ""); !errors.Is(err, ErrWatchLimit) {
		t.Fatalf("a third watch for one session returned %v", err)
	}
	b1, err := hub.Watch(root, "b", false, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hub.Watch(root, "c", false, ""); !errors.Is(err, ErrWatchLimit) {
		t.Fatalf("a watch past the total returned %v", err)
	}
	if hub.Active() != 3 {
		t.Fatalf("active %d", hub.Active())
	}

	// Two watches share the kernel's watch on one folder: closing one leaves
	// the other reporting.
	a1.Close()
	a1.Close()
	os.WriteFile(filepath.Join(root, "still"), nil, 0o644)
	collect(t, a2, func(seen map[string]*Entry, _ map[string]bool, _ int) bool { return seen["still"] != nil })
	if _, ok := <-a1.C; ok {
		t.Fatal("a closed watch kept its channel open")
	}

	a2.Close()
	b1.Close()
	if hub.Active() != 0 {
		t.Fatalf("active %d after every watch closed", hub.Active())
	}
	hub.mu.Lock()
	open := hub.file != nil || len(hub.byWD) != 0
	hub.mu.Unlock()
	if open {
		t.Fatal("the inotify instance outlived its last watch")
	}
	again, err := hub.Watch(root, "a", false, "")
	if err != nil {
		t.Fatalf("a watch after release: %v", err)
	}
	again.Close()
}

func TestWatchRefusesOutsideTheRootsAndFallsBackWhereUnwatchable(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	hub := newTestHub(t, root, 8, 8)
	if _, err := hub.Watch(outside, "a", false, ""); !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("outside the roots returned %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "out")); err != nil {
		t.Fatal(err)
	}
	if _, err := hub.Watch(filepath.Join(root, "out"), "a", false, ""); !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("a link out of the roots returned %v", err)
	}
	if hub.Active() != 0 {
		t.Fatal("a refused watch was counted")
	}
	proc := NewWatchHub(New([]string{"/"}), 8, 8)
	if _, err := proc.Watch("/proc", "a", false, ""); !errors.Is(err, ErrWatchUnavailable) {
		t.Fatalf("/proc returned %v", err)
	}
}

func TestWatchSaysWhenTheFolderGoes(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "going")
	os.Mkdir(dir, 0o755)
	hub := newTestHub(t, root, 8, 8)
	w, err := hub.Watch(dir, "a", false, "")
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err := os.Rename(dir, filepath.Join(root, "gone")); err != nil {
		t.Fatal(err)
	}
	select {
	case change := <-w.C:
		if !change.Gone {
			t.Fatalf("moved folder reported %+v", change)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no word that the folder moved")
	}
}

func TestFingerprintFollowsGrowthAndIgnoresStaging(t *testing.T) {
	root := t.TempDir()
	svc := New([]string{root})
	file := filepath.Join(root, "log")
	os.WriteFile(file, []byte("a"), 0o644)
	first, err := svc.Fingerprint(root, false)
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := svc.Fingerprint(root, false); again != first {
		t.Fatal("fingerprint changed with nothing changing")
	}
	hiddenFirst, _ := svc.Fingerprint(root, true)
	os.WriteFile(filepath.Join(root, ".jd-part-abc"), []byte("partial"), 0o600)
	withStage, _ := svc.Fingerprint(root, false)
	hiddenStage, _ := svc.Fingerprint(root, true)
	if withStage != first || hiddenStage != hiddenFirst {
		t.Fatal("a staging file changed the fingerprint")
	}
	os.WriteFile(file, []byte("ab"), 0o644)
	grown, _ := svc.Fingerprint(root, false)
	if grown == first {
		t.Fatal("a grown file left the fingerprint unchanged")
	}
	if _, err := svc.Fingerprint(t.TempDir(), false); !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("outside the roots returned %v", err)
	}
}
