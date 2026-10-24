package files

import (
	"archive/zip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestZipExtractionEnforcesActualBytesAndEntryCount(t *testing.T) {
	root := t.TempDir()
	archive := filepath.Join(root, "payload.zip")
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for _, entry := range []struct{ name, body string }{
		{name: "large", body: strings.Repeat("a", 1024)},
		{name: "second", body: "x"},
	} {
		w, err := zw.Create(entry.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(entry.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	t.Run("expanded bytes", func(t *testing.T) {
		dest := filepath.Join(root, "bytes")
		if err := os.Mkdir(dest, 0o755); err != nil {
			t.Fatal(err)
		}
		budget := &extractBudget{ctx: context.Background(), maxBytes: 64, maxEntries: 10}
		if _, err := extractZip(archive, dest, budget, new([]string)); !errors.Is(err, ErrArchiveTooLarge) {
			t.Fatalf("extract error = %v, want ErrArchiveTooLarge", err)
		}
		if _, err := os.Stat(filepath.Join(dest, "large")); !os.IsNotExist(err) {
			t.Fatalf("partial oversized file remains: %v", err)
		}
	})

	t.Run("entries", func(t *testing.T) {
		dest := filepath.Join(root, "entries")
		if err := os.Mkdir(dest, 0o755); err != nil {
			t.Fatal(err)
		}
		budget := &extractBudget{ctx: context.Background(), maxBytes: 2048, maxEntries: 1}
		if _, err := extractZip(archive, dest, budget, new([]string)); !errors.Is(err, ErrArchiveTooManyEntries) {
			t.Fatalf("extract error = %v, want ErrArchiveTooManyEntries", err)
		}
	})
}

func TestZipExtractionHonoursCancellation(t *testing.T) {
	root := t.TempDir()
	archive := filepath.Join(root, "payload.zip")
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, err := zw.Create("payload")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("payload")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	budget := &extractBudget{ctx: ctx, maxBytes: 1024, maxEntries: 10}
	if _, err := extractZip(archive, root, budget, new([]string)); !errors.Is(err, context.Canceled) {
		t.Fatalf("extract error = %v, want context.Canceled", err)
	}
}
