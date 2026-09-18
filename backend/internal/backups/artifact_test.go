package backups

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestArtifactNamesSeparateSameSecondJobsAndRuns(t *testing.T) {
	at := time.Unix(1234567890, 0)
	seen := map[string]bool{}
	for _, job := range []*Job{{ID: 1, Name: "daily/files"}, {ID: 2, Name: "daily?files"}} {
		for _, run := range []int64{10, 11, 10} {
			name := artifactName(job, run, at)
			if seen[name] || !strings.HasSuffix(name, ".tar.gz") || filepath.Base(name) != name {
				t.Fatalf("invalid or colliding artifact name: %q", name)
			}
			seen[name] = true
		}
	}
}

func TestBackupDoesNotReplaceAnEarlierArtifact(t *testing.T) {
	stage, target, source := t.TempDir(), t.TempDir(), filepath.Join(t.TempDir(), "data")
	if err := os.WriteFile(source, []byte("first backup"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := &Runner{stage: stage, store: NewStore(nil, nil)}
	job := &Job{ID: 1, Name: "same", Sources: []string{source}, TargetKind: TargetLocal, Target: TargetConfig{Path: target}}
	first, _, err := r.perform(t.Context(), job, 1, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("later backup"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, _, err := r.perform(t.Context(), job, 2, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(first)
	if err != nil || first == second || !bytes.Equal(before, after) {
		t.Fatalf("earlier backup changed: first=%q second=%q err=%v", first, second, err)
	}
	entries, err := os.ReadDir(stage)
	if err != nil || len(entries) != 0 {
		t.Fatalf("private staging files leaked: %v, %v", entries, err)
	}
}

func TestArtifactPublicationRefusesExistingFilesAndSymlinks(t *testing.T) {
	for _, symlink := range []bool{false, true} {
		dir := t.TempDir()
		source, target, outside := filepath.Join(dir, "source"), filepath.Join(dir, "target"), filepath.Join(dir, "outside")
		if err := os.WriteFile(source, []byte("new"), 0o600); err != nil {
			t.Fatal(err)
		}
		protected := target
		if symlink {
			protected = outside
			if err := os.Symlink(outside, target); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(protected, []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := moveFile(source, target); !errors.Is(err, os.ErrExist) {
			t.Fatalf("publication replaced an existing destination: %v", err)
		}
		body, err := os.ReadFile(protected)
		if err != nil || string(body) != "keep" {
			t.Fatalf("protected artifact changed: %q, %v", body, err)
		}
		if _, err := os.Stat(source); err != nil {
			t.Fatalf("failed publication removed its source: %v", err)
		}
	}
}
