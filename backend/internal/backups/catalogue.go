package backups

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// A destination's catalogue is what it holds that this server could take on:
// every archive where the runner writes, read from the manifest beside it
// (sidecar.go) and grouped by the install and job that took it, with what it
// covers, its size and time, whether it is sealed and whether a key this
// server holds opens it. Archives this server wrote and still has the run of
// are left out and counted. An import adopts archives from it as imported
// runs under an imported job (import.go), reading them where they are.

// Destination is where a catalogue is read: a job's destination or one just
// entered, with the keys that reach it. Pin records an SFTP server's host key
// the first time it is reached; for one just entered it records nothing.
type Destination struct {
	Kind    TargetKind
	Target  TargetConfig
	Secrets *TargetSecrets
	Pin     HostKeyPinner
}

func (d Destination) job() *Job {
	return &Job{Name: "catalogue", TargetKind: d.Kind, Target: d.Target}
}

// CatalogueArchive is one archive at a destination.
type CatalogueArchive struct {
	Key        string     `json:"key"`
	Name       string     `json:"name"`
	Bytes      int64      `json:"bytes"`
	ModifiedAt *time.Time `json:"modifiedAt,omitempty"`
	// StartedAt is when its run started, by its manifest or, for an archive
	// with nothing beside it, by its name.
	StartedAt *time.Time `json:"startedAt,omitempty"`
	// Manifest is where what is said about the archive came from: "sidecar",
	// the manifest written beside it since 0.8.2, or "none" for an archive
	// written before, known only by its name and size.
	Manifest string         `json:"manifest"`
	Contents ContentSummary `json:"contents"`
	Kinds    []string       `json:"kinds"`
	// Sources are its recorded paths and volumes, when its manifest is clear
	// or sealed to a key this server holds.
	Sources       []ManifestSource `json:"sources,omitempty"`
	DatabaseDumps []CatalogueDump  `json:"databaseDumps,omitempty"`
	Digest        string           `json:"digest,omitempty"`
	Encrypted     bool             `json:"encrypted"`
	Recipient     string           `json:"recipient,omitempty"`
	Openable      bool             `json:"openable"`
	// NeedsKey is a sealed archive whose key this server does not hold:
	// Recipient names the key to import.
	NeedsKey    bool          `json:"needsKey,omitempty"`
	Importable  bool          `json:"importable"`
	Problem     string        `json:"problem,omitempty"`
	ImportedRun int64         `json:"importedRun,omitempty"`
	ImportedJob int64         `json:"importedJob,omitempty"`
	origin      SidecarOrigin // grouping
	sidecar     *Sidecar      // what import reads
	manifest    *Manifest     // opened, when it could be
	keyErr      error         // the key that would open it, missing
}

// CatalogueDump is a native dump an archive holds, by what it was of.
type CatalogueDump struct {
	Name     string `json:"name"`
	Driver   string `json:"driver"`
	Database string `json:"database"`
	Bytes    int64  `json:"bytes"`
}

// CatalogueJob is one job's archives at the destination, newest first.
type CatalogueJob struct {
	OriginJobID int64              `json:"originJobId"`
	Name        string             `json:"name"`
	Archives    []CatalogueArchive `json:"archives"`
	Bytes       int64              `json:"bytes"`
	Newest      *time.Time         `json:"newest,omitempty"`
	// ImportedJob is the job here its archives were imported into, if any.
	ImportedJob int64 `json:"importedJob,omitempty"`
}

// CatalogueOrigin is one install's jobs. ID is empty for the archives that
// carry no manifest, grouped by the job their names give.
type CatalogueOrigin struct {
	ID         string         `json:"id"`
	Host       string         `json:"host,omitempty"`
	ThisServer bool           `json:"thisServer,omitempty"`
	Jobs       []CatalogueJob `json:"jobs"`
}

type Catalogue struct {
	Destination string            `json:"destination"`
	Origins     []CatalogueOrigin `json:"origins"`
	Archives    int               `json:"archives"`
	// Ours counts the archives left out: this server wrote them and still
	// has their runs.
	Ours      int  `json:"ours"`
	Truncated bool `json:"truncated,omitempty"`
	// An SFTP server's host key, as presented when the catalogue was read —
	// for one just entered, the key to compare with the server's own and to
	// pin when importing.
	HostKey            string `json:"hostKey,omitempty"`
	HostKeyFingerprint string `json:"hostKeyFingerprint,omitempty"`
}

// maxCatalogueArchives bounds what one catalogue reads manifests for.
const maxCatalogueArchives = 2000

func isArchiveName(name string) bool {
	return strings.HasSuffix(name, ".tar.gz") || strings.HasSuffix(name, ".tar.gz"+encryptedSuffix)
}

// artifactNameRe reads what artifactName put in a name: the job's name and
// id, the run's id and when it started.
var artifactNameRe = regexp.MustCompile(`^(.+)-job(\d+)-run(\d+)-(\d{8}-\d{6})-[A-Za-z0-9]+\.tar\.gz(\.age)?$`)

type parsedName struct {
	job       string
	jobID     int64
	started   time.Time
	encrypted bool
}

func parseArtifactName(name string) (parsedName, bool) {
	match := artifactNameRe.FindStringSubmatch(name)
	if match == nil {
		return parsedName{encrypted: strings.HasSuffix(name, encryptedSuffix)}, false
	}
	jobID, _ := strconv.ParseInt(match[2], 10, 64)
	started, err := time.ParseInLocation("20060102-150405", match[4], time.UTC)
	if err != nil {
		return parsedName{}, false
	}
	return parsedName{job: match[1], jobID: jobID, started: started, encrypted: match[5] != ""}, true
}

// prepareDestination validates a destination and resolves a local one, so a
// catalogue and an import read only where this server's roots allow.
func (s *Store) prepareDestination(dest *Destination) error {
	if err := ValidateTarget(dest.Kind, &dest.Target); err != nil {
		return err
	}
	if dest.Kind == TargetLocal {
		resolved, err := s.paths.Resolve(dest.Target.Path)
		if err != nil {
			return err
		}
		dest.Target.Path = resolved
	}
	switch dest.Kind {
	case TargetSFTP:
		if _, err := sftpAuth(dest.Secrets); err != nil {
			return err
		}
		// A key handed back from an earlier reading of the catalogue is held
		// to, as a job's pin is; none means the server is pinned when reached.
		dest.Target.HostKeyFingerprint = ""
		if dest.Target.HostKey != "" {
			key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(dest.Target.HostKey))
			if err != nil {
				return errors.New("the pinned host key is not an SSH public key line")
			}
			dest.Target.HostKey = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
			dest.Target.HostKeyFingerprint = FingerprintHostKey(key)
		}
	case TargetRclone:
		if dest.Secrets == nil {
			return errors.New("an rclone destination needs the remote's configuration")
		}
		remote, err := ParseRcloneRemote(dest.Secrets.RcloneConfig)
		if err != nil {
			return err
		}
		dest.Target.RemoteType = remote.Type
	}
	return nil
}

// ourArtifacts is what this server's own runs point at, which a catalogue
// leaves out; importedArtifacts what it has imported, by the origin run.
func (s *Store) ourArtifacts(ctx context.Context) (map[string]bool, map[string][2]int64, error) {
	rows, err := s.st.DB.QueryContext(ctx, `SELECT id, job_id, artifact, imported,
		COALESCE(json_extract(manifest_json, '$.origin.id'), ''), COALESCE(json_extract(manifest_json, '$.origin.runId'), 0)
		FROM backup_runs WHERE status = ? AND artifact != ''`, string(StatusSuccess))
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	ours, imported := map[string]bool{}, map[string][2]int64{}
	for rows.Next() {
		var (
			id, jobID, originRun int64
			artifact, originID   string
			isImported           int
		)
		if err := rows.Scan(&id, &jobID, &artifact, &isImported, &originID, &originRun); err != nil {
			return nil, nil, err
		}
		if isImported == 1 {
			imported[originKey(originID, originRun)] = [2]int64{id, jobID}
		} else {
			ours[artifact] = true
		}
	}
	return ours, imported, rows.Err()
}

func originKey(id string, run int64) string { return id + "/" + strconv.FormatInt(run, 10) }

// readCatalogue lists a destination and reads the manifest beside each
// archive, eight at a time. With want, only the archives it names are read.
func (r *Runner) readCatalogue(ctx context.Context, reader destinationReader, want func(key string) bool) ([]CatalogueArchive, int, bool, error) {
	objects, truncated, err := reader.list(ctx)
	if err != nil {
		return nil, 0, false, err
	}
	ours, imported, err := r.store.ourArtifacts(ctx)
	if err != nil {
		return nil, 0, false, err
	}
	sidecars := map[string]bool{}
	var archives []remoteObject
	for _, object := range objects {
		name := path.Base(filepath.ToSlash(object.Key))
		switch {
		case strings.HasSuffix(name, sidecarSuffix):
			sidecars[object.Key] = true
		case isArchiveName(name):
			archives = append(archives, object)
		}
	}
	sort.Slice(archives, func(i, j int) bool { return archives[i].Modified.After(archives[j].Modified) })
	if len(archives) > maxCatalogueArchives {
		archives, truncated = archives[:maxCatalogueArchives], true
	}
	ourCount := 0
	var listed []remoteObject
	for _, object := range archives {
		if ours[object.Key] {
			ourCount++
			continue
		}
		if want != nil && !want(object.Key) {
			continue
		}
		listed = append(listed, object)
	}
	out := make([]CatalogueArchive, len(listed))
	var wg sync.WaitGroup
	work := make(chan int)
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range work {
				out[i] = r.describeArchive(ctx, reader, listed[i], sidecars[listed[i].Key+sidecarSuffix], imported)
			}
		}()
	}
	for i := range listed {
		work <- i
	}
	close(work)
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, 0, false, err
	}
	return out, ourCount, truncated, nil
}

// describeArchive reads what one archive is.
func (r *Runner) describeArchive(ctx context.Context, reader destinationReader, object remoteObject, hasSidecar bool, imported map[string][2]int64) CatalogueArchive {
	name := path.Base(filepath.ToSlash(object.Key))
	archive := CatalogueArchive{Key: object.Key, Name: name, Bytes: object.Size, Manifest: "none", Kinds: []string{}}
	if !object.Modified.IsZero() {
		modified := object.Modified
		archive.ModifiedAt = &modified
	}
	parsed, named := parseArtifactName(name)
	archive.Encrypted = parsed.encrypted
	if named {
		started := parsed.started
		archive.StartedAt = &started
	}
	if !hasSidecar {
		archive.Problem = "Written before archives carried their manifest beside them, so there is nothing to prove it against; download it and open it with tar (and age, with the exported key, when it is sealed)."
		archive.origin = SidecarOrigin{JobID: parsed.jobID, JobName: parsed.job}
		if !named {
			archive.origin.JobName = name
		}
		return archive
	}
	raw, err := reader.read(ctx, object.Key+sidecarSuffix, maxSidecarBytes)
	var sidecar *Sidecar
	if err == nil {
		sidecar, err = parseSidecar(raw, name)
	}
	if err != nil {
		archive.Manifest, archive.Problem = "unreadable", "The manifest beside it could not be read: "+err.Error()
		archive.origin = SidecarOrigin{JobID: parsed.jobID, JobName: parsed.job}
		return archive
	}
	archive.Manifest, archive.sidecar, archive.origin = "sidecar", sidecar, sidecar.Origin
	started := sidecar.Origin.StartedAt
	archive.StartedAt = &started
	archive.Digest, archive.Contents = sidecar.ArtifactDigest, sidecar.Contents
	archive.Kinds = sidecar.Contents.Kinds()
	if sidecar.Encryption != nil {
		archive.Encrypted, archive.Recipient = true, sidecar.Encryption.Recipient
	}
	if sidecar.ArtifactBytes > 0 && sidecar.ArtifactBytes != object.Size {
		archive.Problem = fmt.Sprintf("The destination holds %d bytes; its manifest says the archive is %d. It is incomplete or was replaced.", object.Size, sidecar.ArtifactBytes)
		return archive
	}
	if run, ok := imported[originKey(sidecar.Origin.ID, sidecar.Origin.RunID)]; ok {
		archive.ImportedRun, archive.ImportedJob = run[0], run[1]
	}
	manifest, err := r.store.openSidecar(ctx, sidecar)
	if err != nil {
		var keyErr *KeyError
		if errors.As(err, &keyErr) {
			archive.keyErr, archive.NeedsKey = err, true
			archive.Problem = "Sealed to a backup key this server does not hold. Import that key to open it here."
		} else {
			archive.Problem = err.Error()
		}
		return archive
	}
	archive.manifest, archive.Openable = manifest, true
	archive.Importable = archive.ImportedRun == 0
	archive.Sources = manifest.Sources
	for _, dump := range manifest.DatabaseDumps {
		archive.DatabaseDumps = append(archive.DatabaseDumps, CatalogueDump{Name: dump.Name, Driver: dump.Driver, Database: dump.Database, Bytes: dump.Bytes})
	}
	return archive
}

// groupCatalogue folds archives into installs and their jobs.
func groupCatalogue(archives []CatalogueArchive, installID string) []CatalogueOrigin {
	type jobKey struct {
		origin string
		job    int64
		name   string
	}
	origins := map[string]*CatalogueOrigin{}
	jobs := map[jobKey]*CatalogueJob{}
	var originOrder []string
	var jobOrder []jobKey
	for _, archive := range archives {
		id := archive.origin.ID
		if archive.Manifest != "sidecar" {
			id = ""
		}
		origin := origins[id]
		if origin == nil {
			origin = &CatalogueOrigin{ID: id, Host: archive.origin.Host, ThisServer: id != "" && id == installID, Jobs: []CatalogueJob{}}
			origins[id] = origin
			originOrder = append(originOrder, id)
		}
		key := jobKey{id, archive.origin.JobID, archive.origin.JobName}
		job := jobs[key]
		if job == nil {
			job = &CatalogueJob{OriginJobID: archive.origin.JobID, Name: archive.origin.JobName, Archives: []CatalogueArchive{}}
			jobs[key] = job
			jobOrder = append(jobOrder, key)
		}
		job.Archives = append(job.Archives, archive)
		job.Bytes += archive.Bytes
		if archive.StartedAt != nil && (job.Newest == nil || archive.StartedAt.After(*job.Newest)) {
			at := *archive.StartedAt
			job.Newest = &at
		}
		if archive.ImportedJob != 0 {
			job.ImportedJob = archive.ImportedJob
		}
	}
	for _, key := range jobOrder {
		job := jobs[key]
		sort.SliceStable(job.Archives, func(i, j int) bool { return newer(job.Archives[i], job.Archives[j]) })
		origins[key.origin].Jobs = append(origins[key.origin].Jobs, *job)
	}
	out := make([]CatalogueOrigin, 0, len(originOrder))
	for _, id := range originOrder {
		origin := origins[id]
		sort.SliceStable(origin.Jobs, func(i, j int) bool {
			return strings.ToLower(origin.Jobs[i].Name) < strings.ToLower(origin.Jobs[j].Name)
		})
		out = append(out, *origin)
	}
	// Another server's archives first, by its name; then this one's own
	// that lost their records; the archives that carry no manifest last.
	sort.SliceStable(out, func(i, j int) bool {
		rank := func(o CatalogueOrigin) int {
			switch {
			case o.ID == "":
				return 2
			case o.ThisServer:
				return 1
			}
			return 0
		}
		if rank(out[i]) != rank(out[j]) {
			return rank(out[i]) < rank(out[j])
		}
		return strings.ToLower(out[i].Host) < strings.ToLower(out[j].Host)
	})
	return out
}

func newer(a, b CatalogueArchive) bool {
	at, bt := time.Time{}, time.Time{}
	if a.StartedAt != nil {
		at = *a.StartedAt
	}
	if b.StartedAt != nil {
		bt = *b.StartedAt
	}
	if !at.Equal(bt) {
		return at.After(bt)
	}
	return a.Key > b.Key
}

// Catalogue reads what a destination holds that this server did not write.
func (r *Runner) Catalogue(ctx context.Context, dest Destination) (*Catalogue, error) {
	if err := r.store.prepareDestination(&dest); err != nil {
		return nil, err
	}
	job := dest.job()
	reader, err := r.store.openDestination(ctx, job, dest.Secrets, dest.Pin)
	if err != nil {
		return nil, err
	}
	defer reader.close()
	archives, ours, truncated, err := r.readCatalogue(ctx, reader, nil)
	if err != nil {
		return nil, err
	}
	installID, err := r.store.InstallID(ctx)
	if err != nil {
		return nil, err
	}
	return &Catalogue{
		Destination: destinationLabel(job), Origins: groupCatalogue(archives, installID),
		Archives: len(archives), Ours: ours, Truncated: truncated,
		HostKey: job.Target.HostKey, HostKeyFingerprint: job.Target.HostKeyFingerprint,
	}, nil
}

// destinationLabel names a destination the way a run's log names a key in it.
func destinationLabel(job *Job) string {
	switch job.TargetKind {
	case TargetLocal:
		return job.Target.Path
	case TargetSFTP:
		return "sftp://" + job.Target.Username + "@" + job.Target.sftpAddress() + "/" + strings.TrimPrefix(job.Target.Path, "/")
	case TargetRclone:
		return "rclone " + job.Target.RemoteType + ":" + job.Target.Path
	}
	label := job.Target.Bucket
	if prefix := strings.TrimSuffix(s3ListPrefix(job.Target.Prefix), "/"); prefix != "" {
		label += "/" + prefix
	}
	return label
}

// ErrDigestMismatch is an archive whose bytes are not the ones its manifest
// describes: incomplete, damaged, or replaced since it was written.
var ErrDigestMismatch = errors.New("the archive at the destination does not match the digest its manifest records")

// fetchVerified reads an archive whole, hashing it as it streams and, with a
// copy path, writing it there too; a copy of bytes that fail the digest is
// removed.
func fetchVerified(ctx context.Context, reader destinationReader, key, digest string, copyPath string) (int64, error) {
	hash := sha256.New()
	var out io.Writer = hash
	var copied *os.File
	if copyPath != "" {
		f, err := os.OpenFile(copyPath, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
		if err != nil {
			return 0, fmt.Errorf("cannot keep a copy at %s: %w", copyPath, err)
		}
		copied, out = f, io.MultiWriter(hash, f)
	}
	n, err := reader.fetch(ctx, key, out)
	if copied != nil {
		err = errors.Join(err, copied.Close())
	}
	if err == nil && "sha256:"+hex.EncodeToString(hash.Sum(nil)) != digest {
		err = fmt.Errorf("%w (%s)", ErrDigestMismatch, key)
	}
	if err != nil && copied != nil {
		_ = os.Remove(copyPath)
	}
	return n, err
}
