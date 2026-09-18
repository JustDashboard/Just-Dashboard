package backups

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestPathFilterMatchesEntriesAndWhatIsUnderThem(t *testing.T) {
	f := newPathFilter([]RestoreOptions{{Paths: []string{"source-0001/etc/", "/source-0002/app.db"}}})
	for name, want := range map[string]bool{
		"source-0001/etc":            true,
		"source-0001/etc/":           true,
		"source-0001/etc/nginx.conf": true,
		"source-0001/etcetera":       false,
		"source-0002/app.db":         true,
		"source-0002/app.db-wal":     false,
		"source-0003/x":              false,
	} {
		if got := f.wants(name); got != want {
			t.Errorf("%s: %v, want %v", name, got, want)
		}
	}
	if newPathFilter(nil) != nil || newPathFilter([]RestoreOptions{{Paths: []string{"/", "."}}}) != nil {
		t.Fatal("an empty filter must mean everything")
	}
}

func TestRestoreSubsetWritesOnlyTheChosenEntries(t *testing.T) {
	root := t.TempDir()
	stage, target := filepath.Join(root, "stage"), filepath.Join(root, "target")
	one := filepath.Join(root, "one")
	if err := os.MkdirAll(filepath.Join(one, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(one, "etc", "app.conf"), []byte("conf"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(one, "notes.txt"), []byte("notes"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &Runner{stage: stage, store: NewStore(nil, nil)}
	job := &Job{ID: 1, Name: "one", Sources: []string{one}, TargetKind: TargetLocal, Target: TargetConfig{Path: target}}
	artifact, _, _, err := r.performWithManifest(context.Background(), job, 1, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(root, "restored")
	res, err := extractArchiveBounded(context.Background(), artifact, dest, 1, 0,
		restorePlan{filter: newPathFilter([]RestoreOptions{{Paths: []string{"source-0001/etc"}}})})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "source-0001", "etc", "app.conf")); err != nil {
		t.Fatalf("chosen entry missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "source-0001", "notes.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unchosen entry restored: %v", err)
	}
	if res.Entries != 2 {
		t.Fatalf("entries=%d, want the directory and its file", res.Entries)
	}
}

func TestRestoreInPlaceMapsEachSourceRootBackToItsPath(t *testing.T) {
	root := t.TempDir()
	stage, target := filepath.Join(root, "stage"), filepath.Join(root, "target")
	one, two := filepath.Join(root, "one"), filepath.Join(root, "two")
	for _, dir := range []string{filepath.Join(one, "etc"), two} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(one, "etc", "app.conf"), "conf v1")
	write(filepath.Join(two, "data.db"), "data v1")
	r := &Runner{stage: stage, store: NewStore(nil, nil)}
	job := &Job{ID: 1, Name: "pair", Sources: []string{one, two}, TargetKind: TargetLocal, Target: TargetConfig{Path: target}}
	artifact, _, manifest, err := r.performWithManifest(context.Background(), job, 1, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	// The live trees move on; the restore has to put v1 back.
	write(filepath.Join(one, "etc", "app.conf"), "conf v2")
	write(filepath.Join(two, "data.db"), "data v2")

	plan := restorePlan{roots: map[string]string{}}
	for _, source := range manifest.Sources {
		plan.roots[source.ArchivePath] = source.Path
	}
	res, err := extractArchiveBounded(context.Background(), artifact, "", 1, 0, plan)
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{
		filepath.Join(one, "etc", "app.conf"): "conf v1",
		filepath.Join(two, "data.db"):         "data v1",
	} {
		body, err := os.ReadFile(path)
		if err != nil || string(body) != want {
			t.Fatalf("%s = %q (%v), want %q", path, body, err, want)
		}
	}
	if _, err := os.Stat(filepath.Join(one, "source-0001")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the archive root leaked into the restored tree")
	}
	if res.Entries == 0 {
		t.Fatal("nothing restored")
	}
	// An entry outside every recorded root is left in the archive.
	plan.roots = map[string]string{"source-0002": two}
	write(filepath.Join(one, "etc", "app.conf"), "conf v3")
	if _, err := extractArchiveBounded(context.Background(), artifact, "", 1, 0, plan); err != nil {
		t.Fatal(err)
	}
	if body, _ := os.ReadFile(filepath.Join(one, "etc", "app.conf")); string(body) != "conf v3" {
		t.Fatalf("a source outside the plan was written: %q", body)
	}
}
