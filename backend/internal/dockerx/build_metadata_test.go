package dockerx

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The build context holds exactly the repository's files while buildx runs:
// the metadata file buildx writes lives in a private folder of its own,
// which is gone once the build returns. A recording docker on PATH stands in
// for buildx, lists the context it was started in and writes the metadata.
func TestBuildContextIsTheSameBeforeAndDuringTheBuild(t *testing.T) {
	bin, record, context_ := t.TempDir(), t.TempDir(), t.TempDir()
	for name, content := range map[string]string{
		"Dockerfile": "FROM scratch\nCOPY . .\n", "app.txt": "fixture\n", ".dockerignore": "",
	} {
		if err := os.WriteFile(filepath.Join(context_, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	script := `#!/bin/sh
ls -A > "$LANE_RECORD/during"
metadata=""
while [ $# -gt 0 ]; do
  if [ "$1" = "--metadata-file" ]; then metadata="$2"; fi
  shift
done
printf '%s\n' "$metadata" > "$LANE_RECORD/metadata-path"
printf '{"containerimage.digest":"sha256:%064d"}' 0 > "$metadata"
`
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("LANE_RECORD", record)
	before, err := os.ReadDir(context_)
	if err != nil {
		t.Fatal(err)
	}

	// No daemon answers the inspect that follows a build, so the build
	// fails after buildx has run; what it did to the context is the test.
	_, _ = (&Client{}).BuildImmutable(context.Background(), ImmutableBuildOptions{
		Dir: context_, Dockerfile: "Dockerfile", Tag: "just-dashboard/test:run-1",
	}, nil)

	during, err := os.ReadFile(filepath.Join(record, "during"))
	if err != nil {
		t.Fatalf("the recording docker did not run: %v", err)
	}
	names := []string{}
	for _, entry := range before {
		names = append(names, entry.Name())
	}
	if got := strings.Fields(string(during)); !slices.Equal(got, names) {
		t.Fatalf("context during the build = %v, before = %v", got, names)
	}
	raw, err := os.ReadFile(filepath.Join(record, "metadata-path"))
	if err != nil {
		t.Fatal(err)
	}
	metadata := strings.TrimSpace(string(raw))
	if metadata == "" || !filepath.IsAbs(metadata) {
		t.Fatalf("metadata path = %q", metadata)
	}
	if rel, err := filepath.Rel(context_, metadata); err == nil && !strings.HasPrefix(rel, "..") {
		t.Fatalf("metadata file %s is inside the context", metadata)
	}
	if _, err := os.Stat(filepath.Dir(metadata)); !os.IsNotExist(err) {
		t.Fatalf("the metadata folder outlived the build: %v", err)
	}
	after, err := os.ReadDir(context_)
	if err != nil || len(after) != len(before) {
		t.Fatalf("context after the build = %v (%v)", after, err)
	}
}

// A temporary folder that cannot be made fails the build before buildx
// runs, and says what it could not make.
func TestABuildWithoutATemporaryFolderFailsBeforeBuildx(t *testing.T) {
	bin, context_ := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(context_, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ran := filepath.Join(bin, "ran")
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte("#!/bin/sh\ntouch '"+ran+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMPDIR", filepath.Join(bin, "missing"))
	_, err := (&Client{}).BuildImmutable(context.Background(), ImmutableBuildOptions{
		Dir: context_, Dockerfile: "Dockerfile", Tag: "just-dashboard/test:run-1",
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "metadata folder") {
		t.Fatalf("error = %v", err)
	}
	if _, statErr := os.Stat(ran); !os.IsNotExist(statErr) {
		t.Fatal("buildx ran without a metadata folder")
	}
}
