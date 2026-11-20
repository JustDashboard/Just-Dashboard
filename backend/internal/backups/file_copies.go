package backups

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// FileCopy is a backup run that holds a copy of one file, as the Files page
// lists it among that file's versions.
type FileCopy struct {
	RunID       int64      `json:"runId"`
	JobID       int64      `json:"jobId"`
	Job         string     `json:"job"`
	At          time.Time  `json:"at"`
	TargetKind  TargetKind `json:"targetKind"`
	Destination string     `json:"destination"`
	Encrypted   bool       `json:"encrypted,omitempty"`
	Imported    bool       `json:"imported,omitempty"`
	// Local is whether reading the copy stays on this server: a local
	// destination, or an imported run's copy kept here.
	Local bool `json:"local"`
}

// ErrNotInArchive is a run whose manifest covers a path but whose archive
// holds no regular file there: the file did not exist when it ran.
var ErrNotInArchive = errors.New("this backup does not hold the file: it did not exist, or was not a regular file, when the backup ran")

// ErrFileCopyTooLarge is a held copy larger than the caller will read.
var ErrFileCopyTooLarge = errors.New("the backed-up copy is too large to compare or restore here")

// FileCopies lists the completed runs whose manifest covers path, newest
// first, from the store alone. It never reaches a destination: whether the
// archive really holds the file is learnt when the copy is read, on the
// person's click.
func (s *Store) FileCopies(ctx context.Context, path string, limit int) ([]FileCopy, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("a file's backups are looked up by its absolute path")
	}
	path = filepath.Clean(path)
	jobs, err := s.st.DB.QueryContext(ctx, `SELECT `+jobCols+` FROM backup_jobs ORDER BY id`)
	if err != nil {
		return nil, err
	}
	var list []*Job
	for jobs.Next() {
		job, err := s.scanJob(jobs)
		if err != nil {
			jobs.Close()
			return nil, err
		}
		list = append(list, job)
	}
	jobs.Close()
	if err := jobs.Err(); err != nil {
		return nil, err
	}
	out := []FileCopy{}
	for _, job := range list {
		runs, err := s.SuccessfulRuns(ctx, job.ID)
		if err != nil {
			return nil, err
		}
		for _, run := range runs {
			if _, ok := run.Manifest.fileEntry(path); !ok {
				continue
			}
			at := run.StartedAt
			if run.EndedAt != nil {
				at = *run.EndedAt
			}
			kind, label := job.TargetKind, destinationLabel(job)
			if run.Manifest.TargetKind.Valid() && !job.Imported {
				// Where this run went, which a job edited since may no
				// longer say.
				kind, label = run.Manifest.TargetKind, destinationLabel(archiveJob(job, run))
			}
			out = append(out, FileCopy{
				RunID: run.ID, JobID: job.ID, Job: job.Name, At: at,
				TargetKind: kind, Destination: label,
				Encrypted: run.Manifest.Encryption != nil, Imported: run.Imported,
				Local: kind == TargetLocal || run.LocalCopy != "",
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// fileEntry is where path lies in this manifest's archive, if the run took
// it: under a recorded source, and not under anything the job excluded.
func (m *Manifest) fileEntry(path string) (string, bool) {
	if m == nil || m.Version != 1 || !m.Complete {
		return "", false
	}
	for _, source := range m.Sources {
		root := filepath.Clean(source.Path)
		if !filepath.IsAbs(root) {
			continue
		}
		relative, err := filepath.Rel(root, path)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			continue
		}
		// The walk skips an excluded directory whole, so the file and each
		// folder between it and the source are asked.
		for at := path; ; at = filepath.Dir(at) {
			if excluded(at, m.Excludes) {
				return "", false
			}
			if at == root || at == filepath.Dir(at) {
				break
			}
		}
		if relative == "." {
			return source.ArchivePath, true
		}
		return filepath.ToSlash(filepath.Join(source.ArchivePath, relative)), true
	}
	return "", false
}

// ReadFileCopy reads path's copy out of a run, through the same verified
// artifact every restore reads (localArtifact: pinned against retention,
// fetched from the destination when it is remote, proved against the
// manifest's digest and decrypted). The archive path comes from the run's own
// manifest, never from the caller, so a run only ever yields the file asked
// for.
func (r *Runner) ReadFileCopy(ctx context.Context, runID int64, path string, limit int64) ([]byte, error) {
	run, err := r.store.Run(ctx, runID)
	if err != nil {
		return nil, err
	}
	name, ok := run.Manifest.fileEntry(filepath.Clean(path))
	if !ok {
		return nil, ErrNotInArchive
	}
	_, archive, cleanup, err := r.localArtifact(ctx, runID)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	reader, release, err := archive.entries()
	if err != nil {
		return nil, err
	}
	defer release()
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		header, err := reader.Next()
		if err == io.EOF {
			return nil, ErrNotInArchive
		}
		if err != nil {
			return nil, err
		}
		if strings.TrimSuffix(header.Name, "/") != name {
			continue
		}
		if header.Typeflag != tar.TypeReg {
			return nil, ErrNotInArchive
		}
		if header.Size > limit {
			return nil, ErrFileCopyTooLarge
		}
		content, err := io.ReadAll(io.LimitReader(reader, limit+1))
		if err != nil {
			return nil, err
		}
		if int64(len(content)) > limit {
			return nil, ErrFileCopyTooLarge
		}
		return content, nil
	}
}
