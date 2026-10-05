package dockerx

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/opencontainers/image-spec/specs-go/v1"
)

func TestAdoptionImageRequiresAuthoritativePlatform(t *testing.T) {
	client := &Client{}
	for name, descriptor := range map[string]*v1.Descriptor{
		"missing":             nil,
		"missing_platform":    {},
		"windows":             {Platform: &v1.Platform{OS: "windows", Architecture: "amd64"}},
		"unsupported_variant": {Platform: &v1.Platform{OS: "linux", Architecture: "amd64", Variant: "v8"}},
	} {
		t.Run(name, func(t *testing.T) {
			capture := &AdoptionContainer{Inspection: container.InspectResponse{ContainerJSONBase: &container.ContainerJSONBase{ID: "original"}, ImageManifestDescriptor: descriptor}}
			if _, err := client.RecoverAdoptionImage(context.Background(), capture, t.TempDir(), nil); err == nil {
				t.Fatal("recovery accepted an unavailable or unsupported authoritative platform")
			}
		})
	}
}

func TestAdoptionImageFingerprintFencesIdentityAndIgnoresHealthTicks(t *testing.T) {
	inspection := container.InspectResponse{
		ContainerJSONBase:       &container.ContainerJSONBase{ID: "original", Image: "sha256:original", State: &container.State{Running: true, Pid: 42, StartedAt: "original-start", Health: &container.Health{Status: "unhealthy"}}},
		ImageManifestDescriptor: &v1.Descriptor{Platform: &v1.Platform{OS: "linux", Architecture: "amd64"}},
		Config:                  &container.Config{Env: []string{"PRIVATE_TOKEN=artificial-test-value"}},
	}
	changes := []container.FilesystemChange{{Path: "/second", Kind: container.ChangeAdd}, {Path: "/first", Kind: container.ChangeModify}}
	want := adoptionImageFingerprint(inspection, changes)
	inspection.State.Health.Status = "healthy"
	if got := adoptionImageFingerprint(inspection, []container.FilesystemChange{changes[1], changes[0]}); got != want {
		t.Fatal("health tick or diff ordering changed the recovery cache identity")
	}
	inspection.ImageManifestDescriptor.Platform.Architecture = "arm64"
	if adoptionImageFingerprint(inspection, changes) == want {
		t.Fatal("platform descriptor change escaped the recovery fence")
	}
	inspection.ImageManifestDescriptor.Platform.Architecture = "amd64"
	inspection.Config.Env[0] = "PRIVATE_TOKEN=changed-artificial-value"
	if adoptionImageFingerprint(inspection, changes) == want {
		t.Fatal("environment change escaped the recovery fence")
	}
	inspection.Config.Env[0] = "PRIVATE_TOKEN=artificial-test-value"
	inspection.State.Pid++
	if adoptionImageFingerprint(inspection, changes) == want {
		t.Fatal("process replacement escaped the recovery fence")
	}
}

func TestAdoptionImageExportRejectsUnprovedCacheAndUploadEntries(t *testing.T) {
	cache := strings.TrimPrefix(n8nEditorCache, "/")
	uploads := strings.TrimPrefix(n8nUploadDirectory, "/")
	exclusions := adoptionArchiveExclusions([]string{"/", "/home", "/home/node", "/home/node/.cache", "/home/node/.cache/n8n", "/tmp", n8nEditorCache, n8nEditorCache + "/assets/editor.js", n8nUploadDirectory})
	for _, name := range []string{cache, cache + "/assets/editor.js", uploads} {
		if excluded, err := adoptionArchivePathExcluded(name, exclusions); !excluded || err != nil {
			t.Fatal("verified generated path was not excluded")
		}
	}
	for _, name := range []string{cache + "/new-unknown.js", uploads + "/live-application-upload.csv"} {
		if _, err := adoptionArchivePathExcluded(name, exclusions); err == nil {
			t.Fatal("an unproved cache or upload entry escaped the export-time fence")
		}
	}
	for _, name := range []string{"", "home", "home/node", "home/node/.cache", "home/node/.cache/n8n", "tmp", "home/node/base-image-file", cache + "-other/file", uploads + "-other/file"} {
		if excluded, err := adoptionArchivePathExcluded(name, exclusions); excluded || err != nil {
			t.Fatal("cache proof changed unrelated original-image paths")
		}
	}
	if excluded, err := adoptionArchivePathExcluded(cache+"/image-provided.js", nil); excluded || err != nil {
		t.Fatal("an unverified root changed ordinary image capture")
	}
	python := "app/__pycache__/source.cpython-311.pyc"
	if excluded, err := adoptionArchivePathExcluded(python, adoptionArchiveExclusions([]string{"/" + python})); !excluded || err != nil {
		t.Fatal("n8n directory preservation changed Python bytecode exclusion")
	}
}

func TestAdoptionImageArtifactsRejectSymlinksAndPublicFiles(t *testing.T) {
	directory := t.TempDir()
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := os.WriteFile(filepath.Join(directory, "public"), []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("public", filepath.Join(directory, "link")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"public", "link"} {
		if file, err := openAdoptionImageFile(root, name, os.O_RDONLY); err == nil {
			file.Close()
			t.Fatalf("accepted nonprivate or symlink artifact %q", name)
		}
	}
	if _, err := openAdoptionImageFile(root, "link", os.O_CREATE|os.O_TRUNC|os.O_WRONLY); err == nil {
		t.Fatal("wrote through artifact symlink")
	}
	if content, err := os.ReadFile(filepath.Join(directory, "public")); err != nil || string(content) != "original" {
		t.Fatal("artifact symlink changed its target")
	}
	file, err := openAdoptionImageFile(root, "private", os.O_CREATE|os.O_EXCL|os.O_WRONLY)
	if err != nil {
		t.Fatal(err)
	}
	file.Close()
}
