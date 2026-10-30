package backups

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"filippo.io/age"
	"golang.org/x/crypto/ssh"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store/storetest"
)

// server is one dashboard's backups: its own database, master key and
// staging, as a second server in the same test has its own.
type server struct {
	store  *Store
	runner *Runner
}

func newServer(t *testing.T, masterKey string) *server {
	t.Helper()
	db, err := storetest.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	sealer, err := auth.NewSealer(strings.Repeat(masterKey, 32))
	if err != nil {
		t.Fatal(err)
	}
	s := NewStore(db, sealer)
	return &server{store: s, runner: NewRunner(s, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))}
}

// writeTree makes a directory holding a configuration file and a note.
func writeTree(t *testing.T, root, note string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "etc", "app.conf"), []byte("listen 8080\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte(note), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (s *server) job(t *testing.T, name string, sources []string, destination string, encrypt bool) *Job {
	t.Helper()
	job, err := s.store.Create(t.Context(), &Job{Name: name, Sources: sources, Encrypt: encrypt,
		TargetKind: TargetLocal, Target: TargetConfig{Path: destination}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func (s *server) run(t *testing.T, job *Job) *Run {
	t.Helper()
	run, err := s.runner.Execute(t.Context(), job.ID, "test")
	if err != nil || run.Status != StatusSuccess {
		t.Fatalf("run = %+v, %v", run, err)
	}
	return run
}

func dirDestination(dir string) Destination {
	return Destination{Kind: TargetLocal, Target: TargetConfig{Path: dir}, Secrets: &TargetSecrets{}}
}

func TestEveryRunWritesItsManifestBesideTheArchive(t *testing.T) {
	a := newServer(t, "a1")
	source, destination := t.TempDir(), t.TempDir()
	writeTree(t, source, "first")
	for _, encrypt := range []bool{false, true} {
		job := a.job(t, "files-"+map[bool]string{false: "clear", true: "sealed"}[encrypt], []string{source}, destination, encrypt)
		run := a.run(t, job)
		raw, err := os.ReadFile(run.Artifact + sidecarSuffix)
		if err != nil {
			t.Fatalf("no manifest beside %s: %v", run.Artifact, err)
		}
		sidecar, err := parseSidecar(raw, filepath.Base(run.Artifact))
		if err != nil {
			t.Fatal(err)
		}
		id, _ := a.store.InstallID(t.Context())
		if sidecar.Origin.ID != id || sidecar.Origin.RunID != run.ID || sidecar.Origin.JobName != job.Name ||
			sidecar.ArtifactDigest != run.Manifest.ArtifactDigest || sidecar.ArtifactBytes != run.SizeBytes || sidecar.Contents.Paths != 1 {
			t.Fatalf("sidecar = %+v, run = %+v", sidecar, run)
		}
		if run.Manifest.Origin == nil || run.Manifest.Origin.ID != id || run.Manifest.ArtifactBytes != run.SizeBytes {
			t.Fatalf("the run's own manifest has no origin: %+v", run.Manifest)
		}
		if encrypt {
			// Who took it and when stays readable; what it holds does not.
			if sidecar.Manifest != nil || sidecar.SealedManifest == "" || sidecar.Encryption.Recipient != run.Manifest.Encryption.Recipient ||
				strings.Contains(string(raw), source) {
				t.Fatalf("an encrypted archive's manifest is readable beside it: %s", raw)
			}
		} else if sidecar.Manifest == nil || sidecar.Manifest.Sources[0].Path != source {
			t.Fatalf("a clear archive's manifest is not beside it: %s", raw)
		}
		opened, err := a.store.openSidecar(t.Context(), sidecar)
		if err != nil || opened.ArtifactDigest != run.Manifest.ArtifactDigest || opened.Sources[0].Path != source {
			t.Fatalf("opened = %+v, %v", opened, err)
		}
	}
}

func TestSidecarsThatDisagreeWithThemselvesAreRefused(t *testing.T) {
	manifest := &Manifest{Version: 1, Complete: true, ArtifactDigest: digestOf([]byte("archive")), ArtifactBytes: 7,
		Sources: []ManifestSource{{Path: "/srv/app", ArchivePath: "source-0001"}},
		Origin:  &Origin{ID: "abc", Host: "old", JobID: 3, JobName: "files", RunID: 9, StartedAt: time.Unix(1700000000, 0).UTC()}}
	raw, err := newSidecar("files-job3-run9-20231114-221320-ABC.tar.gz", manifest, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseSidecar(raw, "files-job3-run9-20231114-221320-ABC.tar.gz"); err != nil {
		t.Fatal(err)
	}
	for name, edit := range map[string]func(string) string{
		"another format":  func(s string) string { return strings.Replace(s, sidecarFormat, "something-else", 1) },
		"a later version": func(s string) string { return strings.Replace(s, `"version": 1`, `"version": 2`, 1) },
		"no digest":       func(s string) string { return strings.Replace(s, manifest.ArtifactDigest, "sha256:00", 1) },
		"no origin":       func(s string) string { return strings.Replace(s, `"runId": 9`, `"runId": 0`, 1) },
	} {
		if _, err := parseSidecar([]byte(edit(string(raw))), "files-job3-run9-20231114-221320-ABC.tar.gz"); err == nil {
			t.Errorf("%s: a sidecar was accepted", name)
		}
	}
	if _, err := parseSidecar(raw, "another.tar.gz"); err == nil {
		t.Error("a sidecar naming another archive was accepted")
	}
	a := newServer(t, "a2")
	sidecar, _ := parseSidecar(raw, "files-job3-run9-20231114-221320-ABC.tar.gz")
	sidecar.Manifest.ArtifactDigest = digestOf([]byte("other"))
	if _, err := a.store.openSidecar(t.Context(), sidecar); err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("a manifest disagreeing with its sidecar about the digest: %v", err)
	}
}

func TestArtifactNamesSayTheJobAndWhenTheRunStarted(t *testing.T) {
	parsed, ok := parseArtifactName("Dashboard-data-job12-run340-20261028-030000-K7QXW2M4.tar.gz.age")
	if !ok || parsed.job != "Dashboard-data" || parsed.jobID != 12 || !parsed.encrypted ||
		!parsed.started.Equal(time.Date(2026, 10, 28, 3, 0, 0, 0, time.UTC)) {
		t.Fatalf("parsed = %+v, %v", parsed, ok)
	}
	if _, ok := parseArtifactName("hand-made.tar.gz"); ok {
		t.Fatal("a name the runner did not write was read as one")
	}
}

// One server backs up to a directory; a second, fresh one lists it, imports
// the job's history, restores it in place where the trees do not exist yet,
// and never prunes what it imported.
func TestAFreshServerImportsAnotherServersArchivesAndRestoresThem(t *testing.T) {
	ctx := t.Context()
	a, b := newServer(t, "a3"), newServer(t, "b3")
	root := t.TempDir()
	source := filepath.Join(root, "old-host", "srv", "app")
	writeTree(t, source, "first")
	destination := t.TempDir()
	job := a.job(t, "App files", []string{source}, destination, false)
	first := a.run(t, job)
	if err := os.WriteFile(filepath.Join(source, "notes.txt"), []byte("second"), 0o600); err != nil {
		t.Fatal(err)
	}
	second := a.run(t, job)

	// The server that wrote them lists none of them as another's.
	own, err := a.runner.Catalogue(ctx, dirDestination(destination))
	if err != nil || own.Ours != 2 || own.Archives != 0 {
		t.Fatalf("own catalogue = %+v, %v", own, err)
	}

	catalogue, err := b.runner.Catalogue(ctx, dirDestination(destination))
	if err != nil {
		t.Fatal(err)
	}
	aID, _ := a.store.InstallID(ctx)
	if len(catalogue.Origins) != 1 || catalogue.Origins[0].ID != aID || catalogue.Origins[0].ThisServer || catalogue.Ours != 0 {
		t.Fatalf("catalogue = %+v", catalogue)
	}
	group := catalogue.Origins[0].Jobs
	if len(group) != 1 || group[0].Name != "App files" || group[0].OriginJobID != job.ID || len(group[0].Archives) != 2 {
		t.Fatalf("jobs = %+v", group)
	}
	newest := group[0].Archives[0]
	if newest.Key != second.Artifact || !newest.Importable || !newest.Openable || newest.Manifest != "sidecar" ||
		newest.Digest != second.Manifest.ArtifactDigest || strings.Join(newest.Kinds, ",") != "paths" || newest.Sources[0].Path != source {
		t.Fatalf("newest archive = %+v", newest)
	}

	result, err := b.runner.Import(ctx, ImportRequest{Destination: dirDestination(destination), OriginID: aID, OriginJobID: job.ID})
	if err != nil || len(result.Runs) != 2 || len(result.Jobs) != 1 || len(result.Skipped) != 0 {
		t.Fatalf("import = %+v, %v", result, err)
	}
	imported := result.Jobs[0]
	if !imported.Imported || imported.Enabled || imported.Schedule != "" || imported.Origin == nil || imported.Origin.ID != aID ||
		imported.Origin.JobName != "App files" || imported.Name != "App files ("+hostName()+")" || imported.Stored.Runs != 2 {
		t.Fatalf("imported job = %+v", imported)
	}
	// Oldest first, so the history reads in the order it was taken.
	if run := result.Runs[0]; !run.Imported || run.Artifact != first.Artifact || run.Trigger != "import" || run.LocalCopy != "" ||
		run.Manifest.ArtifactDigest != first.Manifest.ArtifactDigest || !run.StartedAt.Equal(first.Manifest.Origin.StartedAt) {
		t.Fatalf("first imported run = %+v", run)
	}

	// Importing again adopts nothing twice.
	again, err := b.runner.Import(ctx, ImportRequest{Destination: dirDestination(destination), Keys: []string{second.Artifact}})
	if err != nil || len(again.Runs) != 0 || len(again.Skipped) != 1 || !strings.Contains(again.Skipped[0].Reason, "already imported") {
		t.Fatalf("second import = %+v, %v", again, err)
	}
	listed, _ := b.runner.Catalogue(ctx, dirDestination(destination))
	if archive := listed.Origins[0].Jobs[0].Archives[0]; archive.ImportedRun == 0 || archive.Importable || listed.Origins[0].Jobs[0].ImportedJob != imported.ID {
		t.Fatalf("an imported archive is not marked: %+v", listed.Origins[0].Jobs[0])
	}

	// Gone from where it was: the restore makes the tree and its parents.
	if err := os.RemoveAll(filepath.Join(root, "old-host")); err != nil {
		t.Fatal(err)
	}
	plan, err := b.runner.PlanRestoreInPlace(ctx, result.Runs[1].ID)
	if err != nil || len(plan) != 1 || plan[0].Target != source || plan[0].Exists || plan[0].Creates != "directory" {
		t.Fatalf("plan = %+v, %v", plan, err)
	}
	restored, err := b.runner.RestoreInPlace(ctx, result.Runs[1].ID)
	if err != nil || len(restored.Created) != 1 || restored.Created[0] != source {
		t.Fatalf("restore = %+v, %v", restored, err)
	}
	if note, err := os.ReadFile(filepath.Join(source, "notes.txt")); err != nil || string(note) != "second" {
		t.Fatalf("restored note = %q, %v", note, err)
	}

	// Never run, never scheduled, never edited, and never pruned from here.
	if _, err := b.runner.Execute(ctx, imported.ID, "test"); !errors.Is(err, ErrImportedJob) {
		t.Fatalf("an imported job ran: %v", err)
	}
	if _, err := b.store.SetEnabled(ctx, imported.ID, true); !errors.Is(err, ErrImportedJob) {
		t.Fatalf("an imported job was scheduled: %v", err)
	}
	edit := *imported
	edit.Imported = false
	if _, err := b.store.Update(ctx, imported.ID, &edit, nil); !errors.Is(err, ErrImportedJob) {
		t.Fatalf("an imported job was edited: %v", err)
	}
	if _, err := b.store.st.DB.Exec(`UPDATE backup_jobs SET retention = 1, retention_days = 1 WHERE id = ?`, imported.ID); err != nil {
		t.Fatal(err)
	}
	pruned, _ := b.store.Get(ctx, imported.ID)
	if err := b.runner.prune(ctx, pruned); err != nil {
		t.Fatal(err)
	}
	// And a job of this server's own keeping one archive in the same place
	// prunes its own and nothing it imported.
	local := b.job(t, "Local files", []string{source}, destination, false)
	if _, err := b.store.st.DB.Exec(`UPDATE backup_jobs SET retention = 1 WHERE id = ?`, local.ID); err != nil {
		t.Fatal(err)
	}
	b.run(t, local)
	b.run(t, local)
	runs, _ := b.store.Runs(ctx, imported.ID, 10)
	if len(runs) != 2 {
		t.Fatalf("imported runs after retention = %d", len(runs))
	}
	for _, run := range append(runs, first, second) {
		if _, err := os.Stat(run.Artifact); err != nil {
			t.Fatalf("retention removed an imported archive: %v", err)
		}
	}
	if own, _ := b.store.Runs(ctx, local.ID, 10); len(own) != 1 {
		t.Fatalf("this server's own retention kept %d runs", len(own))
	}
}

func TestAnImportRefusesAnArchiveThatIsNotTheOneItsManifestDescribes(t *testing.T) {
	ctx := t.Context()
	a, b := newServer(t, "a4"), newServer(t, "b4")
	source, destination := t.TempDir(), t.TempDir()
	writeTree(t, source, "original")
	run := a.run(t, a.job(t, "Tampered", []string{source}, destination, false))
	data, err := os.ReadFile(run.Artifact)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)/2] ^= 0xff
	if err := os.WriteFile(run.Artifact, data, 0o600); err != nil {
		t.Fatal(err)
	}
	copies := t.TempDir()
	_, err = b.runner.Import(ctx, ImportRequest{Destination: dirDestination(destination), Keys: []string{run.Artifact}, CopyTo: copies})
	if !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("a changed archive was imported: %v", err)
	}
	if entries, _ := os.ReadDir(copies); len(entries) != 0 {
		t.Fatalf("the copy of a refused archive was kept: %v", entries)
	}
	jobs, _ := b.store.List(ctx)
	if len(jobs) != 0 {
		t.Fatalf("a refused import left a job: %+v", jobs)
	}
	// A shorter archive is refused by its size before a byte is read.
	if err := os.WriteFile(run.Artifact, data[:len(data)/2], 0o600); err != nil {
		t.Fatal(err)
	}
	catalogue, _ := b.runner.Catalogue(ctx, dirDestination(destination))
	if archive := catalogue.Origins[0].Jobs[0].Archives[0]; archive.Importable || !strings.Contains(archive.Problem, "incomplete") {
		t.Fatalf("a short archive reads as importable: %+v", archive)
	}
	if _, err := b.runner.Import(ctx, ImportRequest{Destination: dirDestination(destination), Keys: []string{run.Artifact}}); !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("a short archive was imported: %v", err)
	}
}

func TestAnEncryptedArchiveImportsOnceItsKeyIsHeldAndNamesTheKeyUntilThen(t *testing.T) {
	ctx := t.Context()
	a, b := newServer(t, "a5"), newServer(t, "b5")
	source, destination := t.TempDir(), t.TempDir()
	writeTree(t, source, "sealed note")
	run := a.run(t, a.job(t, "Sealed", []string{source}, destination, true))
	recipient := run.Manifest.Encryption.Recipient

	catalogue, err := b.runner.Catalogue(ctx, dirDestination(destination))
	if err != nil {
		t.Fatal(err)
	}
	archive := catalogue.Origins[0].Jobs[0].Archives[0]
	if !archive.Encrypted || archive.Recipient != recipient || archive.Openable || archive.Importable || archive.Sources != nil || !archive.NeedsKey ||
		strings.Join(archive.Kinds, ",") != "paths" {
		t.Fatalf("a sealed archive without its key = %+v", archive)
	}
	_, err = b.runner.Import(ctx, ImportRequest{Destination: dirDestination(destination), Keys: []string{run.Artifact}})
	var keyErr *KeyError
	if !errors.As(err, &keyErr) || keyErr.Recipient != recipient || !errors.Is(err, ErrNoArchiveKey) || !strings.Contains(err.Error(), recipient) {
		t.Fatalf("import without the key: %v", err)
	}

	_, exported, err := a.store.ExportArchiveKey(ctx)
	if err != nil {
		t.Fatal(err)
	}
	became, err := b.store.ImportArchiveKey(ctx, string(exported))
	if err != nil || became.Became != "own" || became.Recipient != recipient {
		t.Fatalf("key import = %+v, %v", became, err)
	}
	status, _ := b.store.ArchiveKeyStatus(ctx)
	if !status.Exists || status.Recipient != recipient || status.ExportedAt == nil {
		t.Fatalf("an adopted key reads as %+v", status)
	}
	result, err := b.runner.Import(ctx, ImportRequest{Destination: dirDestination(destination), Keys: []string{run.Artifact}})
	if err != nil || len(result.Runs) != 1 {
		t.Fatalf("import with the key = %+v, %v", result, err)
	}
	target := t.TempDir()
	if _, err := b.runner.Restore(ctx, result.Runs[0].ID, target); err != nil {
		t.Fatal(err)
	}
	if note, err := os.ReadFile(filepath.Join(target, "source-0001", "notes.txt")); err != nil || string(note) != "sealed note" {
		t.Fatalf("restored = %q, %v", note, err)
	}
}

func TestKeysImportAsTheServersOwnFirstAndThenOnlyToOpen(t *testing.T) {
	ctx := t.Context()
	s := newServer(t, "c6")
	first, _ := age.GenerateX25519Identity()
	second, _ := age.GenerateX25519Identity()
	for _, bad := range []string{"", "not a key", first.String() + "\n" + second.String() + "\n"} {
		if _, err := s.store.ImportArchiveKey(ctx, bad); err == nil {
			t.Errorf("ImportArchiveKey(%q) was accepted", bad)
		}
	}
	file := "# Just Dashboard backup key\n# created: 2026-01-02T03:04:05Z\n# public key: " + first.Recipient().String() + "\n" + first.String() + "\n"
	if got, err := s.store.ImportArchiveKey(ctx, file); err != nil || got.Became != "own" {
		t.Fatalf("first key = %+v, %v", got, err)
	}
	status, _ := s.store.ArchiveKeyStatus(ctx)
	if status.CreatedAt == nil || !status.CreatedAt.Equal(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)) {
		t.Fatalf("the adopted key lost when it was made: %+v", status)
	}
	if got, err := s.store.ImportArchiveKey(ctx, file); err != nil || got.Became != "held" {
		t.Fatalf("the same key again = %+v, %v", got, err)
	}
	if got, err := s.store.ImportArchiveKey(ctx, second.String()); err != nil || got.Became != "opens" {
		t.Fatalf("second key = %+v, %v", got, err)
	}
	if got, err := s.store.ImportArchiveKey(ctx, second.String()); err != nil || got.Became != "held" {
		t.Fatalf("second key again = %+v, %v", got, err)
	}
	status, _ = s.store.ArchiveKeyStatus(ctx)
	if status.Recipient != first.Recipient().String() || len(status.Imported) != 1 || status.Imported[0].Recipient != second.Recipient().String() {
		t.Fatalf("status = %+v", status)
	}
	for _, identity := range []*age.X25519Identity{first, second} {
		if got, err := s.store.identityFor(ctx, identity.Recipient().String()); err != nil || got.String() != identity.String() {
			t.Fatalf("identityFor = %v", err)
		}
	}
	// A new archive is still sealed to the server's own key.
	own, _, err := s.store.archiveIdentity(ctx, true)
	if err != nil || own.Recipient().String() != first.Recipient().String() {
		t.Fatalf("own key = %v", err)
	}
}

// fakeVolumes stands in for Docker: volumes live under its root, and one
// made by EnsureVolume is recorded with its labels.
type fakeVolumes struct {
	mu      sync.Mutex
	root    string
	volumes map[string]map[string]string
	created []string
}

func (f *fakeVolumes) mountpoint(name string) string { return filepath.Join(f.root, name, "_data") }

func (f *fakeVolumes) VolumeAt(_ context.Context, path string) (LocalVolume, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for name, labels := range f.volumes {
		if f.mountpoint(name) == filepath.Clean(path) {
			return LocalVolume{Name: name, Mountpoint: path, Labels: labels}, true, nil
		}
	}
	return LocalVolume{}, false, nil
}

func (f *fakeVolumes) VolumeMountpoint(_ context.Context, name string) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.volumes[name]
	return f.mountpoint(name), ok, nil
}

func (f *fakeVolumes) EnsureVolume(_ context.Context, name string, labels map[string]string) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.volumes[name]; ok {
		return f.mountpoint(name), false, nil
	}
	f.volumes[name] = labels
	f.created = append(f.created, name)
	return f.mountpoint(name), true, os.MkdirAll(f.mountpoint(name), 0o755)
}

func TestAVolumeRestoredOnAHostWithoutItIsCreatedThroughDockerWithItsLabels(t *testing.T) {
	ctx := t.Context()
	a, b := newServer(t, "a7"), newServer(t, "b7")
	labels := map[string]string{"com.docker.compose.project": "shop", "com.docker.compose.volume": "db"}
	oldDocker := &fakeVolumes{root: t.TempDir(), volumes: map[string]map[string]string{"shop_db": labels}}
	a.runner.WithVolumeProvisioner(oldDocker)
	volume := oldDocker.mountpoint("shop_db")
	writeTree(t, volume, "rows")
	destination := t.TempDir()
	run := a.run(t, a.job(t, "Shop volume", []string{volume}, destination, false))
	if source := run.Manifest.Sources[0]; source.Kind != SourceVolume || source.Volume != "shop_db" || source.VolumeLabels["com.docker.compose.project"] != "shop" {
		t.Fatalf("the source was not recorded as a volume: %+v", source)
	}

	// The new host keeps its volumes somewhere else, and has none of them.
	newDocker := &fakeVolumes{root: t.TempDir(), volumes: map[string]map[string]string{}}
	b.runner.WithVolumeProvisioner(newDocker)
	result, err := b.runner.Import(ctx, ImportRequest{Destination: dirDestination(destination), Keys: []string{run.Artifact}})
	if err != nil || len(result.Runs) != 1 {
		t.Fatalf("import = %+v, %v", result, err)
	}
	if catalogue, _ := b.runner.Catalogue(ctx, dirDestination(destination)); strings.Join(catalogue.Origins[0].Jobs[0].Archives[0].Kinds, ",") != "volume" {
		t.Fatalf("kinds = %v", catalogue.Origins[0].Jobs[0].Archives[0].Kinds)
	}
	plan, err := b.runner.PlanRestoreInPlace(ctx, result.Runs[0].ID)
	if err != nil || len(plan) != 1 || plan[0].Kind != SourceVolume || plan[0].Creates != SourceVolume || plan[0].Volume != "shop_db" {
		t.Fatalf("plan = %+v, %v", plan, err)
	}
	if len(newDocker.created) != 0 {
		t.Fatal("planning a restore created a volume")
	}
	restored, err := b.runner.RestoreInPlace(ctx, result.Runs[0].ID)
	if err != nil || len(restored.Created) != 1 || restored.Created[0] != "volume shop_db" || restored.Targets[0] != newDocker.mountpoint("shop_db") {
		t.Fatalf("restore = %+v, %v", restored, err)
	}
	if newDocker.volumes["shop_db"]["com.docker.compose.volume"] != "db" {
		t.Fatalf("the volume was made without its labels: %+v", newDocker.volumes)
	}
	if note, err := os.ReadFile(filepath.Join(newDocker.mountpoint("shop_db"), "notes.txt")); err != nil || string(note) != "rows" {
		t.Fatalf("restored = %q, %v", note, err)
	}

	// Without Docker, a volume that is not here is refused rather than made
	// as a bare directory Docker would not know.
	c := newServer(t, "c7")
	imported, err := c.runner.Import(ctx, ImportRequest{Destination: dirDestination(destination), Keys: []string{run.Artifact}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(volume); err != nil {
		t.Fatal(err)
	}
	if _, err := c.runner.RestoreInPlace(ctx, imported.Runs[0].ID); err == nil || !strings.Contains(err.Error(), "Docker") {
		t.Fatalf("a volume restored without Docker: %v", err)
	}
}

func TestAnImportedRunPrefersTheCopyItKeptAndFallsBackToTheDestination(t *testing.T) {
	ctx := t.Context()
	a, b := newServer(t, "a8"), newServer(t, "b8")
	source, destination, copies := t.TempDir(), t.TempDir(), t.TempDir()
	writeTree(t, source, "kept")
	run := a.run(t, a.job(t, "Kept", []string{source}, destination, false))
	result, err := b.runner.Import(ctx, ImportRequest{Destination: dirDestination(destination), Keys: []string{run.Artifact}, CopyTo: copies})
	if err != nil || len(result.Runs) != 1 {
		t.Fatalf("import = %+v, %v", result, err)
	}
	imported := result.Runs[0]
	if imported.LocalCopy != filepath.Join(copies, filepath.Base(run.Artifact)) {
		t.Fatalf("local copy = %q", imported.LocalCopy)
	}
	if digest, _ := fileDigest(ctx, imported.LocalCopy); digest != run.Manifest.ArtifactDigest {
		t.Fatal("the copy is not the archive")
	}
	// With the destination's object gone, the copy is what restores.
	moved := run.Artifact + ".away"
	if err := os.Rename(run.Artifact, moved); err != nil {
		t.Fatal(err)
	}
	if _, err := b.runner.Restore(ctx, imported.ID, t.TempDir()); err != nil {
		t.Fatalf("restore from the copy: %v", err)
	}
	// With the copy gone too, the destination is read again once it is back.
	if err := os.Rename(moved, run.Artifact); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(imported.LocalCopy); err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	if _, err := b.runner.Restore(ctx, imported.ID, target); err != nil {
		t.Fatalf("restore from the destination: %v", err)
	}
	if note, err := os.ReadFile(filepath.Join(target, "source-0001", "notes.txt")); err != nil || string(note) != "kept" {
		t.Fatalf("restored = %q, %v", note, err)
	}
}

func TestRetentionRemovesTheManifestBesideAPrunedArchive(t *testing.T) {
	a := newServer(t, "a9")
	source, destination := t.TempDir(), t.TempDir()
	writeTree(t, source, "one")
	job := a.job(t, "Pruned", []string{source}, destination, false)
	if _, err := a.store.st.DB.Exec(`UPDATE backup_jobs SET retention = 1 WHERE id = ?`, job.ID); err != nil {
		t.Fatal(err)
	}
	first := a.run(t, job)
	second := a.run(t, job)
	if _, err := os.Stat(first.Artifact + sidecarSuffix); !os.IsNotExist(err) {
		t.Fatalf("the pruned archive's manifest was left: %v", err)
	}
	if _, err := os.Stat(second.Artifact + sidecarSuffix); err != nil {
		t.Fatalf("the kept archive's manifest went: %v", err)
	}
}

func TestACatalogueGroupsByServerAndJobAndListsOlderArchivesByName(t *testing.T) {
	ctx := t.Context()
	a, b, c := newServer(t, "aa"), newServer(t, "bb"), newServer(t, "cc")
	source, destination := t.TempDir(), t.TempDir()
	writeTree(t, source, "x")
	a.run(t, a.job(t, "Zeta", []string{source}, destination, false))
	a.run(t, a.job(t, "Alpha", []string{source}, destination, false))
	b.run(t, b.job(t, "Beta", []string{source}, destination, false))
	// What a server before 0.8.2 wrote: an archive and nothing beside it.
	if err := os.WriteFile(filepath.Join(destination, "Legacy-job4-run7-20250101-030000-ABCDEFGH.tar.gz"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	catalogue, err := c.runner.Catalogue(ctx, dirDestination(destination))
	if err != nil {
		t.Fatal(err)
	}
	if len(catalogue.Origins) != 3 || catalogue.Archives != 4 {
		t.Fatalf("catalogue = %+v", catalogue)
	}
	aID, _ := a.store.InstallID(ctx)
	byID := map[string]CatalogueOrigin{}
	for _, origin := range catalogue.Origins {
		byID[origin.ID] = origin
	}
	if jobs := byID[aID].Jobs; len(jobs) != 2 || jobs[0].Name != "Alpha" || jobs[1].Name != "Zeta" {
		t.Fatalf("server a's jobs = %+v", jobs)
	}
	legacy := catalogue.Origins[len(catalogue.Origins)-1]
	if legacy.ID != "" || len(legacy.Jobs) != 1 || legacy.Jobs[0].Name != "Legacy" || legacy.Jobs[0].OriginJobID != 4 {
		t.Fatalf("older archives = %+v", legacy)
	}
	old := legacy.Jobs[0].Archives[0]
	if old.Manifest != "none" || old.Importable || old.StartedAt == nil || !old.StartedAt.Equal(time.Date(2025, 1, 1, 3, 0, 0, 0, time.UTC)) {
		t.Fatalf("an older archive = %+v", old)
	}
	if _, err := c.runner.Import(ctx, ImportRequest{Destination: dirDestination(destination), Keys: []string{old.Key}}); err == nil {
		t.Fatal("an archive with no manifest was imported")
	}
}

// A server whose own records of an archive are gone — its job deleted, or the
// server rebuilt from an older copy of its data — lists it as its own and can
// take it back.
func TestArchivesThisServerWroteButNoLongerRecordsCanBeTakenBack(t *testing.T) {
	ctx := t.Context()
	a := newServer(t, "ab")
	source, destination := t.TempDir(), t.TempDir()
	writeTree(t, source, "mine")
	job := a.job(t, "Deleted later", []string{source}, destination, false)
	run := a.run(t, job)
	if err := a.store.Delete(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	catalogue, err := a.runner.Catalogue(ctx, dirDestination(destination))
	if err != nil || len(catalogue.Origins) != 1 || !catalogue.Origins[0].ThisServer || catalogue.Ours != 0 {
		t.Fatalf("catalogue = %+v, %v", catalogue, err)
	}
	result, err := a.runner.Import(ctx, ImportRequest{Destination: dirDestination(destination), Keys: []string{run.Artifact}})
	if err != nil || len(result.Jobs) != 1 || result.Jobs[0].Name != "Deleted later (imported)" || !result.Jobs[0].Origin.ThisServer {
		t.Fatalf("import = %+v, %v", result, err)
	}
}

// Over SFTP, a destination just entered is read with the server pinned to
// the key it presented, which the catalogue answers so it can be compared and
// which the imported job keeps; another key at the same address is refused.
func TestAnSFTPDestinationsCatalogueImportsAndRestoresThroughOneSession(t *testing.T) {
	ctx := t.Context()
	host := testHostSigner(t)
	sftpServer := newTestSFTPServer(t, host, nil)
	a, b := newServer(t, "ac"), newServer(t, "bc")
	source := t.TempDir()
	writeTree(t, source, "over sftp")
	target := sftpServer.job(t).Target
	secrets := &TargetSecrets{Password: "correct horse"}
	job, err := a.store.Create(ctx, &Job{Name: "Remote", Sources: []string{source}, TargetKind: TargetSFTP, Target: target}, secrets)
	if err != nil {
		t.Fatal(err)
	}
	run := a.run(t, job)

	noted := func(context.Context, ssh.PublicKey) error { return nil }
	entered := Destination{Kind: TargetSFTP, Target: target, Secrets: secrets, Pin: noted}
	catalogue, err := b.runner.Catalogue(ctx, entered)
	if err != nil {
		t.Fatal(err)
	}
	if catalogue.HostKeyFingerprint != ssh.FingerprintSHA256(host.PublicKey()) || len(catalogue.Origins) != 1 ||
		catalogue.Origins[0].Jobs[0].Archives[0].Key != run.Artifact || !catalogue.Origins[0].Jobs[0].Archives[0].Importable {
		t.Fatalf("catalogue = %+v", catalogue)
	}
	pinned := entered
	pinned.Target.HostKey = catalogue.HostKey
	result, err := b.runner.Import(ctx, ImportRequest{Destination: pinned, Keys: []string{run.Artifact}})
	if err != nil || len(result.Runs) != 1 || result.Jobs[0].Target.HostKeyFingerprint != catalogue.HostKeyFingerprint {
		t.Fatalf("import = %+v, %v", result, err)
	}
	restoreTo := t.TempDir()
	if _, err := b.runner.Restore(ctx, result.Runs[0].ID, restoreTo); err != nil {
		t.Fatal(err)
	}
	if note, err := os.ReadFile(filepath.Join(restoreTo, "source-0001", "notes.txt")); err != nil || string(note) != "over sftp" {
		t.Fatalf("restored = %q, %v", note, err)
	}

	impostor := newTestSFTPServer(t, testHostSigner(t), nil)
	moved := pinned
	moved.Target.Port = impostor.job(t).Target.Port
	if _, err := b.runner.Catalogue(ctx, moved); !errors.Is(err, ErrHostKeyChanged) {
		t.Fatalf("a server presenting another key was read: %v", err)
	}
}
