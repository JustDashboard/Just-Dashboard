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
	"strings"
	"syscall"

	"github.com/Wayy01/Just-Dashboard/backend/internal/safepath"
)

type RestoreResult struct {
	RunID       int64    `json:"runId"`
	Destination string   `json:"destination"`
	Entries     int      `json:"entries"`
	Bytes       int64    `json:"bytes"`
	Skipped     []string `json:"skipped,omitempty"`
	// Targets are the directories written to. One for a restore into a
	// destination; one per recorded source for a restore in place.
	Targets []string `json:"targets,omitempty"`
}

// RestoreOptions narrows a restore. Paths are archive paths ("source-0001/etc"
// or a single file); an entry restores when it is one of them or lies under
// one. Empty means everything.
type RestoreOptions struct {
	Paths []string
}

// Restore unpacks a completed run's artifact into a destination directory.
// It never restores in place over the original paths: the caller names an
// explicit destination, so recovering a single file does not require
// overwriting a live tree. RestoreInPlace is the one that does.
func (r *Runner) Restore(ctx context.Context, runID int64, destination string, opts ...RestoreOptions) (*RestoreResult, error) {
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
	res, err := extractArchiveBounded(ctx, archivePath, dest, runID, 0, restorePlan{filter: newPathFilter(opts)})
	if res != nil {
		res.Targets = []string{dest}
	}
	return res, err
}

// RestoreInPlace writes each recorded source back over the path it was taken
// from, dropping the source-NNNN prefix. It needs the run's manifest — a
// legacy archive cannot say where its trees came from — and every original
// path is resolved through the file service again, so a root restriction
// added since the backup still holds. Database dumps are never written to
// disk by this; they go back through the Databases owner.
func (r *Runner) RestoreInPlace(ctx context.Context, runID int64, opts ...RestoreOptions) (*RestoreResult, error) {
	run, archivePath, cleanup, err := r.localArtifact(ctx, runID)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	if run.Manifest == nil || !run.Manifest.Complete {
		return nil, errors.New("this archive has no manifest, so its files cannot be put back where they came from; restore it into a directory instead")
	}
	plan := restorePlan{filter: newPathFilter(opts), roots: map[string]string{}}
	var targets []string
	for _, source := range run.Manifest.Sources {
		resolved, err := r.store.paths.Resolve(source.Path)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", source.Path, err)
		}
		if resolved == "/" {
			return nil, fmt.Errorf("refusing to restore directly over /")
		}
		if err := os.MkdirAll(resolved, 0o755); err != nil {
			return nil, err
		}
		plan.roots[source.ArchivePath] = resolved
		targets = append(targets, resolved)
	}
	res, err := extractArchiveBounded(ctx, archivePath, "", runID, 0, plan)
	if res != nil {
		res.Destination = ""
		res.Targets = targets
	}
	return res, err
}

// restorePlan decides where an entry lands and whether it lands at all.
type restorePlan struct {
	filter *pathFilter
	// roots maps an archive root (source-0001) to the directory it restores
	// into. Nil means every entry restores under one destination.
	roots map[string]string
}

// place returns the directory and relative name an entry restores as, or
// false for an entry the plan leaves in the archive.
func (p restorePlan) place(dest, name string) (string, string, bool) {
	if p.filter != nil && !p.filter.wants(name) {
		return "", "", false
	}
	if p.roots == nil {
		return dest, name, true
	}
	root, rest, _ := strings.Cut(name, "/")
	target, ok := p.roots[root]
	if !ok {
		return "", "", false
	}
	if rest == "" {
		rest = "."
	}
	return target, rest, true
}

type pathFilter struct{ prefixes []string }

func newPathFilter(opts []RestoreOptions) *pathFilter {
	var out []string
	for _, o := range opts {
		for _, p := range o.Paths {
			p = strings.Trim(filepath.ToSlash(filepath.Clean("/"+p)), "/")
			if p != "" && p != "." {
				out = append(out, p)
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return &pathFilter{prefixes: out}
}

func (f *pathFilter) wants(name string) bool {
	name = strings.TrimSuffix(name, "/")
	for _, p := range f.prefixes {
		if name == p || strings.HasPrefix(name, p+"/") {
			return true
		}
	}
	return false
}

func extractArchive(ctx context.Context, archivePath, dest string, runID int64) (*RestoreResult, error) {
	return extractArchiveBounded(ctx, archivePath, dest, runID, 0, restorePlan{})
}

func extractArchiveBounded(ctx context.Context, archivePath, dest string, runID int64, maxBytes int64, plan restorePlan) (*RestoreResult, error) {
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
		base, name, wanted := plan.place(dest, hdr.Name)
		if !wanted {
			continue
		}
		if name == "." {
			// The source root itself: it exists already, and its recorded
			// mode belongs to the directory the operator chose.
			res.Entries++
			continue
		}
		target, err := safepath.Join(base, name)
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
			if err := safepath.CheckLinkTarget(base, name, hdr.Linkname); err != nil {
				res.Skipped = append(res.Skipped, hdr.Name)
				continue
			}
			if err := safepath.Symlink(hdr.Linkname, target); err != nil {
				return res, err
			}
		case tar.TypeReg:
			if maxBytes > 0 {
				var available syscall.Statfs_t
				if err := syscall.Statfs(base, &available); err != nil {
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

// OpenArtifact hands a caller the verified archive file for a run: the local
// artifact, or a private download of a remote one. The cleanup releases the
// retention pin and removes the download, and must be called.
func (r *Runner) OpenArtifact(ctx context.Context, runID int64) (*Run, string, func(), error) {
	return r.localArtifact(ctx, runID)
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
