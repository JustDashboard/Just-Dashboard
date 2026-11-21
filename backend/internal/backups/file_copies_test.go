package backups

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
)

func TestFileCopiesListRunsThatHoldAFileWithoutReachingADestination(t *testing.T) {
	source := t.TempDir()
	conf := filepath.Join(source, "etc", "app.conf")
	if err := os.MkdirAll(filepath.Dir(conf), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(conf, []byte("backed up\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "debug.log"), []byte("noise"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, runner, job, run := manifestFixture(t, []string{source}, []string{"*.log", filepath.Join(source, "cache")})
	if run.Status != StatusSuccess {
		t.Fatalf("backup failed: %+v", run)
	}

	// A second job whose destination answers nothing: any request to it is
	// a listing that went out to a destination.
	var reached atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached.Add(1)
		http.Error(w, "no", http.StatusTeapot)
	}))
	defer destination.Close()
	sealer, err := auth.NewSealer(strings.Repeat("ab", 32))
	if err != nil {
		t.Fatal(err)
	}
	remote, err := NewStore(s.st, sealer).Create(t.Context(), &Job{Name: "offsite", Sources: []string{source}, TargetKind: TargetS3,
		Target: TargetConfig{Bucket: "copies", Endpoint: strings.TrimPrefix(destination.URL, "http://"), Region: "us-east-1"}},
		&TargetSecrets{AccessKeyID: "id", SecretAccessKey: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	remoteRun, err := s.StartRun(t.Context(), remote.ID, "test")
	if err != nil {
		t.Fatal(err)
	}
	manifest := *run.Manifest
	manifest.TargetKind, manifest.Target = TargetS3, remote.Target
	if err := s.finishRun(t.Context(), remoteRun, StatusSuccess, "copies/archive.tar.gz", 10, "", &manifest); err != nil {
		t.Fatal(err)
	}

	copies, err := s.FileCopies(t.Context(), conf, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(copies) != 2 || reached.Load() != 0 {
		t.Fatalf("copies %+v, destination reached %d times", copies, reached.Load())
	}
	byRun := map[int64]FileCopy{}
	for _, c := range copies {
		byRun[c.RunID] = c
	}
	if c := byRun[run.ID]; c.Job != job.Name || !c.Local || c.TargetKind != TargetLocal {
		t.Fatalf("local copy: %+v", c)
	}
	if c := byRun[remoteRun]; c.Job != "offsite" || c.Local || c.TargetKind != TargetS3 || c.Destination != "copies" {
		t.Fatalf("remote copy: %+v", c)
	}

	// Excluded, outside every source, or under an excluded folder: no copy.
	for _, path := range []string{filepath.Join(source, "debug.log"), "/srv/elsewhere.conf", filepath.Join(source, "cache", "x")} {
		if copies, err := s.FileCopies(t.Context(), path, 0); err != nil || len(copies) != 0 {
			t.Fatalf("%s: %+v %v", path, copies, err)
		}
	}

	// Reading the copy goes through the run's verified archive.
	content, err := runner.ReadFileCopy(t.Context(), run.ID, conf, 1<<20)
	if err != nil || string(content) != "backed up\n" {
		t.Fatalf("copy %q %v", content, err)
	}
	if _, err := runner.ReadFileCopy(t.Context(), run.ID, conf, 3); !errors.Is(err, ErrFileCopyTooLarge) {
		t.Fatalf("bound: %v", err)
	}
	// A file made after the run is covered by the source but not held.
	later := filepath.Join(source, "etc", "later.conf")
	if _, err := runner.ReadFileCopy(t.Context(), run.ID, later, 1<<20); !errors.Is(err, ErrNotInArchive) {
		t.Fatalf("a file the run never took: %v", err)
	}
	// A path no run took is not read out of the archive under another name.
	if _, err := runner.ReadFileCopy(t.Context(), run.ID, "/etc/passwd", 1<<20); !errors.Is(err, ErrNotInArchive) {
		t.Fatalf("outside the sources: %v", err)
	}
}
