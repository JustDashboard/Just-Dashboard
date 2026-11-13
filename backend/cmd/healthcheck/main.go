// Command healthcheck is the backend container's health check: it asks the
// local server's liveness route and exits non-zero if it does not answer.
//
// It is a program of its own, not a mode of the server, because Docker starts
// it every thirty seconds: see internal/healthprobe for what that cost. The
// server keeps its -healthcheck flag, which runs the same probe, for a
// container still described by an older image's HEALTHCHECK.
package main

import "github.com/Wayy01/Just-Dashboard/backend/internal/healthprobe"

func main() { healthprobe.Main() }
