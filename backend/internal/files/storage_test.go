package files

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func storageFixture(t *testing.T) (*Service, string) {
	t.Helper()
	root := t.TempDir()
	s := New([]string{root})
	s.storageProc = t.TempDir()
	return s, root
}

func writeStorageFile(t *testing.T, path string, content []byte) StorageFile {
	t.Helper()
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return storageFile(path, info)
}

func scanFixture(t *testing.T, s *Service, root string, copies bool) *StorageReport {
	t.Helper()
	report, err := s.ScanStorage(context.Background(), root, copies)
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func TestStorageMeasuresAllocationAndDeduplicatesHardLinks(t *testing.T) {
	s, root := storageFixture(t)
	file := writeStorageFile(t, filepath.Join(root, "data"), bytes.Repeat([]byte("a"), 1<<20))
	if err := os.Link(file.Path, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	sparse, err := os.Create(filepath.Join(root, "sparse"))
	if err != nil {
		t.Fatal(err)
	}
	if err := sparse.Truncate(2 << 30); err != nil {
		t.Fatal(err)
	}
	sparse.Close()
	outside := writeStorageFile(t, filepath.Join(t.TempDir(), "outside"), bytes.Repeat([]byte("b"), 1<<20))
	if err := os.Symlink(outside.Path, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(root, "fifo"), 0600); err != nil {
		t.Fatal(err)
	}
	report := scanFixture(t, s, root, true)
	if !report.Complete || report.Allocated >= 2<<20 {
		t.Fatalf("wrong allocated total: %+v", report)
	}
	if len(report.LargeFiles) != 2 || len(report.Duplicates) != 0 {
		t.Fatalf("links/special files counted as copies: %+v", report)
	}
	if report.Entries != 6 {
		t.Fatalf("entries = %d, want 6", report.Entries)
	}
}

func TestStorageCopiesRequireContentAndCleanupKeepsOne(t *testing.T) {
	s, root := storageFixture(t)
	writeStorageFile(t, filepath.Join(root, "a"), bytes.Repeat([]byte("a"), 1<<20))
	writeStorageFile(t, filepath.Join(root, "b"), bytes.Repeat([]byte("a"), 1<<20))
	writeStorageFile(t, filepath.Join(root, "different"), bytes.Repeat([]byte("b"), 1<<20))
	if report := scanFixture(t, s, root, false); len(report.Duplicates) != 0 || report.HashedBytes != 0 {
		t.Fatal("default scan read file contents")
	}
	report := scanFixture(t, s, root, true)
	if len(report.Duplicates) != 1 || len(report.Duplicates[0].Files) != 2 {
		t.Fatalf("copies = %+v", report.Duplicates)
	}
	group := report.Duplicates[0]
	selection := StorageSelection{Kind: "duplicate", File: group.Files[1], Keeper: &group.Files[0], SHA256: group.SHA256}
	result, err := s.CleanupStorage(context.Background(), []StorageSelection{selection})
	if err != nil || len(result.Items) != 1 || !result.Items[0].Removed || result.RemovedBytes != group.Reclaimable {
		t.Fatalf("cleanup: %+v, %v", result, err)
	}
	if _, err := os.Stat(group.Files[0].Path); err != nil {
		t.Fatal("keeper removed", err)
	}
	if _, err := os.Stat(group.Files[1].Path); !os.IsNotExist(err) {
		t.Fatal("copy not removed", err)
	}
	entries, _ := os.ReadDir(root)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".jd-cleanup-") {
			t.Fatal("staging directory left behind")
		}
	}
}

func TestStorageCleanupRejectsChangedFilesKeepersAndOpenFiles(t *testing.T) {
	for _, change := range []string{"selection", "keeper", "open", "select-keeper", "checksum", "outside"} {
		t.Run(change, func(t *testing.T) {
			s, root := storageFixture(t)
			a := writeStorageFile(t, filepath.Join(root, "a"), bytes.Repeat([]byte("a"), 1<<20))
			b := writeStorageFile(t, filepath.Join(root, "b"), bytes.Repeat([]byte("a"), 1<<20))
			group := scanFixture(t, s, root, true).Duplicates[0]
			selection := StorageSelection{Kind: "duplicate", File: b, Keeper: &a, SHA256: group.SHA256}
			selections := []StorageSelection{selection}
			switch change {
			case "selection":
				writeStorageFile(t, b.Path, []byte("changed"))
			case "keeper":
				writeStorageFile(t, a.Path, []byte("changed"))
			case "open":
				fd := filepath.Join(s.storageProc, "123", "fd")
				if err := os.MkdirAll(fd, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(b.Path, filepath.Join(fd, "4")); err != nil {
					t.Fatal(err)
				}
			case "select-keeper":
				selections = append(selections, StorageSelection{Kind: "duplicate", File: a, Keeper: &b, SHA256: group.SHA256})
			case "checksum":
				selections[0].SHA256 = strings.Repeat("0", 64)
			case "outside":
				selections[0].File.Path = filepath.Join(t.TempDir(), "unowned")
			}
			result, err := s.CleanupStorage(context.Background(), selections)
			if err == nil && result.Items[0].Removed {
				t.Fatalf("unsafe selection removed: %+v", result)
			}
			if _, err := os.Stat(b.Path); err != nil {
				t.Fatal("selected file lost", err)
			}
			if _, err := os.Stat(a.Path); err != nil {
				t.Fatal("keeper lost", err)
			}
		})
	}
}

func TestStorageTemporaryCleanupRequiresAgeAndAvailableOpenFileCheck(t *testing.T) {
	for _, kind := range []string{"old", "recent", "linked", "unavailable"} {
		t.Run(kind, func(t *testing.T) {
			s, root := storageFixture(t)
			file := writeStorageFile(t, filepath.Join(root, "temp"), []byte("temporary content"))
			if kind != "recent" {
				old := time.Now().Add(-8 * 24 * time.Hour)
				if err := os.Chtimes(file.Path, old, old); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "linked" {
				if err := os.Link(file.Path, filepath.Join(root, "other")); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "unavailable" {
				s.storageProc = filepath.Join(root, "no-proc")
			}
			info, _ := os.Stat(file.Path)
			selection := StorageSelection{Kind: "temporary", File: storageFile(file.Path, info)}
			result, err := s.CleanupStorage(context.Background(), []StorageSelection{selection})
			if err != nil {
				t.Fatal(err)
			}
			wantRemoved := kind == "old"
			if result.Items[0].Removed != wantRemoved {
				t.Fatalf("result = %+v", result)
			}
		})
	}
}

func TestStorageScanReportsLimitsAndSkipsSameDeviceMounts(t *testing.T) {
	s, root := storageFixture(t)
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0700); err != nil {
		t.Fatal(err)
	}
	writeStorageFile(t, filepath.Join(nested, "a"), []byte("a"))
	writeStorageFile(t, filepath.Join(root, "b"), []byte("b"))
	handle, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()
	report, err := s.scanStorage(context.Background(), handle, root, true, 200, map[string]bool{nested: true})
	if err != nil || report.SkippedMounts != 1 || report.Entries != 2 {
		t.Fatalf("mount not excluded: %+v %v", report, err)
	}
	report, err = s.scanStorage(context.Background(), handle, root, true, 2, nil)
	if err != nil || report.Complete || report.DuplicateScanComplete || len(report.Silences) == 0 {
		t.Fatalf("limit hidden: %+v %v", report, err)
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	report, err = s.scanStorage(ctx, handle, root, true, 200, nil)
	if err != nil || report.Complete || report.DuplicateScanComplete {
		t.Fatalf("deadline hidden: %+v %v", report, err)
	}
	canceled, stop := context.WithCancel(context.Background())
	stop()
	if _, err := s.ScanStorage(canceled, root, false); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel = %v", err)
	}
}

func TestStorageRejectsUnownedAndKernelRoots(t *testing.T) {
	s, _ := storageFixture(t)
	if _, err := s.ScanStorage(context.Background(), t.TempDir(), false); !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("outside = %v", err)
	}
	if _, err := New([]string{"/"}).ScanStorage(context.Background(), "/proc", true); err == nil {
		t.Fatal("kernel filesystem scanned for cleanup")
	}
}

func TestStorageConcurrentCleanupCannotRemoveBothCopies(t *testing.T) {
	s, root := storageFixture(t)
	a := writeStorageFile(t, filepath.Join(root, "a"), bytes.Repeat([]byte("a"), 1<<20))
	b := writeStorageFile(t, filepath.Join(root, "b"), bytes.Repeat([]byte("a"), 1<<20))
	group := scanFixture(t, s, root, true).Duplicates[0]
	done := make(chan bool, 2)
	for _, selection := range []StorageSelection{{Kind: "duplicate", File: a, Keeper: &b, SHA256: group.SHA256}, {Kind: "duplicate", File: b, Keeper: &a, SHA256: group.SHA256}} {
		go func() {
			result, err := s.CleanupStorage(t.Context(), []StorageSelection{selection})
			done <- err == nil && result.Items[0].Removed
		}()
	}
	removed := 0
	for range 2 {
		if <-done {
			removed++
		}
	}
	if removed != 1 {
		t.Fatalf("removed %d, want exactly one copy", removed)
	}
	_, aerr := os.Stat(a.Path)
	_, berr := os.Stat(b.Path)
	if os.IsNotExist(aerr) && os.IsNotExist(berr) {
		t.Fatal("both copies removed")
	}
}

func TestStorageDirectoryParentSwapCannotEscapeConfiguredRoot(t *testing.T) {
	allowed, outside := t.TempDir(), t.TempDir()
	parent := filepath.Join(allowed, "data")
	if err := os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	service := New([]string{allowed})
	measured, err := service.Resolve(parent)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(parent); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, parent); err != nil {
		t.Fatal(err)
	}
	handle, err := service.openStorageDirectory(measured)
	if err == nil {
		handle.Close()
		t.Fatal("a parent swapped after Resolve escaped the configured root")
	}
}
