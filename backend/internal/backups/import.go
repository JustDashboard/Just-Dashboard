package backups

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// An import adopts archives a destination holds as this server's record of
// them: an imported job for each job they came from, and an imported run for
// each archive, pointing at the destination's object. Nothing is copied unless
// the import is asked to keep a copy here. Each archive is fetched once and
// proved against its manifest's digest first — an archive that is not the one
// its manifest describes is refused, and so is one whose manifest is sealed to
// a key this server does not hold — and only then recorded. The runs show on
// /backups beside the server's own, restore through the same paths, and are
// never pruned from here.

// ImportRequest names what to adopt from a destination: the archives by their
// keys, or every archive of one job of one install.
type ImportRequest struct {
	Destination Destination
	Keys        []string
	OriginID    string
	OriginJobID int64
	// CopyTo is a directory on this server to keep a copy of each archive in.
	CopyTo string
}

// ImportSkip is an archive the import left, and why.
type ImportSkip struct {
	Key    string `json:"key"`
	Reason string `json:"reason"`
}

type ImportResult struct {
	Destination string       `json:"destination"`
	Jobs        []*Job       `json:"jobs"`
	Runs        []*Run       `json:"runs"`
	Skipped     []ImportSkip `json:"skipped"`
}

// ErrNothingToImport is a request that named no archive the destination holds.
var ErrNothingToImport = errors.New("the destination holds none of the archives named")

// Import adopts archives from a destination. It answers an error only when
// nothing was imported: the first refusal, which says why — a missing key by
// the key's name, a digest that does not match by the archive's.
func (r *Runner) Import(ctx context.Context, req ImportRequest) (*ImportResult, error) {
	dest := req.Destination
	if err := r.store.prepareDestination(&dest); err != nil {
		return nil, err
	}
	if len(req.Keys) == 0 && (req.OriginID == "" || req.OriginJobID <= 0) {
		return nil, errors.New("name the archives to import, or the server and job whose archives to import")
	}
	if len(req.Keys) > maxCatalogueArchives {
		return nil, fmt.Errorf("an import takes at most %d archives", maxCatalogueArchives)
	}
	copyDir := ""
	if strings.TrimSpace(req.CopyTo) != "" {
		resolved, err := r.store.paths.Resolve(req.CopyTo)
		if err != nil {
			return nil, err
		}
		if resolved == "/" {
			return nil, errors.New("refusing to keep copies directly in /")
		}
		if err := os.MkdirAll(resolved, 0o700); err != nil {
			return nil, err
		}
		copyDir = resolved
	}
	job := dest.job()
	reader, err := r.store.openDestination(ctx, job, dest.Secrets, dest.Pin)
	if err != nil {
		return nil, err
	}
	defer reader.close()
	dest.Target = job.Target // an SFTP server reached for the first time is pinned now

	wanted := map[string]bool{}
	for _, key := range req.Keys {
		wanted[key] = true
	}
	var want func(string) bool
	if len(wanted) > 0 {
		want = func(key string) bool { return wanted[key] }
	}

	r.importMu.Lock()
	defer r.importMu.Unlock()
	archives, _, _, err := r.readCatalogue(ctx, reader, want)
	if err != nil {
		return nil, err
	}
	result := &ImportResult{Destination: destinationLabel(job), Jobs: []*Job{}, Runs: []*Run{}, Skipped: []ImportSkip{}}
	var chosen []CatalogueArchive
	found := map[string]bool{}
	for _, archive := range archives {
		found[archive.Key] = true
		if len(wanted) > 0 || (archive.Manifest == "sidecar" && archive.origin.ID == req.OriginID && archive.origin.JobID == req.OriginJobID) {
			chosen = append(chosen, archive)
		}
	}
	ours, _, err := r.store.ourArtifacts(ctx)
	if err != nil {
		return nil, err
	}
	for _, key := range req.Keys {
		if !found[key] {
			reason := "the destination does not hold it where this destination's archives are written"
			if ours[key] {
				reason = "this server wrote it and still has its run"
			}
			result.Skipped = append(result.Skipped, ImportSkip{Key: key, Reason: reason})
		}
	}
	if len(chosen) == 0 && len(result.Skipped) == 0 {
		return nil, ErrNothingToImport
	}
	// Oldest first, so the runs take ids in the order they were taken.
	sort.SliceStable(chosen, func(i, j int) bool { return newer(chosen[j], chosen[i]) })

	var first error
	refuse := func(archive CatalogueArchive, err error) {
		if first == nil {
			first = err
		}
		result.Skipped = append(result.Skipped, ImportSkip{Key: archive.Key, Reason: err.Error()})
	}
	jobs := map[string]*Job{}
	for _, archive := range chosen {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		switch {
		case archive.ImportedRun != 0:
			result.Skipped = append(result.Skipped, ImportSkip{Key: archive.Key, Reason: fmt.Sprintf("already imported as run %d", archive.ImportedRun)})
			continue
		case archive.keyErr != nil:
			refuse(archive, archive.keyErr)
			continue
		case archive.Manifest == "sidecar" && archive.sidecar != nil && archive.sidecar.ArtifactBytes > 0 && archive.sidecar.ArtifactBytes != archive.Bytes:
			refuse(archive, fmt.Errorf("%w (%s: the destination holds %d bytes, the manifest records %d)", ErrDigestMismatch, archive.Key, archive.Bytes, archive.sidecar.ArtifactBytes))
			continue
		case !archive.Openable || archive.manifest == nil:
			refuse(archive, errors.New(archive.Problem))
			continue
		}
		copyPath := ""
		if copyDir != "" {
			copyPath = filepath.Join(copyDir, path.Base(filepath.ToSlash(archive.Key)))
		}
		size, err := fetchVerified(ctx, reader, archive.Key, archive.manifest.ArtifactDigest, copyPath)
		if err != nil {
			refuse(archive, err)
			continue
		}
		groupKey := originKey(archive.origin.ID, archive.origin.JobID)
		imported := jobs[groupKey]
		if imported == nil {
			if imported, err = r.store.importedJob(ctx, dest, archive.origin, archive.manifest); err != nil {
				return nil, err
			}
			jobs[groupKey] = imported
			result.Jobs = append(result.Jobs, imported)
		}
		run, err := r.store.insertImportedRun(ctx, imported, archive, size, copyPath, result.Destination)
		if err != nil {
			if copyPath != "" {
				_ = os.Remove(copyPath)
			}
			return nil, err
		}
		result.Runs = append(result.Runs, run)
	}
	if len(result.Runs) == 0 && first != nil {
		return nil, first
	}
	for i, job := range result.Jobs {
		if fresh, err := r.store.Get(ctx, job.ID); err == nil {
			result.Jobs[i] = fresh
		}
	}
	return result, nil
}

// sameDestination says whether two destinations are one place: the same
// directory, bucket and prefix at the same endpoint, account and directory on
// the same SFTP server, or folder in the same rclone remote.
func sameDestination(kind TargetKind, a, b TargetConfig) bool {
	switch kind {
	case TargetLocal:
		return filepath.Clean(a.Path) == filepath.Clean(b.Path)
	case TargetSFTP:
		return sameSFTPServer(a, b) && a.Username == b.Username && path.Clean(a.Path) == path.Clean(b.Path)
	case TargetRclone:
		return a.RemoteType == b.RemoteType && a.Path == b.Path
	}
	return a.Bucket == b.Bucket && strings.EqualFold(strings.TrimSpace(a.Endpoint), strings.TrimSpace(b.Endpoint)) &&
		s3ListPrefix(a.Prefix) == s3ListPrefix(b.Prefix)
}

// importedJob is the imported job a job of another install's archives at this
// destination go under: the one an earlier import made, or a new one named
// after the job and the server it ran on.
func (s *Store) importedJob(ctx context.Context, dest Destination, origin SidecarOrigin, manifest *Manifest) (*Job, error) {
	rows, err := s.st.DB.QueryContext(ctx, `SELECT id FROM backup_jobs WHERE imported = 1 AND target_kind = ?
		AND json_extract(origin_json, '$.id') = ? AND json_extract(origin_json, '$.jobId') = ?`, string(dest.Kind), origin.ID, origin.JobID)
	if err != nil {
		return nil, err
	}
	var candidates []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		candidates = append(candidates, id)
	}
	rows.Close()
	for _, id := range candidates {
		existing, err := s.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		if !sameDestination(dest.Kind, existing.Target, dest.Target) {
			continue
		}
		if dest.Kind == TargetRclone {
			// Two remotes of one service can share a folder name; only the
			// one with these keys is this destination.
			secrets, err := s.Secrets(ctx, id)
			if err != nil || dest.Secrets == nil || strings.TrimSpace(secrets.RcloneConfig) != strings.TrimSpace(dest.Secrets.RcloneConfig) {
				continue
			}
		}
		return existing, nil
	}

	installID, err := s.InstallID(ctx)
	if err != nil {
		return nil, err
	}
	jobOrigin := JobOrigin{ID: origin.ID, Host: origin.Host, JobID: origin.JobID, JobName: origin.JobName, ThisServer: origin.ID == installID}
	sealed := ""
	if !dest.Secrets.Empty() {
		if sealed, err = s.sealer.Seal(encodeJSON(dest.Secrets)); err != nil {
			return nil, err
		}
	}
	sources := []string{}
	for _, source := range manifest.Sources {
		sources = append(sources, source.Path)
	}
	var recovery *RecoveryPlan
	if manifest.Origin != nil && manifest.Origin.Recovery != nil && manifest.Origin.Recovery.Validate() == nil {
		recovery = manifest.Origin.Recovery
	}
	base := strings.TrimSpace(origin.JobName)
	if base == "" {
		base = "Imported backups"
	}
	switch {
	case jobOrigin.ThisServer:
		base += " (imported)"
	case origin.Host != "":
		base += " (" + origin.Host + ")"
	default:
		base += " (imported)"
	}
	for attempt := 1; attempt <= 100; attempt++ {
		name := base
		if attempt > 1 {
			name = fmt.Sprintf("%s %d", base, attempt)
		}
		var taken bool
		if err := s.st.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM backup_jobs WHERE name = ?)`, name).Scan(&taken); err != nil {
			return nil, err
		}
		if taken {
			continue
		}
		res, err := s.st.DB.ExecContext(ctx,
			`INSERT INTO backup_jobs(name, sources, excludes, target_kind, target_cfg, secrets_enc, schedule, retention, enabled, created_at,
			 recovery_json, sqlite_paths, database_dumps, retention_days, pause_containers, encrypt, imported, origin_json)
			 VALUES(?,?,?,?,?,?,'',0,0,?,?,'[]','[]',0,'[]',0,1,?)`,
			name, encodeJSON(sources), encodeJSON(manifest.Excludes), string(dest.Kind), encodeJSON(dest.Target), sealed,
			time.Now().Unix(), encodeJSON(recovery), encodeJSON(jobOrigin))
		if err != nil {
			return nil, err
		}
		id, _ := res.LastInsertId()
		return s.Get(ctx, id)
	}
	return nil, errors.New("could not name the imported job: every name tried is taken")
}

// insertImportedRun records one proved archive under its imported job.
func (s *Store) insertImportedRun(ctx context.Context, job *Job, archive CatalogueArchive, size int64, copyPath, destination string) (*Run, error) {
	origin := archive.origin
	host := origin.Host
	if host == "" {
		host = "a server that did not record its name"
	}
	var log strings.Builder
	fmt.Fprintf(&log, "imported from %s on %s\n", destination, time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(&log, "originally run %d of job %q on %s (install %s), started %s\n", origin.RunID, origin.JobName, host, origin.ID, origin.StartedAt.UTC().Format(time.RFC3339))
	fmt.Fprintf(&log, "proved the archive against its manifest: %s, %d bytes\n", archive.manifest.ArtifactDigest, size)
	if archive.manifest.Encryption != nil {
		fmt.Fprintf(&log, "sealed to backup key %s, which this server holds\n", archive.manifest.Encryption.Recipient)
	}
	if copyPath != "" {
		fmt.Fprintf(&log, "kept a copy at %s\n", copyPath)
	} else {
		fmt.Fprintf(&log, "read from the destination on every restore; nothing was copied here\n")
	}
	started := origin.StartedAt
	if started.IsZero() {
		started = time.Now()
	}
	res, err := s.st.DB.ExecContext(ctx,
		`INSERT INTO backup_runs(job_id, started_at, ended_at, status, artifact, size_bytes, log, trigger, manifest_json, imported, local_copy)
		 VALUES(?,?,0,?,?,?,?,'import',?,1,?)`,
		job.ID, started.Unix(), string(StatusSuccess), archive.Key, size, log.String(), encodeJSON(archive.manifest), copyPath)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.Run(ctx, id)
}
