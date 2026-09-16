package backups

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Manifest belongs to an archive, not to the editable job that produced it.
// Old runs have no manifest and cannot prove a deployment's coverage.
type Manifest struct {
	Version         int              `json:"version"`
	Sources         []ManifestSource `json:"sources"`
	Excludes        []string         `json:"excludes"`
	TargetKind      TargetKind       `json:"targetKind"`
	Target          TargetConfig     `json:"target"`
	ArtifactDigest  string           `json:"artifactDigest"`
	Complete        bool             `json:"complete"`
	SQLiteSnapshots []SQLiteSnapshot `json:"sqliteSnapshots,omitempty"`
	DatabaseDumps   []DatabaseDump   `json:"databaseDumps,omitempty"`
}

type ManifestSource struct {
	Path        string `json:"path"`
	ArchivePath string `json:"archivePath"`
}

func newManifest(job *Job) *Manifest {
	manifest := &Manifest{Version: 1, Sources: []ManifestSource{},
		Excludes: append([]string{}, job.Excludes...), TargetKind: job.TargetKind, Target: job.Target}
	for i, source := range job.Sources {
		// Named volumes often all end in "_data". A source namespace keeps
		// those independent trees from overwriting each other during restore.
		manifest.Sources = append(manifest.Sources, ManifestSource{
			Path: source, ArchivePath: fmt.Sprintf("source-%04d", i+1),
		})
	}
	return manifest
}

func (s *Store) ResolveSource(source string) (string, error) {
	if !filepath.IsAbs(source) {
		return "", fmt.Errorf("backup coverage needs an absolute, resolved source path")
	}
	return s.paths.Resolve(source)
}

// Covers refuses incomplete and filtered archives. An exclusion can omit a
// database journal or an application file, even when its parent was archived.
func (m *Manifest) Covers(wanted string) bool {
	if m == nil || m.Version != 1 || !m.Complete || len(m.Excludes) != 0 ||
		!strings.HasPrefix(m.ArtifactDigest, "sha256:") || len(m.ArtifactDigest) != 71 || !filepath.IsAbs(wanted) {
		return false
	}
	if _, err := hex.DecodeString(strings.TrimPrefix(m.ArtifactDigest, "sha256:")); err != nil {
		return false
	}
	for _, source := range m.Sources {
		if !filepath.IsAbs(source.Path) {
			continue
		}
		relative, err := filepath.Rel(source.Path, wanted)
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// VerifyCoverage checks the actual archived entries, so a path created after
// an older backup cannot borrow coverage from a recorded ancestor directory.
func (r *Runner) VerifyCoverage(ctx context.Context, runID int64, sources []string) error {
	run, path, cleanup, err := r.localArtifact(ctx, runID)
	if err != nil {
		return err
	}
	defer cleanup()
	if run.Manifest == nil || !run.Manifest.Complete || len(run.Manifest.Excludes) > 0 {
		return fmt.Errorf("backup has no complete, unfiltered coverage manifest")
	}
	wanted := map[string]bool{}
	for _, source := range sources {
		if !run.Manifest.Covers(source) {
			return fmt.Errorf("backup manifest does not cover a persistent source")
		}
		for _, entry := range run.Manifest.Sources {
			relative, err := filepath.Rel(entry.Path, source)
			if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				wanted[filepath.ToSlash(filepath.Join(entry.ArchivePath, relative))] = false
				break
			}
		}
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if _, ok := wanted[header.Name]; ok && (header.Typeflag == tar.TypeDir || header.Typeflag == tar.TypeReg) {
			wanted[header.Name] = true
		}
	}
	for _, found := range wanted {
		if !found {
			return fmt.Errorf("a persistent source is missing from the archived entries")
		}
	}
	return nil
}

func fileDigest(ctx context.Context, path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	hash := sha256.New()
	buffer := make([]byte, 128<<10)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, err := f.Read(buffer)
		_, _ = hash.Write(buffer[:n])
		if err == io.EOF {
			return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
		}
		if err != nil {
			return "", err
		}
	}
}

func archiveJob(job *Job, run *Run) *Job {
	if run.Manifest == nil || run.Manifest.Version != 1 {
		return job
	}
	copy := *job
	copy.TargetKind, copy.Target = run.Manifest.TargetKind, run.Manifest.Target
	return &copy
}
