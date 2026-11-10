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
func BuildEachService(ctx context.Context, dir, compose string, build func(args ...string) error) error {
	list := exec.CommandContext(ctx, "docker", "compose", "-f", compose, "config", "--services")
	list.Dir = dir
	listed, err := list.Output()
	services := strings.Fields(string(listed))
	if err != nil || len(services) == 0 {
		return build("compose", "-f", compose, "build")
	}
	// Compose lists independent services in no fixed order; sorted, the
	// update transcript reads the same way every time.
	slices.Sort(services)
	for _, service := range services {
		if err := build("compose", "-f", compose, "build", service); err != nil {
			return err
		}
	}
	return nil
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
