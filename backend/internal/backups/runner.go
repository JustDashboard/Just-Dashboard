package backups

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"filippo.io/age"
)

// Runner executes backup jobs. Only one run per job may be in flight: a slow
// job whose schedule fires again would otherwise pile up transfers and fight
// itself over the staging directory.
type Runner struct {
	store   *Store
	stage   string
	log     *slog.Logger
	mu      sync.Mutex
	running map[int64]bool
	// Retention must reserve deletion atomically against new readers. A
	// check-then-delete of a reference count still lets a restore race pruning.
	artifactMu     sync.RWMutex
	restoreMu      sync.Mutex
	recovery       RecoveryChecker
	sqliteSnapshot SQLiteSnapshotter
	databases      DatabaseDumper
	pauser         ContainerPauser
	volumes        VolumeProvisioner
	// importMu keeps two imports of one archive from both adopting it.
	importMu sync.Mutex
}

func NewRunner(store *Store, stageDir string, log *slog.Logger) *Runner {
	return &Runner{
		store: store, stage: stageDir, log: log,
		running: map[int64]bool{},
	}
}

// beginRead marks a run's artifact as in use and returns the release.
func (r *Runner) beginRead() (func(), error) {
	if !r.artifactMu.TryRLock() {
		return nil, errors.New("backup retention is removing an artifact; retry the read")
	}
	var once sync.Once
	return func() { once.Do(r.artifactMu.RUnlock) }, nil
}

func (r *Runner) IsRunning(jobID int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running[jobID]
}

var ErrAlreadyRunning = fmt.Errorf("a run for this job is already in progress")

// A fresh process cannot resume a partially captured archive. Mark it failed
// before scheduling new work, retaining the record for diagnosis.
func (r *Runner) RecoverInterruptedRuns(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.running) != 0 {
		return ErrAlreadyRunning
	}
	_, err := r.store.st.DB.ExecContext(ctx, `UPDATE backup_runs SET status='failed',ended_at=?,log=substr(log || ?, -16000) WHERE status='running'`, time.Now().UTC().Unix(), "\nFAILED: backup was interrupted before completion\n")
	return err
}

// Execute performs one backup end to end: archive, transfer, prune. It returns
// once the run is recorded, so a manual trigger can report the outcome.
func (r *Runner) Execute(ctx context.Context, jobID int64, trigger string) (*Run, error) {
	r.mu.Lock()
	if r.running[jobID] {
		r.mu.Unlock()
		return nil, ErrAlreadyRunning
	}
	r.running[jobID] = true
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.running, jobID)
		r.mu.Unlock()
	}()

	job, err := r.store.Get(ctx, jobID)
	if err != nil {
		return nil, err
	}
	if job.Imported {
		return nil, ErrImportedJob
	}
	runID, err := r.store.StartRun(ctx, jobID, trigger)
	if err != nil {
		return nil, err
	}

	var logBuf bytes.Buffer
	artifact, size, manifest, err := r.performWithManifest(ctx, job, runID, &logBuf)
	status := StatusSuccess
	if err != nil {
		status = StatusFailed
		fmt.Fprintf(&logBuf, "\nFAILED: %v\n", err)
		r.log.Error("backup failed", "job", job.Name, "err", err)
	}
	// The run record is written even on failure — a backup that silently did
	// not happen is worse than one that visibly broke.
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if finErr := r.store.finishRun(finishCtx, runID, status, artifact, size, logBuf.String(), manifest); finErr != nil {
		return nil, finErr
	}
	if status == StatusSuccess {
		if job.Recovery != nil && job.Recovery.Automatic {
			if _, verifyErr := r.VerifyRestore(ctx, runID); verifyErr != nil {
				r.log.Warn("backup recovery check failed", "job", job.Name, "run", runID)
			}
		}
		if pruneErr := r.prune(ctx, job); pruneErr != nil {
			r.log.Warn("retention prune failed", "job", job.Name, "err", pruneErr)
		}
	}
	readCtx, readCancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer readCancel()
	return r.store.Run(readCtx, runID)
}

func (r *Runner) perform(ctx context.Context, job *Job, runID int64, logBuf *bytes.Buffer) (string, int64, error) {
	artifact, size, _, err := r.performWithManifest(ctx, job, runID, logBuf)
	return artifact, size, err
}

func (r *Runner) performWithManifest(ctx context.Context, job *Job, runID int64, logBuf *bytes.Buffer) (string, int64, *Manifest, error) {
	if err := r.store.ValidatePaths(job); err != nil {
		return "", 0, nil, err
	}
	manifest := newManifest(job)
	// Without its origin the archive is whole and restores here; only the
	// manifest beside it, for another server to import it by, is left out.
	if origin, err := r.store.origin(ctx, job, runID, time.Now()); err == nil {
		manifest.Origin = origin
	} else {
		fmt.Fprintf(logBuf, "could not read this server's identity (%v); the archive is written without its manifest beside it\n", err)
	}
	r.annotateVolumes(ctx, manifest, logBuf)
	for _, source := range job.Sources {
		// A source that contains the staging directory would archive the
		// archive being written. The dashboard's own data directory is the
		// one source where that is legitimate — and it is excluded there.
		if relative, err := filepath.Rel(source, r.stage); err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !excluded(r.stage, job.Excludes) {
			return "", 0, manifest, fmt.Errorf("backup staging %s must be outside every source directory, or excluded", r.stage)
		}
	}
	if err := os.MkdirAll(r.stage, 0o700); err != nil {
		return "", 0, nil, err
	}
	stage, err := os.MkdirTemp(r.stage, fmt.Sprintf("run-%d-", runID))
	if err != nil {
		return "", 0, nil, err
	}
	defer os.RemoveAll(stage)
	snapshots, snapshotEvidence, err := r.prepareSQLiteSnapshots(ctx, job, stage)
	if err != nil {
		return "", 0, manifest, err
	}
	manifest.SQLiteSnapshots = snapshotEvidence
	dumps, dumpEvidence, err := r.prepareDatabaseDumps(ctx, job, stage, logBuf)
	if err != nil {
		return "", 0, manifest, err
	}
	manifest.DatabaseDumps = dumpEvidence
	var seal *archiveSeal
	if job.Encrypt {
		identity, _, err := r.store.archiveIdentity(ctx, true)
		if err != nil {
			return "", 0, manifest, err
		}
		seal = newArchiveSeal(identity)
	}
	name := artifactName(job, runID, time.Now())
	local := filepath.Join(stage, name)

	// Dumps come first, while the engines can still answer; the pause covers
	// only the filesystem walk, which is the one step a writing container
	// can corrupt.
	resume, err := r.pauseContainers(ctx, job, logBuf)
	if err != nil {
		return "", 0, manifest, err
	}
	manifest.PausedContainers = append([]string{}, job.PauseContainers...)
	fmt.Fprintf(logBuf, "archiving %d source(s) and %d database dump(s) into %s\n", len(job.Sources), len(dumps), name)
	size, count, err := r.archiveWithSnapshots(ctx, job, local, logBuf, seal, snapshots, dumps...)
	resume()
	if err != nil {
		return "", 0, manifest, err
	}
	// Over the bytes the destination holds, so every later read proves the
	// ciphertext before it decrypts any of it.
	manifest.ArtifactDigest, err = fileDigest(ctx, local)
	if err != nil {
		return "", 0, manifest, err
	}
	manifest.Encryption = seal.evidence()
	manifest.ArtifactBytes = size
	manifest.Complete = true
	fmt.Fprintf(logBuf, "archived %d file(s), %d bytes\n", count, size)
	if seal != nil {
		fmt.Fprintf(logBuf, "encrypted with this server's backup key before leaving the server\n")
	}

	switch job.TargetKind {
	case TargetLocal:
		dest := filepath.Join(job.Target.Path, name)
		if err := os.MkdirAll(job.Target.Path, 0o700); err != nil {
			return "", 0, manifest, err
		}
		if err := moveFile(local, dest); err != nil {
			return "", 0, manifest, err
		}
		fmt.Fprintf(logBuf, "stored at %s\n", dest)
		r.writeSidecar(manifest, name, seal, logBuf, func(sidecar []byte) error {
			return writeNewFile(dest+sidecarSuffix, sidecar)
		})
		return dest, size, manifest, nil

	case TargetS3, TargetB2, TargetSFTP, TargetRclone:
		// The staging copy is removed once uploaded; keeping it would double
		// the disk cost of every backup.
		defer os.Remove(local)
		secrets, err := r.store.Secrets(ctx, job.ID)
		if err != nil {
			return "", 0, manifest, err
		}
		key := remoteKey(job, name)
		pinnedBefore := job.Target.HostKey
		fmt.Fprintf(logBuf, "uploading to %s\n", remoteLabel(job, key))
		if err := uploadArtifact(ctx, job, secrets, r.store.Pinner(job.ID), key, local); err != nil {
			return "", 0, manifest, err
		}
		if pinnedBefore == "" && job.Target.HostKey != "" {
			// The archive remembers the key it was written under, so its
			// restore insists on the same server.
			manifest.Target.HostKey, manifest.Target.HostKeyFingerprint = job.Target.HostKey, job.Target.HostKeyFingerprint
			fmt.Fprintf(logBuf, "pinned the server's host key %s\n", job.Target.HostKeyFingerprint)
		}
		fmt.Fprintf(logBuf, "upload complete\n")
		r.writeSidecar(manifest, name, seal, logBuf, func(sidecar []byte) error {
			staged := local + sidecarSuffix
			if err := writeNewFile(staged, sidecar); err != nil {
				return err
			}
			defer os.Remove(staged)
			return uploadArtifact(ctx, job, secrets, r.store.Pinner(job.ID), key+sidecarSuffix, staged)
		})
		return key, size, manifest, nil

	default:
		return "", 0, manifest, fmt.Errorf("unknown target kind %q", job.TargetKind)
	}
}

func (r *Runner) archive(ctx context.Context, job *Job, dest string, logBuf *bytes.Buffer) (int64, int, error) {
	return r.archiveWithSnapshots(ctx, job, dest, logBuf, nil, nil)
}

// archiveWithSnapshots writes the archive to dest: through seal's encryption
// when it has one, so no plaintext copy of an encrypted archive is ever on disk.
func (r *Runner) archiveWithSnapshots(ctx context.Context, job *Job, dest string, logBuf *bytes.Buffer, seal *archiveSeal, snapshots map[string]string, extras ...archiveExtra) (int64, int, error) {
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()

	var out io.Writer = f
	var sealed io.WriteCloser
	if seal != nil {
		if sealed, err = age.Encrypt(f, seal.recipient); err != nil {
			return 0, 0, err
		}
		out = io.MultiWriter(sealed, seal)
	}
	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)

	count := 0
	archivedSnapshots := map[string]bool{}
	for index, source := range job.Sources {
		if ctx.Err() != nil {
			return 0, count, ctx.Err()
		}
		root := filepath.Clean(source)
		walkErr := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil {
				return fmt.Errorf("cannot archive %s: %w", path, err)
			}
			if sqliteSidecar(path, snapshots) {
				return nil
			}
			if excluded(path, job.Excludes) {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			link := ""
			if info.Mode()&os.ModeSymlink != 0 {
				link, err = os.Readlink(path)
				if err != nil {
					return err
				}
			}
			hdr, err := tar.FileInfoHeader(info, link)
			if err != nil {
				return err
			}
			hdr.Name = filepath.ToSlash(filepath.Join(fmt.Sprintf("source-%04d", index+1), rel))
			readPath := path
			if snapshot, ok := snapshots[path]; ok {
				if !info.Mode().IsRegular() {
					return errors.New("SQLite source changed type during backup")
				}
				readPath = snapshot
				info, err = os.Stat(snapshot)
				if err != nil {
					return err
				}
				hdr.Size = info.Size()
				archivedSnapshots[path] = true
			}
			if err := tw.WriteHeader(hdr); err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return nil
			}
			src, err := os.Open(readPath)
			if err != nil {
				return err
			}
			defer src.Close()
			if _, err := io.Copy(tw, src); err != nil {
				return err
			}
			after, err := src.Stat()
			if err != nil {
				return err
			}
			if after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
				return fmt.Errorf("source changed while archiving %s; use an application-consistent snapshot", path)
			}
			count++
			return nil
		})
		if walkErr != nil {
			tw.Close()
			gz.Close()
			return 0, count, walkErr
		}
	}
	if len(archivedSnapshots) != len(snapshots) {
		tw.Close()
		gz.Close()
		return 0, count, errors.New("a configured SQLite snapshot was excluded from the archive")
	}
	for _, extra := range extras {
		if err := archiveStagedFile(tw, extra); err != nil {
			tw.Close()
			gz.Close()
			return 0, count, err
		}
		count++
	}
	if err := tw.Close(); err != nil {
		gz.Close()
		return 0, count, err
	}
	if err := gz.Close(); err != nil {
		return 0, count, err
	}
	// The last chunk is written only here, and without it the archive is
	// one age refuses as truncated.
	if sealed != nil {
		if err := sealed.Close(); err != nil {
			return 0, count, err
		}
	}
	st, err := f.Stat()
	if err != nil {
		return 0, count, err
	}
	return st.Size(), count, f.Close()
}

// archiveStagedFile adds one staged file (a database dump) under its own
// archive root. The header is built from the file itself, so a dump that
// changed after it was digested fails the run rather than being misrecorded.
func archiveStagedFile(tw *tar.Writer, extra archiveExtra) error {
	info, err := os.Lstat(extra.path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("staged file %s is not a regular file", extra.archivePath)
	}
	header, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return err
	}
	header.Name = extra.archivePath
	header.Mode = 0o600
	if err := tw.WriteHeader(header); err != nil {
		return err
	}
	src, err := os.Open(extra.path)
	if err != nil {
		return err
	}
	defer src.Close()
	written, err := io.Copy(tw, src)
	if err != nil {
		return err
	}
	if written != info.Size() {
		return fmt.Errorf("staged file %s changed while archiving", extra.archivePath)
	}
	return nil
}

// excluded matches a path against glob patterns, testing both the full path
// and the base name so "*.log" and "/var/cache/*" both behave as expected.
func excluded(path string, patterns []string) bool {
	base := filepath.Base(path)
	for _, p := range patterns {
		if p == "" {
			continue
		}
		if ok, _ := filepath.Match(p, base); ok {
			return true
		}
		if ok, _ := filepath.Match(p, path); ok {
			return true
		}
		if strings.HasPrefix(path, strings.TrimSuffix(p, "/")+"/") {
			return true
		}
	}
	return false
}

// writeSidecar puts the finished manifest beside the archive. A failure is
// the log's to say and not the run's: the archive is whole and restores here,
// and only an import on another server needs what is beside it.
func (r *Runner) writeSidecar(manifest *Manifest, name string, seal *archiveSeal, logBuf *bytes.Buffer, put func([]byte) error) {
	if manifest.Origin == nil {
		return
	}
	sidecar, err := newSidecar(name, manifest, seal)
	if err == nil {
		err = put(sidecar)
	}
	if err != nil {
		fmt.Fprintf(logBuf, "could not write the manifest beside the archive (%v); another server cannot import this archive\n", err)
		r.log.Warn("backup manifest was not written beside its archive", "archive", name, "err", err)
		return
	}
	fmt.Fprintf(logBuf, "wrote the manifest beside the archive as %s%s\n", name, sidecarSuffix)
}

// writeNewFile creates a file that must not exist yet, private to the owner.
func writeNewFile(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(path)
		return err
	}
	return f.Close()
}

// prune enforces retention, deleting the artifacts beyond the keep count or
// older than the keep age from both the destination and the run history.
//
// An imported job is never pruned, and neither is an imported run: those
// archives were written by another server, which may still be keeping them
// by its own rules, and this one has no business deleting them.
func (r *Runner) prune(ctx context.Context, job *Job) error {
	if job.Imported {
		return nil
	}
	if job.Retention <= 0 && job.RetentionDays <= 0 {
		return nil
	}
	runs, err := r.store.SuccessfulRuns(ctx, job.ID)
	if err != nil {
		return err
	}
	expired := pruneCandidates(runs, job.Retention, job.RetentionDays, time.Now())
	if len(expired) == 0 {
		return nil
	}
	var secrets *TargetSecrets
	if job.TargetKind != TargetLocal {
		if secrets, err = r.store.Secrets(ctx, job.ID); err != nil {
			return err
		}
	}
	for _, old := range expired {
		if old.Imported {
			continue
		}
		if !r.artifactMu.TryLock() {
			// It will be pruned by the next run. Deleting an artifact out
			// from under a restore in progress buys nothing and costs the
			// operator the restore.
			r.log.Info("skipping retention prune of a run being read", "run", old.ID)
			continue
		}
		pruneErr := func() error {
			defer r.artifactMu.Unlock()
			var active bool
			if err := r.store.st.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM backup_restore_tests WHERE run_id=? AND state IN ('running','cleanup_failed'))`, old.ID).Scan(&active); err != nil {
				return err
			}
			if active {
				return nil
			}
			archivedJob := archiveJob(job, old)
			switch archivedJob.TargetKind {
			case TargetLocal:
				path, err := r.store.paths.ResolveEntry(old.Artifact)
				if err != nil {
					return err
				}
				if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
					return err
				}
				if hasSidecar(old) {
					_ = os.Remove(path + sidecarSuffix)
				}
			default:
				if secrets == nil {
					if secrets, err = r.store.Secrets(ctx, job.ID); err != nil {
						return err
					}
				}
				target, pin := r.store.transferJob(job, old)
				if err := deleteArtifact(ctx, target, secrets, pin, old.Artifact); err != nil {
					r.log.Warn("could not delete remote artifact", "key", old.Artifact, "err", err)
					return nil
				}
				if hasSidecar(old) {
					if err := deleteArtifact(ctx, target, secrets, pin, old.Artifact+sidecarSuffix); err != nil {
						r.log.Warn("could not delete the manifest beside a pruned archive", "key", old.Artifact, "err", err)
					}
				}
			}
			return r.store.DeleteRun(ctx, old.ID)
		}()
		if pruneErr != nil {
			return pruneErr
		}
	}
	return nil
}

// hasSidecar says whether a run's archive was written with its manifest beside
// it, which arrived with the manifest's origin.
func hasSidecar(run *Run) bool {
	return run.Manifest != nil && run.Manifest.Origin != nil
}

// pruneCandidates picks, from runs sorted newest first, the ones retention
// no longer keeps: everything past the count, and everything older than the
// age. The newest successful artifact survives both rules — a job whose
// schedule quietly stopped firing must not age its last good backup away.
func pruneCandidates(runs []*Run, keep, keepDays int, now time.Time) []*Run {
	var out []*Run
	cutoff := time.Time{}
	if keepDays > 0 {
		cutoff = now.Add(-time.Duration(keepDays) * 24 * time.Hour)
	}
	for i, run := range runs {
		if i == 0 {
			continue
		}
		if (keep > 0 && i >= keep) || (keepDays > 0 && run.StartedAt.Before(cutoff)) {
			out = append(out, run)
		}
	}
	return out
}

// Publishing must never replace an existing artifact. A hard link is atomic
// on one filesystem; exclusive creation preserves that guarantee across devices.
func moveFile(src, dst string) error {
	if err := os.Link(src, dst); err == nil {
		return os.Remove(src)
	} else if errors.Is(err, os.ErrExist) {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(dst)
		return err
	}
	return os.Remove(src)
}

func artifactName(job *Job, runID int64, at time.Time) string {
	name := sanitise(job.Name)
	if len(name) > 80 {
		name = name[:80]
	}
	// IDs distinguish jobs with the same sanitized name; entropy also separates
	// restored database copies writing to a shared object-store prefix.
	out := fmt.Sprintf("%s-job%d-run%d-%s-%s.tar.gz", name, job.ID, runID,
		at.UTC().Format("20060102-150405"), rand.Text())
	if job.Encrypt {
		out += encryptedSuffix
	}
	return out
}

func sanitise(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := b.String()
	if out == "" {
		return "backup"
	}
	return out
}
