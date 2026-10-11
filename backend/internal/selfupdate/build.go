package selfupdate

import (
	"context"
	"os/exec"
	"slices"
	"strings"
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
