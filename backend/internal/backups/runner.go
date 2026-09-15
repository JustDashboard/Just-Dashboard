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
	// reading counts the restores and archive listings currently holding a
	// run's artifact open. Retention pruning deletes artifacts the moment a
	// run succeeds, with no regard for whoever is halfway through reading one;
	// the result was a restore failing on an I/O error nothing explained.
	reading map[int64]int
}

func NewRunner(store *Store, stageDir string, log *slog.Logger) *Runner {
	return &Runner{
		store: store, stage: stageDir, log: log,
		running: map[int64]bool{}, reading: map[int64]int{},
	}
}

// beginRead marks a run's artifact as in use and returns the release.
func (r *Runner) beginRead(runID int64) func() {
	r.mu.Lock()
	r.reading[runID]++
	r.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			r.mu.Lock()
			if r.reading[runID] <= 1 {
				delete(r.reading, runID)
			} else {
				r.reading[runID]--
			}
			r.mu.Unlock()
		})
	}
}

func (r *Runner) beingRead(runID int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reading[runID] > 0
}

func (r *Runner) IsRunning(jobID int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running[jobID]
}

var ErrAlreadyRunning = fmt.Errorf("a run for this job is already in progress")

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
	artifact, size, err := r.perform(ctx, job, runID, &logBuf)
	status := StatusSuccess
	if err != nil {
		status = StatusFailed
		fmt.Fprintf(&logBuf, "\nFAILED: %v\n", err)
		r.log.Error("backup failed", "job", job.Name, "err", err)
	}
	// The run record is written even on failure — a backup that silently did
	// not happen is worse than one that visibly broke.
	if finErr := r.store.FinishRun(ctx, runID, status, artifact, size, logBuf.String()); finErr != nil {
		return nil, finErr
	}
	if status == StatusSuccess {
		if pruneErr := r.prune(ctx, job); pruneErr != nil {
			r.log.Warn("retention prune failed", "job", job.Name, "err", pruneErr)
		}
	}
	return r.store.Run(ctx, runID)
}

func (r *Runner) perform(ctx context.Context, job *Job, runID int64, logBuf *bytes.Buffer) (string, int64, error) {
	if err := r.store.ValidatePaths(job); err != nil {
		return "", 0, err
	}
	if err := os.MkdirAll(r.stage, 0o700); err != nil {
		return "", 0, err
	}
	stage, err := os.MkdirTemp(r.stage, fmt.Sprintf("run-%d-", runID))
	if err != nil {
		return "", 0, err
	}
	defer os.RemoveAll(stage)
	name := artifactName(job, runID, time.Now())
	local := filepath.Join(stage, name)

	fmt.Fprintf(logBuf, "archiving %d source(s) into %s\n", len(job.Sources), name)
	size, count, err := r.archive(ctx, job, local, logBuf)
	if err != nil {
		return "", 0, err
	}
	fmt.Fprintf(logBuf, "archived %d file(s), %d bytes\n", count, size)

	switch job.TargetKind {
	case TargetLocal:
		dest := filepath.Join(job.Target.Path, name)
		if err := os.MkdirAll(job.Target.Path, 0o700); err != nil {
			return "", 0, err
		}
		if err := moveFile(local, dest); err != nil {
			return "", 0, err
		}
		fmt.Fprintf(logBuf, "stored at %s\n", dest)
		return dest, size, nil

	case TargetS3, TargetB2:
		// The staging copy is removed once uploaded; keeping it would double
		// the disk cost of every backup.
		defer os.Remove(local)
		secrets, err := r.store.Secrets(ctx, job.ID)
		if err != nil {
			return "", 0, err
		}
		key := strings.TrimPrefix(filepath.Join(job.Target.Prefix, name), "/")
		fmt.Fprintf(logBuf, "uploading to %s/%s\n", job.Target.Bucket, key)
		if err := uploadObject(ctx, job, secrets, key, local); err != nil {
			return "", 0, err
		}
		fmt.Fprintf(logBuf, "upload complete\n")
		return key, size, nil

	default:
		return "", 0, fmt.Errorf("unknown target kind %q", job.TargetKind)
	}
}

func (r *Runner) archive(ctx context.Context, job *Job, dest string, logBuf *bytes.Buffer) (int64, int, error) {
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()

	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)

	count := 0
	for _, source := range job.Sources {
		if ctx.Err() != nil {
			return 0, count, ctx.Err()
		}
		root := filepath.Clean(source)
		base := filepath.Dir(root)
		walkErr := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil {
				// An unreadable file is noted and skipped: one permission
				// error should not abandon the whole backup.
				fmt.Fprintf(logBuf, "skip %s: %v\n", path, err)
				return nil
			}
			if excluded(path, job.Excludes) {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			rel, err := filepath.Rel(base, path)
			if err != nil {
				return nil
			}
			link := ""
			if info.Mode()&os.ModeSymlink != 0 {
				link, _ = os.Readlink(path)
			}
			hdr, err := tar.FileInfoHeader(info, link)
			if err != nil {
				return nil
			}
			hdr.Name = filepath.ToSlash(rel)
			if err := tw.WriteHeader(hdr); err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return nil
			}
			src, err := os.Open(path)
			if err != nil {
				fmt.Fprintf(logBuf, "skip %s: %v\n", path, err)
				return nil
			}
			defer src.Close()
			if _, err := io.Copy(tw, src); err != nil {
				return err
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
		if r.beingRead(old.ID) {
			// It will be pruned by the next run. Deleting an artifact out
			// from under a restore in progress buys nothing and costs the
			// operator the restore.
			r.log.Info("skipping retention prune of a run being read", "run", old.ID)
			continue
		}
		switch job.TargetKind {
		case TargetLocal:
			path, err := r.store.paths.ResolveEntry(old.Artifact)
			if err != nil {
				return err
			}
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return err
			}
		default:
			if err := deleteObject(ctx, job, secrets, old.Artifact); err != nil {
				r.log.Warn("could not delete remote artifact", "key", old.Artifact, "err", err)
				continue
			}
		}
		if err := r.store.DeleteRun(ctx, old.ID); err != nil {
			return err
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
