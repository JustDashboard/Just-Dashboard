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
	for _, source := range job.Sources {
		if relative, err := filepath.Rel(source, r.stage); err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return "", 0, manifest, errors.New("backup staging must be outside every source directory")
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
	name := artifactName(job, runID, time.Now())
	local := filepath.Join(stage, name)

	fmt.Fprintf(logBuf, "archiving %d source(s) and %d database dump(s) into %s\n", len(job.Sources), len(dumps), name)
	size, count, err := r.archiveWithSnapshots(ctx, job, local, logBuf, snapshots, dumps...)
	if err != nil {
		return "", 0, manifest, err
	}
	manifest.ArtifactDigest, err = fileDigest(ctx, local)
	if err != nil {
		return "", 0, manifest, err
	}
	manifest.Complete = true
	fmt.Fprintf(logBuf, "archived %d file(s), %d bytes\n", count, size)

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
		return dest, size, manifest, nil

	case TargetS3, TargetB2:
		// The staging copy is removed once uploaded; keeping it would double
		// the disk cost of every backup.
		defer os.Remove(local)
		secrets, err := r.store.Secrets(ctx, job.ID)
		if err != nil {
			return "", 0, manifest, err
		}
		key := strings.TrimPrefix(filepath.Join(job.Target.Prefix, name), "/")
		fmt.Fprintf(logBuf, "uploading to %s/%s\n", job.Target.Bucket, key)
		if err := uploadObject(ctx, job, secrets, key, local); err != nil {
			return "", 0, manifest, err
		}
		fmt.Fprintf(logBuf, "upload complete\n")
		return key, size, manifest, nil

	default:
		return "", 0, manifest, fmt.Errorf("unknown target kind %q", job.TargetKind)
	}
}

func (r *Runner) archive(ctx context.Context, job *Job, dest string, logBuf *bytes.Buffer) (int64, int, error) {
	return r.archiveWithSnapshots(ctx, job, dest, logBuf, nil)
}

func (r *Runner) archiveWithSnapshots(ctx context.Context, job *Job, dest string, logBuf *bytes.Buffer, snapshots map[string]string, extras ...archiveExtra) (int64, int, error) {
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()

	gz := gzip.NewWriter(f)
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

// prune enforces retention, deleting the oldest artifacts beyond the keep
// count from both the destination and the run history.
func (r *Runner) prune(ctx context.Context, job *Job) error {
	if job.Retention <= 0 {
		return nil
	}
	runs, err := r.store.SuccessfulRuns(ctx, job.ID)
	if err != nil {
		return err
	}
	if len(runs) <= job.Retention {
		return nil
	}
	var secrets *TargetSecrets
	if job.TargetKind != TargetLocal {
		if secrets, err = r.store.Secrets(ctx, job.ID); err != nil {
			return err
		}
	}
	for _, old := range runs[job.Retention:] {
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
			default:
				if secrets == nil {
					if secrets, err = r.store.Secrets(ctx, job.ID); err != nil {
						return err
					}
				}
				if err := deleteObject(ctx, archivedJob, secrets, old.Artifact); err != nil {
					r.log.Warn("could not delete remote artifact", "key", old.Artifact, "err", err)
					return nil
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
	return fmt.Sprintf("%s-job%d-run%d-%s-%s.tar.gz", name, job.ID, runID,
		at.UTC().Format("20060102-150405"), rand.Text())
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
