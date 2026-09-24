package deploy

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGeneratedDockerfileCannotFollowCheckoutSymlinkOutsideContext(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	canary := filepath.Join(outside, "Dockerfile")
	if err := os.WriteFile(canary, []byte("outside-sentinel"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, ".just-dashboard")); err != nil {
		t.Fatal(err)
	}
	if err := writeGeneratedFile(root, "Dockerfile", "replacement"); err == nil {
		t.Fatal("generated file escaped context")
	}
	content, err := os.ReadFile(canary)
	if err != nil || string(content) != "outside-sentinel" {
		t.Fatalf("outside file changed: %q, %v", content, err)
	}
}
