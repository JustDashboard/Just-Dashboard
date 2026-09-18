package backups

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/store"
)

// fakeDumper stands in for the Databases owner: it writes a known dump file
// and records what a restore received.
type fakeDumper struct {
	content   map[int64]string
	failOn    int64
	restored  []restoredDump
	described map[int64]DatabaseDescription
}

type restoredDump struct {
	connectionID int64
	database     string
	content      string
}

func (f *fakeDumper) DescribeDatabase(_ context.Context, id int64) (DatabaseDescription, error) {
	if description, ok := f.described[id]; ok {
		return description, nil
	}
	return DatabaseDescription{}, errors.New("no such connection")
}

func (f *fakeDumper) DumpDatabase(_ context.Context, id int64, directory string) (DatabaseDumpResult, error) {
	if id == f.failOn {
		return DatabaseDumpResult{}, errors.New("pg_dump: connection refused")
	}
	path := filepath.Join(directory, fmt.Sprintf("app-%d.dump", id))
	if err := os.WriteFile(path, []byte(f.content[id]), 0o600); err != nil {
		return DatabaseDumpResult{}, err
	}
	return DatabaseDumpResult{Description: f.described[id], Path: path, Method: "pg_dump"}, nil
}

func (f *fakeDumper) RestoreDatabase(_ context.Context, id int64, database, dumpPath string) (string, error) {
	data, err := os.ReadFile(dumpPath)
	if err != nil {
		return "", err
	}
	f.restored = append(f.restored, restoredDump{connectionID: id, database: database, content: string(data)})
	return "restored", nil
}

func dumpFixture(t *testing.T, dumper *fakeDumper, connections []int64) (*Store, *Runner, *Job) {
	t.Helper()
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s := NewStore(db, nil).WithDatabaseValidator(dumper.DescribeDatabase)
	r := NewRunner(s, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil))).WithDatabaseDumper(dumper)
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "uploads.txt"), []byte("files"), 0o600); err != nil {
		t.Fatal(err)
	}
	job, err := s.Create(t.Context(), &Job{Name: "with-dumps", Sources: []string{source}, DatabaseDumps: connections,
		TargetKind: TargetLocal, Target: TargetConfig{Path: t.TempDir()}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return s, r, job
}

func TestBackupCapturesNativeDatabaseDumpsBesideFilesAndRestoresThem(t *testing.T) {
	dumper := &fakeDumper{
		content:   map[int64]string{7: "PGDMP shop rows", 9: "PGDMP crm rows"},
		described: map[int64]DatabaseDescription{7: {ID: 7, Name: "shop", Driver: "postgres", Database: "shop"}, 9: {ID: 9, Name: "crm", Driver: "mysql", Database: "crm"}},
	}
	_, runner, job := dumpFixture(t, dumper, []int64{9, 7})
	if got := job.DatabaseDumps; len(got) != 2 || got[0] != 7 || got[1] != 9 {
		t.Fatalf("stored dumps = %v, want sorted distinct ids", got)
	}
	run, err := runner.Execute(t.Context(), job.ID, "test")
	if err != nil || run.Status != StatusSuccess {
		t.Fatalf("run = %+v, %v", run, err)
	}
	if run.Manifest == nil || len(run.Manifest.DatabaseDumps) != 2 {
		t.Fatalf("manifest = %#v", run.Manifest)
	}
	first := run.Manifest.DatabaseDumps[0]
	if first.ConnectionID != 7 || first.Name != "shop" || first.Driver != "postgres" || first.Method != "pg_dump" ||
		first.ArchivePath != "database-0001/app-7.dump" || first.Bytes != int64(len("PGDMP shop rows")) || !strings.HasPrefix(first.Digest, "sha256:") {
		t.Fatalf("dump evidence = %+v", first)
	}
	if !strings.Contains(run.Log, "dumped database shop (postgres, shop) with pg_dump") {
		t.Fatalf("log = %s", run.Log)
	}
	entries, err := runner.ListArchive(t.Context(), run.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]int64{}
	for _, entry := range entries {
		names[entry.Name] = entry.Size
	}
	if names["database-0001/app-7.dump"] != int64(len("PGDMP shop rows")) || names["database-0002/app-9.dump"] != int64(len("PGDMP crm rows")) {
		t.Fatalf("archive entries = %v", names)
	}
	if !run.Manifest.CoversDatabase(7) || !run.Manifest.CoversDatabase(9) || run.Manifest.CoversDatabase(8) {
		t.Fatal("manifest coverage is wrong")
	}
	if err := runner.VerifyDatabaseCoverage(t.Context(), run.ID, []int64{7, 9}); err != nil {
		t.Fatal(err)
	}
	if err := runner.VerifyDatabaseCoverage(t.Context(), run.ID, []int64{8}); err == nil {
		t.Fatal("an undumped connection was reported covered")
	}
	// Restore into a drill database: the owner receives exactly the archived bytes.
	if _, err := runner.RestoreDatabase(t.Context(), run.ID, 7, "shop_drill"); err != nil {
		t.Fatal(err)
	}
	if len(dumper.restored) != 1 || dumper.restored[0] != (restoredDump{connectionID: 7, database: "shop_drill", content: "PGDMP shop rows"}) {
		t.Fatalf("restore received %+v", dumper.restored)
	}
	if _, err := runner.RestoreDatabase(t.Context(), run.ID, 8, ""); err == nil {
		t.Fatal("restore of an undumped connection succeeded")
	}
	// Filesystem coverage is untouched by the extra roots.
	if err := runner.VerifyCoverage(t.Context(), run.ID, job.Sources); err != nil {
		t.Fatal(err)
	}
}

func TestBackupFailsWhenADatabaseDumpFails(t *testing.T) {
	dumper := &fakeDumper{
		content: map[int64]string{7: "ok"}, failOn: 9,
		described: map[int64]DatabaseDescription{7: {ID: 7, Name: "shop", Driver: "postgres"}, 9: {ID: 9, Name: "crm", Driver: "postgres"}},
	}
	_, runner, job := dumpFixture(t, dumper, []int64{7, 9})
	run, err := runner.Execute(t.Context(), job.ID, "test")
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != StatusFailed || !strings.Contains(run.Log, "database dump for connection 9 failed: pg_dump: connection refused") {
		t.Fatalf("a failed dump did not fail the run: %+v", run)
	}
	if run.Manifest != nil && run.Manifest.Complete {
		t.Fatal("a run without its dump claims completeness")
	}
}

func TestBackupJobRefusesUnknownOrDuplicateDatabaseDumps(t *testing.T) {
	dumper := &fakeDumper{described: map[int64]DatabaseDescription{7: {ID: 7, Name: "shop"}}}
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s := NewStore(db, nil).WithDatabaseValidator(dumper.DescribeDatabase)
	base := Job{Name: "bad", Sources: []string{t.TempDir()}, TargetKind: TargetLocal, Target: TargetConfig{Path: t.TempDir()}}
	for name, dumps := range map[string][]int64{"unknown": {7, 42}, "duplicate": {7, 7}, "negative": {-1}} {
		job := base
		job.Name, job.DatabaseDumps = name, dumps
		if _, err := s.Create(t.Context(), &job, nil); err == nil {
			t.Fatalf("%s dump list was accepted", name)
		}
	}
	// A runner without a dump owner refuses to pretend.
	job := base
	job.Name, job.DatabaseDumps = "ok", []int64{7}
	created, err := s.Create(t.Context(), &job, nil)
	if err != nil {
		t.Fatal(err)
	}
	runner := NewRunner(s, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	run, err := runner.Execute(t.Context(), created.ID, "test")
	if err != nil || run.Status != StatusFailed || !strings.Contains(run.Log, "database dump owner is unavailable") {
		t.Fatalf("run without a dump owner = %+v, %v", run, err)
	}
}
