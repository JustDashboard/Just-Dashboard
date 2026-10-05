package dockerx

import (
	"archive/tar"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/opencontainers/image-spec/specs-go/v1"
)

func TestAdoptionImageArchiveBudgetRetainsCushionAndImportRoom(t *testing.T) {
	for _, test := range []struct {
		name      string
		available uint64
		budget    int64
		blocked   bool
	}{
		{"empty", 0, 0, true},
		{"below_cushion", adoptionImageDiskCushion - 1, 0, true},
		{"exact_cushion", adoptionImageDiskCushion, 0, true},
		{"no_room_for_second_copy", adoptionImageDiskCushion + 1, 0, true},
		{"room_for_both_copies", adoptionImageDiskCushion + 8<<20, 4 << 20, false},
		{"large_filesystem", math.MaxUint64, adoptionImageArchiveLimit, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			budget, err := adoptionImageArchiveBudget(test.available)
			if budget != test.budget || errors.Is(err, errAdoptionImageSpace) != test.blocked {
				t.Fatal("recovery budget does not retain disk cushion and import room")
			}
		})
	}
}

func privateAdoptionStorageTestFile(t *testing.T) *os.File {
	t.Helper()
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	file, err := openAdoptionImageFile(root, "rootfs.tmp", os.O_CREATE|os.O_EXCL|os.O_WRONLY)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { file.Close() })
	return file
}

func TestAdoptionImageStorageMeasuresThePrivateOpenDescriptor(t *testing.T) {
	file := privateAdoptionStorageTestFile(t)
	if info, err := file.Stat(); err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Fatal("recovery staging file is not private")
	}
	if _, err := adoptionFileAvailable(file); err != nil {
		t.Fatal("private staging filesystem could not be measured", err)
	}
	file.Close()
	if _, err := adoptionFileAvailable(file); err == nil {
		t.Fatal("capacity verification accepted a closed descriptor")
	}
}

func TestAdoptionImageStorageStopsBeforeWritingAfterSpaceLoss(t *testing.T) {
	file := privateAdoptionStorageTestFile(t)
	available := adoptionImageDiskCushion + 1024
	writer := &adoptionArchiveWriter{file: file, budget: 512, available: func(*os.File) (uint64, error) { return available, nil }}
	if written, err := writer.Write(make([]byte, 128)); err != nil || written != 128 {
		t.Fatal("initial bounded write failed")
	}
	available = adoptionImageDiskCushion + 128
	if written, err := writer.Write([]byte{1}); written != 0 || !errors.Is(err, errAdoptionImageSpace) {
		t.Fatal("recovery consumed its disk cushion")
	}
	info, err := file.Stat()
	if err != nil || info.Size() != 128 {
		t.Fatal("blocked write changed the recovery artifact")
	}
}

func TestAdoptionImageArchiveBudgetIncludesTarHeadersAndPadding(t *testing.T) {
	file := privateAdoptionStorageTestFile(t)
	writer := &adoptionArchiveWriter{file: file, budget: 1024, available: func(*os.File) (uint64, error) { return adoptionImageDiskCushion + 8192, nil }}
	archive := tar.NewWriter(writer)
	if err := archive.WriteHeader(&tar.Header{Name: "one-byte", Size: 1, Mode: 0o600}); err != nil {
		t.Fatal(err)
	}
	if _, err := archive.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); !errors.Is(err, errAdoptionImageSpace) {
		t.Fatal("tar overhead escaped the actual staged-byte budget")
	}
	info, err := file.Stat()
	if err != nil || info.Size() > 1024 {
		t.Fatal("archive exceeded the staging byte budget")
	}
}

func TestAdoptionImageCachedProofAvoidsExportImportAndArchiveWrites(t *testing.T) {
	capture := &AdoptionContainer{Inspection: container.InspectResponse{
		ContainerJSONBase:       &container.ContainerJSONBase{ID: "original", Image: "sha256:missing", State: &container.State{Running: true, Pid: 42}},
		ImageManifestDescriptor: &v1.Descriptor{Platform: &v1.Platform{OS: "linux", Architecture: "amd64"}},
		Config:                  &container.Config{},
	}}
	fingerprint := adoptionHash([]byte(adoptionImageFingerprint(capture.Inspection, capture.Changes)), []byte("linux/amd64"), adoptionJSON([]string(nil)))
	imageID := "sha256:" + strings.Repeat("1", 64)
	root := t.TempDir()
	directory := filepath.Join(root, "images", fingerprint)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := adoptionImageManifest{Version: 1, ID: imageID, SourceDigest: fingerprint, Platform: "linux/amd64"}
	if err := os.WriteFile(filepath.Join(directory, "manifest.json"), adoptionJSON(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/_ping"):
			w.Header().Set("API-Version", "1.47")
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/containers/original/json"):
			json.NewEncoder(w).Encode(capture.Inspection)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/containers/original/changes"):
			json.NewEncoder(w).Encode(capture.Changes)
		case r.Method == http.MethodGet && (strings.HasSuffix(r.URL.Path, "/containers/json") || strings.HasSuffix(r.URL.Path, "/history")):
			json.NewEncoder(w).Encode([]any{})
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/images/"):
			json.NewEncoder(w).Encode(map[string]any{"Id": imageID, "Os": "linux", "Architecture": "amd64", "Config": map[string]any{"Labels": map[string]string{"io.just-dashboard.adoption-source": fingerprint}}})
		default:
			t.Errorf("cached recovery performed an export, import or unexpected runtime action: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer engine.Close()
	client := New(engine.URL)
	defer client.Close()
	image, err := client.RecoverAdoptionImage(t.Context(), capture, root, nil)
	if err != nil || image == nil || image.ID != imageID {
		t.Fatal("verified cached recovery image was not reused", err)
	}
	for _, name := range []string{"rootfs.tmp", "rootfs.tar"} {
		if _, err := os.Stat(filepath.Join(directory, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("cached recovery attempted archive staging")
		}
	}
}
