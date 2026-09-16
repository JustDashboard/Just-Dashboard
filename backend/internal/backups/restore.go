package backups

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"github.com/Wayy01/Just-Dashboard/backend/internal/safepath"
)

type RestoreResult struct {
	RunID       int64    `json:"runId"`
	Destination string   `json:"destination"`
	Entries     int      `json:"entries"`
	Bytes       int64    `json:"bytes"`
	Skipped     []string `json:"skipped,omitempty"`
}

// Restore unpacks a completed run's artifact into a destination directory.
// It never restores in place over the original paths by default: the caller
// names an explicit destination, so recovering a single file does not require
// overwriting a live tree.
func (r *Runner) Restore(ctx context.Context, runID int64, destination string) (*RestoreResult, error) {
	_, archivePath, cleanup, err := r.localArtifact(ctx, runID)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	dest, err := r.store.paths.Resolve(destination)
	if err != nil {
		return nil, err
	}
	if dest == "/" {
		return nil, fmt.Errorf("refusing to restore directly over /")
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return nil, err
	}
	return extractArchive(ctx, archivePath, dest, runID)
}

func extractArchive(ctx context.Context, archivePath, dest string, runID int64) (*RestoreResult, error) {
	return extractArchiveBounded(ctx, archivePath, dest, runID, 0)
}

func extractArchiveBounded(ctx context.Context, archivePath, dest string, runID int64, maxBytes int64) (*RestoreResult, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer gz.Close()

	res := &RestoreResult{RunID: runID, Destination: dest, Skipped: []string{}}
	tr := tar.NewReader(gz)
	seen := 0
	for {
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		hdr, err := tr.Next()
		if err == io.EOF {
			return res, nil
		}
		if err != nil {
			return res, err
		}
		seen++
		if maxBytes > 0 && (seen > 200000 || hdr.Size > maxBytes-res.Bytes) {
			return res, errors.New("restore extraction exceeds its configured limit")
		}
		target, err := safepath.Join(dest, hdr.Name)
		if err != nil {
			// A tampered or hand-built archive could carry ../ entries; the
			// restore refuses them rather than writing outside the target.
			res.Skipped = append(res.Skipped, hdr.Name)
			continue
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := safepath.Mkdir(target, os.FileMode(hdr.Mode).Perm()); err != nil {
				return res, err
			}
			if maxBytes > 0 && os.Geteuid() == 0 {
				if err := os.Chown(target, hdr.Uid, hdr.Gid); err != nil {
					return res, err
				}
			}
		case tar.TypeSymlink:
			if err := safepath.CheckLinkTarget(dest, hdr.Name, hdr.Linkname); err != nil {
				res.Skipped = append(res.Skipped, hdr.Name)
				continue
			}
			if err := safepath.Symlink(hdr.Linkname, target); err != nil {
				return res, err
			}
		case tar.TypeReg:
			if maxBytes > 0 {
				var available syscall.Statfs_t
				if err := syscall.Statfs(dest, &available); err != nil {
					return res, err
				}
				if uint64(hdr.Size)+(256<<20) > available.Bavail*uint64(available.Bsize) {
					return res, errors.New("restore needs space for the file plus a 256 MiB reserve")
				}
			}
			if err := safepath.MkdirParents(target); err != nil {
				return res, err
			}
			out, err := safepath.Create(target, os.FileMode(hdr.Mode).Perm())
			if err != nil {
				return res, err
			}
			n, err := io.Copy(out, &recoveryContextReader{ctx: ctx, reader: tr})
			if err == nil && maxBytes > 0 && os.Geteuid() == 0 {
				err = out.Chown(hdr.Uid, hdr.Gid)
			}
			err = errors.Join(err, out.Close())
			if err != nil {
				return res, err
			}
			res.Bytes += n
		default:
			res.Skipped = append(res.Skipped, hdr.Name)
			continue
		}
		res.Entries++
	}
}

// ListArchive shows what a run contains without extracting it, so an operator
// can confirm a backup holds what they expect before restoring anything.
func (r *Runner) ListArchive(ctx context.Context, runID int64, limit int) ([]ArchiveEntry, error) {
	_, path, cleanup, err := r.localArtifact(ctx, runID)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer gz.Close()

	if limit <= 0 || limit > 20000 {
		limit = 2000
	}
	out := []ArchiveEntry{}
	tr := tar.NewReader(gz)
	for len(out) < limit {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return out, err
		}
		out = append(out, ArchiveEntry{
			Name:  hdr.Name,
			Size:  hdr.Size,
			Mode:  fmt.Sprintf("%04o", os.FileMode(hdr.Mode).Perm()),
			IsDir: hdr.Typeflag == tar.TypeDir,
		})
	}
	return out, nil
}

type ArchiveEntry struct {
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	Mode  string `json:"mode"`
	IsDir bool   `json:"isDir"`
}

func verifyArtifact(ctx context.Context, path string, run *Run) error {
	if run.Manifest == nil {
		return nil // Legacy archives remain recoverable without inventing evidence.
	}
	if run.Manifest.Version != 1 || !run.Manifest.Complete || run.Manifest.ArtifactDigest == "" {
		return fmt.Errorf("backup artifact has no complete supported manifest")
	}
	digest, err := fileDigest(ctx, path)
	if err != nil {
		return err
	}
	if digest != run.Manifest.ArtifactDigest {
		return fmt.Errorf("backup artifact checksum does not match its recorded manifest")
	}
	return nil
}

// localArtifact pins the run against retention and uses its original target
// configuration. Cleanup releases both the pin and a private download, if any.
func (r *Runner) localArtifact(ctx context.Context, runID int64) (*Run, string, func(), error) {
	release, err := r.beginRead()
	if err != nil {
		return nil, "", nil, err
	}
	stage := ""
	cleanup := func() {
		if stage != "" {
			_ = os.RemoveAll(stage)
		}
		release()
	}
	failed := func(err error) (*Run, string, func(), error) {
		cleanup()
		return nil, "", nil, err
	}
	run, err := r.store.Run(ctx, runID)
	if err != nil {
		return failed(err)
	}
	if run.Status != StatusSuccess || run.Artifact == "" {
		return failed(fmt.Errorf("run %d has no successful artifact", runID))
	}
	job, err := r.store.Get(ctx, run.JobID)
	if err != nil {
		return failed(err)
	}
	job = archiveJob(job, run)
	path := run.Artifact
	if job.TargetKind == TargetLocal {
		path, err = r.store.paths.Resolve(path)
		if err != nil {
			return failed(err)
		}
	} else {
		secrets, err := r.store.Secrets(ctx, job.ID)
		if err != nil {
			return failed(err)
		}
		if err := os.MkdirAll(r.stage, 0o700); err != nil {
			return failed(err)
		}
		stage, err = os.MkdirTemp(r.stage, "read-*")
		if err != nil {
			return failed(err)
		}
		path = filepath.Join(stage, "archive.tar.gz")
		if err := downloadObject(ctx, job, secrets, run.Artifact, path); err != nil {
			return failed(err)
		}
	}
	if err := verifyArtifact(ctx, path, run); err != nil {
		return failed(err)
	}
	return run, path, cleanup, nil
}
