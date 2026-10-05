package dockerx

import (
	"archive/tar"
	"bytes"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/container"
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
