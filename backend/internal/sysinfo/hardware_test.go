package sysinfo

import (
	"path/filepath"
	"testing"
)

func TestReadFileHandlesParsesAllocatedAndMax(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file-nr")
	write(t, path, "4640\t0\t9223372036854775807\n")
	restore := useFileNR(t, path)
	defer restore()

	got := ReadFileHandles()
	if got.Open != 4640 {
		t.Errorf("open = %d, want 4640", got.Open)
	}
	// The 64-bit maximum is a container runtime saying "no limit", and a
	// percentage of it would read as zero forever.
	if got.Max != 0 {
		t.Errorf("max = %d, want 0 for an unbounded ceiling", got.Max)
	}
}

func TestReadFileHandlesKeepsARealCeiling(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file-nr")
	write(t, path, "1856\t0\t9223372\n")
	restore := useFileNR(t, path)
	defer restore()

	got := ReadFileHandles()
	if got.Open != 1856 || got.Max != 9223372 {
		t.Errorf("got %+v, want open 1856 max 9223372", got)
	}
}

func TestReadFileHandlesMissingFileIsZero(t *testing.T) {
	restore := useFileNR(t, filepath.Join(t.TempDir(), "absent"))
	defer restore()

	if got := ReadFileHandles(); got != (FileHandles{}) {
		t.Errorf("got %+v, want zero value", got)
	}
}

func useFileNR(t *testing.T, path string) func() {
	t.Helper()
	old := fileNRPath
	fileNRPath = path
	return func() { fileNRPath = old }
}
