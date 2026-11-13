package selfupdate

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Wayy01/Just-Dashboard/backend/internal/selfupdate/selfupdatetest"
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

// A build of anything but a release says which commit it is. A server on a
// patch branch reported the last release's number exactly as a server on that
// release did, in the wordmark, in `jd version` and in every bug report.
func TestABuildIsStampedUnlessItIsTheRelease(t *testing.T) {
	upstream, checkout := installed(t)
	ctx := context.Background()
	short := strings.TrimSpace(git(t, checkout, "rev-parse", "--short", "HEAD"))

	if got := BuildCommit(ctx, checkout); got != short {
		t.Fatalf("a checkout with nothing to say it is a release is stamped %q, want %q", got, short)
	}
	// A clone has the tags; the release's own commit needs no stamp.
	git(t, checkout, "tag", "v0.5")
	if got := BuildCommit(ctx, checkout); got != "" {
		t.Fatalf("the tagged release is stamped %q", got)
	}
	// A commit on top of the release is not the release.
	write(t, filepath.Join(checkout, "local.txt"), "mine\n")
	git(t, checkout, "add", ".")
	git(t, checkout, "commit", "-q", "-m", "mine")
	if got := BuildCommit(ctx, checkout); got == "" || got == short {
		t.Fatalf("a commit past the release is stamped %q", got)
	}
	if got := BuildCommit(ctx, t.TempDir()); got != "" {
		t.Fatalf("a directory that is not a checkout is stamped %q", got)
	}
	_ = upstream
}

// The update verifies the tag origin has and trusts no tag in the checkout,
// which may hold none (it is set to fetch without them) or the operator's own
// under the same name. What the update writes instead is what keeps the build
// that follows, and every rebuild after it, unstamped.
func TestAnInstalledReleaseIsRememberedAsOne(t *testing.T) {
	upstream, checkout := installed(t)
	git(t, upstream, "tag", "v0.6")
	git(t, checkout, "config", "remote.origin.tagOpt", "--no-tags")
	var log strings.Builder
	landed, err := fastForward(context.Background(), checkout, request{ref: "main", version: "0.6", tagOnly: true}, &log)
	if err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	if tags := strings.TrimSpace(git(t, checkout, "tag", "--list")); tags != "" {
		t.Fatalf("this checkout fetches no tags, and has %q", tags)
	}
	if at := strings.TrimSpace(git(t, checkout, "rev-parse", "refs/just-dashboard/release/v0.6")); at != landed.commit {
		t.Fatalf("the release is recorded at %s, want %s", at, landed.commit)
	}
	if got := BuildCommit(context.Background(), checkout); got != "" {
		t.Fatalf("the release an update installed is stamped %q", got)
	}
}

// A fork given the head of its branch has not installed a release, and the
// build says which commit that head was.
func TestABranchHeadInstalledForAForkIsStamped(t *testing.T) {
	_, checkout := installed(t)
	var log strings.Builder
	commit, _, err := forward(checkout, "0.6", &log)
	if err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	got := BuildCommit(context.Background(), checkout)
	if got == "" || !strings.HasPrefix(commit, got) {
		t.Fatalf("stamped %q, want the start of %s", got, commit)
	}
}

// The stamp travels as a build argument on every build command, the one per
// service and the one for everything at once.
func TestTheStampIsPassedToTheBuild(t *testing.T) {
	_, checkout := installed(t)
	short := strings.TrimSpace(git(t, checkout, "rev-parse", "--short", "HEAD"))
	// No compose here: with nothing listed, everything is built at once.
	selfupdatetest.Docker(t)
	var calls [][]string
	if err := BuildEachService(context.Background(), checkout, "docker-compose.yml", recordBuilds(&calls)); err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"compose", "-f", "docker-compose.yml", "build", "--build-arg", "JD_COMMIT=" + short}}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("built with %q, want %q", calls, want)
	}

	git(t, checkout, "tag", "v0.5")
	calls = nil
	if err := BuildEachService(context.Background(), checkout, "docker-compose.yml", recordBuilds(&calls)); err != nil {
		t.Fatal(err)
	}
	if want := [][]string{{"compose", "-f", "docker-compose.yml", "build"}}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("a release was built with %q, want %q", calls, want)
	}
}
