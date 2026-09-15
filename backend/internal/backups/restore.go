package backups

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

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
	defer r.beginRead(runID)()
	run, err := r.store.Run(ctx, runID)
	if err != nil {
		return nil, err
	}
	if run.Status != StatusSuccess || run.Artifact == "" {
		return nil, fmt.Errorf("run %d has no artifact to restore", runID)
	}
	job, err := r.store.Get(ctx, run.JobID)
	if err != nil {
		return nil, err
	}
	dest, err := r.store.paths.Resolve(destination)
	if err != nil {
		return nil, err
	}
	if dest == "/" {
		return nil, fmt.Errorf("refusing to restore directly over /")
	}
	archivePath := run.Artifact
	if job.TargetKind == TargetLocal {
		archivePath, err = r.store.paths.Resolve(archivePath)
		if err != nil {
			return nil, err
		}
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return nil, err
	}
	if job.TargetKind != TargetLocal {
		secrets, err := r.store.Secrets(ctx, job.ID)
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(r.stage, 0o700); err != nil {
			return nil, err
		}
		stage, err := os.MkdirTemp(r.stage, "restore-*")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(stage)
		archivePath = filepath.Join(stage, "archive.tar.gz")
		if err := downloadObject(ctx, job, secrets, run.Artifact, archivePath); err != nil {
			return nil, err
		}
	}
	return extractArchive(ctx, archivePath, dest, runID)
}

func extractArchive(ctx context.Context, archivePath, dest string, runID int64) (*RestoreResult, error) {
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
		case tar.TypeSymlink:
			if err := safepath.CheckLinkTarget(dest, hdr.Name, hdr.Linkname); err != nil {
				res.Skipped = append(res.Skipped, hdr.Name)
				continue
			}
			if err := safepath.Symlink(hdr.Linkname, target); err != nil {
				return res, err
			}
		case tar.TypeReg:
			if err := safepath.MkdirParents(target); err != nil {
				return res, err
			}
			out, err := safepath.Create(target, os.FileMode(hdr.Mode).Perm())
			if err != nil {
				return res, err
			}
			n, err := io.Copy(out, tr)
			out.Close()
			if err != nil {
				return res, err
			}
			res.Bytes += n
		default:
			continue
		}
		res.Entries++
	}
}

// ListArchive shows what a run contains without extracting it, so an operator
// can confirm a backup holds what they expect before restoring anything.
func (r *Runner) ListArchive(ctx context.Context, runID int64, limit int) ([]ArchiveEntry, error) {
	defer r.beginRead(runID)()
	run, err := r.store.Run(ctx, runID)
	if err != nil {
		return nil, err
	}
	if run.Status != StatusSuccess || run.Artifact == "" {
		return nil, fmt.Errorf("run %d has no artifact", runID)
	}
	job, err := r.store.Get(ctx, run.JobID)
	if err != nil {
		return nil, err
	}
	path := run.Artifact
	if job.TargetKind == TargetLocal {
		path, err = r.store.paths.Resolve(path)
		if err != nil {
			return nil, err
		}
	}
	if job.TargetKind != TargetLocal {
		secrets, err := r.store.Secrets(ctx, job.ID)
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(r.stage, 0o700); err != nil {
			return nil, err
		}
		stage, err := os.MkdirTemp(r.stage, "list-*")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(stage)
		path = filepath.Join(stage, "archive.tar.gz")
		if err := downloadObject(ctx, job, secrets, run.Artifact, path); err != nil {
			return nil, err
		}
	}
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
