package backups

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"filippo.io/age"

	"github.com/Wayy01/Just-Dashboard/backend/internal/auth"
	"github.com/Wayy01/Just-Dashboard/backend/internal/store/storetest"
)

// sealedFixture is a store that can seal secrets, a runner, and a local job
// over one directory holding a configuration file and a note.
func sealedFixture(t *testing.T, encrypt bool) (*Store, *Runner, *Job, string) {
	t.Helper()
	db, err := storetest.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	sealer, err := auth.NewSealer(strings.Repeat("7e", 32))
	if err != nil {
		t.Fatal(err)
	}
	s := NewStore(db, sealer)
	r := NewRunner(s, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	source := t.TempDir()
	if err := os.MkdirAll(filepath.Join(source, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "etc", "app.conf"), []byte("listen 8080\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "notes.txt"), []byte("first notes"), 0o644); err != nil {
		t.Fatal(err)
	}
	job, err := s.Create(t.Context(), &Job{Name: "sealed", Sources: []string{source}, Encrypt: encrypt,
		TargetKind: TargetLocal, Target: TargetConfig{Path: t.TempDir()}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return s, r, job, source
}

func runJob(t *testing.T, r *Runner, job *Job) *Run {
	t.Helper()
	run, err := r.Execute(t.Context(), job.ID, "test")
	if err != nil || run.Status != StatusSuccess {
		t.Fatalf("run = %+v, %v", run, err)
	}
	return run
}

func digestOf(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// rewriteManifest stands in for somebody who could change the run record as
// well as the artifact, which is what the digest alone cannot catch.
func rewriteManifest(t *testing.T, s *Store, run *Run, artifact []byte) {
	t.Helper()
	manifest := *run.Manifest
	manifest.ArtifactDigest = digestOf(artifact)
	if err := os.WriteFile(run.Artifact, artifact, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.st.DB.Exec(`UPDATE backup_runs SET manifest_json = ? WHERE id = ?`, encodeJSON(&manifest), run.ID); err != nil {
		t.Fatal(err)
	}
}

func TestEncryptedArchiveRoundTripsThroughEveryReader(t *testing.T) {
	s, r, job, source := sealedFixture(t, true)
	run := runJob(t, r, job)
	if !strings.HasSuffix(run.Artifact, ".tar.gz.age") {
		t.Fatalf("artifact %s does not say it needs the key", run.Artifact)
	}
	stored, err := os.ReadFile(run.Artifact)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(stored, []byte("age-encryption.org/v1\n")) {
		t.Fatalf("artifact is not an age file: %q", stored[:min(len(stored), 32)])
	}
	key, err := s.ArchiveKeyStatus(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	evidence := run.Manifest.Encryption
	if evidence == nil || evidence.Format != "age" || evidence.Recipient != key.Recipient || !strings.HasPrefix(key.Recipient, "age1") {
		t.Fatalf("encryption evidence = %+v, key %+v", evidence, key)
	}
	// The artifact digest is over what the destination holds; the plaintext
	// digest is over the .tar.gz an operator decrypting by hand ends up with.
	if run.Manifest.ArtifactDigest != digestOf(stored) {
		t.Fatal("artifact digest is not over the stored ciphertext")
	}
	identity, _, err := s.archiveIdentity(t.Context(), false)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := age.Decrypt(bytes.NewReader(stored), identity)
	if err != nil {
		t.Fatal(err)
	}
	inside, err := io.ReadAll(plain)
	if err != nil {
		t.Fatal(err)
	}
	if digestOf(inside) != evidence.PlaintextDigest || int64(len(inside)) != evidence.PlaintextBytes || inside[0] != 0x1f || inside[1] != 0x8b {
		t.Fatalf("plaintext evidence %+v does not describe the gzip inside (%d bytes)", evidence, len(inside))
	}
	if !strings.Contains(run.Log, "encrypted with this server's backup key") ||
		strings.Contains(run.Log, "AGE-SECRET-KEY") || strings.Contains(run.Log, key.Recipient) {
		t.Fatalf("log = %s", run.Log)
	}

	entries, err := r.ListArchive(t.Context(), run.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	for _, entry := range entries {
		listed[entry.Name] = true
	}
	if !listed["source-0001/etc/app.conf"] || !listed["source-0001/notes.txt"] {
		t.Fatalf("listed = %v", listed)
	}
	if err := r.VerifyCoverage(t.Context(), run.ID, []string{source}); err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	if _, err := r.Restore(t.Context(), run.ID, dest); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(dest, "source-0001", "etc", "app.conf")); err != nil || string(data) != "listen 8080\n" {
		t.Fatalf("restored app.conf = %q, %v", data, err)
	}
	if err := os.WriteFile(filepath.Join(source, "notes.txt"), []byte("later notes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := r.RestoreInPlace(t.Context(), run.ID); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(source, "notes.txt")); string(data) != "first notes" {
		t.Fatalf("in-place restore left %q", data)
	}
}

func TestEncryptedArchiveRefusesTampering(t *testing.T) {
	for _, damage := range []string{"stored bytes", "payload", "header", "truncated"} {
		t.Run(damage, func(t *testing.T) {
			s, r, job, _ := sealedFixture(t, true)
			run := runJob(t, r, job)
			stored, err := os.ReadFile(run.Artifact)
			if err != nil {
				t.Fatal(err)
			}
			body := bytes.Index(stored, []byte("\n--- ")) + 1
			changed := append([]byte{}, stored...)
			want := "damaged or was altered"
			switch damage {
			case "stored bytes":
				// The record is intact, so the digest catches it before
				// anything is decrypted.
				changed[len(changed)-5] ^= 0x01
				if err := os.WriteFile(run.Artifact, changed, 0o600); err != nil {
					t.Fatal(err)
				}
				want = "checksum does not match"
			case "payload":
				changed[len(changed)-20] ^= 0x01
				rewriteManifest(t, s, run, changed)
			case "header":
				changed[body+5] ^= 0x01
				rewriteManifest(t, s, run, changed)
			case "truncated":
				rewriteManifest(t, s, run, changed[:len(changed)-40])
			}
			_, err = r.Restore(t.Context(), run.ID, t.TempDir())
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("restore of a damaged archive = %v, want %q", err, want)
			}
			if _, err := r.ListArchive(t.Context(), run.ID, 100); err == nil {
				t.Fatal("listing a damaged archive succeeded")
			}
		})
	}
}

func TestEncryptedArchiveOpensOnlyWithTheKeyItWasSealedTo(t *testing.T) {
	s, r, job, _ := sealedFixture(t, true)
	run := runJob(t, r, job)
	if _, err := s.st.DB.Exec(`DELETE FROM settings WHERE key = ?`, archiveKeySetting); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Restore(t.Context(), run.ID, t.TempDir()); !errors.Is(err, ErrNoArchiveKey) {
		t.Fatalf("restore without a key = %v", err)
	}
	// A read never makes a key: a new one would not open the archive.
	if status, _ := s.ArchiveKeyStatus(t.Context()); status.Exists {
		t.Fatal("a read made a key")
	}
	if err := s.createArchiveKey(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Restore(t.Context(), run.ID, t.TempDir()); !errors.Is(err, ErrArchiveKey) {
		t.Fatalf("restore with another key = %v", err)
	}
	// age's own check, beneath the manifest's: the wrong identity cannot open
	// the file whatever the record says.
	other, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (ArchiveFile{Path: run.Artifact, identity: other}).Open(); !errors.Is(err, ErrArchiveKey) {
		t.Fatalf("open with the wrong identity = %v", err)
	}
	// The job itself keeps working, sealing to the key the server now has.
	next := runJob(t, r, job)
	if _, err := r.Restore(t.Context(), next.ID, t.TempDir()); err != nil {
		t.Fatal(err)
	}
}

func TestLargeEncryptedArchiveStreamsWithoutLoss(t *testing.T) {
	if testing.Short() {
		t.Skip("writes and restores 80 MiB")
	}
	_, r, job, source := sealedFixture(t, true)
	// Incompressible and not a whole number of age's 64 KiB chunks.
	const size = 80<<20 + 12345
	big, err := os.Create(filepath.Join(source, "blob.bin"))
	if err != nil {
		t.Fatal(err)
	}
	want := sha256.New()
	random := rand.NewChaCha8([32]byte{1, 2, 3})
	if _, err := io.CopyN(io.MultiWriter(big, want), random, size); err != nil {
		t.Fatal(err)
	}
	if err := big.Close(); err != nil {
		t.Fatal(err)
	}
	run := runJob(t, r, job)
	if run.Manifest.Encryption.PlaintextBytes < size || run.SizeBytes <= run.Manifest.Encryption.PlaintextBytes {
		t.Fatalf("sizes: stored %d, plaintext %d", run.SizeBytes, run.Manifest.Encryption.PlaintextBytes)
	}
	dest := t.TempDir()
	if _, err := r.Restore(t.Context(), run.ID, dest, RestoreOptions{Paths: []string{"source-0001/blob.bin"}}); err != nil {
		t.Fatal(err)
	}
	restored, err := os.Open(filepath.Join(dest, "source-0001", "blob.bin"))
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	got := sha256.New()
	if _, err := io.Copy(got, restored); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Sum(nil), want.Sum(nil)) {
		t.Fatal("the restored file differs from the one backed up")
	}
}

func TestArchivesTakenInTheClearStillRestoreAroundEncryptedOnes(t *testing.T) {
	s, r, job, _ := sealedFixture(t, false)
	before := runJob(t, r, job)
	job.Encrypt = true
	job, err := s.Update(t.Context(), job.ID, job, nil)
	if err != nil {
		t.Fatal(err)
	}
	sealed := runJob(t, r, job)
	job.Encrypt = false
	if job, err = s.Update(t.Context(), job.ID, job, nil); err != nil {
		t.Fatal(err)
	}
	after := runJob(t, r, job)
	if before.Manifest.Encryption != nil || after.Manifest.Encryption != nil || sealed.Manifest.Encryption == nil ||
		strings.HasSuffix(before.Artifact, ".age") || !strings.HasSuffix(sealed.Artifact, ".age") {
		t.Fatalf("runs: %s %s %s", before.Artifact, sealed.Artifact, after.Artifact)
	}
	// An archive from before manifests existed reads the same as it did.
	if _, err := s.st.DB.Exec(`UPDATE backup_runs SET manifest_json = '{}' WHERE id = ?`, before.ID); err != nil {
		t.Fatal(err)
	}
	for _, run := range []*Run{before, sealed, after} {
		dest := t.TempDir()
		if _, err := r.Restore(t.Context(), run.ID, dest); err != nil {
			t.Fatalf("run %d: %v", run.ID, err)
		}
		if data, err := os.ReadFile(filepath.Join(dest, "source-0001", "etc", "app.conf")); err != nil || string(data) != "listen 8080\n" {
			t.Fatalf("run %d restored %q, %v", run.ID, data, err)
		}
	}
	status, err := s.ArchiveKeyStatus(t.Context())
	if err != nil || !status.Exists || status.EncryptingJobs != 0 || status.EncryptedArchives != 1 {
		t.Fatalf("status = %+v, %v", status, err)
	}
}

func TestBackupKeyIsMadeOnceAndExportsAsAnAgeIdentity(t *testing.T) {
	s, r, job, _ := sealedFixture(t, false)
	status, err := s.ArchiveKeyStatus(t.Context())
	if err != nil || status.Exists || status.EncryptingJobs != 0 {
		t.Fatalf("status before any job encrypts = %+v, %v", status, err)
	}
	// Two jobs encrypting for the first time at once share one key.
	recipients := make([]string, 8)
	var wg sync.WaitGroup
	for i := range recipients {
		wg.Go(func() {
			identity, _, err := s.archiveIdentity(context.Background(), true)
			if err == nil {
				recipients[i] = identity.Recipient().String()
			}
		})
	}
	wg.Wait()
	for _, recipient := range recipients {
		if recipient == "" || recipient != recipients[0] {
			t.Fatalf("recipients = %v", recipients)
		}
	}
	var row string
	if err := s.st.DB.QueryRow(`SELECT value FROM settings WHERE key = ?`, archiveKeySetting).Scan(&row); err != nil || strings.Contains(row, "AGE-SECRET-KEY") {
		t.Fatalf("stored key row is not sealed: %v", err)
	}
	job.Encrypt = true
	if job, err = s.Update(t.Context(), job.ID, job, nil); err != nil {
		t.Fatal(err)
	}
	run := runJob(t, r, job)

	recipient, file, err := s.ExportArchiveKey(t.Context())
	if err != nil || recipient != recipients[0] || !bytes.Contains(file, []byte("# public key: "+recipient+"\n")) {
		t.Fatalf("export = %s %v\n%s", recipient, err, file)
	}
	// What `age --decrypt --identity` does with the file, away from the dashboard.
	identities, err := age.ParseIdentities(bytes.NewReader(file))
	if err != nil || len(identities) != 1 {
		t.Fatalf("exported file holds %d identities: %v", len(identities), err)
	}
	stored, err := os.Open(run.Artifact)
	if err != nil {
		t.Fatal(err)
	}
	defer stored.Close()
	plain, err := age.Decrypt(stored, identities...)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.New()
	gz, err := gzip.NewReader(io.TeeReader(plain, digest))
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	reader := tar.NewReader(gz)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, header.Name)
	}
	_, _ = io.Copy(io.Discard, plain)
	if !strings.Contains(strings.Join(names, " "), "source-0001/etc/app.conf") ||
		"sha256:"+hex.EncodeToString(digest.Sum(nil)) != run.Manifest.Encryption.PlaintextDigest {
		t.Fatalf("decrypted by hand: %v", names)
	}

	status, err = s.ArchiveKeyStatus(t.Context())
	if err != nil || !status.Exists || status.Recipient != recipient || status.ExportedAt == nil || status.CreatedAt == nil ||
		status.EncryptingJobs != 1 || status.EncryptedArchives != 1 || strings.Contains(encodeJSON(status), "AGE-SECRET-KEY") {
		t.Fatalf("status after export = %+v, %v", status, err)
	}
}

func TestEncryptedArchiveCarriesDatabaseDumpsAndRecoveryChecks(t *testing.T) {
	dumper := &fakeDumper{
		content:   map[int64]string{7: "PGDMP shop rows"},
		described: map[int64]DatabaseDescription{7: {ID: 7, Name: "shop", Driver: "postgres", Database: "shop"}},
	}
	s, r, job, source := sealedFixture(t, true)
	s.WithDatabaseValidator(dumper.DescribeDatabase)
	r.WithDatabaseDumper(dumper)
	if err := os.WriteFile(filepath.Join(source, "canary"), []byte("known-record"), 0o600); err != nil {
		t.Fatal(err)
	}
	job.DatabaseDumps = []int64{7}
	job.Recovery = &RecoveryPlan{Image: "sha256:" + strings.Repeat("a", 64), Command: []string{"/app/check"},
		SchemaVersion: "schema-v1", ExpectedOutputDigest: canaryDigest("known-record")}
	job, err := s.Update(t.Context(), job.ID, job, nil)
	if err != nil {
		t.Fatal(err)
	}
	run := runJob(t, r, job)
	if err := r.VerifyDatabaseCoverage(t.Context(), run.ID, []int64{7}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.RestoreDatabase(t.Context(), run.ID, 7, "drill"); err != nil {
		t.Fatal(err)
	}
	if len(dumper.restored) != 1 || dumper.restored[0].content != "PGDMP shop rows" || dumper.restored[0].database != "drill" {
		t.Fatalf("restored = %+v", dumper.restored)
	}
	checker := &recoveryFixtureChecker{}
	r.WithRecoveryChecker(checker)
	record, err := r.VerifyRestore(t.Context(), run.ID)
	if err != nil || !record.Matches(run, job.Recovery) {
		t.Fatalf("verification = %+v, %v", record, err)
	}
}
