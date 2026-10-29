package imagescan

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

// fakeDocker is the daemon as the scanner sees it: images it holds, what a
// copy into the volume carried, and a scanner container that writes the
// report it is told to.
type fakeDocker struct {
	mu       sync.Mutex
	images   map[string]bool
	pulled   []string
	removed  []string
	copied   map[string][]byte
	volumes  []string
	specs    []dockerx.ContainerSpec
	report   []byte
	exitCode int
	copyErr  error
}

func newFakeDocker(images ...string) *fakeDocker {
	d := &fakeDocker{images: map[string]bool{}, copied: map[string][]byte{}}
	for _, image := range images {
		d.images[image] = true
	}
	return d
}

func (d *fakeDocker) EnsureVolume(_ context.Context, name string, _ map[string]string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.volumes = append(d.volumes, name)
	return nil
}

func (d *fakeDocker) ImagePresent(_ context.Context, ref string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.images[ref]
}

func (d *fakeDocker) PullImage(_ context.Context, ref string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.pulled = append(d.pulled, ref)
	d.images[ref] = true
	return nil
}

func (d *fakeDocker) RemoveImage(_ context.Context, ref string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.removed = append(d.removed, ref)
	delete(d.images, ref)
	return nil
}

func (d *fakeDocker) SaveImage(_ context.Context, ref string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("archive of " + ref)), nil
}

func (d *fakeDocker) CopyIntoVolume(_ context.Context, volume, image, dir string, archive io.Reader) error {
	if d.copyErr != nil {
		_, _ = io.Copy(io.Discard, archive)
		return d.copyErr
	}
	tr := tar.NewReader(archive)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		body, _ := io.ReadAll(tr)
		d.mu.Lock()
		d.copied[volume+":"+dir+"/"+header.Name] = body
		d.mu.Unlock()
		if header.Uid != 0 || (header.Typeflag == tar.TypeReg && header.Mode != 0o644) {
			return fmt.Errorf("%s was copied as %d with mode %o", header.Name, header.Uid, header.Mode)
		}
	}
}

func (d *fakeDocker) Run(_ context.Context, spec dockerx.ContainerSpec) (int, []string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.specs = append(d.specs, spec)
	for _, mount := range spec.Mounts {
		if mount.Target == "/scan" {
			archive, err := os.ReadFile(filepath.Join(mount.Source, "image.tar"))
			if err != nil || !strings.HasPrefix(string(archive), "archive of ") {
				return 1, []string{"FATAL no archive"}, nil
			}
		}
		if mount.Target == "/out" && d.report != nil {
			if err := os.WriteFile(filepath.Join(mount.Source, "report.json"), d.report, 0o644); err != nil {
				return 1, nil, err
			}
		}
	}
	if d.exitCode != 0 {
		return d.exitCode, []string{"", "2026-10-29T08:00:00Z\tFATAL\tunable to open the database"}, nil
	}
	return 0, nil, nil
}

// registry serves one database artifact, behind a bearer challenge as
// ghcr.io does or openly as mirror.gcr.io does.
type registry struct {
	server   *httptest.Server
	layer    []byte
	digest   string
	token    bool
	served   int
	tamper   bool
	requests []string
	mu       sync.Mutex
}

func databaseLayer(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		body := entries[name]
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(body)), Typeflag: tar.TypeReg, Uid: 1000}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

const metadataJSON = `{"Version":2,"NextUpdate":"2026-10-30T01:22:22Z","UpdatedAt":"2026-10-29T01:22:22Z"}`

func newRegistry(t *testing.T, layer []byte, token bool) *registry {
	t.Helper()
	sum := sha256.Sum256(layer)
	r := &registry{layer: layer, digest: "sha256:" + hex.EncodeToString(sum[:]), token: token}
	r.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		r.requests = append(r.requests, req.URL.Path)
		r.mu.Unlock()
		if req.URL.Path == "/token" {
			if req.URL.Query().Get("scope") != "repository:aquasecurity/trivy-db:pull" {
				http.Error(w, "scope", http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "pull-token"})
			return
		}
		if r.token && req.Header.Get("Authorization") != "Bearer pull-token" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="`+r.server.URL+`/token",service="test",scope="repository:aquasecurity/trivy-db:pull"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch req.URL.Path {
		case "/v2/aquasecurity/trivy-db/manifests/2":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"schemaVersion": 2,
				"layers": []map[string]any{{
					"mediaType": databaseLayerType, "digest": r.digest, "size": len(r.layer),
				}},
			})
		case "/v2/aquasecurity/trivy-db/blobs/" + r.digest:
			r.mu.Lock()
			r.served++
			r.mu.Unlock()
			body := r.layer
			if r.tamper {
				body = append([]byte(nil), body...)
				body[len(body)/2] ^= 0xff
			}
			_, _ = w.Write(body)
		default:
			http.NotFound(w, req)
		}
	}))
	t.Cleanup(r.server.Close)
	return r
}

func testScanner(t *testing.T, docker *fakeDocker, reg *registry) *Scanner {
	t.Helper()
	s := New(docker, t.TempDir())
	s.volume = "jd-image-scan-db-test"
	s.sources = []Source{{Registry: "test", Repository: "aquasecurity/trivy-db", Tag: "2"}}
	s.registryBase = func(Source) string { return reg.server.URL }
	clock := time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return clock }
	return s
}

func TestTheDatabaseIsFetchedCheckedAndCopiedIntoTheVolumeOnly(t *testing.T) {
	layer := databaseLayer(t, map[string]string{
		"trivy.db": "bolt", "metadata.json": metadataJSON,
		// Anything else in the layer stays out of the volume.
		"../../etc/cron.d/x": "* * * * * root id", "notes.txt": "x",
	})
	reg := newRegistry(t, layer, true)
	docker := newFakeDocker()
	s := testScanner(t, docker, reg)

	state, err := s.EnsureDatabase(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !state.Installed || state.Digest != reg.digest || !state.UpdatedAt.Equal(time.Date(2026, 10, 29, 1, 22, 22, 0, time.UTC)) {
		t.Fatalf("state = %+v", state)
	}
	if got := state.Age(s.now()); got != 7*time.Hour+37*time.Minute+38*time.Second {
		t.Fatalf("age = %s", got)
	}
	want := []string{
		"jd-image-scan-db-test:/cache/db/", "jd-image-scan-db-test:/cache/db/metadata.json", "jd-image-scan-db-test:/cache/db/trivy.db",
	}
	var copied []string
	for name := range docker.copied {
		copied = append(copied, name)
	}
	slices.Sort(copied)
	if !slices.Equal(copied, want) {
		t.Fatalf("the volume was given %v", copied)
	}
	if string(docker.copied["jd-image-scan-db-test:/cache/db/trivy.db"]) != "bolt" {
		t.Fatal("the database's bytes were not the layer's")
	}
	if on := s.Database(); !on.Installed || on.Digest != reg.digest {
		t.Fatalf("the state file says %+v", on)
	}

	// Within the publisher's next-update time nothing is fetched again.
	if _, err := s.EnsureDatabase(context.Background()); err != nil {
		t.Fatal(err)
	}
	if reg.served != 1 {
		t.Fatalf("the blob was fetched %d times", reg.served)
	}
}

func TestADatabaseThatDoesNotMatchItsDigestNeverReachesTheVolume(t *testing.T) {
	reg := newRegistry(t, databaseLayer(t, map[string]string{"trivy.db": "bolt", "metadata.json": metadataJSON}), false)
	reg.tamper = true
	docker := newFakeDocker()
	s := testScanner(t, docker, reg)

	state, err := s.EnsureDatabase(context.Background())
	if !errors.Is(err, ErrUnavailable) || !strings.Contains(err.Error(), "does not match the digest") {
		t.Fatalf("a tampered layer = %v", err)
	}
	if state.Installed || len(docker.copied) != 0 {
		t.Fatalf("a tampered layer was installed: %+v, %v", state, docker.copied)
	}
	if _, err := os.Stat(filepath.Join(s.dir, "database-layer.tar.gz")); !os.IsNotExist(err) {
		t.Fatal("the rejected layer was left on disk")
	}
	// A failed download is not retried on every scan: the next attempt
	// waits out the retry interval.
	reg.tamper = false
	if _, err := s.EnsureDatabase(context.Background()); err == nil || reg.served != 1 {
		t.Fatalf("retried at once: served %d, %v", reg.served, err)
	}
	later := s.now().Add(DatabaseRetry)
	s.now = func() time.Time { return later }
	if state, err := s.EnsureDatabase(context.Background()); err != nil || !state.Installed {
		t.Fatalf("the retry after the interval = %+v, %v", state, err)
	}
}

func TestALayerWithoutTheDatabaseOrOfAnotherSchemaIsRefused(t *testing.T) {
	for name, entries := range map[string]map[string]string{
		"no database": {"metadata.json": metadataJSON},
		"no metadata": {"trivy.db": "bolt"},
		"schema 1":    {"trivy.db": "bolt", "metadata.json": `{"Version":1,"UpdatedAt":"2026-10-29T01:22:22Z"}`},
	} {
		t.Run(name, func(t *testing.T) {
			reg := newRegistry(t, databaseLayer(t, entries), false)
			s := testScanner(t, newFakeDocker(), reg)
			if state, err := s.EnsureDatabase(context.Background()); err == nil || state.Installed {
				t.Fatalf("installed %+v, %v", state, err)
			}
		})
	}
}

func TestAStaleDatabaseIsStillReadWhenItCannotBeReplaced(t *testing.T) {
	reg := newRegistry(t, databaseLayer(t, map[string]string{"trivy.db": "bolt", "metadata.json": metadataJSON}), false)
	docker := newFakeDocker("sha256:release")
	docker.report = readFixture(t, "trivy-alpine-3.12.json")
	s := testScanner(t, docker, reg)
	if _, err := s.EnsureDatabase(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Two days on, the registry is gone: the scan reads the old database
	// and says how old it is.
	later := s.now().Add(48 * time.Hour)
	s.now = func() time.Time { return later }
	reg.server.Close()
	scanned, err := s.Scan(context.Background(), "sha256:release", false)
	if err != nil {
		t.Fatal(err)
	}
	if !scanned.Database.Installed || scanned.Database.Age(later) < 48*time.Hour {
		t.Fatalf("database = %+v", scanned.Database)
	}
	if s.Database().LastError == "" {
		t.Fatal("the failed refresh was not recorded")
	}
}

// The scanner container is the boundary: no network, no capabilities, a
// read-only root, no new privileges, nobody's account, the archive and the
// database read-only, and nothing of the host's else — never the socket.
func TestTheScannerRunsOfflineUnprivilegedAndSeesOnlyWhatItReads(t *testing.T) {
	spec := ScanSpec("jd-image-scan-db", "/var/lib/just-dashboard/image-scans/scan-1", "abc")
	if spec.NetworkMode != "none" || spec.Privileged || len(spec.CapAdd) != 0 || !slices.Equal(spec.CapDrop, []string{"ALL"}) ||
		!spec.ReadOnly || !spec.NoNewPrivileges || spec.User != "65534:65534" || len(spec.Ports) != 0 || len(spec.Devices) != 0 {
		t.Fatalf("the scanner is not confined: %+v", spec)
	}
	if spec.Image != Image || !strings.Contains(Image, ":"+Version+"@sha256:") {
		t.Fatalf("the scanner image is not pinned by tag and digest: %s", spec.Image)
	}
	want := map[string]dockerx.MountSpec{
		"/cache": {Type: "volume", Source: "jd-image-scan-db", Target: "/cache", ReadOnly: true},
		"/scan":  {Type: "bind", Source: "/var/lib/just-dashboard/image-scans/scan-1/in", Target: "/scan", ReadOnly: true},
		"/out":   {Type: "bind", Source: "/var/lib/just-dashboard/image-scans/scan-1/out", Target: "/out"},
		"/work":  {Type: "bind", Source: "/var/lib/just-dashboard/image-scans/scan-1/tmp", Target: "/work"},
	}
	if len(spec.Mounts) != len(want) {
		t.Fatalf("mounts = %+v", spec.Mounts)
	}
	for _, mount := range spec.Mounts {
		if want[mount.Target] != mount || strings.Contains(mount.Source, "docker.sock") {
			t.Fatalf("mount %+v", mount)
		}
	}
	args := strings.Join(spec.Command, " ")
	for _, flag := range []string{"--offline-scan", "--skip-db-update", "--skip-java-db-update", "--input " + ArchivePath, "--format json", "--scanners vuln"} {
		if !strings.Contains(args, flag) {
			t.Fatalf("the scan does not pass %s: %s", flag, args)
		}
	}
	if spec.Limits.MemoryMB == 0 || spec.Limits.PidsLimit == 0 || spec.Limits.CPUs == 0 {
		t.Fatalf("the scanner has no limits: %+v", spec.Limits)
	}
}

func TestAReleaseImageIsScannedFromItsArchiveAndABaseIsPulledAndRemoved(t *testing.T) {
	reg := newRegistry(t, databaseLayer(t, map[string]string{"trivy.db": "bolt", "metadata.json": metadataJSON}), false)
	docker := newFakeDocker("sha256:release")
	docker.report = readFixture(t, "trivy-alpine-3.12.json")
	s := testScanner(t, docker, reg)

	scanned, err := s.Scan(context.Background(), "sha256:release", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(scanned.Report.Findings) != 1 || scanned.Report.Findings[0].ID != "CVE-2022-37434" || !scanned.Database.Installed {
		t.Fatalf("scanned = %+v", scanned)
	}
	if len(docker.pulled) != 0 || len(docker.removed) != 0 {
		t.Fatal("a release image on this server was pulled or removed")
	}
	if entries, _ := os.ReadDir(s.dir); len(entries) != 1 || entries[0].Name() != "database.json" {
		t.Fatalf("the scan left its work behind: %v", entries)
	}

	// A release image that is gone is not pulled: it may never have been in a registry.
	if _, err := s.Scan(context.Background(), "sha256:gone", false); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("a missing release image = %v", err)
	}

	base := "alpine@sha256:" + strings.Repeat("1", 64)
	if _, err := s.Scan(context.Background(), base, true); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(docker.pulled, []string{base}) || !slices.Equal(docker.removed, []string{base}) {
		t.Fatalf("pulled %v removed %v", docker.pulled, docker.removed)
	}

	docker.exitCode = 1
	if _, err := s.Scan(context.Background(), "sha256:release", false); err == nil || !strings.Contains(err.Error(), "unable to open the database") {
		t.Fatalf("a failing scanner = %v", err)
	}
}

func TestABearerChallengeIsParsed(t *testing.T) {
	params := parseChallenge(`Bearer realm="https://ghcr.io/token",service="ghcr.io",scope="repository:a/b:pull,push"`)
	if params["realm"] != "https://ghcr.io/token" || params["service"] != "ghcr.io" || params["scope"] != "repository:a/b:pull,push" {
		t.Fatalf("params = %v", params)
	}
	if len(parseChallenge(`Basic realm="x"`)) != 0 {
		t.Fatal("a basic challenge was read as a bearer one")
	}
}

func TestAChallengeCannotSendTheDownloadToPlainHTTPElsewhere(t *testing.T) {
	client := &registryClient{http: http.DefaultClient, source: Source{Repository: "x"}, base: "https://mirror.gcr.io"}
	if err := client.authorize(context.Background(), `Bearer realm="http://169.254.169.254/token"`); err == nil {
		t.Fatal("a plain-HTTP realm was followed")
	}
}

func TestARedirectAwayToPlainHTTPIsNotFollowed(t *testing.T) {
	origin, _ := http.NewRequest(http.MethodGet, "https://ghcr.io/v2/x/blobs/sha256:1", nil)
	cdn, _ := http.NewRequest(http.MethodGet, "https://pkg-containers.githubusercontent.com/blob", nil)
	if err := httpsRedirectsOnly(cdn, []*http.Request{origin}); err != nil {
		t.Fatalf("an https blob store was refused: %v", err)
	}
	internal, _ := http.NewRequest(http.MethodGet, "http://169.254.169.254/latest", nil)
	if err := httpsRedirectsOnly(internal, []*http.Request{origin}); err == nil {
		t.Fatal("a plain-HTTP redirect to another host was followed")
	}
}
