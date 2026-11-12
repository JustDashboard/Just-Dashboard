package selfupdate

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"slices"
	"strings"
	"time"
)

// BuildEachService builds the dashboard's own images one service at a time.
//
// `docker compose build` builds every service at once, so the stack's two
// compilers peak together: Turbopack needs between 2.5 and 3 GB for the
// frontend, and the Go compiler more than 1 GB for the backend. One after the
// other they need only the larger of the two, and that peak is what decides the
// smallest server that can update itself. Where there were spare cores this
// takes longer; where there were not, the two builds were sharing them anyway.
//
// The services are read from the compose file rather than named here, because
// the file being built is the one an update has just fetched and may name
// others. If they cannot be read, everything is built together, as before.
//
// Every build is told which commit it is of, when that is not a release's own
// (BuildCommit), so the three things that build this stack, which are an
// update, a rebuild from Settings and install.sh's copy of the same rule,
// stamp it alike.
func BuildEachService(ctx context.Context, dir, compose string, build func(args ...string) error) error {
	command := []string{"compose", "-f", compose, "build"}
	if commit := BuildCommit(ctx, dir); commit != "" {
		command = append(command, "--build-arg", "JD_COMMIT="+commit)
	}
	list := exec.CommandContext(ctx, "docker", "compose", "-f", compose, "config", "--services")
	list.Dir = dir
	listed, err := list.Output()
	services := strings.Fields(string(listed))
	if err != nil || len(services) == 0 {
		return build(command...)
	}
	// Compose lists independent services in no fixed order; sorted, the
	// update transcript reads the same way every time.
	slices.Sort(services)
	for _, service := range services {
		if err := build(append(slices.Clone(command), service)...); err != nil {
			return err
		}
	}
	return nil
}

// releaseRefs is where a checkout records that a commit is the release of a
// version: the tag, when the checkout has it, and a ref of this dashboard's
// own that an update writes when it installs a tag from origin. A fetch
// usually brings the tag along, but not into a checkout configured to fetch
// without tags, and not over a tag of the operator's own by the same name,
// which the update deliberately never trusts. The second ref is what a later
// rebuild reads in those checkouts.
func releaseRefs(version string) []string {
	version = Normalise(version)
	return []string{"refs/tags/v" + version, "refs/just-dashboard/release/v" + version}
}

// BuildCommit is the commit to stamp a build of the checkout at dir with: the
// short commit when HEAD is not the release of the version it carries, and ""
// when it is, or when dir is not a checkout that can be asked.
//
// scripts/build-stamp.sh is the same rule in shell, for install.sh.
func BuildCommit(ctx context.Context, dir string) string {
	head, err := gitOutput(ctx, dir, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return ""
	}
	head = strings.TrimSpace(head)
	for _, ref := range releaseRefs(installedVersion(ctx, dir)) {
		if at, err := gitOutput(ctx, dir, "rev-parse", "--verify", "--quiet", ref+"^{commit}"); err == nil && strings.TrimSpace(at) == head {
			return ""
		}
	}
	short, err := gitOutput(ctx, dir, "rev-parse", "--short", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(short)
}

// buildCachePrune removes the build cache nothing has used for a week.
//
// Every update builds both images without cache mounts (backend/Dockerfile
// says why), and each build leaves its layers in BuildKit's cache. Docker
// collects that on its own schedule, which by default lets an entry sit for
// sixty days and the cache grow until a fifth of the disk is left; on the
// 20 GB server this product is aimed at, that is updates failing for want of
// room before the collector has anything to say. A week is long enough that
// the build which has just run, and anything else built on this server
// lately, keeps its cache.
//
// The cache is the daemon's, not this project's: the entries of other things
// built here and unused for a week go too. That costs their next build the
// time the cache saved and nothing else.
var buildCachePrune = []string{"builder", "prune", "--force", "--filter", "until=168h"}

// PruneTimeout bounds the prune. It runs at the end of an update and of an
// uninstall, and neither waits on it for long.
const PruneTimeout = 2 * time.Minute

// PruneBuildCache runs buildCachePrune through docker and writes what it
// freed to out. It reports no error: nothing that was asked for depends on it,
// and a cache that could not be pruned is a line in the transcript.
func PruneBuildCache(out io.Writer, docker func(args ...string) ([]byte, error)) {
	fmt.Fprintf(out, "\n$ docker %s\n", strings.Join(buildCachePrune, " "))
	output, err := docker(buildCachePrune...)
	if err != nil {
		detail := lastLine(string(output))
		if detail == "" {
			detail = err.Error()
		}
		fmt.Fprintf(out, "the build cache was not pruned: %s\n", detail)
		return
	}
	fmt.Fprintf(out, "build cache unused for a week removed: %s\n", reclaimed(string(output)))
}

// reclaimed is the total from `docker builder prune`, which prints one line
// per cache entry before it and has worded the total two ways over the years
// ("Total:" and "Total reclaimed space:").
func reclaimed(output string) string {
	lines := strings.Split(output, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); strings.HasPrefix(line, "Total") {
			if _, amount, ok := strings.Cut(line, ":"); ok && strings.TrimSpace(amount) != "" {
				return strings.TrimSpace(amount)
			}
		}
	}
	return "nothing"
}

func lastLine(output string) string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}
