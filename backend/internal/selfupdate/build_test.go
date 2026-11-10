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

// What an update leaves in BuildKit's cache is removed once it is a week old,
// and the transcript carries the one line of the prune that matters: a real
// one prints a row for every cache entry first.
func TestPruningTheBuildCacheSaysWhatWasFreed(t *testing.T) {
	for name, tc := range map[string]struct {
		output string
		err    error
		want   string
	}{
		"buildkit": {
			output: "ID\t\t\t\t\t\tRECLAIMABLE\tSIZE\t\tLAST ACCESSED\nq1w2e3*\t\t\t\ttrue\t\t1.1GB\t\t9 days ago\nTotal:\t3.482GB\n",
			want:   "build cache unused for a week removed: 3.482GB\n",
		},
		"the older wording": {
			output: "Deleted build cache objects:\nq1w2e3\n\nTotal reclaimed space: 912MB\n",
			want:   "build cache unused for a week removed: 912MB\n",
		},
		"nothing old enough": {output: "", want: "build cache unused for a week removed: nothing\n"},
		"a daemon that refuses": {
			output: "ERROR: failed to solve\nCannot connect to the Docker daemon\n",
			err:    os.ErrPermission,
			want:   "the build cache was not pruned: Cannot connect to the Docker daemon\n",
		},
	} {
		t.Run(name, func(t *testing.T) {
			var asked []string
			var out strings.Builder
			PruneBuildCache(&out, func(args ...string) ([]byte, error) {
				asked = args
				return []byte(tc.output), tc.err
			})
			// A week, and never --all: the build that has just finished, and
			// whatever else was built here lately, keeps its cache.
			if got := strings.Join(asked, " "); got != "builder prune --force --filter until=168h" {
				t.Fatalf("ran docker %s", got)
			}
			want := "\n$ docker builder prune --force --filter until=168h\n" + tc.want
			if out.String() != want {
				t.Fatalf("transcript %q, want %q", out.String(), want)
			}
		})
	}
}
