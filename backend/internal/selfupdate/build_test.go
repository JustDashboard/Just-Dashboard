package selfupdate

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func recordBuilds(calls *[][]string) func(args ...string) error {
	return func(args ...string) error {
		*calls = append(*calls, args)
		return nil
	}
}

func TestBuildEachServiceBuildsInTurn(t *testing.T) {
	if exec.Command("docker", "compose", "version").Run() != nil {
		t.Skip("docker compose is not installed")
	}
	dir := t.TempDir()
	compose := "docker-compose.yml"
	if err := os.WriteFile(filepath.Join(dir, compose), []byte(strings.Join([]string{
		"services:",
		"  backend:",
		"    build: ./backend",
		"  frontend:",
		"    build: ./frontend",
		"",
	}, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	var calls [][]string
	if err := BuildEachService(context.Background(), dir, compose, recordBuilds(&calls)); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"compose", "-f", compose, "build", "backend"},
		{"compose", "-f", compose, "build", "frontend"},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("builds = %q, want %q", calls, want)
	}
}

// A compose file that cannot be read is no reason to stop an update the old
// way would have attempted: the build is then the single call it always was.
func TestBuildEachServiceFallsBackToOneBuild(t *testing.T) {
	var calls [][]string
	if err := BuildEachService(context.Background(), t.TempDir(), "missing.yml", recordBuilds(&calls)); err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"compose", "-f", "missing.yml", "build"}}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("builds = %q, want %q", calls, want)
	}
}
