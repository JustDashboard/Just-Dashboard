package dockerx

import (
	"archive/tar"
	"bytes"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
)

func TestVerifiedN8nCacheRequiresOnlyAddedGeneratedFilesAndEmptyUploads(t *testing.T) {
	for _, name := range []string{"standard", "unknown-file", "unknown-directory", "modified", "deleted", "symlink", "actual-upload", "missing-cache-proof", "unknown-ancestor"} {
		t.Run(name, func(t *testing.T) {
			changes := []container.FilesystemChange{{Path: "/home", Kind: container.ChangeModify}, {Path: n8nEditorCache, Kind: container.ChangeAdd}, {Path: n8nEditorCache + "/assets/editor.js", Kind: container.ChangeAdd}, {Path: n8nEditorCache + "/types/credentials.json", Kind: container.ChangeAdd}, {Path: "/tmp", Kind: container.ChangeModify}, {Path: n8nUploadDirectory, Kind: container.ChangeAdd}}
			modes := map[string]os.FileMode{"/home": os.ModeDir, "/tmp": os.ModeDir}
			distribution := map[string]os.FileMode{"": os.ModeDir, "assets": os.ModeDir, "assets/editor.js": 0o644}
			cache := map[string]os.FileMode{"": os.ModeDir, "assets/editor.js": 0o644, "types/credentials.json": 0o644}
			uploads := map[string]os.FileMode{"": os.ModeDir}
			switch name {
			case "unknown-file":
				cache["assets/operator-data.js"] = 0o644
			case "unknown-directory":
				cache["assets/operator-data"] = os.ModeDir
			case "modified":
				changes[2].Kind = container.ChangeModify
			case "deleted":
				changes[2].Kind = container.ChangeDelete
			case "symlink":
				cache["assets/editor.js"] = os.ModeSymlink
			case "actual-upload":
				uploads["customer.csv"] = 0o644
			case "missing-cache-proof":
				delete(cache, "assets/editor.js")
			case "unknown-ancestor":
				delete(modes, "/home")
			}
			verified, err := verifiedN8nRegenerableChanges(changes, modes, distribution, cache, uploads)
			if name == "standard" || name == "unknown-ancestor" {
				if err != nil || len(verified) == 0 {
					t.Fatal("verified standard cache rejected", err)
				}
				if name == "unknown-ancestor" {
					for _, p := range verified {
						if p == "/home" {
							t.Fatal("unverified ancestor exempted")
						}
					}
				}
			} else if err == nil {
				t.Fatal("unsafe cache exception accepted")
			}
		})
	}
}

func TestN8nArchiveProofRejectsLinksDuplicatesAndEscapes(t *testing.T) {
	for _, name := range []string{"standard", "link", "hardlink", "duplicate", "escape", "large"} {
		t.Run(name, func(t *testing.T) {
			var content bytes.Buffer
			writer := tar.NewWriter(&content)
			header := &tar.Header{Name: "public", Mode: 0o755, Typeflag: tar.TypeDir}
			if err := writer.WriteHeader(header); err != nil {
				t.Fatal(err)
			}
			header = &tar.Header{Name: "public/assets/editor.js", Mode: 0o644, Typeflag: tar.TypeReg}
			switch name {
			case "link":
				header.Typeflag, header.Linkname = tar.TypeSymlink, "/outside"
			case "hardlink":
				header.Typeflag, header.Linkname = tar.TypeLink, "public/assets/another.js"
			case "duplicate":
				header.Name, header.Typeflag = "public", tar.TypeDir
			case "escape":
				header.Name = "public/../../outside"
			case "large":
				header.Size = (128 << 20) + 1
			}
			if err := writer.WriteHeader(header); err != nil {
				t.Fatal(err)
			}
			if name != "large" {
				writer.Close()
			}
			proof, err := readN8nArchiveModes(bytes.NewReader(content.Bytes()), "public")
			if name == "standard" {
				if err != nil || !proof["assets/editor.js"].IsRegular() {
					t.Fatal("standard archive rejected", err)
				}
			} else if err == nil {
				t.Fatal("unsafe archive proof accepted")
			}
		})
	}
}

func TestLiveHeldN8nCacheProof(t *testing.T) {
	id := os.Getenv("JD_N8N_CACHE_PROOF_CONTAINER")
	if id == "" {
		t.Skip("set JD_N8N_CACHE_PROOF_CONTAINER to a held owned n8n source-proof fixture")
	}
	client := New("unix:///var/run/docker.sock")
	api, err := client.api()
	if err != nil {
		t.Fatal(err)
	}
	before, err := api.ContainerInspect(t.Context(), id)
	if err != nil || !strings.HasPrefix(strings.TrimPrefix(before.Name, "/"), "jd-n8n-cache-proof-") {
		t.Fatal("not an owned held source-proof fixture")
	}
	capture, err := client.CaptureAdoptionContainer(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	verified := map[string]bool{}
	for _, name := range capture.RegenerablePaths {
		verified[name] = true
	}
	for _, change := range capture.Changes {
		if !verified[change.Path] {
			t.Fatalf("writable path not positively identified as regenerated: %s (proof stage=%s)", change.Path, capture.RegenerableProofFailure)
		}
	}
	if len(capture.RegenerablePaths) < 100 {
		t.Fatal("n8n generator identity/cache proof missing")
	}
	after, err := api.ContainerInspect(t.Context(), id)
	if err != nil || before.ID != after.ID || before.State.Pid != after.State.Pid || before.State.StartedAt != after.State.StartedAt || !reflect.DeepEqual(before.Config, after.Config) {
		t.Fatal("read-only proof changed fixture runtime")
	}
	t.Logf("verified regenerated cache changes=%d; runtime ID/PID/start/config unchanged", len(capture.Changes))
}

func TestN8nImmutableImageProofReuseRequiresFreshUnchangedSourceAndNoShadowMount(t *testing.T) {
	const image = "sha256:owned-immutable-fixture"
	proof := map[string]os.FileMode{"": os.ModeDir, "assets/editor.js": 0644}
	client := &Client{}
	fixture := func() *AdoptionContainer {
		return &AdoptionContainer{Image: &ImageDetail{ID: image}, Inspection: container.InspectResponse{ContainerJSONBase: &container.ContainerJSONBase{Image: image}}, Changes: []container.FilesystemChange{{Path: n8nEditorCache + "/assets/editor.js", Kind: container.ChangeAdd}}}
	}
	first := fixture()
	client.rememberN8nImageProof(first, proof)
	if got := client.cachedN8nImageProof(fixture()); !reflect.DeepEqual(got, proof) {
		t.Fatal("same immutable source reread rather than reused")
	}
	for _, name := range []string{"source_modified", "source_deleted", "source_ancestor", "distribution_addition", "source_mount", "different_image", "missing_image"} {
		t.Run(name, func(t *testing.T) {
			current := fixture()
			source := "/usr/local/lib/node_modules/n8n/dist/commands/start.js"
			switch name {
			case "source_modified":
				current.Changes = append(current.Changes, container.FilesystemChange{Path: source, Kind: container.ChangeModify})
			case "source_deleted":
				current.Changes = append(current.Changes, container.FilesystemChange{Path: source, Kind: container.ChangeDelete})
			case "source_ancestor":
				current.Changes = append(current.Changes, container.FilesystemChange{Path: "/usr/local/lib/node_modules/n8n", Kind: container.ChangeModify})
			case "distribution_addition":
				current.Changes = append(current.Changes, container.FilesystemChange{Path: "/usr/local/lib/node_modules/n8n/node_modules/n8n-editor-ui/dist/assets/private.js", Kind: container.ChangeAdd})
			case "source_mount":
				current.Inspection.Mounts = []container.MountPoint{{Type: mount.TypeBind, Destination: "/usr/local/lib/node_modules/n8n"}}
			case "different_image":
				current.Inspection.Image = "sha256:another-image"
			case "missing_image":
				current.Image = nil
			}
			if client.cachedN8nImageProof(current) != nil {
				t.Fatal("mutable or different source reused immutable proof")
			}
		})
	}
}
