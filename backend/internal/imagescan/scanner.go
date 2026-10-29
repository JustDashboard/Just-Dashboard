package imagescan

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

const (
	// Version is the Trivy release the dashboard scans with, and Image the
	// image it runs, pinned by tag and by its index digest so the tag
	// cannot be moved under it.
	Version = "0.76.1"
	Image   = "aquasec/trivy:" + Version + "@sha256:790fd66d1697395d8693d1acaa1ea702406a20f109b6fc7bac8db623eca6c06c"
	// DatabaseVolume holds the vulnerability database the scanner reads.
	DatabaseVolume = "jd-image-scan-db"
	// RoleLabel names what a container or volume of the scanner's is, so a
	// start-up sweep finds a scan a stopped process left behind.
	RoleLabel = "io.just-dashboard.image-scan"
	// scanUser is nobody: the scanner reads the image and the database and
	// writes its report, and needs no account of its own for any of it.
	scanUser = "65534:65534"
	// MaxImageBytes bounds the archive of one image written for the scanner.
	MaxImageBytes = 8 << 30
	// ArchivePath is where the scanner reads the image's archive.
	ArchivePath    = "/scan/image.tar"
	maxReportBytes = 64 << 20
	scanTimeout    = 15 * time.Minute
)

// ErrUnavailable is a scan that could not be carried out: no Docker, no
// database, an image no longer on this server.
var ErrUnavailable = errors.New("the image could not be scanned")

// Docker is what the scanner needs from the daemon. The scanner never hands
// a container the daemon's socket: an image reaches it as an archive in a
// folder, and the database in a volume.
type Docker interface {
	EnsureVolume(ctx context.Context, name string, labels map[string]string) error
	ImagePresent(ctx context.Context, ref string) bool
	PullImage(ctx context.Context, ref string) error
	RemoveImage(ctx context.Context, ref string) error
	// SaveImage streams the archive `docker save` writes.
	SaveImage(ctx context.Context, ref string) (io.ReadCloser, error)
	// CopyIntoVolume unpacks a tar into a directory of a volume, through a
	// container of image that is created, never started, and removed.
	CopyIntoVolume(ctx context.Context, volume, image, dir string, archive io.Reader) error
	// Run runs a container to completion and removes it, returning its exit
	// code and the last lines it wrote to standard error.
	Run(ctx context.Context, spec dockerx.ContainerSpec) (int, []string, error)
}

// Scanner scans images, one at a time, and keeps the database current.
type Scanner struct {
	docker Docker
	// dir is a folder on the host the scanner's work happens in: the
	// database's state, the downloaded layer, and each scan's archive and
	// report while it runs.
	dir string
	// volume holds the database; DatabaseVolume unless a test names its own.
	volume  string
	sources []Source
	http    *http.Client
	now     func() time.Time
	// registryBase overrides https://<registry> for a test.
	registryBase func(Source) string

	// mu makes a download and a scan take turns: a scan never reads a
	// database while it is being replaced.
	mu sync.Mutex
}

// New is a scanner working in dir.
func New(docker Docker, dir string) *Scanner {
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 15 * time.Second}).DialContext,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	}
	return &Scanner{
		docker: docker, dir: dir, volume: DatabaseVolume, sources: DefaultSources, now: time.Now,
		http: &http.Client{Transport: transport, CheckRedirect: httpsRedirectsOnly},
		registryBase: func(source Source) string {
			return "https://" + source.Registry
		},
	}
}

// WithVolume keeps the database in another volume: a live test's own, so it
// never touches the one a dashboard on the same host reads.
func (s *Scanner) WithVolume(name string) *Scanner {
	s.volume = name
	return s
}

// httpsRedirectsOnly follows a registry's redirect to its blob storage only
// over https: the layer is checked against its digest whatever serves it,
// but this process does not go asking plain-HTTP addresses on a registry's
// say-so.
func httpsRedirectsOnly(req *http.Request, via []*http.Request) error {
	if len(via) >= 5 {
		return errors.New("the registry redirected too many times")
	}
	if req.URL.Scheme != "https" && (len(via) == 0 || req.URL.Host != via[0].URL.Host) {
		return fmt.Errorf("the registry redirected to %s, which is not https", req.URL.Redacted())
	}
	return nil
}

// Database is the state of the database in the scanner's volume.
func (s *Scanner) Database() DatabaseState {
	if s == nil {
		return DatabaseState{}
	}
	return readState(s.dir)
}

// EnsureDatabase downloads a new database when the one in the volume is
// missing or its publisher's next one is due. A failed download leaves the
// database that was there, and the scans go on reading it at its age, which
// they report.
func (s *Scanner) EnsureDatabase(ctx context.Context) (DatabaseState, error) {
	if s == nil || s.docker == nil {
		return DatabaseState{}, ErrUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ensureDatabase(ctx)
}

func (s *Scanner) ensureDatabase(ctx context.Context) (DatabaseState, error) {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return DatabaseState{}, err
	}
	state := readState(s.dir)
	now := s.now().UTC()
	if !state.due(now) {
		if !state.Installed {
			return state, fmt.Errorf("%w: there is no vulnerability database yet (%s)", ErrUnavailable, state.LastError)
		}
		return state, nil
	}
	refreshed, err := s.download(ctx, state)
	refreshed.LastAttemptAt = now
	refreshed.LastError = ""
	if err != nil {
		refreshed.LastError = err.Error()
	}
	if writeErr := writeState(s.dir, refreshed); writeErr != nil && err == nil {
		err = writeErr
	}
	if !refreshed.Installed {
		return refreshed, fmt.Errorf("%w: the vulnerability database could not be downloaded: %v", ErrUnavailable, err)
	}
	// A database that could not be replaced is still a database: the scan
	// reads it, and says how old it is.
	return refreshed, nil
}

// download fetches the database from the first source that answers and
// copies it into the volume. The layer is written to a file and checked
// before anything in the volume is touched, so a download that fails part
// of the way leaves the old database whole.
func (s *Scanner) download(ctx context.Context, previous DatabaseState) (DatabaseState, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	layer := filepath.Join(s.dir, "database-layer.tar.gz")
	defer os.Remove(layer)
	var failures []string
	for _, source := range s.sources {
		client := &registryClient{http: s.http, source: source, base: s.registryBase(source)}
		digest, size, err := client.layer(ctx)
		if err == nil && digest == previous.Digest && previous.Installed {
			// Nothing new has been published yet; the one in the volume is
			// the latest, so it counts as downloaded now.
			previous.DownloadedAt = s.now().UTC()
			if !previous.NextUpdate.After(previous.DownloadedAt) {
				previous.NextUpdate = previous.DownloadedAt.Add(6 * time.Hour)
			}
			return previous, nil
		}
		if err == nil {
			err = client.download(ctx, digest, size, layer)
		}
		if err != nil {
			failures = append(failures, err.Error())
			continue
		}
		if err := s.docker.EnsureVolume(ctx, s.volume, map[string]string{RoleLabel: "database"}); err != nil {
			return previous, err
		}
		// From here the volume's database is being replaced: until the copy
		// finishes there is none to read.
		installing := previous
		installing.Installed = false
		if err := writeState(s.dir, installing); err != nil {
			return previous, err
		}
		metadata, err := s.install(ctx, layer)
		if err != nil {
			return installing, err
		}
		return DatabaseState{
			Installed: true, UpdatedAt: metadata.UpdatedAt.UTC(), NextUpdate: metadata.NextUpdate.UTC(),
			DownloadedAt: s.now().UTC(), Digest: digest, Source: source.String(),
		}, nil
	}
	return previous, errors.New(strings.Join(failures, "; "))
}

// install copies a checked layer into the volume, repacked so that only the
// database's two files reach it.
func (s *Scanner) install(ctx context.Context, layer string) (databaseMetadata, error) {
	file, err := os.Open(layer)
	if err != nil {
		return databaseMetadata{}, err
	}
	defer file.Close()
	reader, writer := io.Pipe()
	result := make(chan struct {
		metadata databaseMetadata
		err      error
	}, 1)
	go func() {
		metadata, err := repackDatabase(file, writer)
		writer.CloseWithError(err)
		result <- struct {
			metadata databaseMetadata
			err      error
		}{metadata, err}
	}()
	copyErr := s.docker.CopyIntoVolume(ctx, s.volume, Image, "/cache", reader)
	reader.CloseWithError(errors.New("the copy into the volume ended"))
	repacked := <-result
	if repacked.err != nil {
		return repacked.metadata, repacked.err
	}
	if copyErr != nil {
		return repacked.metadata, fmt.Errorf("the database could not be copied into its volume: %v", copyErr)
	}
	return repacked.metadata, nil
}

// Scanned is one image's report with the database it was read against.
type Scanned struct {
	Report   *Report
	Database DatabaseState
}

// Scan reads one image on this server. With pull, an image that is not here
// is pulled first and removed again afterwards — a release's base images,
// which BuildKit fetched into its own store rather than the daemon's.
func (s *Scanner) Scan(ctx context.Context, ref string, pull bool) (*Scanned, error) {
	if s == nil || s.docker == nil {
		return nil, ErrUnavailable
	}
	ref = strings.TrimSpace(ref)
	if ref == "" || strings.ContainsAny(ref, " \x00\r\n") {
		return nil, fmt.Errorf("%w: no image reference", ErrUnavailable)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	database, err := s.ensureDatabase(ctx)
	if err != nil {
		return nil, err
	}
	if !s.docker.ImagePresent(ctx, ref) {
		if !pull {
			return nil, fmt.Errorf("%w: %s is no longer on this server", ErrUnavailable, ref)
		}
		if err := s.docker.PullImage(ctx, ref); err != nil {
			return nil, fmt.Errorf("%w: %s could not be pulled: %v", ErrUnavailable, ref, err)
		}
		defer func() { _ = s.docker.RemoveImage(context.WithoutCancel(ctx), ref) }()
	}
	work, err := s.workspace()
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(work)
	if err := s.saveImage(ctx, ref, filepath.Join(work, "in", "image.tar")); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, scanTimeout)
	defer cancel()
	code, stderr, err := s.docker.Run(ctx, ScanSpec(s.volume, work, randomSuffix()))
	if err != nil {
		return nil, fmt.Errorf("%w: the scanner did not run: %v", ErrUnavailable, err)
	}
	if code != 0 {
		return nil, fmt.Errorf("%w: the scanner exited with %d: %s", ErrUnavailable, code, lastLine(stderr))
	}
	raw, err := readBounded(filepath.Join(work, "out", "report.json"), maxReportBytes)
	if err != nil {
		return nil, fmt.Errorf("%w: the scanner wrote no report: %v", ErrUnavailable, err)
	}
	report, err := ParseReport(raw)
	if err != nil {
		return nil, err
	}
	return &Scanned{Report: report, Database: database}, nil
}

// workspace is a folder for one scan: in/ for the archive, which the
// scanner reads, out/ for its report and tmp/ for its scratch, which it
// writes as nobody. Docker binds each by its path on the host.
func (s *Scanner) workspace() (string, error) {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return "", err
	}
	work := filepath.Join(s.dir, "scan-"+randomSuffix())
	for _, dir := range []string{work, filepath.Join(work, "in"), filepath.Join(work, "out"), filepath.Join(work, "tmp")} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			_ = os.RemoveAll(work)
			return "", err
		}
		// The folder's own mode, not the umask's: the scanner's account
		// must traverse it.
		if err := os.Chmod(dir, 0o755); err != nil {
			_ = os.RemoveAll(work)
			return "", err
		}
	}
	for _, dir := range []string{filepath.Join(work, "out"), filepath.Join(work, "tmp")} {
		if err := os.Chown(dir, 65534, 65534); err != nil {
			// Not root: a test's own folder, which it may hand to anybody.
			if err := os.Chmod(dir, 0o777); err != nil {
				_ = os.RemoveAll(work)
				return "", err
			}
		}
	}
	return work, nil
}

func (s *Scanner) saveImage(ctx context.Context, ref, path string) error {
	stream, err := s.docker.SaveImage(ctx, ref)
	if err != nil {
		return fmt.Errorf("%w: %s could not be read from Docker: %v", ErrUnavailable, ref, err)
	}
	defer stream.Close()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	written, err := io.Copy(file, io.LimitReader(stream, MaxImageBytes+1))
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("%w: %s could not be written for the scanner: %v", ErrUnavailable, ref, err)
	}
	if written > MaxImageBytes {
		return fmt.Errorf("%w: %s is larger than the %d GiB the scanner reads", ErrUnavailable, ref, MaxImageBytes>>30)
	}
	return os.Chmod(path, 0o644)
}

// ScanSpec is the scanner's container: Trivy reading the archive offline
// against the volume's database. It has no network, no capabilities, a
// read-only root, no way to gain privileges, runs as nobody, and sees the
// archive and the database read-only; the report and its scratch are the
// only things it writes.
func ScanSpec(volume, work, suffix string) dockerx.ContainerSpec {
	return dockerx.ContainerSpec{
		Name:  "jd-image-scan-" + suffix,
		Image: Image,
		Command: []string{
			"image", "--input", ArchivePath,
			"--cache-dir", "/cache", "--cache-backend", "memory",
			"--skip-db-update", "--skip-java-db-update", "--skip-vex-repo-update", "--offline-scan",
			"--skip-version-check", "--disable-telemetry",
			"--scanners", "vuln", "--list-all-pkgs=false",
			"--format", "json", "--output", "/out/report.json",
			"--timeout", "14m", "--quiet",
		},
		Env:             []dockerx.EnvVar{{Name: "TMPDIR", Value: "/work"}},
		User:            scanUser,
		NetworkMode:     "none",
		ReadOnly:        true,
		CapDrop:         []string{"ALL"},
		NoNewPrivileges: true,
		Mounts: []dockerx.MountSpec{
			{Type: "volume", Source: volume, Target: "/cache", ReadOnly: true},
			{Type: "bind", Source: filepath.Join(work, "in"), Target: "/scan", ReadOnly: true},
			{Type: "bind", Source: filepath.Join(work, "out"), Target: "/out"},
			{Type: "bind", Source: filepath.Join(work, "tmp"), Target: "/work"},
		},
		Labels:  []dockerx.LabelSpec{{Name: RoleLabel, Value: "scan"}},
		Limits:  dockerx.ResourceLimits{MemoryMB: 2048, MemorySwapMB: 2048, CPUs: 1, PidsLimit: 256},
		Logging: dockerx.CappedLogging(),
	}
}

func readBounded(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("the report is larger than %d MiB", limit>>20)
	}
	return raw, nil
}

func lastLine(lines []string) string {
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return clip(line, 300)
		}
	}
	return "it said nothing"
}

func randomSuffix() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
