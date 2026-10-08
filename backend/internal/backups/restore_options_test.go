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

func TestRestoreInPlaceRestoresIndividualFileSources(t *testing.T) {
	for _, state := range []string{"existing", "missing", "missing parents", "empty", "directory conflict"} {
		t.Run(state, func(t *testing.T) {
			parent := filepath.Join(t.TempDir(), "app")
			if err := os.Mkdir(parent, 0o700); err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(parent, "app.conf")
			contents := "backup contents"
			if state == "empty" {
				contents = ""
			}
			if err := os.WriteFile(source, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
			_, runner, _, run := manifestFixture(t, []string{source}, nil)
			if run.Status != StatusSuccess {
				t.Fatalf("backup failed: %+v", run)
			}
			var err error
			switch state {
			case "missing":
				err = os.Remove(source)
			case "missing parents":
				err = os.RemoveAll(parent)
			case "directory conflict":
				if err := os.Remove(source); err != nil {
					t.Fatal(err)
				}
				err = os.Mkdir(source, 0o700)
			default:
				err = os.WriteFile(source, []byte("changed contents"), 0o600)
			}
			if err != nil {
				t.Fatal(err)
			}
			res, err := runner.RestoreInPlace(t.Context(), run.ID)
			if state == "directory conflict" {
				if err == nil {
					t.Fatalf("a file restored onto a directory reported success: %+v", res)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if data, err := os.ReadFile(source); err != nil || string(data) != contents {
				t.Fatalf("restored file = %q, %v, want %q", data, err, contents)
			}
			if res.Entries != 1 || res.Bytes != int64(len(contents)) || len(res.Skipped) != 0 {
				t.Fatalf("incorrect restore counts: %+v", res)
			}
			if res.Destination != "" || len(res.Targets) != 1 || res.Targets[0] != source {
				t.Fatalf("incorrect in-place destination: %+v", res)
			}
		})
	}
}

func TestRestoreInPlaceSelectsFileSourcesAlongsideDirectories(t *testing.T) {
	root := t.TempDir()
	file, directory := filepath.Join(root, "app.conf"), filepath.Join(root, "data")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	for path, data := range map[string]string{file: "conf", filepath.Join(directory, "canary"): "record"} {
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	_, runner, _, run := manifestFixture(t, []string{file, directory}, nil)
	if run.Status != StatusSuccess {
		t.Fatalf("backup failed: %+v", run)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(directory); err != nil {
		t.Fatal(err)
	}
	res, err := runner.RestoreInPlace(t.Context(), run.ID, RestoreOptions{Paths: []string{"source-0001"}})
	if err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(file); err != nil || string(data) != "conf" || res.Entries != 1 || res.Bytes != 4 {
		t.Fatalf("selected file = %q, %v, result=%+v", data, err, res)
	}
	if _, err := os.Stat(directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unselected source was created: %v", err)
	}
	if _, err := runner.RestoreInPlace(t.Context(), run.ID); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(directory, "canary")); err != nil || string(data) != "record" {
		t.Fatalf("directory source was not restored: %q, %v", data, err)
	}
}
