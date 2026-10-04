package dockerx

import (
	"context"
	"os"
	"path/filepath"
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
