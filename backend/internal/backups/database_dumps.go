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
	"slices"
	"strings"
)

// A backup job may name saved database connections. Each run asks the
// Databases owner for a native dump of every one of them (pg_dump, mysqldump,
// mongodump, a Redis RDB, or the built-in driver dump) and stores the files in
// the archive next to the filesystem sources. A dump is a transaction boundary
// the engine chose; a filesystem copy of a running database is not, which is
// why the deployment gate prefers this evidence for a linked database.

// DatabaseDescription is what the owner says about a connection when a job is
// saved: enough to validate and to label the dump, never a credential.
type DatabaseDescription struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Driver   string `json:"driver"`
	Database string `json:"database"`
}

// DatabaseDumpResult is one captured dump inside a run's staging directory.
type DatabaseDumpResult struct {
	Description DatabaseDescription
	Path        string
	// Method names how the file was produced ("pg_dump", "built-in dump").
	Method string
}

// DatabaseDumper belongs to the Databases owner. Backups owns staging,
// archiving, evidence, retention and the restore hand-off.
type DatabaseDumper interface {
	DescribeDatabase(context.Context, int64) (DatabaseDescription, error)
	DumpDatabase(ctx context.Context, connectionID int64, directory string) (DatabaseDumpResult, error)
	RestoreDatabase(ctx context.Context, connectionID int64, database, dumpPath string) (string, error)
}

func (r *Runner) WithDatabaseDumper(dumper DatabaseDumper) *Runner {
	r.databases = dumper
	return r
}

// WithDatabaseValidator lets the store refuse a job that names a connection
// the Databases owner does not have, at save time rather than at 3 a.m.
func (s *Store) WithDatabaseValidator(describe func(context.Context, int64) (DatabaseDescription, error)) *Store {
	s.describeDatabase = describe
	return s
}

// DatabaseDump is the manifest evidence for one captured dump.
type DatabaseDump struct {
	ConnectionID int64  `json:"connectionId"`
	Name         string `json:"name"`
	Driver       string `json:"driver"`
	Database     string `json:"database"`
	Method       string `json:"method"`
	File         string `json:"file"`
	ArchivePath  string `json:"archivePath"`
	Digest       string `json:"digest"`
	Bytes        int64  `json:"bytes"`
}

const maxDatabaseDumps = 32

func (s *Store) validateDatabaseDumps(ctx context.Context, job *Job) error {
	if len(job.DatabaseDumps) > maxDatabaseDumps {
		return fmt.Errorf("at most %d database dumps can be included in a backup", maxDatabaseDumps)
	}
	seen := map[int64]bool{}
	for _, id := range job.DatabaseDumps {
		if id <= 0 || seen[id] {
			return errors.New("database dumps must name distinct saved connections")
		}
		seen[id] = true
		if s.describeDatabase != nil {
			if _, err := s.describeDatabase(ctx, id); err != nil {
				return fmt.Errorf("database connection %d is not available for dumps: %w", id, err)
			}
		}
	}
	slices.Sort(job.DatabaseDumps)
	return nil
}

// archiveExtra is a staged file that enters the archive under its own root,
// beside the source-NNNN trees.
type archiveExtra struct {
	path        string
	archivePath string
}

func databaseArchiveRoot(index int) string {
	return fmt.Sprintf("database-%04d", index+1)
}

// prepareDatabaseDumps captures every configured dump into the run's staging
// directory. A failed dump fails the run: a backup that silently lacks the
// database it promised is the outcome this feature exists to prevent.
func (r *Runner) prepareDatabaseDumps(ctx context.Context, job *Job, stage string, logBuf io.Writer) ([]archiveExtra, []DatabaseDump, error) {
	if len(job.DatabaseDumps) == 0 {
		return nil, nil, nil
	}
	if r.databases == nil {
		return nil, nil, errors.New("the database dump owner is unavailable")
	}
	extras := make([]archiveExtra, 0, len(job.DatabaseDumps))
	evidence := make([]DatabaseDump, 0, len(job.DatabaseDumps))
	for index, id := range job.DatabaseDumps {
		directory := filepath.Join(stage, databaseArchiveRoot(index))
		if err := os.Mkdir(directory, 0o700); err != nil {
			return nil, nil, err
		}
		result, err := r.databases.DumpDatabase(ctx, id, directory)
		if err != nil {
			return nil, nil, fmt.Errorf("database dump for connection %d failed: %w", id, err)
		}
		if filepath.Dir(result.Path) != directory {
			return nil, nil, errors.New("database dump owner returned an uncontained file")
		}
		info, err := os.Lstat(result.Path)
		if err != nil || !info.Mode().IsRegular() {
			return nil, nil, errors.New("database dump is not a regular file")
		}
		digest, err := fileDigest(ctx, result.Path)
		if err != nil {
			return nil, nil, err
		}
		file := filepath.Base(result.Path)
		archivePath := filepath.ToSlash(filepath.Join(databaseArchiveRoot(index), file))
		extras = append(extras, archiveExtra{path: result.Path, archivePath: archivePath})
		evidence = append(evidence, DatabaseDump{
			ConnectionID: id, Name: result.Description.Name, Driver: result.Description.Driver,
			Database: result.Description.Database, Method: result.Method, File: file,
			ArchivePath: archivePath, Digest: digest, Bytes: info.Size(),
		})
		fmt.Fprintf(logBuf, "dumped database %s (%s, %s) with %s: %d bytes\n", result.Description.Name, result.Description.Driver, result.Description.Database, result.Method, info.Size())
	}
	return extras, evidence, nil
}

// CoversDatabase reports whether this archive carries a native dump of the
// connection. Like Covers, it refuses incomplete archives.
func (m *Manifest) CoversDatabase(connectionID int64) bool {
	if m == nil || m.Version != 1 || !m.Complete || !strings.HasPrefix(m.ArtifactDigest, "sha256:") {
		return false
	}
	for _, dump := range m.DatabaseDumps {
		if dump.ConnectionID == connectionID && strings.HasPrefix(dump.Digest, "sha256:") && dump.ArchivePath != "" {
			return true
		}
	}
	return false
}

// VerifyDatabaseCoverage confirms the archive really holds the dump entries
// the manifest names for these connections, with the recorded sizes.
func (r *Runner) VerifyDatabaseCoverage(ctx context.Context, runID int64, connectionIDs []int64) error {
	run, path, cleanup, err := r.localArtifact(ctx, runID)
	if err != nil {
		return err
	}
	defer cleanup()
	wanted := map[string]int64{}
	for _, id := range connectionIDs {
		if !run.Manifest.CoversDatabase(id) {
			return fmt.Errorf("backup manifest has no native dump for database connection %d", id)
		}
		for _, dump := range run.Manifest.DatabaseDumps {
			if dump.ConnectionID == id {
				wanted[dump.ArchivePath] = dump.Bytes
			}
		}
	}
	found := map[string]bool{}
	if err := walkArchive(ctx, path, func(header *tar.Header, _ *tar.Reader) error {
		if size, ok := wanted[header.Name]; ok && header.Typeflag == tar.TypeReg && header.Size == size {
			found[header.Name] = true
		}
		return nil
	}); err != nil {
		return err
	}
	for name := range wanted {
		if !found[name] {
			return fmt.Errorf("database dump %s is missing from the archived entries", name)
		}
	}
	return nil
}

// RestoreDatabase extracts one recorded dump from a run's archive, checks it
// against the manifest digest and hands it to the Databases owner to load into
// the saved connection. `database` may name another database on the same
// server, so a restore drill never has to touch the live one.
func (r *Runner) RestoreDatabase(ctx context.Context, runID, connectionID int64, database string) (string, error) {
	if r.databases == nil {
		return "", errors.New("the database dump owner is unavailable")
	}
	run, path, cleanup, err := r.localArtifact(ctx, runID)
	if err != nil {
		return "", err
	}
	defer cleanup()
	var selected *DatabaseDump
	for index := range run.Manifest.DatabaseDumps {
		if run.Manifest.DatabaseDumps[index].ConnectionID == connectionID {
			selected = &run.Manifest.DatabaseDumps[index]
		}
	}
	if selected == nil || !run.Manifest.CoversDatabase(connectionID) {
		return "", fmt.Errorf("run %d holds no native dump for database connection %d", runID, connectionID)
	}
	if err := os.MkdirAll(r.stage, 0o700); err != nil {
		return "", err
	}
	directory, err := os.MkdirTemp(r.stage, "restore-db-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(directory)
	target := filepath.Join(directory, selected.File)
	extracted := false
	if err := walkArchive(ctx, path, func(header *tar.Header, reader *tar.Reader) error {
		if header.Name != selected.ArchivePath || header.Typeflag != tar.TypeReg {
			return nil
		}
		file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		// Bounded by the size the manifest recorded: a tampered header cannot
		// make this fill the disk.
		_, copyErr := io.Copy(file, io.LimitReader(reader, selected.Bytes+1))
		closeErr := file.Close()
		if err := errors.Join(copyErr, closeErr); err != nil {
			return err
		}
		extracted = true
		return nil
	}); err != nil {
		return "", err
	}
	if !extracted {
		return "", fmt.Errorf("database dump %s is missing from the archive", selected.ArchivePath)
	}
	digest, err := fileDigest(ctx, target)
	if err != nil {
		return "", err
	}
	if digest != selected.Digest {
		return "", errors.New("extracted database dump does not match its recorded digest")
	}
	return r.databases.RestoreDatabase(ctx, connectionID, database, target)
}

func walkArchive(ctx context.Context, path string, visit func(*tar.Header, *tar.Reader) error) error {
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
			return nil
		}
		if err != nil {
			return err
		}
		if err := visit(header, reader); err != nil {
			return err
		}
	}
}
